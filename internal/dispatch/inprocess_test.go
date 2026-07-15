package dispatch

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	meshapi "github.com/meshcloud/building-block-runner/internal/meshapi"
)

// fakeHandler is a RunHandler whose behavior each test supplies via a closure.
type fakeHandler struct {
	fn func(ctx context.Context, run ClaimedRun) error
}

func (h fakeHandler) Execute(ctx context.Context, run ClaimedRun) error {
	return h.fn(ctx, run)
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func terraformRun(id string) ClaimedRun {
	return ClaimedRun{Id: RunId(id), Type: meshapi.RunnerTypeTerraform}
}

func newTestInProcess(t *testing.T, grace time.Duration, h RunHandler) *InProcess {
	t.Helper()
	d, err := NewInProcess(
		map[meshapi.RunnerImplementationType]RunHandler{meshapi.RunnerTypeTerraform: h},
		grace, testLogger())
	require.NoError(t, err)
	return d
}

func TestNewInProcess_RejectsAllCapabilityAsHandlerKey(t *testing.T) {
	_, err := NewInProcess(
		map[meshapi.RunnerImplementationType]RunHandler{
			meshapi.RunnerTypeAll: fakeHandler{fn: func(context.Context, ClaimedRun) error { return nil }},
		}, time.Minute, testLogger())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ALL")
}

func TestNewInProcess_RejectsNilHandler(t *testing.T) {
	_, err := NewInProcess(
		map[meshapi.RunnerImplementationType]RunHandler{meshapi.RunnerTypeTerraform: nil},
		time.Minute, testLogger())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil handler")
}

func TestNewInProcess_AppliesDefaultsForGraceAndLogger(t *testing.T) {
	// Non-positive grace and a nil logger must not produce an unusable dispatcher.
	d, err := NewInProcess(
		map[meshapi.RunnerImplementationType]RunHandler{}, 0, nil)
	require.NoError(t, err)
	assert.Equal(t, DefaultShutdownGrace, d.grace)
	assert.NotNil(t, d.logger)
}

func TestInProcess_DispatchUnhandledType_FailsFastWithoutSpawning(t *testing.T) {
	d := newTestInProcess(t, time.Minute,
		fakeHandler{fn: func(context.Context, ClaimedRun) error {
			t.Error("handler must not run for an unhandled type")
			return nil
		}})

	// Only TERRAFORM is registered; a MANUAL run has no handler.
	err := d.Dispatch(ClaimedRun{Id: "r1", Type: meshapi.RunnerTypeManual})

	var ute *UnhandledTypeError
	require.ErrorAs(t, err, &ute)
	assert.Equal(t, meshapi.RunnerTypeManual, ute.Type)
	assert.Equal(t, NewInProcessUnhandledTypeError(meshapi.RunnerTypeManual).Message, ute.Message)

	// A refused run is not "in flight" and never pokes the wake channel.
	n, inErr := d.InFlight()
	require.NoError(t, inErr)
	assert.Equal(t, 0, n)
	select {
	case <-d.Done():
		t.Fatal("unhandled dispatch must not signal a completion")
	default:
	}
}

func TestInProcess_RunsHandlerToCompletionAndSignalsDone(t *testing.T) {
	var got atomic.Pointer[ClaimedRun]
	var gotCtx atomic.Bool
	d := newTestInProcess(t, time.Minute,
		fakeHandler{fn: func(ctx context.Context, run ClaimedRun) error {
			gotCtx.Store(ctx != nil)
			got.Store(&run)
			return nil
		}})

	require.NoError(t, d.Dispatch(terraformRun("r1")))

	select {
	case <-d.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("expected a completion wake on Done()")
	}
	require.Eventually(t, func() bool { n, _ := d.InFlight(); return n == 0 },
		time.Second, time.Millisecond)

	require.NotNil(t, got.Load())
	assert.Equal(t, RunId("r1"), got.Load().Id)
	assert.True(t, gotCtx.Load(), "handler must receive a non-nil context")
}

func TestInProcess_InFlightCountsSynchronouslyOnDispatch(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	d := newTestInProcess(t, time.Minute,
		fakeHandler{fn: func(context.Context, ClaimedRun) error {
			close(started)
			<-release
			return nil
		}})

	require.NoError(t, d.Dispatch(terraformRun("r1")))

	// The count must already reflect the run the instant Dispatch returns: no wait,
	// no Eventually -- if it weren't synchronous this read would flake.
	n, err := d.InFlight()
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	<-started
	close(release)
	require.Eventually(t, func() bool { n, _ := d.InFlight(); return n == 0 },
		time.Second, time.Millisecond)
}

func TestInProcess_ConcurrentDispatch_CountersAreRaceFree(t *testing.T) {
	const runs = 50
	var executed atomic.Int64
	release := make(chan struct{})
	d := newTestInProcess(t, time.Minute,
		fakeHandler{fn: func(context.Context, ClaimedRun) error {
			<-release // hold every run in flight until we release them together
			executed.Add(1)
			return nil
		}})

	var wg sync.WaitGroup
	for i := 0; i < runs; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Concurrent Dispatch calls stress the in-flight counter mutex under -race; the
			// real loop is single-goroutine, but the counter must be safe regardless.
			assert.NoError(t, d.Dispatch(terraformRun("r")))
		}()
	}
	wg.Wait()

	n, _ := d.InFlight()
	assert.Equal(t, runs, n, "all dispatched runs are in flight until released")

	close(release)
	require.Eventually(t, func() bool { n, _ := d.InFlight(); return n == 0 },
		2*time.Second, time.Millisecond)
	assert.Equal(t, int64(runs), executed.Load())
}

