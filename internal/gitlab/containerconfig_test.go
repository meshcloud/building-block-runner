package gitlab

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLoadConfig_RealContainerFiles_DeepMerge drives LoadConfig against the ACTUAL shipped
// shared fit config (cmd/runner-config.yml) rather than a fixture, proving it stays in sync
// with LoadConfig's expectations (a broken file would otherwise only surface at
// container-smoke or deploy time). gitlab no longer has a shared base layer to fall back
// to for a private key, so single-run mode must load cleanly while polling mode -- with no
// operator-supplied key -- must fail fast. api.url comes from the compiled-in defaultApiUrl,
// since the shared file drops url.
func TestLoadConfig_RealContainerFiles_DeepMerge(t *testing.T) {
	t.Setenv("RUNNER_CONFIG_FILE", "../../cmd/runner-config.yml")

	cfg, err := LoadConfig(testLog(), "test-version", true)
	require.NoError(t, err)
	require.Equal(t, "98520496-627d-43e6-82da-ce499179ff3f", cfg.Uuid)
	require.Equal(t, "http://localhost:8080", cfg.Api.Url)

	t.Setenv("RUNNER_PRIVATE_KEY_FILE", filepath.Join(t.TempDir(), "no-such-key-file.pem"))
	_, err = LoadConfig(testLog(), "test-version", false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "private key")
}
