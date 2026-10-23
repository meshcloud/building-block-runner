package poller

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
	"uuid"

	"github.com/meshcloud/meshstack-cli/client"
	"github.com/meshcloud/meshstack-cli/client/types/xurl"
	"github.com/meshcloud/meshstack-cli/pkg/setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/v2/internal/config"
	"github.com/meshcloud/building-block-runner/v2/internal/testmeshstack"
)

func Test_NewClientOnceMeshfedAnswers_RetriesUntilMeshfedAnswers(t *testing.T) {
	testmeshstack.Isolate(t)
	t.Setenv(setting.SkipVersionCheck.EnvKey(), "false")
	closedPort, err := new(net.ListenConfig).Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := closedPort.Addr().String()
	require.NoError(t, closedPort.Close())
	var api config.Api
	//goland:noinspection HttpUrlsUsage // a local test server
	api.Url = xurl.MustParsef("http://%s", addr)
	api.Auth.Key = config.ApiKey{ClientId: uuid.New().String(), ClientSecret: "a-secret"}
	returned := make(chan error, 1)
	go func() {
		_, err := newClientOnceMeshfedAnswers(t.Context(), setting.SingleSource(api), 10*time.Millisecond)
		returned <- err
	}()

	t.Run("retries while nothing listens", func(t *testing.T) {
		select {
		case err := <-returned:
			t.Fatalf("returned before meshfed answered: %v", err)
		case <-time.After(200 * time.Millisecond):
		}
	})

	t.Run("returns the first answer", func(t *testing.T) {
		listener, err := new(net.ListenConfig).Listen(t.Context(), "tcp", addr)
		require.NoError(t, err)
		server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}))
		server.Listener = listener
		server.Start()
		t.Cleanup(server.Close)

		select {
		case err := <-returned:
			httpErr, ok := errors.AsType[client.HttpError](err)
			require.True(t, ok, "not an http error: %v", err)
			assert.Equal(t, http.StatusUnauthorized, httpErr.StatusCode)
		case <-time.After(5 * time.Second):
			t.Fatal("did not return once meshfed answered")
		}
	})

	t.Run("a gateway error counts as unanswered", func(t *testing.T) {
		assert.True(t, isUnanswered(client.HttpError{StatusCode: http.StatusServiceUnavailable}))
	})
}
