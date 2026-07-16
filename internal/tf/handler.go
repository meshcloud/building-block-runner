package tf

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/dispatch"
	meshapi "github.com/meshcloud/building-block-runner/internal/meshapi"
)

// Handler is the tf in-process run handler: it implements
// dispatch.RunHandler so the tf type can run on the shared dispatch.Loop + dispatch.InProcess
// framework instead of the bespoke Manager/Worker token loop. It is the in-process counterpart
// of the k8s job template -- where KubernetesJobDispatcher hands a run to a Job pod, InProcess
// hands one claimed TERRAFORM run to Handler.Execute in its own goroutine.
//
// Handler deliberately reuses the exact per-run execution the polling Worker already runs
// (Worker.tfExecution: per-run working dir, initRunContextInfo, work/observer routines, terminal-
// status metering) so the wire behavior -- register body, PATCH status bodies, artifact upload,
// runner_* metrics -- is byte-identical to today's polling path. The only structural changes are
// the ones safe concurrency demands: each Execute builds its OWN RunApi with the run's
// own runToken (never shared across runs), so N concurrent runs can never cross-authenticate.
type Handler struct {
	workerDir            string
	timeout              time.Duration
	statusUpdateInterval time.Duration
	tfBinaries           *TfBinaries
	meter                Meter
	log                  *slog.Logger
	newRunApi            RunApiFactory
	// cfg is the execution-path config threaded into each per-run Worker.
	cfg execConfig
}

// RunApiFactory builds a run-scoped RunApi for one claimed run, authenticated with that run's
// own runToken (runToken-only auth). It is the injected seam: production wires the
// real meshapi-backed NewRunApi; tests wire one over a fake RoundTripper.
type RunApiFactory func(runToken string) RunApi

// HandlerConfig carries the tf run-execution config a handler needs, threaded explicitly
// rather than read from the AppConfig global.
type HandlerConfig struct {
	// WorkingDir is the parent dir under which each run gets its own os.MkdirTemp workdir.
	WorkingDir string
	// TfCommandTimeout bounds one run's total tofu execution.
	TfCommandTimeout time.Duration
	// InitTimeout / WsTimeout bound tofu init and workspace operations respectively.
	InitTimeout time.Duration
	WsTimeout   time.Duration
	// RunnerUuid is the runner identity: the frozen wire Source id and the run API's rid.
	RunnerUuid string
	// ApiBackend is the meshfed API connection the run-scoped RunApi authenticates against and
	// whose Url is the tf state-backend fallback URL.
	ApiBackend config.Api
	// SkipHostKeyValidation is the insecure-host-key policy threaded into each run's git source.
	SkipHostKeyValidation bool
}

// HandlerDeps are the handler's injected collaborators (main wires them).
type HandlerDeps struct {
	// TfBinaries provides the tofu/terraform binary per run (concurrency-safe, exercised by
	// tfbinaries_concurrency_test.go).
	TfBinaries *TfBinaries
	// Meter receives the runner_* series (RunClaimed/RunSucceeded/RunFailed/PollError).
	// A nil Meter is treated as NoopMeter.
	Meter Meter
	// Log is the runner type logger; Execute derives a per-run child carrying the run id.
	Log *slog.Logger
	// NewRunApi builds each run's run-scoped RunApi. Optional: nil falls back to the
	// production factory (real meshapi client with the run's runToken). Injected by tests.
	NewRunApi RunApiFactory
}

// NewHandler builds the tf handler. A nil Meter/Log fall back to NoopMeter/slog.Default so a
// minimally-wired handler is always usable. statusUpdateInterval matches the Worker's 10s.
func NewHandler(cfg HandlerConfig, deps HandlerDeps) Handler {
	log := deps.Log
	if log == nil {
		log = slog.Default()
	}
	meter := deps.Meter
	if meter == nil {
		meter = NoopMeter{}
	}
	// Diagnostic breadcrumb kept in intentionally: a zero/short timeout here is born-expired at
	// context.WithTimeout time and fails every run at init in ~1ms, so logging the resolved
	// values makes such a misconfig (e.g. an unset HandlerConfig field defaulting to 0) obvious.
	log.Debug("tf handler timeouts resolved", "command", cfg.TfCommandTimeout, "init", cfg.InitTimeout, "ws", cfg.WsTimeout)
	newRunApi := deps.NewRunApi
	if newRunApi == nil {
		// The production run-scoped RunApi factory: a fresh RunApi (its own runToken, never
		// shared across concurrent runs) built from the threaded API backend + runner uuid, so
		// all run-scoped reporting authenticates as the run, not the runner's claim credentials.
		apiBackend := cfg.ApiBackend
		runnerUuid := cfg.RunnerUuid
		newRunApi = func(runToken string) RunApi {
			return NewRunApi(apiBackend, runnerUuid, runToken)
		}
	}
	return Handler{
		workerDir:            cfg.WorkingDir,
		timeout:              cfg.TfCommandTimeout,
		statusUpdateInterval: 10 * time.Second,
		tfBinaries:           deps.TfBinaries,
		meter:                meter,
		log:                  log,
		newRunApi:            newRunApi,
		cfg: execConfig{
			RunnerUuid:            cfg.RunnerUuid,
			ApiBackendUrl:         cfg.ApiBackend.Url,
			SkipHostKeyValidation: cfg.SkipHostKeyValidation,
			TfCommandTimeout:      cfg.TfCommandTimeout,
			InitTimeout:           cfg.InitTimeout,
			WsTimeout:             cfg.WsTimeout,
		},
	}
}

