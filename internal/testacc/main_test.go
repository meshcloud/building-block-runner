package testacc

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/meshcloud/meshstack-cli/client"
	"github.com/meshcloud/meshstack-cli/pkg/auth"
	"github.com/opentofu/tofudl"

	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/executor/tf"
)

const (
	envTestAcc = "BUILDING_BLOCK_RUNNER_TEST_ACC"
	envKeep    = "RUNNER_TESTACC_KEEP"
	envOn      = "1"

	envEndpoint           = "MESHSTACK_ENDPOINT"
	envRunnerClientId     = "RUNNER_API_CLIENT_ID"
	envRunnerClientSecret = "RUNNER_API_CLIENT_SECRET"

	moduleRoot       = "../.."
	providerCheckout = "../../../terraform-provider-meshstack"
)

// suite is nil while the acceptance tests are off.
var suite *accSuite

type accSuite struct {
	endpoint  string
	api       client.Client
	runnerKey config.ApiKey
	keycloak  *keycloakClient
	pipelines *pipelineMock
	keep      bool

	runnerBinary string
	coverDir     string
	tfCacheDir   string
	tofu         *tf.TfBinaries
	tofuVersion  string
	// tofuCliConfig is empty when the provider comes from the registry.
	tofuCliConfig string
}

func TestMain(m *testing.M) {
	flag.Parse()
	if os.Getenv(envTestAcc) != envOn {
		os.Exit(m.Run())
	}
	os.Exit(runSuite(m))
}

func runSuite(m *testing.M) int {
	dir, err := os.MkdirTemp("", "runner-testacc-")
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		return 1
	}
	s, err := setUpSuite(context.Background(), dir)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "setting up the acceptance tests failed:", err)
		return 1
	}
	suite = s
	code := m.Run()
	s.pipelines.server.Close()
	s.reportCoverage(dir)
	if s.keep {
		_, _ = fmt.Fprintln(os.Stderr, "kept the suite dir", dir, "and the Keycloak client", s.keycloak.clientId)
		return code
	}
	_ = os.RemoveAll(dir)
	if err := s.keycloak.delete(context.Background()); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "deleting the Keycloak client failed:", err)
		return cmp.Or(code, 1)
	}
	return code
}

func requireSuite(t *testing.T) *accSuite {
	t.Helper()
	if suite == nil {
		t.Skipf("acceptance tests are off. Bring up the local dev stack of ../meshfed-release, export its values with `set -a; . ../.env-testacc-building-block-runner; set +a`, and run `go test ./internal/testacc/ -run TestAcc` with %s=%s", envTestAcc, envOn)
	}
	return suite
}

func setUpSuite(ctx context.Context, dir string) (*accSuite, error) {
	s := &accSuite{keep: os.Getenv(envKeep) == envOn, tfCacheDir: filepath.Join(dir, "tfcache")}
	var err error
	if s.endpoint, err = loopbackEndpoint(ctx); err != nil {
		return nil, err
	}
	if s.runnerKey, err = runnerKeyFromEnv(); err != nil {
		return nil, err
	}
	if s.api, err = harnessClient(ctx, dir); err != nil {
		return nil, err
	}
	if s.keycloak, err = setUpKeycloakClient(ctx); err != nil {
		return nil, err
	}
	s.pipelines = newPipelineMock(s.api.Runner)
	if s.runnerBinary, err = buildRunner(ctx, dir); err != nil {
		return nil, err
	}
	s.coverDir = coverDir(dir)
	if err = os.MkdirAll(s.coverDir, 0o750); err != nil {
		return nil, err
	}
	if s.tofuVersion, err = newestStableTofu(ctx); err != nil {
		return nil, err
	}
	if s.tofu, err = tf.NewTfBin(filepath.Join(s.tfCacheDir, "bin"), time.Minute, os.Stderr); err != nil {
		return nil, err
	}
	// Installs tofu once for the harness and for every runner, which share the cache dir.
	if _, err = s.tofu.GetTF(ctx, dir, s.tofuVersion); err != nil {
		return nil, err
	}
	if s.tofuCliConfig, err = devOverrideProvider(ctx, dir); err != nil {
		return nil, err
	}
	return s, nil
}

