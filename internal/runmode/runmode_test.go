package runmode

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/internal/dispatch"
	"github.com/meshcloud/building-block-runner/internal/meshapi"
	"github.com/meshcloud/building-block-runner/internal/observability"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestNewLogger(t *testing.T) {
	require.NotNil(t, NewLogger())
}

func TestMain_SingleRun(t *testing.T) {
	var sawErr error
	var sawCtx bool
	r := Runner{
		Name: "test-runner",
		Log:  discardLogger(),
		SingleRun: func(ctx context.Context) int {
			sawCtx = ctx != nil
			sawErr = ctx.Err()
			return 42
		},
		Poll: func(ctx context.Context) int {
			t.Fatal("Poll must not be invoked in single-run mode")
			return -1
		},
	}

	code := Main(true, r)

	require.Equal(t, 42, code)
	require.True(t, sawCtx)
	require.NoError(t, sawErr, "ctx must not be cancelled while SingleRun runs")
}

func TestMain_Poll(t *testing.T) {
	var sawErr error
	var sawCtx bool
	r := Runner{
		Name: "test-runner",
		Log:  discardLogger(),
		SingleRun: func(ctx context.Context) int {
			t.Fatal("SingleRun must not be invoked in poll mode")
			return -1
		},
		Poll: func(ctx context.Context) int {
			sawCtx = ctx != nil
			sawErr = ctx.Err()
			return 7
		},
	}

	code := Main(false, r)

	require.Equal(t, 7, code)
	require.True(t, sawCtx)
	require.NoError(t, sawErr, "ctx must not be cancelled while Poll runs")
}

// fakeLooper records Start/Stop calls; Start marks wg.Done immediately so Serve's
// wg.Wait() returns without a real dispatch.Loop.
type fakeLooper struct {
	mu       sync.Mutex
	started  bool
	stopped  chan struct{}
	stopOnce sync.Once
}

func newFakeLooper() *fakeLooper {
	return &fakeLooper{stopped: make(chan struct{})}
}

func (f *fakeLooper) Start(wg *sync.WaitGroup) {
	f.mu.Lock()
	f.started = true
	f.mu.Unlock()
	wg.Done()
}

func (f *fakeLooper) Stop() {
	f.stopOnce.Do(func() { close(f.stopped) })
}

type fakeDrainer struct {
	waited chan struct{}
}

func newFakeDrainer() *fakeDrainer {
	return &fakeDrainer{waited: make(chan struct{})}
}

func (f *fakeDrainer) Wait() {
	close(f.waited)
}

func TestServe(t *testing.T) {
	loop := newFakeLooper()
	inproc := newFakeDrainer()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel up front: Serve's internal goroutine calls loop.Stop() off ctx.Done()

	code := Serve(ctx, loop, inproc)

	require.Equal(t, 0, code)
	loop.mu.Lock()
	started := loop.started
	loop.mu.Unlock()
	require.True(t, started, "Start must have been called")

	select {
	case <-inproc.waited:
	default:
		t.Fatal("inproc.Wait must have been called")
	}

	// The Stop-on-cancel goroutine races with Serve's return; wait for it rather than
	// asserting immediately (a non-blocking check would be flaky under scheduling).
	select {
	case <-loop.stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("loop.Stop must have been called once ctx was cancelled")
	}
}

func writeRunFile(t *testing.T, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "run.json")
	require.NoError(t, os.WriteFile(path, data, 0o600))
	return path
}

func validRunDetailsJson(t *testing.T) []byte {
	t.Helper()
	dto := &meshapi.RunDetailsDTO{
		Metadata: meshapi.RunMetaDTO{Uuid: "run-uuid"},
	}
	data, err := json.Marshal(dto)
	require.NoError(t, err)
	return data
}

