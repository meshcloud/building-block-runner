package tf

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/meshcloud/building-block-runner/internal/dispatch"
	meshapi "github.com/meshcloud/building-block-runner/internal/meshapi"
)

// Handler is the tf in-process run handler (PLAN_DETAIL_05 step 5): it implements
// dispatch.RunHandler so the tf persona can run on the shared dispatch.Loop + dispatch.InProcess
// framework instead of the bespoke Manager/Worker token loop. It is the in-process counterpart
// of the k8s job template -- where KubernetesJobDispatcher hands a run to a Job pod, InProcess
// hands one claimed TERRAFORM run to Handler.Execute in its own goroutine.
//
// Handler deliberately reuses the exact per-run execution the polling Worker already runs
// (Worker.tfExecution: per-run working dir, initRunContextInfo, work/observer routines, terminal-
// status metering) so the wire behavior -- register body, PATCH status bodies, artifact upload,
// runner_* metrics -- is byte-identical to today's polling path. The only structural changes are
// the ones H5/§8 mandate for safe concurrency: each Execute builds its OWN RunApi with the run's
// own runToken (never shared across runs), so N concurrent runs can never cross-authenticate.
type Handler struct {
	workerDir            string
	timeout              time.Duration
	statusUpdateInterval time.Duration
	tfBinaries           *TfBinaries
	dec                  Decryptor
	meter                Meter
	log                  *slog.Logger
	newRunApi            RunApiFactory
}

// RunApiFactory builds a run-scoped RunApi for one claimed run, authenticated with that run's
// own runToken (runToken-only auth, H5). It is the injected seam (P3): production wires the
// real meshapi-backed NewRunApi; tests wire one over a fake RoundTripper.
type RunApiFactory func(dec Decryptor, runToken string) RunApi

// defaultRunApiFactory builds the production run-scoped RunApi: a fresh RunApi (its own
// runToken slot, never shared across concurrent runs) with the run's runToken set so all
// run-scoped reporting authenticates as the run, not the runner's claim credentials.
func defaultRunApiFactory(dec Decryptor, runToken string) RunApi {
	api := NewRunApi(dec)
	api.SetRunToken(runToken)
	return api
}

// HandlerConfig carries the tf run-execution config a handler needs, threaded explicitly (P3)
// rather than read from the AppConfig global.
type HandlerConfig struct {
	// WorkingDir is the parent dir under which each run gets its own os.MkdirTemp workdir.
	WorkingDir string
	// TfCommandTimeoutMins bounds one run's total tofu execution.
	TfCommandTimeoutMins int
}

// HandlerDeps are the handler's injected collaborators (D11: main wires them).
type HandlerDeps struct {
	// TfBinaries provides the tofu/terraform binary per run (concurrency-safe, exercised by
	// tfbinaries_concurrency_test.go).
	TfBinaries *TfBinaries
	// Decryptor decrypts sensitive inputs + the SSH key while mapping each run (polling
	// semantics -- certDecryptor). Shared and read-only, safe to reuse across runs.
	Decryptor Decryptor
	// Meter receives the D12 runner_* series (RunClaimed/RunSucceeded/RunFailed/PollError).
	// A nil Meter is treated as NoopMeter.
	Meter Meter
	// Log is the persona logger; Execute derives a per-run child carrying the run id.
	Log *slog.Logger
	// NewRunApi builds each run's run-scoped RunApi. Optional: nil falls back to the
	// production factory (real meshapi client with the run's runToken). Injected by tests.
	NewRunApi RunApiFactory
}

// NewHandler builds the tf handler. A nil Meter/Log fall back to NoopMeter/slog.Default so a
// minimally-wired handler is always usable (P8). statusUpdateInterval matches the Worker's 10s.
func NewHandler(cfg HandlerConfig, deps HandlerDeps) Handler {
	log := deps.Log
	if log == nil {
		log = slog.Default()
	}
	meter := deps.Meter
	if meter == nil {
		meter = NoopMeter{}
	}
	newRunApi := deps.NewRunApi
	if newRunApi == nil {
		newRunApi = defaultRunApiFactory
	}
	return Handler{
		workerDir:            cfg.WorkingDir,
		timeout:              time.Minute * time.Duration(cfg.TfCommandTimeoutMins),
		statusUpdateInterval: 10 * time.Second,
		tfBinaries:           deps.TfBinaries,
		dec:                  deps.Decryptor,
		meter:                meter,
		log:                  log,
		newRunApi:            newRunApi,
	}
}

// Execute runs one claimed TERRAFORM run to completion (dispatch.RunHandler). It maps the
// claimed DTO to an internal Run with the cert Decryptor (polling decrypt semantics -- pins
// intact), builds a run-scoped RunApi authenticated with the run's own runToken, and drives the
// exact Worker.tfExecution machinery.
//
// A mapping failure (bad behavior, unparseable implementation, or an SSH-key decrypt failure) is
// returned as an out-of-band error WITHOUT reporting a status -- byte-for-byte the historic
// polling behavior, where such a failure surfaced inside FetchRunDetails and was handled as a
// fetch error (backoff, no status report; the coordinator times the run out). Once mapping
// succeeds the run is metered as claimed and Worker.tfExecution owns the terminal status +
// success/failure metering, so Execute returns nil (RunHandler contract: run-level outcomes are
// reported by the handler, not via this return value).
//
// ctx (InProcess shutdown cancellation) is intentionally NOT propagated into the run: like the
// former Manager, an in-flight tf run finishes on its own TfCommandTimeoutMins rather than being
// force-aborted on shutdown. The plan's ABORTED-on-shutdown behavior is a deliberate divergence
// from today and is not adopted here (see the run-log addendum); this keeps the handler path
// equivalent to the Manager path it stands in for.
func (h Handler) Execute(_ context.Context, cr dispatch.ClaimedRun) error {
	log := h.log.With("run", cr.Id)

	run, err := runDTOToInternal(cr.Details, h.dec)
	if err != nil {
		return err
	}

	h.meter.RunClaimed()

	// Each run gets its OWN RunApi instance with its OWN runToken slot (H5): concurrent runs
	// never share the mutable token, so run A can never report under run B's credentials. The
	// runToken takes priority in runApiAuth, so all run-scoped reporting uses runToken-only auth.
	api := h.newRunApi(h.dec, cr.Details.Spec.RunToken)

	worker := &Worker{
		workerNumber:         1,
		workerDir:            h.workerDir,
		timeout:              h.timeout,
		runApi:               api,
		tfBinaries:           h.tfBinaries,
		log:                  log,
		statusUpdateInterval: h.statusUpdateInterval,
		dec:                  h.dec,
		meter:                h.meter,
	}
	worker.tfExecution(run)
	return nil
}

// NewClaimClassifier builds the tf persona's dispatch.ClaimClassifier, reproducing the former
// Worker.handleFetchRunError policy (PLAN_DETAIL_05 §3.2): a 404 is the normal idle poll (no
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
