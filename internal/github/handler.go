package github

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/meshcloud/building-block-runner/internal/dispatch"
	"github.com/meshcloud/building-block-runner/internal/meshapi"
	"github.com/meshcloud/building-block-runner/internal/report"
)

// ReporterFactory builds a run-scoped report.Reporter for one claimed run (runToken-only
// auth underneath — the handler never touches the runner's claim credentials). The
// injected seam: cmd/github wires the real meshapi-backed factory; tests wire a
// capturing fake.
type ReporterFactory func(run dispatch.ClaimedRun) report.Reporter

// HandlerDeps are the github handler's injected collaborators. Nil Clock/HTTP/Log
// fall back to sensible defaults. Decryption already happened at the dispatch boundary
// (internal/rundecrypt), so the handler itself no longer decrypts anything.
type HandlerDeps struct {
	Reporters ReporterFactory
	// HTTP is the external-API seam. It defaults to the shared singleton
	// (meshapi.SharedHTTPClient); redirects are disabled per-request on the dispatch call
	// (meshapi.WithNoRedirect), not at client construction. Tests swap the transport via
	// httptest.
	HTTP  *http.Client
	Clock Clock        // JWT claims + find/poll waits
	Log   *slog.Logger // D15
}

// Handler is the GITHUB_WORKFLOW run handler (value type). It satisfies
// dispatch.RunHandler. Timeout/find constants live here as constructor defaults.
type Handler struct {
	cfg  Config
	deps HandlerDeps

	findAttempts int
	pollInterval time.Duration
	pollTimeout  time.Duration
	findBuffer   time.Duration
}

// NewHandler builds the github handler with the frozen constructor-default constants
// (findAttempts=12, pollInterval=10s, pollTimeout=30m). A nil Clock/HTTP/Log fall back to
// defaults.
func NewHandler(cfg Config, deps HandlerDeps) Handler {
	if deps.Clock == nil {
		deps.Clock = RealClock{}
	}
	if deps.Log == nil {
		deps.Log = slog.Default()
	}
	if deps.HTTP == nil {
		deps.HTTP = meshapi.SharedHTTPClient()
	}
	return Handler{
		cfg:          cfg,
		deps:         deps,
		findAttempts: defaultFindAttempts,
		pollInterval: defaultPollInterval,
		pollTimeout:  defaultPollTimeout,
		findBuffer:   findRunBufferWindow,
	}
}

