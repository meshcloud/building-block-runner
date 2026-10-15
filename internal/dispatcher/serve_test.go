package dispatcher

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json/v2"
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
	"github.com/meshcloud/building-block-runner/internal/crypto"
	"github.com/meshcloud/building-block-runner/internal/testmeshstack"
)

type failingDispatcher struct {
	finished chan RunFinished
}

func (failingDispatcher) Init(context.Context) (InitState, error) {
	return InitState{}, nil
}

func (d failingDispatcher) Start(_ context.Context, run Run) error {
	go func() {
		d.finished <- RunFinished{RunUuid: run.Metadata.Uuid, Err: errors.New("tofu failed")}
		close(d.finished)
	}()
	return nil
}

func (d failingDispatcher) Finished() <-chan RunFinished {
	return d.finished
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
	encoded, err := json.Marshal(run)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, encoded, 0o600))

	var dispatched config.Config
	previous := Current
	t.Cleanup(func() { Current = previous })
	Current = func(_ context.Context, cfg config.Config, _ client.MeshRunnerClient) (Dispatcher, error) {
		dispatched = cfg
		return failingDispatcher{finished: make(chan RunFinished)}, nil
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

func Test_NewClient_RetriesUntilMeshfedAnswers(t *testing.T) {
	testmeshstack.Isolate(t)
	t.Setenv(setting.SkipVersionCheck.EnvKey(), "")
	var calls atomic.Int32
	answering := func(version string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			assert.Equal(t, "/mesh/info", r.URL.Path)
			_, _ = w.Write([]byte(`{"version":"` + version + `"}`))
		})
	}
	newClient := func(endpoint string) error {
		api := config.Api{Url: xurl.MustParse(endpoint)}
		api.Auth.Basic = config.BasicAuth{Username: "user", Password: "password"}
		_, err := newClientOnceMeshfedAnswers(t.Context(), log.New(io.Discard, "", 0), setting.SingleSource(api), 10*time.Millisecond)
		return err
	}

	t.Run("retries until meshfed listens", func(t *testing.T) {
		calls.Store(0)
		listener, err := new(net.ListenConfig).Listen(t.Context(), "tcp", "127.0.0.1:0")
		require.NoError(t, err)
		addr := listener.Addr().String()
		require.NoError(t, listener.Close())
		server := httptest.NewUnstartedServer(answering("2099.1.0"))
		t.Cleanup(server.Close)
		time.AfterFunc(100*time.Millisecond, func() {
			lateListener, err := new(net.ListenConfig).Listen(t.Context(), "tcp", addr)
			if err != nil {
				t.Error(err)
				return
			}
			server.Listener = lateListener
			server.Start()
		})

		require.NoError(t, newClient("http://"+addr))
		assert.Equal(t, int32(1), calls.Load())
	})

	t.Run("fails at once on an unsupported version", func(t *testing.T) {
		calls.Store(0)
		server := httptest.NewServer(answering("2020.1.0"))
		t.Cleanup(server.Close)

		require.ErrorContains(t, newClient(server.URL), "unsupported meshStack version")
		assert.Equal(t, int32(1), calls.Load())
	})
}

func Test_Serve_FillsAnEmptyForTypesFromTheBuild(t *testing.T) {
	testmeshstack.Isolate(t)
	t.Setenv(setting.SkipVersionCheck.EnvKey(), "")
	t.Setenv(claimRunYmlEnv, "")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"version":"2020.1.0"}`))
	}))
	t.Cleanup(server.Close)
	previous := DefaultForTypes
	t.Cleanup(func() { DefaultForTypes = previous })
	DefaultForTypes = func() config.RunnerTypes { return config.RunnerTypes{config.RunnerTypeTf} }
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	cfg := testConfig(1)
	cfg.Api.Url = xurl.MustParse(server.URL)
	cfg.Api.Auth.Basic = config.BasicAuth{Username: "user", Password: "password"}
	cfg.Crypto = crypto.Crypto{PrivateKeys: crypto.PrivateKeys{{PrivateKey: key}}}
	cfg.Dispatcher.Timeout = xtime.Minutes(60)

	err = Serve(t.Context(), cfg, log.New(io.Discard, "", 0))

	assert.ErrorContains(t, err, "unsupported meshStack version", "the config passed validation and Serve contacted meshStack")
}
