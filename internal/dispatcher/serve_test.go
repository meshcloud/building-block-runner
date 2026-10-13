package dispatcher

import (
	"context"
	"errors"
	"io"
	"log"
	gohttp "net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/config/xtime"
	"github.com/meshcloud/building-block-runner/internal/http"
	"github.com/meshcloud/building-block-runner/internal/http/meshapi"
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

func Test_ServeRunFile_ReportsTheFailureWithTheRunTokenAndWithoutApiConfig(t *testing.T) {
	var mu sync.Mutex
	authorizations := map[string]string{}
	server := httptest.NewServer(gohttp.HandlerFunc(func(w gohttp.ResponseWriter, r *gohttp.Request) {
		mu.Lock()
		defer mu.Unlock()
		authorizations[r.Method] = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(server.Close)

	run := newManualRun(t)
	run.Links.RegisterSource.Href = server.URL + "/source"
	run.Links.UpdateSource.Href = server.URL + "/source/{sourceId}"
	path := filepath.Join(t.TempDir(), "run.yml")
	encoded, err := EncodeRunYaml(run)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, encoded, 0o600))

	var dispatched config.Config
	previous := Current
	t.Cleanup(func() { Current = previous })
	Current = func(_ context.Context, cfg config.Config, _ meshapi.MeshRunnerClient) (Dispatcher, error) {
		dispatched = cfg
		return failingDispatcher{status: make(chan RunStatus)}, nil
	}
	cfg := config.Config{Uuid: runnerUuid}
	cfg.Dispatcher.ForTypes = config.RunnerTypes{config.RunnerTypeManual}
	cfg.Dispatcher.Timeout = xtime.Minutes(60)
	client := http.Client{Client: server.Client(), UserAgent: http.UserAgent{GitHubRepo: "meshcloud/building-block-runner", Version: "test"}}

	require.NoError(t, serveRunFile(t.Context(), cfg, log.New(io.Discard, "", 0), client, path))

	bearer := "Bearer " + run.Spec.RunToken
	assert.Equal(t, map[string]string{gohttp.MethodPost: bearer, gohttp.MethodPatch: bearer}, authorizations)
	assert.Zero(t, dispatched.Dispatcher.Timeout.Duration, "the writer of the run file owns the deadline")
}
