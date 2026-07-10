package gitlab

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/meshcloud/building-block-runner/internal/dispatch"
	"github.com/meshcloud/building-block-runner/internal/meshapi"
	"github.com/meshcloud/building-block-runner/internal/report"
)

// HandlerDeps are the gitlab handler's injected collaborators (umbrella §5.3 template,
// narrowed/grown per 06A §17's fit review): unlike manual, gitlab decrypts (a Decryptor)
// and makes one external call (an *http.Client seam, fakeable in tests).
type HandlerDeps struct {
	Reporters ReporterFactory
	// Decryptor decrypts the pipeline trigger token and (via meshapi.DecryptInputs) the
	// run's sensitive inputs. Cert-based in polling mode, meshapi.NoopDecryptor in
	// single-run mode (the controller already decrypted, §2.6 k8s caveat).
	Decryptor meshapi.Decryptor
	// HTTP is the external-API seam. It MUST have redirects disabled at construction
	// (noFollowRedirectClient, G-P10) -- the persona wiring (cmd/gitlab) owns that; tests
	// swap the transport via httptest.
	HTTP *http.Client
	Log  *slog.Logger
}

// Handler is the GITLAB_PIPELINE run handler (value type, P4). It satisfies
// dispatch.RunHandler.
type Handler struct {
	cfg  Config
	deps HandlerDeps
}

// NewHandler builds the gitlab handler. A nil Log/HTTP fall back to sensible defaults so
// a minimally-wired handler is always usable (P8); a nil Decryptor defaults to
// meshapi.NoopDecryptor rather than panicking on first use -- a caller that truly needs
// decryption must wire one explicitly, exactly as it must wire a Reporters factory.
func NewHandler(cfg Config, deps HandlerDeps) Handler {
	if deps.Log == nil {
		deps.Log = slog.Default()
	}
	if deps.HTTP == nil {
		deps.HTTP = noFollowRedirectClient()
	}
	if deps.Decryptor == nil {
		deps.Decryptor = meshapi.NoopDecryptor{}
	}
	return Handler{cfg: cfg, deps: deps}
}

// Execute runs one GITLAB_PIPELINE run to completion (§4.1 skeleton, semantically = the
// Kotlin GitLabBlockRunnerService.processBlock, §2.1):
//  1. register the single "gl-trigger" step (transport error -> return wrapped error, A1
//     contract: the run stays unreported, §2.5 parity);
//  2. decode+validate the GitLab implementation (wrong/missing type -> row-C FAILED);
//  3. sanitize the base URL, decrypt the trigger token (empty -> "", T8/G-P11), and
//     decrypt the run's sensitive inputs via meshapi.DecryptInputs (any failure -> row-C
//     FAILED);
//  4. POST the multipart trigger payload; an *ExternalCallError -> row-B FAILED, any other
//     error -> row-C FAILED;
//  5. success -> the always-async IN_PROGRESS handover (D9) -- gitlab has no `async` field,
//     the handover is unconditional (umbrella §10.10/flag §16.10 of plan 06B).
//
// A row-B/row-C FAILED update is a reported run-level failure, so Execute returns nil
// after it (A1 contract) -- unless reporting THAT failure itself fails on transport,
// which propagates (§2.5 parity). No ticker, no abort handling (umbrella §7.5); ctx is
// passed to the external HTTP request (cancellation is new-but-inert, no Kotlin
// counterpart, same treatment as 06A's debug waits).
func (h Handler) Execute(ctx context.Context, run dispatch.ClaimedRun) error {
	log := h.deps.Log.With("run", run.Id)
	reporter := h.deps.Reporters(run)
	runId := string(run.Id)

	register := report.RunStatus{
		RunId: runId,
		Steps: []report.StepStatus{{Name: StepId, DisplayName: StepDisplayName}},
	}
	if err := reporter.Register(register); err != nil {
		return err
	}

	impl, err := decodeImplementation(run)
	if err != nil {
		return h.failInternal(reporter, runId, err)
	}

	baseURL, err := sanitizeBaseUrl(impl.GitlabBaseUrl)
	if err != nil {
		return h.failInternal(reporter, runId, err)
	}

	token, err := h.deps.Decryptor.Decrypt(impl.PipelineTriggerToken)
	if err != nil {
		return h.failInternal(reporter, runId, fmt.Errorf("decrypting pipeline trigger token: %w", err))
	}

	rawRunJson, err := decodeRawRunJson(run)
	if err != nil {
		return h.failInternal(reporter, runId, err)
	}
	decryptedRunJson, err := meshapi.DecryptInputs(rawRunJson, h.deps.Decryptor, log)
	if err != nil {
		return h.failInternal(reporter, runId, fmt.Errorf("decrypting run inputs: %w", err))
	}

	links := meshapi.LinksDTO{}
	if run.Details != nil {
		links = run.Details.Links
	}
	form, contentType, err := buildTriggerForm(token, impl.RefName, decryptedRunJson, links, log)
	if err != nil {
		return h.failInternal(reporter, runId, err)
	}

	if err := triggerPipeline(ctx, h.deps.HTTP, baseURL, impl.ProjectId, form, contentType); err != nil {
		var extErr *ExternalCallError
		if errors.As(err, &extErr) {
			return h.failExternal(reporter, runId, extErr)
		}
		return h.failInternal(reporter, runId, err)
	}

	return h.reportHandover(reporter, runId, impl.ProjectId)
}