// Execute runs one GITHUB_WORKFLOW run: register gh-trigger → parse impl → sanitize
// base URL → decrypt appPem + mint JWT → installation id → installation token (permission
// gate) → select workflow by behavior → build inputs → dispatch → success update or FAILED
// → async return (IN_PROGRESS handover) / sync poll → nil. Follows the RunHandler contract:
// a run reported FAILED returns nil; only a register/report transport
// failure (or a failed FAILED-report) returns a non-nil error.
func (h Handler) Execute(ctx context.Context, run dispatch.ClaimedRun) error {
	log := h.deps.Log.With("runId", run.Id)
	reporter := h.deps.Reporters(run)

	// Register gh-trigger BEFORE the implementation is parsed: register
	// failures propagate (infrastructure).
	register := report.RunStatus{
		RunId: string(run.Id),
		Steps: []report.StepStatus{{Name: StepId, DisplayName: StepDisplayName}},
	}
	if err := reporter.Register(register); err != nil {
		return err
	}

	if run.Run == nil {
		return h.failRun(reporter, string(run.Id), genericErrorMessage("run details are missing"))
	}

	// Parse GithubImplementation; a wrong type is the Kotlin IllegalStateException path
	// (:43-48, generic).
	implType, err := run.Run.Spec.Definition.Spec.GetImplementationType()
	if err != nil {
		return h.failRun(reporter, string(run.Id), systemMessageForError(err))
	}
	if implType != meshapi.ImplTypeGitHubWorkflow {
		msg := genericErrorMessage(fmt.Sprintf("The building block implementation of run %s was not of expected type.", run.Run.Metadata.Uuid))
		return h.failRun(reporter, string(run.Id), msg)
	}
	var impl meshapi.GithubImplementation
	if err := unmarshalImpl(run.Run.Spec.Definition.Spec.Implementation, &impl); err != nil {
		return h.failRun(reporter, string(run.Id), systemMessageForError(err))
	}

	// Sanitize base URL (factory errors ⇒ generic, :50-57).
	baseURL, err := sanitizeBaseUrl(impl.GithubBaseUrl)
	if err != nil {
		return h.failRun(reporter, string(run.Id), systemMessageForError(err))
	}

	// Auth chain (:59-95): decrypt appPem → mint JWT → installation id → installation token.
	appJWT, err := h.mintAppToken(impl)
	if err != nil {
		return h.failRun(reporter, string(run.Id), systemMessageForError(err))
	}

	gc := newGithubClient(baseURL, h.deps.HTTP, meshapi.SlogLogger(log))

	installID, err := gc.installationId(appJWT, impl.Owner, impl.Repository)
	if err != nil {
		return h.failRun(reporter, string(run.Id), systemMessageForError(err))
	}
	installToken, err := gc.installationToken(appJWT, installID)
	if err != nil {
		return h.failRun(reporter, string(run.Id), systemMessageForError(err))
	}

	// Workflow selection (frozen, :97-109). Only destroyWorkflow is nullable.
	workflow, err := selectWorkflow(run.Run.Spec.Behavior, impl)
	if err != nil {
		return h.failRun(reporter, string(run.Id), nullWorkflowMessage)
	}

	// Build the dispatch inputs from the run (inputs already decrypted at the dispatch
	// boundary). One WARN surfaces the forwarded sensitive keys before they leave the process.
	inputs := decodeInputs(run.RawJson, run.Run, log)
	if run.Run != nil {
		if sensitiveKeys := meshapi.SensitiveInputKeys(run.Run.Spec.BuildingBlock.Spec.Inputs); len(sensitiveKeys) > 0 {
			log.Warn("forwarding sensitive inputs to the GitHub workflow dispatch", "sensitiveInputKeys", sensitiveKeys)
		}
	}
	dr := decryptedRun{details: run.Run, inputs: inputs}
	inputMap, err := dispatchInputs(dr, impl)
	if err != nil {
		return h.failRun(reporter, string(run.Id), systemMessageForError(err))
	}

	// Dispatch (:111-149).
	result, err := gc.dispatchWorkflow(installToken, impl.Owner, impl.Repository, workflow, dispatchPayload{Ref: impl.Branch, Inputs: inputMap}, recognizedUnsupportedInputs)
	if err != nil {
		return h.failRun(reporter, string(run.Id), systemMessageForError(err))
	}
	switch result.outcome {
	case dispatchSuccess:
		// fall through below
	case dispatchUnsupportedInput:
		return h.failRun(reporter, string(run.Id), unsupportedInputsMessage(workflow, result.unsupportedNames, impl.OmitRunObjectInput))
	case dispatchAPIError:
		return h.failRun(reporter, string(run.Id), triggerApiErrorMessage(result.statusCode, result.body))
	}

	// Trigger-success update: IN_PROGRESS + gh-trigger SUCCEEDED.
	abort, err := h.reportTriggerSuccess(reporter, string(run.Id), workflow, impl.Async)
	if err != nil {
		return err
	}
	if abort {
		// Backend-requested abort (T1) on the trigger-success response: covers both
		// async's no-op handover window and a pre-poll abort in sync mode.
		return h.sendAborted(reporter, string(run.Id))
	}

	if impl.Async {
		// handover: run stays IN_PROGRESS, the workflow reports back via the API.
		log.Info("triggered GitHub Action (async); handing over to API callback", "workflow", workflow)
		return nil
	}

	// Sync: poll the run/jobs to completion. A reported FAILED still returns nil;
	// only a report transport failure returns non-nil.
	return h.pollWorkflow(ctx, reporter, gc, impl, string(run.Id), workflow, installToken, h.deps.Clock.Now())
}

