package gitlab

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLoadConfig_RealContainerFiles_DeepMerge drives LoadConfig against the ACTUAL shipped
// files (containers/runner-config.yml, the shared base introduced by this port, and
// containers/gitlab-block-runner/runner-config.yml, the per-impl overlay) rather than a
// fixture -- proving the base < per-impl < env layering actually works end to end
// and that these files stay in sync with LoadConfig's expectations (a broken shared base
// file would otherwise only surface at container-smoke or deploy time).
func TestLoadConfig_RealContainerFiles_DeepMerge(t *testing.T) {
	t.Setenv("RUNNER_BASE_CONFIG_FILE", "../../containers/runner-config.yml")
	t.Setenv("RUNNER_CONFIG_FILE", "../../containers/gitlab-block-runner/runner-config.yml")
	t.Setenv("RUNNER_PRIVATE_KEY_FILE", filepath.Join(t.TempDir(), "no-such-key-file.pem"))

	cfg, err := LoadConfig(testLog(), "test-version", false)
	require.NoError(t, err)
	require.Equal(t, "bfe76555-7a69-48e8-8cc0-8e02eb76fc22", cfg.Uuid)
	require.Equal(t, "http://localhost:8303", cfg.Api.Url)
	require.NotEmpty(t, cfg.PrivateKeyPEM, "the shared base layer's dev key must be picked up")
}
