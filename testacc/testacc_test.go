package testacc

import (
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/meshcloud/meshstack-cli/client"
	"github.com/meshcloud/meshstack-cli/pkg/auth"
	"github.com/stretchr/testify/require"
)

const (
	envTestAcc = "BUILDING_BLOCK_RUNNER_TEST_ACC"
	testAccOn  = "1"

	envEndpoint = "MESHSTACK_ENDPOINT"
	// The meshcloud-hosted-runner key, with exactly the rights meshStack gives its own runners.
	envRunnerKey    = "MESHSTACK_API_KEY"
	envRunnerSecret = "MESHSTACK_API_SECRET"
)

type localStack struct {
	endpoint     string
	api          client.Client
	runnerKey    string
	runnerSecret string
}

func requireLocalStack(t *testing.T) localStack {
	t.Helper()
	if os.Getenv(envTestAcc) != testAccOn {
		t.Skip("acceptance tests are off. Bring up the local dev stack of ../meshfed-release, export its values with `set -a; . ../.env-testacc-building-block-runner; set +a`, and run `go test ./testacc/... -run TestAcc`")
	}
	endpoint := strings.TrimSuffix(requireEnv(t, envEndpoint), "/")
	endpointUrl, err := url.Parse(endpoint)
	require.NoError(t, err)
	require.Truef(t, slices.Contains([]string{"localhost", "127.0.0.1", "::1"}, endpointUrl.Hostname()),
		"%s=%q does not name a loopback address. These tests write objects, so they run against a local dev stack and nothing else.",
		envEndpoint, endpoint)
	runnerKey, runnerSecret := requireEnv(t, envRunnerKey), requireEnv(t, envRunnerSecret)
	requireAnswering(t, endpoint)

	// An own config dir keeps a developer's meshStack CLI profiles out of the resolution. The
	// version check would ask GitHub for releases of this repository, which do not version a client.
	t.Setenv("MESHSTACK_CONFIG_DIR", t.TempDir())
	t.Setenv("MESHSTACK_SKIP_VERSION_CHECK", "1")
	api, err := auth.ResolveClient(t.Context(), auth.ResolveClientOptions{
		Version:    "0.0.0-testacc",
		GitHubRepo: "meshcloud/building-block-runner",
	})
	require.NoError(t, err)

	return localStack{
		endpoint:     endpoint,
		api:          api,
		runnerKey:    runnerKey,
		runnerSecret: runnerSecret,
	}
}

func requireEnv(t *testing.T, key string) string {
	t.Helper()
	value := os.Getenv(key)
	require.NotEmptyf(t, value,
		"%s is not set. `./gradlew :building-block-runner:satelliteEnv` in ../meshfed-release writes ../.env-testacc-building-block-runner, and `set -a; . ../.env-testacc-building-block-runner; set +a` exports it.", key)
	return value
}

// requireAnswering fails at once where no meshStack listens, because the clients under test retry.
func requireAnswering(t *testing.T, endpoint string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, endpoint+"/mesh/info", nil)
	require.NoError(t, err)
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	require.NoErrorf(t, err, "%s is not answering; bring the local dev stack up first", endpoint)
	defer func() { _ = resp.Body.Close() }()
	require.Equalf(t, http.StatusOK, resp.StatusCode, "%s/mesh/info answered %s", endpoint, resp.Status)
}
