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

// HandlerDeps are the gitlab handler's injected collaborators: unlike manual, gitlab
// makes one external call (an *http.Client seam, fakeable in tests). Decryption already
// happened at the dispatch boundary, so the handler itself no longer decrypts anything.
type HandlerDeps struct {
	Reporters ReporterFactory
	// HTTP is the external-API seam. It defaults to the shared singleton
	// (meshapi.SharedHTTPClient); redirects are disabled per-request on the trigger call
	// (meshapi.WithNoRedirect), not at client construction. Tests swap the transport via
	// httptest.
	HTTP *http.Client
	Log  *slog.Logger
}

// Handler is the GITLAB_PIPELINE run handler (value type). It satisfies
// dispatch.RunHandler.
type Handler struct {
	cfg  Config
	deps HandlerDeps
}

// NewHandler builds the gitlab handler. A nil Log/HTTP fall back to sensible defaults so
// a minimally-wired handler is always usable -- a caller that truly needs a custom HTTP
// client must wire one explicitly, exactly as it must wire a Reporters factory.
func NewHandler(cfg Config, deps HandlerDeps) Handler {
	if deps.Log == nil {
		deps.Log = slog.Default()
	}
	if deps.HTTP == nil {
		deps.HTTP = meshapi.SharedHTTPClient()
	}
	return Handler{cfg: cfg, deps: deps}
}

// Execute runs one GITLAB_PIPELINE run to completion (semantically = the
// Kotlin GitLabBlockRunnerService.processBlock):
//  1. register the single "gl-trigger" step (transport error -> return wrapped error:
//     the run stays unreported);
//  2. decode+validate the GitLab implementation (wrong/missing type -> row-C FAILED);
//  3. sanitize the base URL and strip the run's implementation object via
//     meshapi.SanitizeRunObjectForHandover (the trigger token and run inputs arrive
//     already decrypted at the dispatch boundary; any failure -> row-C FAILED);
//  4. POST the multipart trigger payload; an *ExternalCallError -> row-B FAILED, any other
//     error -> row-C FAILED;
//  5. success -> the always-async IN_PROGRESS handover -- gitlab has no `async` field,
//     the handover is unconditional.
//
// A row-B/row-C FAILED update is a reported run-level failure, so Execute returns nil
// after it -- unless reporting THAT failure itself fails on transport,
// which propagates. No ticker; gitlab has no poll loop to interrupt, so it honors a
// backend-requested runAborted (T1) on the handover response by reporting terminal ABORTED
// instead of leaving the run handed-over IN_PROGRESS, rather than by cancelling a
// context. ctx is passed to the external HTTP request (cancellation is new-but-inert, no
// Kotlin counterpart, same treatment as the manual type's debug waits).
func (h Handler) Execute(ctx context.Context, run dispatch.ClaimedRun) error {
	log := h.deps.Log.With("runId", run.Id)
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

	rawRunJson, err := decodeRawRunJson(run)
	if err != nil {
		return h.failInternal(reporter, runId, err)
	}
	sanitizedRunJson, err := meshapi.SanitizeRunObjectForHandover(rawRunJson)
	if err != nil {
		return h.failInternal(reporter, runId, fmt.Errorf("sanitizing run JSON for handover: %w", err))
	}

	if sensitiveKeys := meshapi.SensitiveInputKeys(run.Details.Spec.BuildingBlock.Spec.Inputs); len(sensitiveKeys) > 0 {
		log.Warn("forwarding sensitive inputs as GitLab pipeline variables", "sensitiveInputKeys", sensitiveKeys)
	}

	links := meshapi.LinksDTO{}
	if run.Details != nil {
		links = run.Details.Links
	}
	form, contentType, err := buildTriggerForm(impl.PipelineTriggerToken, impl.RefName, sanitizedRunJson, links, log)
	if err != nil {
		return h.failInternal(reporter, runId, err)
	}

	if err := triggerPipeline(ctx, h.deps.HTTP, meshapi.SlogLogger(log), baseURL, impl.ProjectId, form, contentType); err != nil {
		// A cancelled ctx (shutdown grace expiry, or a standalone k8s Job receiving SIGTERM)
		// interrupting the trigger is not a GitLab failure: report the run terminal ABORTED, like
		// tf/azdevops/github do on cancellation, rather than a spurious FAILED. Checked before the
		// HTTP-error classification since a cancelled request never carries a classified status.
		if ctx.Err() != nil {
			return reportAborted(reporter, runId)
		}
		var extErr *ExternalCallError
		if errors.As(err, &extErr) {
			return h.failExternal(reporter, runId, extErr)
		}
		return h.failInternal(reporter, runId, err)
	}

	abort, err := h.reportHandover(reporter, runId, impl.ProjectId)
	if err != nil {
		return err
	}
	if abort {
		return reportAborted(reporter, runId)
	}
	return nil
}

// decodeImplementation unmarshals+validates the GitLab implementation: a wrong
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

// decodeRawRunJson returns the claimed run's raw bytes (already decrypted at the dispatch
// boundary) -- the input meshapi.SanitizeRunObjectForHandover transforms. RawJson is base64
// (dispatch.ClaimedRun); gitlab (unlike manual) has no Details-only fallback, since
// SanitizeRunObjectForHandover needs the generic-JSON document, not just the typed
// projection.
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

// failInternal reports the row-C FAILED update: internal errors -- impl-type
// mismatch, URL sanitization, decrypt failures, network I/O, 3xx-read-as-error.
func (h Handler) failInternal(reporter report.Reporter, runId string, cause error) error {
	return h.reportFailed(reporter, runId,
		fmt.Sprintf("There was an internal error while trying to contact GitLab: %s", cause))
}

// failExternal reports the row-B FAILED update: any classified GitLab HTTP
// failure (404/identity-verification/generic/undeserializable -- all four collapse into
// the SAME wire shape).
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

// reportHandover sends the always-async handover update: run IN_PROGRESS,
// step SUCCEEDED. This is gitlab's terminal action -- the runner's job ends here; the
// pipeline itself becomes the run's status source via the callback URLs it received. It
// returns the reporter's abort signal so Execute can override this handover with a terminal
// ABORTED (T1) instead of leaving the run stuck IN_PROGRESS.
func (h Handler) reportHandover(reporter report.Reporter, runId, projectId string) (bool, error) {
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
	return reporter.Report(status)
}

// reportAborted sends the terminal ABORTED update for a run the backend flagged via
// runAborted (T1) on the handover response -- gitlab's only abort window, since it has no
// poll loop to interrupt: once handed over, the pipeline itself becomes the run's status
// source.
func reportAborted(reporter report.Reporter, runId string) error {
	status := report.RunStatus{
		RunId:  runId,
		Status: report.ABORTED,
		Steps: []report.StepStatus{{
			Name:          StepId,
			Status:        report.ABORTED,
			UserMessage:   ptr(userMessageAborted),
			SystemMessage: ptr("The backend flagged this run as aborted before the handover took effect."),
		}},
	}
	_, err := reporter.Report(status)
	return err
}

func ptr[T any](v T) *T { return &v }
