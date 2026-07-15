package dispatch_test

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/meshcloud/building-block-runner/internal/dispatch"
)

// Test_Loop_WakeAndTickerRace pins the concurrency invariant: Loop's internal
// shutdown flag is read on every tick from the loop goroutine and written by Stop from
// whatever goroutine a type's shutdown handling calls it from -- concurrent ticks and
// concurrent Stop calls must never race or deadlock the loop.
//
// This is also the regression test for the fix landed alongside it: Loop.shutdownCalled
// was a plain bool written by Stop and read by run()/drainRuns() with no synchronization
// between them -- exactly the "plain bool touched from two goroutines" data race the tf
// type's Manager already fixed once (sync/atomic). -race must stay clean here.
func Test_Loop_WakeAndTickerRace(t *testing.T) {
	claimer := newHazardQueueClaimer() // empty queue: every tick is a fast no-op no-run cycle

	loop := dispatch.NewLoop(dispatch.LoopConfig{
		PollInterval:  time.Millisecond,
		ClaimBackoff:  time.Millisecond,
		MaxConcurrent: 1,
	}, dispatch.LoopDeps{
		RunnerUuid: "test-runner",
		Claimer:    claimer,
		Dispatcher: zeroDispatcher{},
		StatusApi:  noopStatusApi{},
		Classify:   alwaysNoRun,
		Metrics:    dispatch.NewMetricsCollector(),
		Logger:     discardLogger(),
	})

	var wg sync.WaitGroup
	wg.Add(1)
	loop.Start(&wg)

	// Hammer Stop() concurrently from several goroutines while the loop ticks as fast as it
	// can -- exactly the concurrent write/read pattern this test targets.
	var stopWG sync.WaitGroup
	for i := 0; i < 8; i++ {
		stopWG.Add(1)
		go func(delay time.Duration) {
			defer stopWG.Done()
			time.Sleep(delay)
			loop.Stop()
		}(time.Duration(i) * time.Millisecond)
	}
	stopWG.Wait()

	stopped := make(chan struct{})
	go func() {
		wg.Wait()
		close(stopped)
	}()

	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("loop did not stop: possible deadlock in the ticker/Stop interaction")
	}

	// Once the loop goroutine has actually exited, no further claims must ever happen.
	servedAtStop := claimer.Served()
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, servedAtStop, claimer.Served(), "no claim may happen once the loop goroutine has exited")
}
