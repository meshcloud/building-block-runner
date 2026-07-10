package dispatch

import (
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/meshcloud/building-block-runner/internal/meshapi"
)

// maxDrainPerCycleUnlimited bounds how many runs are drained in a single polling cycle when
// concurrency is configured as unlimited. It is a safety backstop against an infinite loop if
// the API were to keep returning runs indefinitely; draining stops naturally on the first
// no-run outcome.
const maxDrainPerCycleUnlimited = 10

// LoopConfig configures Loop's polling cadence and capacity guard. It generalizes the
// former controller-only pollingIntervalSeconds/maxConcurrentJobs pair so any persona can
// drive the same drain loop (PLAN_DETAIL_05 §4.1/§6).
type LoopConfig struct {
	// PollInterval is how often Loop wakes up to drain the backlog (controller: the
	// configured pollingIntervalSeconds, default 10s).
	PollInterval time.Duration
	// ClaimBackoff suppresses claiming for this long after a ClaimClassifier returns
	// OutcomeBackoff. Zero means "no extra backoff, just wait for the next PollInterval
	// tick" -- the run-controller persona's behavior.
	ClaimBackoff time.Duration
	// MaxConcurrent is the capacity guard (maxConcurrentJobs / maxConcurrentRuns);
	// negative means unlimited (bounded per-cycle by maxDrainPerCycleUnlimited).
	MaxConcurrent int
}

// LoopDeps are Loop's collaborators, all injected (P3 -- no package-level mutable state).
type LoopDeps struct {
	// RunnerUuid identifies this runner/controller for logging and metric labels.
	RunnerUuid string
	Claimer    Claimer
	Dispatcher Dispatcher
	StatusApi  StatusApi
	// Classify turns a claim error into a ClaimOutcome; see ControllerClaimClassifier for
	// the run-controller persona's frozen policy.
	Classify ClaimClassifier
	Metrics  *MetricsCollector
	Logger   *slog.Logger
}

// Loop is the backend-agnostic claim/drain loop (PLAN_DETAIL_05 §4.1), generalized from the
// former internal/controller.Controller: it owns what is generic (ticker, capacity math,
// claim, implementation-type resolution, fail-fast reporting); the injected Dispatcher owns
// what is backend-specific (routing to a template/handler, decryption placement, in-flight
// tracking).
type Loop struct {
	cfg    LoopConfig
	deps   LoopDeps
	logger *slog.Logger

	// shutdownCalled is written by Stop (any goroutine) and read by run/drainRuns (the loop
	// goroutine) with no other synchronization between them, so it must be atomic -- the
	// same B6 fix already applied to internal/tf.Manager's identically-shaped field (H8: a
	// plain bool here is exactly the "Stop vs. loop-goroutine-read" data race the
	// concurrency-hazard suite exists to catch under -race).
	shutdownCalled atomic.Bool
	// backoffUntil suppresses claiming until this time after a ClaimOutcome of
	// OutcomeBackoff. Read/written only from the single loop goroutine.
	backoffUntil time.Time
}

// NewLoop constructs a Loop. A nil deps.Logger falls back to slog.Default() (D15:
// dispatch is slog-native from the start).
func NewLoop(cfg LoopConfig, deps LoopDeps) *Loop {
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Loop{cfg: cfg, deps: deps, logger: logger}
}

// processResult describes the outcome of attempting to process a single run, so the drain
// loop knows whether to keep going (a run was dispatched) or stop until the next polling
// cycle.
type processResult int

const (
	// runProcessed means a run was fetched and dispatched successfully.
	runProcessed processResult = iota
	// noRunAvailable means there was no run to process (no-run claim outcome); nothing was claimed.
	noRunAvailable
	// processFailed means a run was claimed but could not be dispatched; it was reported as
	// FAILED (every dispatch failure is reported since L14 -- the former decrypt-failure
	// silent-timeout quirk is gone).
	processFailed
)

func (l *Loop) Start(wg *sync.WaitGroup) {
	l.logger.Info("dispatch loop started")

	go func() {
		defer wg.Done()
		l.run()
	}()
}

func (l *Loop) Stop() {
	l.logger.Info("dispatch loop shutdown requested")
	l.shutdownCalled.Store(true)
}

func (l *Loop) run() {
	l.logger.Info("dispatch loop running", "poll_interval", l.cfg.PollInterval)

	ticker := time.NewTicker(l.cfg.PollInterval)
	defer ticker.Stop()

	for !l.shutdownCalled.Load() {
		<-ticker.C
		l.deps.Metrics.IncLoopIteration()
		l.drainRuns()
	}

	l.logger.Info("dispatch loop stopped")
}

