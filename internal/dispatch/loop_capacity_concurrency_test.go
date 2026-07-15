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
)

// Test_Loop_NeverExceedsMaxConcurrentRuns pins the concurrency invariant: the
// claim loop must never let more than LoopConfig.MaxConcurrent runs be in flight at once,
// even though claiming (on the loop goroutine) and run completion (on each run's own
// goroutine) happen concurrently. It drives a real dispatch.Loop over a real
// dispatch.InProcess dispatcher (only the Claimer/StatusApi are fakes) with a queue of 5
// claimable runs and MaxConcurrent=2, and asserts peak in-flight never exceeds 2 and all 5
// runs eventually complete.
func Test_Loop_NeverExceedsMaxConcurrentRuns(t *testing.T) {
	const maxConcurrent = 2
	const totalRuns = 5

	release := make(chan struct{})
	var mu sync.Mutex
	current, peak := 0, 0
	started := make(chan dispatch.RunId, totalRuns)

	handler := concurrencyTestHandlerFunc(func(ctx context.Context, run dispatch.ClaimedRun) error {
		mu.Lock()
		current++
		if current > peak {
			peak = current
		}
		mu.Unlock()
		started <- run.Id

		<-release // held open until the test explicitly frees this run

		mu.Lock()
		current--
		mu.Unlock()
		return nil
	})

	in, err := dispatch.NewInProcess(
		map[meshapi.RunnerImplementationType]dispatch.RunHandler{meshapi.RunnerTypeTerraform: handler},
		time.Second, discardLogger())
	require.NoError(t, err)

	runs := make([]dispatch.ClaimedRun, totalRuns)
	for i := range runs {
		runs[i] = newClaimedRun(runIdFor(i), "tok")
	}
	claimer := newHazardQueueClaimer(runs...)

	loop := dispatch.NewLoop(dispatch.LoopConfig{
		PollInterval:  5 * time.Millisecond,
		ClaimBackoff:  5 * time.Millisecond,
		MaxConcurrent: maxConcurrent,
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
	t.Cleanup(func() {
		loop.Stop()
		wg.Wait()
	})

	// Wait for exactly `maxConcurrent` runs to start, then hold: the loop must not claim a
	// 3rd run while at capacity.
	waitForStarted(t, started, maxConcurrent)
	assertStableClaimCount(t, claimer, maxConcurrent, 150*time.Millisecond)

	// Free runs one at a time; each release should let exactly one more run start, and the
	// in-flight count must never spike above maxConcurrent while doing so.
	for i := 0; i < totalRuns; i++ {
		release <- struct{}{}
	}

	require.Eventually(t, func() bool {
		return claimer.Served() == totalRuns
	}, 2*time.Second, 5*time.Millisecond, "all queued runs must eventually be claimed and dispatched")

	require.Eventually(t, func() bool {
		n, _ := in.InFlight()
		return n == 0
	}, 2*time.Second, 5*time.Millisecond, "in-flight count must drain back to 0 once every run completes")

	mu.Lock()
	defer mu.Unlock()
	assert.LessOrEqual(t, peak, maxConcurrent, "peak concurrent in-flight runs must never exceed MaxConcurrent")
}

func runIdFor(i int) string {
	return "run-" + string(rune('a'+i))
}

// waitForStarted drains n run-start signals from started, failing the test if that does not
// happen within a generous bound (a hang here means the loop under-claims, not a design this
// test is trying to characterize).
func waitForStarted(t *testing.T, started <-chan dispatch.RunId, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for run %d/%d to start", i+1, n)
		}
	}
}

// assertStableClaimCount asserts the claimer's served count stays at want for the given
// window -- the "the loop stays at capacity, it does not over-claim" half of the invariant.
func assertStableClaimCount(t *testing.T, c *hazardQueueClaimer, want int, window time.Duration) {
	t.Helper()
	deadline := time.Now().Add(window)
	for time.Now().Before(deadline) {
		require.Equal(t, want, c.Served(), "loop must not claim beyond capacity while all handlers are still running")
		time.Sleep(10 * time.Millisecond)
	}
}