// loopbackEndpoint refuses a remote meshStack, because the tests write objects.
func loopbackEndpoint(ctx context.Context) (string, error) {
	endpoint := strings.TrimSuffix(os.Getenv(envEndpoint), "/")
	endpointUrl, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	if !slices.Contains([]string{"localhost", "127.0.0.1", "::1"}, endpointUrl.Hostname()) {
		return "", fmt.Errorf("%s=%q does not name a loopback address", envEndpoint, endpoint)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/mesh/info", nil)
	if err != nil {
		return "", err
	}
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return "", fmt.Errorf("%s is not answering, bring the local dev stack up first: %w", endpoint, err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s/mesh/info answered %s", endpoint, resp.Status)
	}
	return endpoint, nil
}

func runnerKeyFromEnv() (config.ApiKey, error) {
	key := config.ApiKey{ClientId: os.Getenv(envRunnerClientId), ClientSecret: os.Getenv(envRunnerClientSecret)}
	if key.ClientId == "" || key.ClientSecret == "" {
		return key, fmt.Errorf("%s and %s name the API key of the runners under test. `./gradlew :building-block-runner:satelliteEnv` in ../meshfed-release writes them to ../.env-testacc-building-block-runner", envRunnerClientId, envRunnerClientSecret)
	}
	return key, nil
}

// harnessClient authenticates with the MESHSTACK_* environment, which the provider in tofu reads too.
func harnessClient(ctx context.Context, dir string) (client.Client, error) {
	// An own config dir keeps a developer's meshStack CLI profiles out of the resolution.
	if err := os.Setenv("MESHSTACK_CONFIG_DIR", filepath.Join(dir, "meshstack-config")); err != nil {
		return client.Client{}, err
	}
	if err := os.Setenv("MESHSTACK_SKIP_VERSION_CHECK", "1"); err != nil {
		return client.Client{}, err
	}
	return auth.ResolveClient(ctx, auth.ResolveClientOptions{Version: "0.0.0-testacc", GitHubRepo: "meshcloud/building-block-runner"})
}

// buildRunner builds the default image's binary, which starts each run as a process and links every executor.
func buildRunner(ctx context.Context, dir string) (string, error) {
	binary := filepath.Join(dir, "runner")
	build := exec.CommandContext(ctx, "go", "build", "-cover", "-coverpkg=./...", "-o", binary, "./cmd/runner")
	build.Dir = moduleRoot
	if output, err := build.CombinedOutput(); err != nil {
		return "", fmt.Errorf("build the runner: %w\n%s", err, output)
	}
	return binary, nil
}

// coverDir is the one `go test -args -test.gocoverdir` names, so that the runners' coverage joins the run's.
func coverDir(dir string) string {
	if gocoverdir := flag.Lookup("test.gocoverdir"); gocoverdir != nil && gocoverdir.Value.String() != "" {
		return gocoverdir.Value.String()
	}
	return filepath.Join(dir, "cover")
}

func newestStableTofu(ctx context.Context) (string, error) {
	downloader, err := tofudl.New()
	if err != nil {
		return "", err
	}
	versions, err := downloader.ListVersions(ctx, tofudl.ListVersionOptMinimumStability(tofudl.StabilityStable))
	if err != nil {
		return "", err
	}
	if len(versions) == 0 {
		return "", errors.New("OpenTofu lists no stable version")
	}
	return string(versions[0].ID), nil
}

func devOverrideProvider(ctx context.Context, dir string) (string, error) {
	if !exists(providerCheckout) {
		_, _ = fmt.Fprintln(os.Stderr, "no checkout at", providerCheckout, "so tofu takes the meshStack provider from the registry")
		return "", nil
	}
	providerDir := filepath.Join(dir, "provider")
	build := exec.CommandContext(ctx, "go", "build", "-o", filepath.Join(providerDir, "terraform-provider-meshstack"), ".")
	build.Dir = providerCheckout
	if output, err := build.CombinedOutput(); err != nil {
		return "", fmt.Errorf("build the meshStack provider: %w\n%s", err, output)
	}
	cliConfig := filepath.Join(dir, "tofu.tfrc")
	content := fmt.Sprintf(`provider_installation {
  dev_overrides {
    "registry.opentofu.org/meshcloud/meshstack" = %q
  }
  direct {}
}
`, providerDir)
	return cliConfig, os.WriteFile(cliConfig, []byte(content), 0o600)
}

// reportCoverage leaves coverage that `-test.gocoverdir` collects to whoever asked for it.
func (s *accSuite) reportCoverage(dir string) {
	if !strings.HasPrefix(s.coverDir, dir) {
		return
	}
	percent := exec.CommandContext(context.Background(), "go", "tool", "covdata", "percent", "-i="+s.coverDir)
	percent.Dir = moduleRoot
	percent.Stdout, percent.Stderr = os.Stdout, os.Stderr
	if err := percent.Run(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "reporting the coverage of the runners failed:", err)
	}
}
