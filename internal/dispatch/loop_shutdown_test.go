package dispatch_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/internal/dispatch"
	"github.com/meshcloud/building-block-runner/internal/meshapi"
	"github.com/meshcloud/building-block-runner/internal/report"
)

// Test_Loop_ShutdownWaitsForInFlightRuns pins concurrency hazard H7 (plan-05 §7, D9's
// graceful-shutdown amendment): stopping the claim loop must not touch runs already in
// flight, and a run still executing when the shutdown grace period expires must be
// cancelled and MUST report a terminal status -- ABORTED, falling back to FAILED, but
// NEVER SUCCEEDED -- so the coordinator never observes a stale IN_PROGRESS.
//
// This drives the full persona-shutdown sequence a real main would perform: dispatch.Loop
// claims runs and hands them to a real dispatch.InProcess; Loop.Stop() then stops further
// claiming while InProcess.Wait() drains in-flight work. One run finishes on its own within
// the grace period (heir of a quick synchronous handler); a second models a "sync-polling"
// handler (D9) that only reacts to context cancellation, exactly like a handler blocked
// polling an external pipeline would.
func Test_Loop_ShutdownWaitsForInFlightRuns(t *testing.T) {
	const grace = 100 * time.Millisecond

	var mu sync.Mutex
	terminal := map[dispatch.RunId]report.ExecutionStatus{}
	record := func(id dispatch.RunId, s report.ExecutionStatus) {
		mu.Lock()
		defer mu.Unlock()
		terminal[id] = s
	}
	terminalOf := func(id dispatch.RunId) (report.ExecutionStatus, bool) {
		mu.Lock()
		defer mu.Unlock()
		s, ok := terminal[id]
		return s, ok
	}

	const fastId = dispatch.RunId("fast-run")
	const slowId = dispatch.RunId("slow-run")

	// The fake handler below stands in for the not-yet-landed tf/handler ports: it must
	// uphold the same D9 contract they will (never leave a cancelled run's status
	// unreported, never report SUCCEEDED for a run that was cancelled).
	handler := concurrencyTestHandlerFunc(func(ctx context.Context, run dispatch.ClaimedRun) error {
		if run.Id == fastId {
			record(run.Id, report.SUCCEEDED)
			return nil
		}
		// A "sync-polling" run (D9): blocks until either it finishes on its own (it never
		// does here) or its context is cancelled at grace expiry.
		<-ctx.Done()
		record(run.Id, report.ABORTED)
		return nil
	})

	in, err := dispatch.NewInProcess(
		map[meshapi.RunnerImplementationType]dispatch.RunHandler{meshapi.RunnerTypeTerraform: handler},
		grace, discardLogger())
	require.NoError(t, err)

	claimer := newHazardQueueClaimer(
		newClaimedRun(string(fastId), "tok-fast"),
		newClaimedRun(string(slowId), "tok-slow"),
	)

	loop := dispatch.NewLoop(dispatch.LoopConfig{
		PollInterval:  5 * time.Millisecond,
		ClaimBackoff:  5 * time.Millisecond,
		MaxConcurrent: 2,
	}, dispatch.LoopDeps{
		RunnerUuid: "test-runner",
		Claimer:    claimer,
		Dispatcher: in,
		StatusApi:  noopStatusApi{},
		Classify:   alwaysNoRun,
		Metrics:    dispatch.NewMetricsCollector(),
		Logger:     discardLogger(),
	})

	var wg sync.WaitGroup
	wg.Add(1)
	loop.Start(&wg)

	require.Eventually(t, func() bool { return claimer.Served() == 2 },
		time.Second, 5*time.Millisecond, "both runs must be claimed before shutdown begins")

	// Let the fast run finish on its own, via the normal (non-cancellation) path, before
	// shutdown starts.
	require.Eventually(t, func() bool {
		_, ok := terminalOf(fastId)
		return ok
	}, time.Second, 5*time.Millisecond, "the fast run must finish on its own before shutdown starts")

	// Stop claiming; the loop goroutine itself must exit promptly -- it never owns draining
	// in-flight runs, that is InProcess.Wait's job.
	loop.Stop()
	loopStopped := make(chan struct{})
	go func() { wg.Wait(); close(loopStopped) }()
	select {
	case <-loopStopped:
	case <-time.After(time.Second):
		t.Fatal("loop goroutine did not exit after Stop")
	}

	shutdownStart := time.Now()
	in.Wait()
	elapsed := time.Since(shutdownStart)

	assert.GreaterOrEqual(t, elapsed, grace,
		"Wait must not return before the configured grace period while a run is still in flight")
	assert.Less(t, elapsed, grace+2*time.Second,
		"Wait must return promptly once it cancels the still-running handler, not hang")

	fastStatus, ok := terminalOf(fastId)
	require.True(t, ok)
	assert.Equal(t, report.SUCCEEDED, fastStatus, "a run that finished on its own keeps its own terminal status")

	slowStatus, ok := terminalOf(slowId)
	require.True(t, ok, "the still-in-flight run must have reported a terminal status by the time Wait returns")
	assert.Equal(t, report.ABORTED, slowStatus,
		"a run still in flight at grace expiry must be cancelled and report ABORTED, never SUCCEEDED")
	assert.NotEqual(t, report.SUCCEEDED, slowStatus, "never SUCCEEDED for a cancelled run (D9)")

	n, _ := in.InFlight()
	assert.Equal(t, 0, n, "no run may still be considered in flight once Wait has returned")
}