func TestInProcess_WaitDrainsInFlightWithinGracePeriod(t *testing.T) {
	d := newTestInProcess(t, time.Minute, // generous grace; runs finish well before it
		fakeHandler{fn: func(context.Context, ClaimedRun) error {
			time.Sleep(20 * time.Millisecond)
			return nil
		}})

	require.NoError(t, d.Dispatch(terraformRun("r1")))
	require.NoError(t, d.Dispatch(terraformRun("r2")))

	start := time.Now()
	d.Wait()
	assert.Less(t, time.Since(start), time.Minute, "Wait returned when runs drained, not after grace")

	n, _ := d.InFlight()
	assert.Equal(t, 0, n)
}

func TestInProcess_WaitCancelsRunsAfterGraceExpiry(t *testing.T) {
	sawCancel := make(chan struct{})
	d := newTestInProcess(t, 20*time.Millisecond, // tiny grace; the run outlives it
		fakeHandler{fn: func(ctx context.Context, run ClaimedRun) error {
			<-ctx.Done() // a long/sync-polling run that only stops on cancellation
			close(sawCancel)
			return ctx.Err()
		}})

	require.NoError(t, d.Dispatch(terraformRun("r1")))
	require.Eventually(t, func() bool { n, _ := d.InFlight(); return n == 1 },
		time.Second, time.Millisecond)

	d.Wait() // grace expires -> context cancelled -> handler unblocks -> Wait returns

	select {
	case <-sawCancel:
	case <-time.After(time.Second):
		t.Fatal("handler's context was not cancelled at grace expiry")
	}
	n, _ := d.InFlight()
	assert.Equal(t, 0, n)
}

func TestInProcess_DoneCoalescesBurstsOfCompletions(t *testing.T) {
	d := newTestInProcess(t, time.Minute,
		fakeHandler{fn: func(context.Context, ClaimedRun) error { return nil }})

	// Three fast runs complete without anyone draining Done(); the buffered (cap 1) channel
	// must coalesce their wakes rather than block a completing goroutine.
	for i := 0; i < 3; i++ {
		require.NoError(t, d.Dispatch(terraformRun("r")))
	}
	d.Wait()

	select {
	case <-d.Done():
	case <-time.After(time.Second):
		t.Fatal("expected at least one completion wake")
	}
	select {
	case <-d.Done():
		t.Fatal("wakes must coalesce: at most one pending signal, not one per completion")
	default:
	}
}
