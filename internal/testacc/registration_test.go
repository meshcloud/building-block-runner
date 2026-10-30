package testacc

import (
	"net"
	gohttp "net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/meshcloud/meshstack-cli/client"
	"github.com/meshcloud/meshstack-cli/client/http"
	"github.com/meshcloud/meshstack-cli/client/types/xurl"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/v2/internal/executor"
	"github.com/meshcloud/building-block-runner/v2/internal/testacc/harness"
	"github.com/meshcloud/building-block-runner/v2/internal/testacc/runnerproc"
)

func TestAccRegistration(t *testing.T) {
	t.Parallel()
	s := requireSuite(t)
	const displayName = "building-block-runner TestAcc, registered"
	metricsPort := freePort(t)

	d := harness.Deploy(t, s, harness.Case{
		ImplementationType:   "TERRAFORM",
		Runner:               runnerproc.Options{ForType: "tf", RegisterAs: displayName, MetricsPort: metricsPort},
		WithoutBuildingBlock: true,
	})

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		runner, err := s.Api.BuildingBlockRunner.Read(t.Context(), d.Runner.Uuid)
		require.NoError(c, err)
		assert.Equal(c, displayName, runner.Spec.DisplayName)
		assert.Equal(c, client.MeshBuildingBlockRunnerImplementationTypeTerraform, runner.Spec.ImplementationType)
		require.NotNil(c, runner.Status)
		assert.NotNil(c, runner.Status.LastSeen, "meshStack shows a runner that claims as online through lastSeen")
	}, time.Minute, runnerproc.PollInterval)
	assert.Contains(t, scrapeMetrics(t, metricsPort), "building_block_runner_")
}

func TestAccRegistrationAfterRefusedLogins(t *testing.T) {
	t.Parallel()
	s := requireSuite(t)
	const displayName = "building-block-runner TestAcc, registered late"

	d := harness.Deploy(t, s, harness.Case{
		ImplementationType:   "TERRAFORM",
		Runner:               runnerproc.Options{ForType: "tf", RegisterAs: displayName, ApiUrl: refusingFirstLogins(t, s.Endpoint, 2)},
		WithoutBuildingBlock: true,
	})

	d.Runner.Records.Await(t, "that the registration failed and is tried again", func(r runnerproc.Record) bool {
		return r.Attr("msg") == "updating the registration failed, trying again"
	})
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		runner, err := s.Api.BuildingBlockRunner.Read(t.Context(), d.Runner.Uuid)
		require.NoError(c, err)
		assert.Equal(c, displayName, runner.Spec.DisplayName)
	}, time.Minute, runnerproc.PollInterval)
}

// refusingFirstLogins answers like a meshStack that still starts: it does not know the API key yet.
func refusingFirstLogins(t *testing.T, endpoint string, refused int32) string {
	t.Helper()
	meshStack, err := url.Parse(endpoint)
	require.NoError(t, err)
	proxy := httputil.NewSingleHostReverseProxy(meshStack)
	var logins atomic.Int32
	server := httptest.NewServer(gohttp.HandlerFunc(func(w gohttp.ResponseWriter, r *gohttp.Request) {
		if r.URL.Path == "/api/login" && logins.Add(1) <= refused {
			gohttp.Error(w, `{"error":"invalid_client"}`, gohttp.StatusUnauthorized)
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	return server.URL
}

func freePort(t *testing.T) int {
	t.Helper()
	listener, err := new(net.ListenConfig).Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := netip.MustParseAddrPort(listener.Addr().String()).Port()
	require.NoError(t, listener.Close())
	return int(port)
}

func scrapeMetrics(t *testing.T, port int) string {
	t.Helper()
	body, err := http.ReadAllBody(executor.NewHttpClient().DoRequest(t.Context(), http.MethodGet, xurl.MustParsef("http://127.0.0.1:%d/metrics", port).URL))
	require.NoError(t, err)
	return string(body)
}