// recognizedUnsupportedInputs is the set of input names the 422 heuristic tests for:
// the run-object/URL names plus the two system tokens (matches the Kotlin set at :196-201 —
// MESHSTACK_ENDPOINT is deliberately NOT included there).
var recognizedUnsupportedInputs = []string{inputKeyRunUrl, inputKeyRunObject, inputKeyApiToken, inputKeyRunToken}

// mintAppToken parses the PKCS#1 key (already decrypted at the dispatch boundary) and
// mints the App JWT.
func (h Handler) mintAppToken(impl meshapi.GithubImplementation) (string, error) {
	key, err := parseAppPem(impl.AppPem)
	if err != nil {
		return "", fmt.Errorf("parsing appPem: %w", err)
	}
	tok, err := appToken(h.deps.Clock, impl.AppId, key)
	if err != nil {
		return "", fmt.Errorf("minting app JWT: %w", err)
	}
	return tok, nil
}

// selectWorkflow maps behavior→workflow (frozen): APPLY/DETECT→applyWorkflow,
// DESTROY→destroyWorkflow. A null destroyWorkflow is the "Workflow file name must not be
// null" path.
func selectWorkflow(behavior string, impl meshapi.GithubImplementation) (string, error) {
	switch behavior {
	case behaviorApply, behaviorDetect:
		return impl.ApplyWorkflow, nil
	case behaviorDestroy:
		if impl.DestroyWorkflow == nil {
			return "", fmt.Errorf("null destroy workflow")
		}
		return *impl.DestroyWorkflow, nil
	default:
		// Unknown behavior: treat like APPLY selection (behavior is a frozen enum upstream).
		return impl.ApplyWorkflow, nil
	}
}

// reportTriggerSuccess sends the IN_PROGRESS trigger-success update. It returns the
// reporter's abort signal so Execute can override with a terminal ABORTED (T1) instead of
// proceeding to the async handover or the sync poll.
func (h Handler) reportTriggerSuccess(reporter report.Reporter, runID, workflow string, async bool) (bool, error) {
	user, system := triggerSuccessMessages(workflow, async)
	status := report.RunStatus{
		RunId:  runID,
		Status: report.IN_PROGRESS,
		Steps: []report.StepStatus{{
			Name:          StepId,
			DisplayName:   StepDisplayName,
			Status:        report.SUCCEEDED,
			UserMessage:   ptr(user),
			SystemMessage: ptr(system),
		}},
	}
	return reporter.Report(status)
}

// failRun is the single FAILED-update funnel (replacing the three Kotlin
// updateFailed… helpers): SourceUpdate{FAILED, [gh-trigger FAILED, user "Could not trigger
// the GitHub Action", system <message>]}. It returns the report transport error (nil on
// success) — Execute propagates it so a failed FAILED-report exits single-run non-zero.
func (h Handler) failRun(reporter report.Reporter, runID, systemMessage string) error {
	status := report.RunStatus{
		RunId:  runID,
		Status: report.FAILED,
		Steps: []report.StepStatus{{
			Name:          StepId,
			DisplayName:   StepDisplayName,
			Status:        report.FAILED,
			UserMessage:   ptr(failUserMessage),
			SystemMessage: ptr(systemMessage),
		}},
	}
	_, err := reporter.Report(status)
	return err
}

func ptr(s string) *string { return &s }

// unmarshalImpl parses the raw implementation JSON into a GithubImplementation. appPem and
// sensitive fields ride as ciphertext strings and are decrypted later (handler-side).
func unmarshalImpl(raw []byte, impl *meshapi.GithubImplementation) error {
	if err := json.Unmarshal(raw, impl); err != nil {
		return fmt.Errorf("parsing GitHub implementation: %w", err)
	}
	return nil
}
