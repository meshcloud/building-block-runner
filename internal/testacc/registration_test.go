package testacc

import (
	"io"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
		assert.Equal(c, "TERRAFORM", runner.Spec.ImplementationType)
		require.NotNil(c, runner.Status)
		assert.NotNil(c, runner.Status.LastSeen, "meshStack shows a runner that claims as online through lastSeen")
	}, time.Minute, runnerproc.PollInterval)
	assert.Contains(t, scrapeMetrics(t, metricsPort), "building_block_runner_")
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
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://127.0.0.1:"+strconv.Itoa(port)+"/metrics", nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return string(body)
}
