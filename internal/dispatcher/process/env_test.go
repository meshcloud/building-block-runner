package process

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/v2/internal/config"
)

func Test_childEnv_PassesProxyAndCaSettingsAndNothingUnlisted(t *testing.T) {
	proxyAndCa := []string{
		"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy", "SSL_CERT_DIR", "SSL_CERT_FILE",
	}
	for _, key := range proxyAndCa {
		t.Setenv(key, "value of "+key)
	}
	t.Setenv("UNLISTED", "value")
	d := &Dispatcher{cfg: testConfig()}

	env, err := d.childEnv(newRun(), config.RunnerTypeManual)
	require.NoError(t, err)

	vars := map[string]string{}
	for _, entry := range env {
		key, value, _ := strings.Cut(entry, "=")
		vars[key] = value
	}
	for _, key := range proxyAndCa {
		assert.Equal(t, "value of "+key, vars[key])
	}
	assert.Subset(t, slices.Concat(proxyAndCa, []string{
		"RUNNER_CONFIG", "RUNNER_UUID", "MESHSTACK_ENDPOINT", "MESHSTACK_API_TOKEN",
		"PATH", "HOME", "TMPDIR", "TMP", "TEMP", "RUNNER_LOG_LEVEL", "RUNNER_LOG_FORMAT", "GOCOVERDIR",
		"CUSTOM_CA_CERTS_PATH", "SSH_KNOWN_HOSTS", "NIX_CONFIG", "MESHSTACK_SKIP_VERSION_CHECK",
		"TF_LOG", "TF_LOG_CORE", "TF_LOG_PROVIDER", "TF_LOG_PATH", "CHECKPOINT_DISABLE", "TF_IN_AUTOMATION",
	}), slices.Collect(maps.Keys(vars)))
}
