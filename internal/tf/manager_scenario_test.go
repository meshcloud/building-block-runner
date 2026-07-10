package tf

// CP12 (PLAN_DETAIL_01_tf_characterization_tests.md §9): DefaultRunManager token protocol.
//
// The manager is the polling use-case boundary: its channel protocol is exactly what Worker.work()
// consumes and what phase 2 keeps as the engine loop's contract (design decision §13). These are
// direct tests on DefaultRunManager with injected channels — Start/run themselves spawn a real
// Worker with NewRunApi() and 10-60s sleeps, so they stay uncovered by design (§9 CP12, STOP-C: no
// production seam is added to reach them).

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestManager builds a DefaultRunManager with buffered channels and a discarded logger, so its
// token protocol can be driven without the real Worker/NewRunApi() wiring that Start/run assemble.
func newTestManager() *DefaultRunManager {
	return &DefaultRunManager{
		workerIn:       make(chan workerToken, 4),
		managerIn:      make(chan workerToken, 4),
		defaultTimeout: time.Minute,
		logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

// recvToken reads one token from ch, failing the test on timeout so a missing hand-out never hangs
// the suite.
func recvToken(t *testing.T, ch chan workerToken) workerToken {
	t.Helper()
	select {
	case tok := <-ch:
		return tok
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a worker token")
		return stop
	}
}

// Test_NewManager_SetsDefaults pins the constructor: timeout derives from AppConfig.TfCommandTimeoutMins,
// channels are buffered, and shutdown starts false.
func Test_NewManager_SetsDefaults(t *testing.T) {
	AppConfig.TfCommandTimeoutMins = 7
	rm, ok := NewManager(nil, nil, NoopMeter{}, slog.New(slog.NewTextHandler(io.Discard, nil))).(*DefaultRunManager)
	require.True(t, ok, "NewManager must return *DefaultRunManager")

	assert.Equal(t, 7*time.Minute, rm.defaultTimeout)
	assert.False(t, rm.shutdownCalled.Load())
	assert.NotNil(t, rm.workerIn)
	assert.NotNil(t, rm.managerIn)
	assert.Equal(t, 1, cap(rm.workerIn))
	assert.Equal(t, 1, cap(rm.managerIn))
}

// Test_HandleWorkers_DoneHandsOutWork pins that a `done` token yields a fresh `work` token (a worker
// finishing a run is immediately handed the next), and that a `stopped` token ends the loop.
func Test_HandleWorkers_DoneHandsOutWork(t *testing.T) {
	rm := newTestManager()

	loopDone := make(chan struct{})
	go func() {
		rm.handleWorkers()
		close(loopDone)
	}()

	rm.managerIn <- done
	assert.Equal(t, work, recvToken(t, rm.workerIn), "done must hand out a new work token")

	rm.managerIn <- stopped
	select {
	case <-loopDone:
	case <-time.After(2 * time.Second):
		t.Fatal("handleWorkers did not stop after a stopped token")
	}
}

// Test_HandleWorkers_DoneWhileShuttingDown pins that once Stop() has been called, a finishing worker
// is told to stop rather than handed more work.
func Test_HandleWorkers_DoneWhileShuttingDown(t *testing.T) {
	rm := newTestManager()
	rm.shutdownCalled.Store(true)

	loopDone := make(chan struct{})
	go func() {
		rm.handleWorkers()
		close(loopDone)
	}()

	rm.managerIn <- done
	assert.Equal(t, stop, recvToken(t, rm.workerIn), "done during shutdown must hand out a stop token")

	rm.managerIn <- stopped
	select {
	case <-loopDone:
	case <-time.After(2 * time.Second):
		t.Fatal("handleWorkers did not stop after a stopped token")
	}
}

// Test_HandoutWorkerToken_Branches pins handoutWorkerToken's three outcomes without timed runs:
// shutdown-at-entry => stop; no shutdown => work (after the delay, here 0); shutdown observed only
// after the delay => stop (the post-sleep re-check branch).
func Test_HandoutWorkerToken_Branches(t *testing.T) {
	t.Run("shutdown at entry hands out stop", func(t *testing.T) {
		rm := newTestManager()
		rm.shutdownCalled.Store(true)
		go rm.handoutWorkerToken(0)
		assert.Equal(t, stop, recvToken(t, rm.workerIn))
	})

	t.Run("no shutdown hands out work", func(t *testing.T) {
		rm := newTestManager()
		go rm.handoutWorkerToken(0)
		assert.Equal(t, work, recvToken(t, rm.workerIn))
	})

	t.Run("shutdown observed after the delay hands out stop", func(t *testing.T) {
		rm := newTestManager()
		// A small non-zero delay lets us flip shutdownCalled while handoutWorkerToken sleeps, so the
		// post-sleep re-check takes the stop branch. shutdownCalled is now an atomic.Bool (B6 fixed
		// structurally in phase 2), so this concurrent write/read is race-free under -race.
		go rm.handoutWorkerToken(50 * time.Millisecond)
		time.Sleep(10 * time.Millisecond)
		rm.shutdownCalled.Store(true)
		assert.Equal(t, stop, recvToken(t, rm.workerIn))
	})
}

// Test_Manager_DelayConstants pins the norun/failed hand-out delays as constants rather than timing
// a 10s/60s run through handleWorkers (§9 CP12).
func Test_Manager_DelayConstants(t *testing.T) {
	assert.Equal(t, 10*time.Second, NORUN_WORKER_DELAY)
	assert.Equal(t, 60*time.Second, FAILED_WORKER_DELAY)
}

// Test_Manager_Stop pins that Stop() records the shutdown request (B6 is recorded, not asserted as a
// race — A5).
func Test_Manager_Stop(t *testing.T) {
	rm := newTestManager()
	assert.False(t, rm.shutdownCalled.Load())
	rm.Stop()
	assert.True(t, rm.shutdownCalled.Load())
}