func TestSingleRunResultFromFile_MissingEnv(t *testing.T) {
	code := SingleRunResultFromFile(context.Background(), discardLogger(), "runner-1", meshapi.RunnerImplementationType("tf"),
		func(context.Context, dispatch.ClaimedRun) (bool, error) {
			t.Fatal("fn must not be invoked")
			return false, nil
		})
	require.Equal(t, 1, code)
}

func TestSingleRunResultFromFile_FileNotFound(t *testing.T) {
	t.Setenv(RunJsonFilePathEnv, filepath.Join(t.TempDir(), "does-not-exist.json"))

	code := SingleRunResultFromFile(context.Background(), discardLogger(), "runner-1", meshapi.RunnerImplementationType("tf"),
		func(context.Context, dispatch.ClaimedRun) (bool, error) {
			t.Fatal("fn must not be invoked")
			return false, nil
		})
	require.Equal(t, 1, code)
}

func TestSingleRunResultFromFile_MalformedJson(t *testing.T) {
	t.Setenv(RunJsonFilePathEnv, writeRunFile(t, []byte("{not json")))

	code := SingleRunResultFromFile(context.Background(), discardLogger(), "runner-1", meshapi.RunnerImplementationType("tf"),
		func(context.Context, dispatch.ClaimedRun) (bool, error) {
			t.Fatal("fn must not be invoked")
			return false, nil
		})
	require.Equal(t, 1, code)
}

func TestSingleRunResultFromFile_Success(t *testing.T) {
	t.Setenv(RunJsonFilePathEnv, writeRunFile(t, validRunDetailsJson(t)))

	var gotRun dispatch.ClaimedRun
	code := SingleRunResultFromFile(context.Background(), discardLogger(), "runner-1", meshapi.RunnerImplementationType("tf"),
		func(ctx context.Context, run dispatch.ClaimedRun) (bool, error) {
			gotRun = run
			return true, nil
		})

	require.Equal(t, 0, code)
	require.Equal(t, dispatch.RunId("run-uuid"), gotRun.Id)
}

func TestSingleRunResultFromFile_UnsuccessfulMetered(t *testing.T) {
	var pushed []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pushed = append(pushed, r.Method)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()
	t.Setenv(observability.EnvPushGatewayURL, srv.URL)
	t.Setenv(RunJsonFilePathEnv, writeRunFile(t, validRunDetailsJson(t)))

	code := SingleRunResultFromFile(context.Background(), discardLogger(), "runner-1", meshapi.RunnerImplementationType("tf"),
		func(context.Context, dispatch.ClaimedRun) (bool, error) {
			return false, nil
		})

	require.Equal(t, 0, code)
	require.Equal(t, []string{http.MethodPut}, pushed, "an unsuccessful-but-error-free run must push without deleting")
}

func TestSingleRunResultFromFile_FnError(t *testing.T) {
	t.Setenv(RunJsonFilePathEnv, writeRunFile(t, validRunDetailsJson(t)))

	code := SingleRunResultFromFile(context.Background(), discardLogger(), "runner-1", meshapi.RunnerImplementationType("tf"),
		func(context.Context, dispatch.ClaimedRun) (bool, error) {
			return false, os.ErrClosed
		})

	require.Equal(t, 1, code)
}

func TestSingleRunFromFile_Success(t *testing.T) {
	t.Setenv(RunJsonFilePathEnv, writeRunFile(t, validRunDetailsJson(t)))

	code := SingleRunFromFile(context.Background(), discardLogger(), "runner-1", meshapi.RunnerImplementationType("tf"),
		func(context.Context, dispatch.ClaimedRun) error {
			return nil
		})

	require.Equal(t, 0, code)
}

func TestSingleRunFromFile_Error(t *testing.T) {
	t.Setenv(RunJsonFilePathEnv, writeRunFile(t, validRunDetailsJson(t)))

	code := SingleRunFromFile(context.Background(), discardLogger(), "runner-1", meshapi.RunnerImplementationType("tf"),
		func(context.Context, dispatch.ClaimedRun) error {
			return os.ErrClosed
		})

	require.Equal(t, 1, code)
}
