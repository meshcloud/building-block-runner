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
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/internal/dispatch"
	"github.com/meshcloud/building-block-runner/internal/meshapi"
	"github.com/meshcloud/building-block-runner/internal/observability"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestDetectSingleRun_EnvUnset(t *testing.T) {
	single, err := detectSingleRun(func(string) string { return "" }, os.Stat)

	require.NoError(t, err)
	require.False(t, single)
}

func TestDetectSingleRun_FileMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.json")
	getenv := func(string) string { return path }

	single, err := detectSingleRun(getenv, os.Stat)

	require.Error(t, err)
	require.Contains(t, err.Error(), RunJsonFilePathEnv)
	require.Contains(t, err.Error(), path)
	require.False(t, single)
}

func TestDetectSingleRun_FileEmpty(t *testing.T) {
	path := writeRunFile(t, []byte{})
	getenv := func(string) string { return path }

	single, err := detectSingleRun(getenv, os.Stat)

	require.Error(t, err)
	require.Contains(t, err.Error(), RunJsonFilePathEnv)
	require.Contains(t, err.Error(), path)
	require.False(t, single)
}

func TestDetectSingleRun_FilePresent(t *testing.T) {
	path := writeRunFile(t, validRunDetailsJson(t))
	getenv := func(string) string { return path }

	single, err := detectSingleRun(getenv, os.Stat)

	require.NoError(t, err)
	require.True(t, single)
}

func TestDetectSingleRun_RealEnv(t *testing.T) {
	single, err := DetectSingleRun()

	require.NoError(t, err)
	require.False(t, single)
}

func writeRunFile(t *testing.T, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "run.json")
	require.NoError(t, os.WriteFile(path, data, 0o600))
	return path
}

func validRunDetailsJson(t *testing.T) []byte {
	t.Helper()
	dto := &meshapi.Run{
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