// drainRuns processes as many queued runs as there is capacity for in a single polling
// cycle, back-to-back. It determines the available capacity once up front, then keeps
// dispatching runs until there are no more, a run fails to dispatch, capacity is exhausted,
// or a shutdown is requested. Draining back-to-back (instead of one run per polling
// interval) lets a backlog drain quickly without waiting a full interval between each run.
func (l *Loop) drainRuns() {
	if time.Now().Before(l.backoffUntil) {
		return
	}

	capacity := l.availableCapacity()
	if capacity <= 0 {
		l.logger.Info("at capacity, skipping run fetch this cycle", "max_concurrent", l.cfg.MaxConcurrent)
		l.deps.Metrics.IncJobsAtCapacitySkips(l.deps.RunnerUuid)
		return
	}

	for created := 0; created < capacity && !l.shutdownCalled.Load(); {
		// Only claim a run while we still have capacity budget remaining, so we don't claim
		// runs we'd be unable to place and have to mark as FAILED.
		switch l.processNextRun() {
		case runProcessed:
			created++
		default:
			// noRunAvailable: backlog drained, wait for the next polling cycle.
			// processFailed: stop draining this cycle; the run was already handled.
			return
		}
	}
}

// availableCapacity returns how many additional runs the dispatcher may take on right now.
// A negative MaxConcurrent means unlimited. If the in-flight count cannot be determined we
// return 0 (skip this cycle) rather than risk claiming runs we cannot place.
func (l *Loop) availableCapacity() int {
	if l.cfg.MaxConcurrent < 0 {
		return maxDrainPerCycleUnlimited
	}

	inFlight, err := l.deps.Dispatcher.InFlight()
	if err != nil {
		l.logger.Warn("failed to determine in-flight count; skipping run fetch this cycle", "error", err)
		return 0
	}

	available := l.cfg.MaxConcurrent - inFlight
	if available < 0 {
		return 0
	}
	return available
}

func (l *Loop) processNextRun() processResult {
	run, err := l.deps.Claimer.Claim()
	if err != nil {
		switch l.deps.Classify(err) {
		case OutcomeNoRun:
			// normal idle poll, no log
		case OutcomeNoRunLogged:
			l.logger.Warn("error fetching run", "error", err)
		case OutcomeBackoff:
			l.logger.Warn("error fetching run, backing off", "error", err, "backoff", l.cfg.ClaimBackoff)
			l.backoffUntil = time.Now().Add(l.cfg.ClaimBackoff)
		}
		return noRunAvailable
	}

	implType, err := run.Details.Spec.Definition.Spec.GetImplementationType()
	if err != nil {
		l.logger.Error("failed to determine implementation type", "run_id", run.Id, "error", err)
		l.reportRunFailure(run.Id, "Failed to determine implementation type: "+err.Error())
		return processFailed
	}
	run.Type = meshapi.ToRunnerType(implType)

	l.logger.Info("dispatching run", "run_id", run.Id, "type", run.Type)

	if err := l.deps.Dispatcher.Dispatch(run); err != nil {
		var unhandled *UnhandledTypeError
		if errors.As(err, &unhandled) {
			l.logger.Warn("no handler configured for run type", "run_id", run.Id, "type", run.Type)
			l.reportRunFailure(run.Id, unhandled.Message)
			return processFailed
		}

		// Every dispatch failure is now reported (L14/P5: never suppress silently). The
		// former decrypt-failure "silent, wait for the coordinator timeout" quirk is gone --
		// the dispatcher authors an actionable FAILED message (e.g. the key-mismatch
		// guidance) and fires its own error-specific metrics (e.g. decryption errors).
		l.logger.Error("failed to dispatch run", "run_id", run.Id, "error", err)
		l.reportRunFailure(run.Id, err.Error())
		return processFailed
	}

	return runProcessed
}

// reportRunFailure registers this identity as a status source and marks the run as FAILED.
// Uses this runner's/controller's own process credentials (via l.deps.StatusApi), never the
// claimed run's runToken: fail-fast fires before any handler owns the run (D5, §16.6).
func (l *Loop) reportRunFailure(runId RunId, errorMessage string) {
	if regErr := l.deps.StatusApi.RegisterSource(runId); regErr != nil {
		l.logger.Error("failed to register as status source", "run_id", runId, "error", regErr)
		return
	}

	if statusErr := l.deps.StatusApi.UpdateRunStatus(runId, "FAILED", errorMessage, errorMessage); statusErr != nil {
		l.logger.Error("failed to report error back to meshfed", "run_id", runId, "error", statusErr)
	}
}
