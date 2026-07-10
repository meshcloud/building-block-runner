package azdevops

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/meshcloud/building-block-runner/internal/dispatch"
	"github.com/meshcloud/building-block-runner/internal/meshapi"
	"github.com/meshcloud/building-block-runner/internal/report"
)

// pollInterval and pollBudget are the frozen Kotlin poll constants (§4.4): constructor
// defaults, not a config surface (Kotlin has none either) -- overridable only by tests via a
// fake Clock.
const (
	defaultPollInterval = 10 * time.Second
	defaultPollBudget   = 30 * time.Minute
)

// HandlerDeps are the azdevops handler's injected collaborators (06A §4.1 HandlerDeps
// pattern, grown per §4.6/§17: + Decryptor, HTTP, Clock -- the shape 06A's own comment
// anticipated for exactly this consumer).
type HandlerDeps struct {
	// Reporters builds a per-run report.Reporter (runToken-only, risk #5).
	Reporters ReporterFactory
	// Decryptor decrypts the PAT and sensitive inputs: cert-based in polling mode, NoOp in
	// single-run mode (the controller has already decrypted).
	Decryptor meshapi.Decryptor
	// HTTP is the external-API HTTP seam (real client with redirect/timeout policy in
	// production, a fake-transport client in tests, §4.2).
	HTTP *http.Client
	// Clock governs the 10s poll wait and the 30-min timeout budget (fake in tests).
	Clock Clock
	// Log receives per-run diagnostics; a nil Log falls back to slog.Default().
	Log *slog.Logger

	pollInterval time.Duration // test-only override; zero uses defaultPollInterval
	pollBudget   time.Duration // test-only override; zero uses defaultPollBudget
}

// Handler is the AZURE_DEVOPS_PIPELINE run handler (value type, P4). It satisfies
// dispatch.RunHandler.
type Handler struct {
	cfg  Config
	deps HandlerDeps
}

// NewHandler builds the azdevops handler. Nil Decryptor/HTTP/Clock/Log fall back to usable
// defaults (P8) -- Decryptor defaults to NoopDecryptor (matching "no key configured" being a
// valid single-run-mode wiring), HTTP to a real client with the standard timeout, Clock to
// RealClock, Log to slog.Default().
func NewHandler(cfg Config, deps HandlerDeps) Handler {
	if deps.Decryptor == nil {
		deps.Decryptor = meshapi.NoopDecryptor{}
	}
	if deps.HTTP == nil {
		deps.HTTP = NewHTTPClient(adoHTTPTimeout)
	}
	if deps.Clock == nil {
		deps.Clock = RealClock{}
	}
	if deps.Log == nil {
		deps.Log = slog.Default()
	}
	if deps.pollInterval <= 0 {
		deps.pollInterval = defaultPollInterval
	}
	if deps.pollBudget <= 0 {
		deps.pollBudget = defaultPollBudget
	}
	return Handler{cfg: cfg, deps: deps}
}

// Execute runs one AZURE_DEVOPS_PIPELINE run to completion (§2.1): register the trigger
// step, parse the implementation, decrypt the PAT + sensitive inputs, trigger the pipeline,
// report the trigger-success update, then either return (async handover, D9) or poll to a
// terminal report (sync). Every rung of the failure ladder (§2.1.3) reports run FAILED + step
// FAILED and returns nil (A1: a run-level failure was reported, not an infrastructure
// failure); only a register/report transport failure returns a non-nil error (the run stays
// unreported, Kotlin parity §2.7).
func (h Handler) Execute(ctx context.Context, run dispatch.ClaimedRun) error {
	log := h.deps.Log.With("run", run.Id)
	reporter := h.deps.Reporters(run)
	runId := string(run.Id)

	if err := reporter.Register(registerStatus(runId)); err != nil {
		return err
	}

	impl, err := parseImplementation(run)
	if err != nil {
		return h.reportFailure(reporter, runId, failureMessage(err))
	}

	pat, err := h.deps.Decryptor.Decrypt(impl.PersonalAccessToken)
	if err != nil {
		return h.reportFailure(reporter, runId, failureMessage(fmt.Errorf("decrypting personal access token: %w", err)))
	}

	inputs, err := readInputs(run, log)
	if err != nil {
		return h.reportFailure(reporter, runId, failureMessage(fmt.Errorf("reading run inputs: %w", err)))
	}
	inputs, err = meshapi.DecryptInputs(h.deps.Decryptor, log, inputs)
	if err != nil {
		return h.reportFailure(reporter, runId, failureMessage(err))
	}

	client := newADOClient(impl.AzureDevOpsBaseUrl, impl.Organization, impl.Project, impl.PipelineId, pat, h.deps.HTTP)
	params := buildTemplateParameters(inputs, run.Details.Spec.Behavior)

	pr, err := client.TriggerPipeline(ctx, params, impl.RefName)
	if err != nil {
		return h.reportFailure(reporter, runId, failureMessage(err))
	}

	if _, err := reporter.Report(triggerSuccessUpdate(runId, pr, impl.Async)); err != nil {
		return err
	}

	if impl.Async {
		return nil
	}

	return h.pollCompletion(ctx, client, reporter, runId, pr)
}

