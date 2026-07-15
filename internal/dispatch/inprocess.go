package dispatch

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	meshapi "github.com/meshcloud/building-block-runner/internal/meshapi"
)

// DefaultShutdownGrace is the drain window InProcess.Wait honors before cancelling handlers
// that are still running on shutdown. It is deliberately longer than a typical
// graceful-shutdown budget yet far below the coordinator's ~30-min external run timeout, so
// short in-flight runs finish cleanly while a wedged/long-polling run is still bounded.
const DefaultShutdownGrace = 120 * time.Second

// InProcess is the standalone-runner-type Dispatcher: it runs each claimed run in its own
// goroutine inside the same process, instead of handing it to a k8s Job. Which run types it
// can serve is fixed at LINK TIME by the handlers its binary wired in -- a runner type binary
// (cmd/tf) registers one handler; only the cmd/bbrunner superset links every handler and can
// serve ALL. A claimed run whose type has no registered handler fails fast with an actionable
// message rather than being executed.
//
// Concurrency model: the in-flight counter is incremented synchronously on the
// loop's goroutine inside Dispatch (before the run goroutine spawns), and decremented on
// completion, so the loop's capacity guard can never oversubscribe by racing a
// not-yet-counted run. A completion also pokes Done() so the loop drains again
// immediately, reproducing the old token loop's "refetch right after a run finishes" cadence
// (manager.go:93-95) instead of waiting a full poll interval.
type InProcess struct {
	handlers map[meshapi.RunnerImplementationType]RunHandler
	grace    time.Duration
	logger   *slog.Logger

	// runCtx is the parent context of every in-flight run; cancelAll cancels it once the
	// shutdown grace period expires so long-running handlers observe cancellation and can
	// report a terminal status (never a stale IN_PROGRESS).
	runCtx    context.Context
	cancelAll context.CancelFunc

	mu       sync.Mutex
	inFlight int

	wg   sync.WaitGroup
	done chan struct{}
}

// NewInProcess builds an in-process dispatcher over one handler per concrete run type.
// Registering a handler under RunnerTypeAll is a programmer error (ALL is a registration
// concept, not a dispatchable type, controller/config.go:320-333) and is rejected at
// construction, as is a nil handler -- so a constructed InProcess is always usable. A
// non-positive shutdownGrace falls back to DefaultShutdownGrace; a nil logger to
// slog.Default().
func NewInProcess(handlers map[meshapi.RunnerImplementationType]RunHandler, shutdownGrace time.Duration, logger *slog.Logger) (*InProcess, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if shutdownGrace <= 0 {
		shutdownGrace = DefaultShutdownGrace
	}

	registered := make(map[meshapi.RunnerImplementationType]RunHandler, len(handlers))
	for t, h := range handlers {
		if t == meshapi.RunnerTypeAll {
			return nil, fmt.Errorf(
				"cannot register a handler under capability %q: ALL is a registration concept, not a dispatchable run type - register one handler per concrete type",
				meshapi.RunnerTypeAll)
		}
		if h == nil {
			return nil, fmt.Errorf("nil handler registered for run type %q", t)
		}
		registered[t] = h
	}

	ctx, cancel := context.WithCancel(context.Background())
	return &InProcess{
		handlers:  registered,
		grace:     shutdownGrace,
		logger:    logger,
		runCtx:    ctx,
		cancelAll: cancel,
		done:      make(chan struct{}, 1),
	}, nil
}

// Dispatch places run for in-process execution. If no handler is registered for run.Type it
// returns a *UnhandledTypeError (claim-and-fail-fast) WITHOUT spawning a goroutine or
// touching the in-flight counter -- the run is not "in flight", it was refused. Otherwise it
// increments the in-flight counter synchronously and starts the run in its own goroutine,
// returning nil immediately (dispatch does not block on execution).
func (d *InProcess) Dispatch(run ClaimedRun) error {
	handler, ok := d.handlers[run.Type]
	if !ok {
		return NewInProcessUnhandledTypeError(run.Type)
	}

	// Increment on the caller's (loop) goroutine, before spawning, so InFlight already
	// reflects this run the moment Dispatch returns -- the loop's capacity math is then
	// exact, never racing a goroutine that has not yet counted itself.
	d.mu.Lock()
	d.inFlight++
	d.mu.Unlock()

	d.wg.Add(1)
	go d.execute(handler, run)
	return nil
}

func (d *InProcess) execute(handler RunHandler, run ClaimedRun) {
	// LIFO: decrement runs first (so a woken loop reads freed capacity), then signalDone
	// (wake the loop), then wg.Done (release Wait) -- Wait must not unblock before capacity
	// has been given back.
	defer d.wg.Done()
	defer d.signalDone()
	defer d.decrement()

	d.logger.Info("dispatching run in-process", "runId", run.Id, "type", run.Type)
	if err := handler.Execute(d.runCtx, run); err != nil {
		// A non-nil error is an out-of-band (infrastructure) failure; any run-level FAILED was
		// already reported by the handler itself (RunHandler contract). Log it so it is not
		// silent; run-outcome metrics stay with the handler, which alone knows the terminal
		// status (worker.go:154-159).
		d.logger.Error("in-process run handler returned an error",
			"runId", run.Id, "type", run.Type, "err", err)
	}
	d.logger.Info("finished in-process run", "runId", run.Id, "type", run.Type)
}

// InFlight returns the number of runs currently executing. It never errors (the k8s
// dispatcher's InFlight can, since it lists Jobs; this one just reads a counter), satisfying
// the Dispatcher interface uniformly.
func (d *InProcess) InFlight() (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.inFlight, nil
}

// Done is signaled once per run completion so the loop can drain the backlog immediately
// rather than waiting for its next poll tick. The channel is buffered (cap 1) and sends are
// non-blocking, so bursts of completions coalesce into at most one pending wake -- the loop
// only needs to know "at least one slot freed", then re-reads InFlight itself.
func (d *InProcess) Done() <-chan struct{} {
	return d.done
}

// Wait drains in-flight runs on shutdown. It waits up to the configured grace period for runs
// to finish on their own (they report their own terminal status); if the grace period expires
// with runs still executing, it cancels their context so they abort and report a terminal
// ABORTED/FAILED status (never a stale IN_PROGRESS), then waits for them to return.
// The loop must have stopped claiming (Loop.Stop) before Wait is called.
func (d *InProcess) Wait() {
	defer d.cancelAll()

	drained := make(chan struct{})
	go func() {
		d.wg.Wait()
		close(drained)
	}()

	select {
	case <-drained:
		return
	case <-time.After(d.grace):
	}

	d.logger.Warn("shutdown grace period expired with runs still in flight; cancelling them",
		"grace", d.grace)
	d.cancelAll()
	<-drained
}

func (d *InProcess) decrement() {
	d.mu.Lock()
	d.inFlight--
	d.mu.Unlock()
}

func (d *InProcess) signalDone() {
	select {
	case d.done <- struct{}{}:
	default:
	}
}