// decodeImplementation unmarshals+validates the GitLab implementation (§2.1.3): a wrong
// implementation type reproduces MeshBuildingBlockRun's exact message
// ("The building block implementation of run <uuid> was not of expected type.").
func decodeImplementation(run dispatch.ClaimedRun) (meshapi.GitlabImplementation, error) {
	if run.Details == nil {
		return meshapi.GitlabImplementation{}, fmt.Errorf("run has no parsed details")
	}
	implType, err := run.Details.Spec.Definition.Spec.GetImplementationType()
	if err != nil {
		return meshapi.GitlabImplementation{}, fmt.Errorf("reading implementation type: %w", err)
	}
	if implType != meshapi.ImplTypeGitLabCICD {
		return meshapi.GitlabImplementation{}, fmt.Errorf(
			"the building block implementation of run %s was not of expected type", run.Id)
	}
	var impl meshapi.GitlabImplementation
	if err := json.Unmarshal(run.Details.Spec.Definition.Spec.Implementation, &impl); err != nil {
		return meshapi.GitlabImplementation{}, fmt.Errorf("parsing GitLab implementation: %w", err)
	}
	return impl, nil
}

// decodeRawRunJson returns the claimed run's raw bytes (still ciphertext where sensitive)
// -- the input meshapi.DecryptInputs transforms. RawJson is base64 (dispatch.ClaimedRun,
// T5); gitlab (unlike manual) has no Details-only fallback, since DecryptInputs needs the
// generic-JSON document, not just the typed projection.
func decodeRawRunJson(run dispatch.ClaimedRun) ([]byte, error) {
	if run.RawJson == "" {
		return nil, fmt.Errorf("run has no raw JSON payload")
	}
	raw, err := base64.StdEncoding.DecodeString(run.RawJson)
	if err != nil {
		return nil, fmt.Errorf("run raw JSON is not valid base64: %w", err)
	}
	return raw, nil
}

// failInternal reports the row-C FAILED update (§2.3): internal errors -- impl-type
// mismatch, URL sanitization, decrypt failures, network I/O, 3xx-read-as-error.
func (h Handler) failInternal(reporter report.Reporter, runId string, cause error) error {
	return h.reportFailed(reporter, runId,
		fmt.Sprintf("There was an internal error while trying to contact GitLab: %s", cause))
}

// failExternal reports the row-B FAILED update (§2.3): any classified GitLab HTTP
// failure (404/identity-verification/generic/undeserializable -- all four collapse into
// the SAME wire shape, G-P4/flag §16.1).
func (h Handler) failExternal(reporter report.Reporter, runId string, extErr *ExternalCallError) error {
	return h.reportFailed(reporter, runId,
		fmt.Sprintf("GitLab responded with status: %d and body: %s", extErr.StatusCode, extErr.ResponseBody))
}

func (h Handler) reportFailed(reporter report.Reporter, runId, systemMessage string) error {
	status := report.RunStatus{
		RunId:  runId,
		Status: report.FAILED,
		Steps: []report.StepStatus{{
			Name:          StepId,
			Status:        report.FAILED,
			UserMessage:   ptr(userMessageTriggerFailed),
			SystemMessage: ptr(systemMessage),
		}},
	}
	_, err := reporter.Report(status)
	return err
}

// reportHandover sends the always-async handover update (D9, §2.1.7): run IN_PROGRESS,
// step SUCCEEDED. This is gitlab's terminal action -- the runner's job ends here; the
// pipeline itself becomes the run's status source via the callback URLs it received.
func (h Handler) reportHandover(reporter report.Reporter, runId, projectId string) error {
	status := report.RunStatus{
		RunId:  runId,
		Status: report.IN_PROGRESS,
		Steps: []report.StepStatus{{
			Name:          StepId,
			Status:        report.SUCCEEDED,
			UserMessage:   ptr(userMessageHandover),
			SystemMessage: ptr(fmt.Sprintf("Triggered pipeline in project '%s'", projectId)),
		}},
	}
	_, err := reporter.Report(status)
	return err
}

func ptr[T any](v T) *T { return &v }
