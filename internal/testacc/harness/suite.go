package harness

import (
	"crypto/rand"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/meshcloud/meshstack-cli/client"
	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/v2/internal/config"
	"github.com/meshcloud/building-block-runner/v2/internal/executor/tf"
	"github.com/meshcloud/building-block-runner/v2/internal/testacc/toxiproxy"
)

type Suite struct {
	Endpoint  string
	Api       client.Client
	RunnerKey config.ApiKey
	Keep      bool

	RunnerBinary string
	CoverDir     string
	TfCacheDir   string
	Tofu         tf.Binaries
	TofuVersion  string
	// TofuCliConfig is empty when the provider comes from the registry.
	TofuCliConfig string
}

func (s Suite) WorkDir(t *testing.T, name string) string {
	t.Helper()
	if !s.Keep {
		dir := filepath.Join(t.TempDir(), name)
		require.NoError(t, os.Mkdir(dir, 0o700))
		return dir
	}
	dir, err := os.MkdirTemp("", "runner-testacc-"+name+"-") //nolint:usetesting // a kept dir outlives the test.
	require.NoError(t, err)
	t.Logf("keeping %s", dir)
	return dir
}

func (s Suite) StartApiProxy(t *testing.T) toxiproxy.Proxy {
	t.Helper()
	endpoint, err := url.Parse(s.Endpoint)
	require.NoError(t, err)
	return toxiproxy.Start(t, endpoint.Host)
}

// UniqueName is a valid workspace identifier.
func UniqueName(t *testing.T, prefix string) string {
	t.Helper()
	return fmt.Sprintf("%s-%s", prefix, strings.ToLower(rand.Text()[:10]))
}