// Execute runs one claimed TERRAFORM run to completion (dispatch.RunHandler). It derives the
// tf-specific interpretation (Behavior, TerraformImplementation, GitSource, Vars) of the
// claimed meshapi.Run -- already decrypted at the claim boundary (rundecrypt.Wrap) -- builds a
// run-scoped RunApi authenticated with the run's own runToken, and drives the exact
// Worker.tfExecution machinery.
//
// A mapping failure (bad behavior, unparseable implementation, or an SSH-key decrypt failure) is
// returned as an out-of-band error WITHOUT reporting a status -- byte-for-byte the historic
// polling behavior, where such a failure surfaced inside the old FetchRunDetails and was handled
// as a fetch error (backoff, no status report; the coordinator times the run out). Once mapping
// succeeds the run is metered as claimed and Worker.tfExecution owns the terminal status +
// success/failure metering; Execute now propagates tfExecution's own return value, which is
// non-nil only for its pre-flight error class (workdir/logs-dir/run-context setup, registration --
// see tfExecution's doc comment), never for a failed tofu apply/destroy. This is wire-neutral for
// dispatch: InProcess.execute (internal/dispatch/inprocess.go) only logs a non-nil error, it does
// not report a status itself.
//
// ctx is the InProcess shutdown context: it is threaded into Worker.tfExecution, which
// derives the run's execution deadline from it, so a SIGINT/SIGTERM that outlives the configured
// RUNNER_SHUTDOWN_GRACE cancels any in-flight tofu command and the run is reported ABORTED
// instead of finishing on its own TfCommandTimeout.
func (h Handler) Execute(ctx context.Context, cr dispatch.ClaimedRun) error {
	log := h.log.With("runId", cr.Id)

	behavior, err := BehaviorFor(cr.Run)
	if err != nil {
		return err
	}
	impl, err := ParseTerraformImplementation(cr.Run)
	if err != nil {
		return err
	}
	source := terraformImplToGitSource(&impl)
	vars := VariablesFor(cr.Run.Spec.BuildingBlock.Spec.Inputs)

	h.meter.RunClaimed()

	// Each run gets its OWN RunApi instance with its OWN runToken: concurrent runs never
	// share a mutable token, so run A can never report under run B's credentials.
	api := h.newRunApi(cr.Run.Spec.RunToken)

	worker := &Worker{
		workerNumber:         1,
		workerDir:            h.workerDir,
		timeout:              h.timeout,
		runApi:               api,
		tfBinaries:           h.tfBinaries,
		log:                  log,
		statusUpdateInterval: h.statusUpdateInterval,
		meter:                h.meter,
		cfg:                  h.cfg,
	}
	// cr.RawJson (the decrypted claim bytes' base64, on both the polling and single-run paths)
	// is handed to the pre-run script verbatim -- the meshfed run object 1:1, rather than a
	// re-serialization of cr.Run that would silently drop any field meshapi.Run does not model.
	return worker.tfExecution(ctx, cr.Run, behavior, impl, source, vars, cr.RawJson)
}

// NewClaimClassifier builds the tf type's dispatch.ClaimClassifier, reproducing the former
// Worker.handleFetchRunError policy: a 404 is the normal idle poll (no
// log, no backoff -- the loop waits one PollInterval == the old NORUN_WORKER_DELAY of 10s); a
// 409 conflict is likewise treated as no-run; the specific chunked-transfer-encoding transport
// glitch meshfed occasionally emits when proxying is swallowed as no-run; any other fetch error
// increments the poll-error meter and backs off (the loop's ClaimBackoff == the old
// FAILED_WORKER_DELAY of 60s).
func NewClaimClassifier(meter Meter) dispatch.ClaimClassifier {
	if meter == nil {
		meter = NoopMeter{}
	}
	return func(err error) dispatch.ClaimOutcome {
		if he, ok := meshapi.AsHttpError(err); ok {
			switch {
			case he.IsNotFound():
				return dispatch.OutcomeNoRun
			case he.IsConflict():
				return dispatch.OutcomeNoRunLogged
			default:
				meter.PollError()
				return dispatch.OutcomeBackoff
			}
		}
		// golang's net/http disallows multiple Transfer-Encoding headers, but meshfed can emit
		// them when proxying run requests to coordinator; skip the error-wait and keep polling.
		if strings.Contains(err.Error(), "transport connection broken: too many transfer encodings: [\"chunked\" \"chunked\"]") {
			return dispatch.OutcomeNoRun
		}
		meter.PollError()
		return dispatch.OutcomeBackoff
	}
}