// reportFailure sends the U-P8 failed update. It returns nil when the report itself
// succeeds (the business failure was reported terminally, A1); a non-nil return means the
// report/PATCH itself failed (an infrastructure failure that propagates, Kotlin parity).
func (h Handler) reportFailure(reporter report.Reporter, runId, message string) error {
	_, err := reporter.Report(failedUpdate(runId, message))
	return err
}

// failureMessage picks the U-P8 message pair: an ExternalCallError (a non-2xx ADO response)
// renders the request/status/body form; any other error renders the generic
// internal-error form (§2.1.3/§2.7).
func failureMessage(err error) string {
	var extErr ExternalCallError
	if errors.As(err, &extErr) {
		return fmt.Sprintf("Request: %s\nAzure DevOps responded with status: %d and body: %s",
			extErr.RequestUrl, extErr.StatusCode, extErr.ResponseBody)
	}
	return fmt.Sprintf("There was an internal error while trying to contact Azure DevOps: %s", err.Error())
}

// parseImplementation reads + type-checks the run's implementation (§2.1.2/§4.5). The
// wrong-implementation-type check is defense-in-depth (A-P3's first rung): the dispatch loop
// already routes by resolved type before a handler is ever invoked, so this branch is
// structurally unreachable via normal dispatch, but is kept (and tested directly) because it
// is a pinned Kotlin behavior (IllegalStateException, MeshBuildingBlockRun.kt:133-139).
func parseImplementation(run dispatch.ClaimedRun) (meshapi.AzureDevOpsImplementation, error) {
	if run.Details == nil {
		return meshapi.AzureDevOpsImplementation{}, fmt.Errorf("run has no details")
	}

	implType, err := run.Details.Spec.Definition.Spec.GetImplementationType()
	if err != nil {
		return meshapi.AzureDevOpsImplementation{}, fmt.Errorf("determining implementation type: %w", err)
	}
	if implType != meshapi.ImplTypeAzureDevOps {
		return meshapi.AzureDevOpsImplementation{}, fmt.Errorf(
			"the building block implementation of run %s was not of expected type", run.Id)
	}

	var impl meshapi.AzureDevOpsImplementation
	if err := json.Unmarshal(run.Details.Spec.Definition.Spec.Implementation, &impl); err != nil {
		return meshapi.AzureDevOpsImplementation{}, fmt.Errorf("parsing azure devops implementation: %w", err)
	}
	return impl, nil
}

// buildTemplateParameters builds the trigger POST's templateParameters (§2.5/A-P4):
// non-environment inputs stringified via renderValue, environment inputs excluded entirely
// (no gitlab-style `variables` channel exists here), plus MESHSTACK_BEHAVIOR overwriting any
// same-keyed user input.
func buildTemplateParameters(inputs []meshapi.BuildingBlockInputSpecDTO, behavior string) map[string]string {
	params := make(map[string]string, len(inputs)+1)
	for _, in := range inputs {
		if in.Env {
			continue
		}
		params[in.Key] = renderValue(in.Value)
	}
	params["MESHSTACK_BEHAVIOR"] = behavior
	return params
}

// rawInputs is the minimal projection of a run JSON needed to read inputs with UseNumber
// fidelity (C2/§4.2 -- required for renderValue's numeric-literal rendering). Duplicated from
// the 06A template pattern (manual.decodeInputs/rawInputs): a sibling persona package must
// not import another (D11, manual/outputtype.go precedent).
type rawInputs struct {
	Spec struct {
		BuildingBlock struct {
			Spec struct {
				Inputs []meshapi.BuildingBlockInputSpecDTO `json:"inputs"`
			} `json:"spec"`
		} `json:"buildingBlock"`
	} `json:"spec"`
}

// readInputs reads the run's inputs with number fidelity: the raw claimed/file bytes are
// re-decoded with json.Decoder.UseNumber() so large/exotic numeric literals round-trip
// byte-faithfully into renderValue instead of being float64-ized by default encoding/json.
// Falls back to the already-parsed Details when RawJson is empty (defensive).
func readInputs(run dispatch.ClaimedRun, log *slog.Logger) ([]meshapi.BuildingBlockInputSpecDTO, error) {
	if run.RawJson != "" {
		raw, err := base64.StdEncoding.DecodeString(run.RawJson)
		if err != nil {
			log.Warn("run raw JSON is not valid base64; using parsed details for inputs", "err", err)
		} else {
			var parsed rawInputs
			dec := json.NewDecoder(bytes.NewReader(raw))
			dec.UseNumber()
			if err := dec.Decode(&parsed); err != nil {
				return nil, err
			}
			return parsed.Spec.BuildingBlock.Spec.Inputs, nil
		}
	}
	if run.Details != nil {
		return run.Details.Spec.BuildingBlock.Spec.Inputs, nil
	}
	return nil, nil
}
