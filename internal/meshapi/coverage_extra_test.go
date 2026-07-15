package meshapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Exercises the happy-path bodies of the run-endpoint methods through the real transport,
// plus the backward-compat constructor wrappers, so the consolidated client's success
// paths are pinned (not just its error paths).
func TestRunClient_HappyPaths(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/meshobjects/meshbuildingblockruns/create":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"metadata":{"uuid":"run-1"},"spec":{"runToken":"tok"}}`))
		case r.Method == http.MethodPost: // register-source
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodPatch: // status update
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"runAborted":true}`))
		default:
			w.WriteHeader(http.StatusTeapot)
		}
	}))
	defer srv.Close()

	// NewClient / NewClientWithHTTP are the backward-compat wrappers over NewRunClient.
	client := NewClientWithHTTP(srv.URL, "node", BasicAuth{Username: "u", Password: "p"}, srv.Client())

	dto, raw, err := client.FetchRun("runner")
	require.NoError(t, err)
	assert.Equal(t, "run-1", dto.Metadata.Uuid)
	assert.Equal(t, "tok", dto.Spec.RunToken)
	assert.NotEmpty(t, raw)

	require.NoError(t, client.RegisterSource("run-1", RegistrationDTO{Source: SourceDTO{Id: "s"}}))

	body, err := client.PatchStatus("run-1", "s", map[string]string{"status": "SUCCEEDED"})
	require.NoError(t, err)
	assert.Contains(t, string(body), "runAborted")

	// NewClient (default http.Client wrapper) constructs against an unreachable host: it
	// must build without panicking; the request itself is not driven here.
	assert.NotNil(t, NewClient(srv.URL, "node", BasicAuth{}))
}

// FetchRun rejects a 2xx response whose body parses but carries no UUID (the frozen
// "fetched run has no UUID" guard).
func TestRunClient_FetchRun_EmptyUuidRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"metadata":{"uuid":""}}`))
	}))
	defer srv.Close()

	_, _, err := NewClientWithHTTP(srv.URL, "node", BasicAuth{}, srv.Client()).FetchRun("runner")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no UUID")
}

func TestNewApiKeyAuth_WrapsRetryTransport(t *testing.T) {
	auth := NewApiKeyAuth("http://example.invalid", "id", "secret")
	require.NotNil(t, auth)
	_, ok := auth.httpClient.Transport.(*retryTransport)
	assert.True(t, ok, "the production login client's transport should be retry-wrapped")
}

func TestNoopLoggerAndSlogAdapterInfoWarn(t *testing.T) {
	// noop logger: all three levels are no-ops and must not panic.
	n := noopLogger{}
	n.Debug(context.Background(), "d")
	n.Info(context.Background(), "i")
	n.Warn(context.Background(), "w")

	// slog adapter: Debug/Info/Warn forward without panicking.
	l := SlogLogger(slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug})))
	l.Debug(context.Background(), "d", "k", "v")
	l.Info(context.Background(), "i", "k", "v")
	l.Warn(context.Background(), "w", "k", "v")

	assert.Equal(t, "{\n  \"a\": 1\n}", loggedBody([]byte(`{"a":1}`)).String())
}

// ---- DTO helpers (pure functions; pinned so they count toward the shared-package gate) ----

func TestDtoHelpers(t *testing.T) {
	raw := []byte(`{"metadata":{"uuid":"u1"},"spec":{"buildingBlock":{"spec":{"workspaceIdentifier":"ws"}},"buildingBlockDefinition":{"uuid":"def","spec":{"workspaceIdentifier":"defws","implementation":{"type":"TERRAFORM"}}}}}`)

	dto, err := ParseRunDetails(raw)
	require.NoError(t, err)
	assert.Equal(t, "u1", dto.Metadata.Uuid)

	info := dto.GetRunInfo()
	assert.Equal(t, "u1", info.Uuid)
	assert.Equal(t, "ws", info.WorkspaceIdentifier)
	assert.Equal(t, "def", info.BuildingBlockDefinitionUuid)
	assert.Equal(t, "defws", info.BuildingBlockDefinitionWorkspace)

	implType, err := dto.Spec.Definition.Spec.GetImplementationType()
	require.NoError(t, err)
	assert.Equal(t, ImplTypeTerraform, implType)

	_, err = ParseRunDetails([]byte("not json"))
	require.Error(t, err)

	assert.Equal(t, RunnerTypeGitLabPipeline, ToRunnerType(ImplTypeGitLabCICD))
	assert.Equal(t, RunnerTypeAzureDevOpsPipeline, ToRunnerType(ImplTypeAzureDevOps))
	assert.Equal(t, RunnerTypeTerraform, ToRunnerType(ImplTypeTerraform))
}
