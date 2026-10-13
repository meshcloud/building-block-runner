package dispatcher

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/meshcloud/meshstack-cli/client"
	"github.com/meshcloud/meshstack-cli/pkg/setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/config/xtime"
	"github.com/meshcloud/building-block-runner/internal/config/xurl"
	"github.com/meshcloud/building-block-runner/internal/testmeshstack"
)

type failingDispatcher struct {
	status chan RunStatus
}

func (d failingDispatcher) Start(_ context.Context, run Run) error {
	go func() {
		d.status <- RunStatus{RunUuid: run.Metadata.Uuid, Ended: true, Err: errors.New("tofu failed")}
		close(d.status)
	}()
	return nil
}

func (d failingDispatcher) Status() <-chan RunStatus {
	return d.status
}

func Test_ServeRunFile_ReportsTheFailureWithTheRunTokenAloneAndNoVersionCheck(t *testing.T) {
	testmeshstack.Isolate(t)
	t.Setenv(setting.SkipVersionCheck.EnvKey(), "")
	var mu sync.Mutex
	authorizations := map[string]string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		assert.NotEqual(t, "/mesh/info", r.URL.Path, "the runner that wrote the run file checked the version")
		authorizations[r.Method] = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(server.Close)

	run := newManualRun(t)
	run.Spec.RunToken = testmeshstack.Token
	run.Links.RegisterSource.Href = server.URL + "/source"
	run.Links.UpdateSource.Href = server.URL + "/source/{sourceId}"
	path := filepath.Join(t.TempDir(), "run.yml")
	encoded, err := EncodeRunYaml(run)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, encoded, 0o600))

	var dispatched config.Config
	previous := Current
	t.Cleanup(func() { Current = previous })
	Current = func(_ context.Context, cfg config.Config, _ client.MeshRunnerClient) (Dispatcher, error) {
		dispatched = cfg
		return failingDispatcher{status: make(chan RunStatus)}, nil
	}
	cfg := config.Config{Uuid: runnerUuid}
	cfg.Api.Url = xurl.MustParse(server.URL)
	cfg.Api.Auth.Key = config.ApiKey{ClientId: "a-second-credential", ClientSecret: "secret"}
	cfg.Dispatcher.ForTypes = config.RunnerTypes{config.RunnerTypeManual}
	cfg.Dispatcher.Timeout = xtime.Minutes(60)

	require.NoError(t, serveRunFile(t.Context(), cfg, log.New(io.Discard, "", 0), path))

	bearer := "Bearer " + run.Spec.RunToken
	assert.Equal(t, map[string]string{http.MethodPost: bearer, http.MethodPatch: bearer}, authorizations)
	assert.Zero(t, dispatched.Dispatcher.Timeout.Duration, "the writer of the run file owns the deadline")
}

func meshInfoAnswering(t *testing.T, version string, calls *atomic.Int32) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		assert.Equal(t, "/mesh/info", r.URL.Path)
		_, _ = w.Write([]byte(`{"version":"` + version + `"}`))
	})
}

func versionCheckingSettings(t *testing.T, endpoint string) setting.Sources {
	t.Helper()
	testmeshstack.Isolate(t)
	t.Setenv(setting.SkipVersionCheck.EnvKey(), "")
	api := config.Api{Url: xurl.MustParse(endpoint)}
	api.Auth.Basic = config.BasicAuth{Username: "user", Password: "password"}
	return setting.SingleSource(api)
}

func Test_NewClientOnceMeshfedAnswers_RetriesUntilMeshfedListens(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := listener.Addr().String()
	require.NoError(t, listener.Close())
	var calls atomic.Int32
	server := httptest.NewUnstartedServer(meshInfoAnswering(t, "2099.1.0", &calls))
	t.Cleanup(server.Close)
	time.AfterFunc(100*time.Millisecond, func() {
		lateListener, err := net.Listen("tcp", addr)
		assert.NoError(t, err)
		server.Listener = lateListener
		server.Start()
	})

	_, err = newClientOnceMeshfedAnswers(t.Context(), log.New(io.Discard, "", 0), versionCheckingSettings(t, "http://"+addr), 10*time.Millisecond)

	require.NoError(t, err)
	assert.Equal(t, int32(1), calls.Load())
}

func Test_NewClientOnceMeshfedAnswers_FailsAtOnceOnAnUnsupportedVersion(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(meshInfoAnswering(t, "2020.1.0", &calls))
	t.Cleanup(server.Close)

	_, err := newClientOnceMeshfedAnswers(t.Context(), log.New(io.Discard, "", 0), versionCheckingSettings(t, server.URL), 10*time.Millisecond)

	assert.ErrorContains(t, err, "unsupported meshStack version")
	assert.Equal(t, int32(1), calls.Load())
}

func Test_Serve_ValidatesTheDefaultForTypes(t *testing.T) {
	t.Setenv(claimRunYmlEnv, "")
	previous := DefaultForTypes
	t.Cleanup(func() { DefaultForTypes = previous })
	DefaultForTypes = func() config.RunnerTypes { return config.RunnerTypes{config.RunnerTypeTf} }

	err := Serve(t.Context(), config.Config{}, log.New(io.Discard, "", 0))

	require.Error(t, err)
	assert.NotContains(t, err.Error(), "dispatcher.forTypes")
}
