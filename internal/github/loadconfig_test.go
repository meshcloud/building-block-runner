package github

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/internal/config"
)

func writeConfig(t *testing.T, yaml string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "runner-config.yml")
	require.NoError(t, os.WriteFile(path, []byte(yaml), 0o600))
	t.Setenv("RUNNER_CONFIG_FILE", path)
}

// single-run mode needs no key/auth: the defaults load and validate.
func TestLoadConfig_SingleRunDefaults(t *testing.T) {
	t.Setenv("RUNNER_CONFIG_FILE", filepath.Join(t.TempDir(), "absent.yml"))
	cfg, err := LoadConfig(testLog(), "build-v", true)
	require.NoError(t, err)
	require.Equal(t, config.DefaultRunnerUuid, cfg.Uuid)
	require.Equal(t, config.DefaultApiUrl, cfg.Api.Url)
	require.Equal(t, "build-v", cfg.Version)
	require.Equal(t, config.DefaultMaxConcurrentRuns, cfg.MaxConcurrentRuns) // shared default 3 (was github-only 1)
	require.Empty(t, cfg.PrivateKey)                                         // not resolved in single-run mode
}

// polling mode resolves an inline private key (flat key) and validates.
func TestLoadConfig_PollingWithInlineKey(t *testing.T) {
	writeConfig(t, "uuid: cfg-uuid\nprivateKey: INLINE_PEM\napi:\n  url: http://mesh\n  username: u\n  password: p\n")
	cfg, err := LoadConfig(testLog(), "build-v", false)
	require.NoError(t, err)
	require.Equal(t, "cfg-uuid", cfg.Uuid)
	require.Equal(t, "INLINE_PEM", cfg.PrivateKey)
}

// the Kotlin-era blockrunner: block populates uuid/api + the private key.
func TestLoadConfig_BlockRunnerCompat(t *testing.T) {
	writeConfig(t, `blockrunner:
  uuid: block-uuid
  privateKey: BLOCK_PEM
  api:
    url: http://block-mesh
  auth:
    username: bu
    password: bp
`)
	cfg, err := LoadConfig(testLog(), "build-v", false)
	require.NoError(t, err)
	require.Equal(t, "block-uuid", cfg.Uuid)
	require.Equal(t, "http://block-mesh", cfg.Api.Url)
	require.Equal(t, "BLOCK_PEM", cfg.PrivateKey)
}

// blockrunner.privateKey/privateKeyFile are honored but must warn -- github's port had
// silently dropped this warning (unlike gitlab/azdevops); the accumulated alias inventory
// audit (docs/DEPRECATIONS.md) closes the gap.
func TestLoadConfig_BlockRunnerCompat_PrivateKeyWarns(t *testing.T) {
	writeConfig(t, `blockrunner:
  privateKey: BLOCK_PEM
  api:
    url: http://block-mesh
  auth:
    username: bu
    password: bp
`)
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	cfg, err := LoadConfig(log, "build-v", false)
	require.NoError(t, err)
	require.Equal(t, "BLOCK_PEM", cfg.PrivateKey)
	require.Contains(t, buf.String(), "deprecated")
	require.Contains(t, buf.String(), "blockrunner.privateKey")
}

// env wins last; RUNNER_MAX_CONCURRENT_RUNS is honored; a bad value is a hard error.
func TestLoadConfig_EnvOverridesAndMaxConcurrent(t *testing.T) {
	t.Setenv("RUNNER_CONFIG_FILE", filepath.Join(t.TempDir(), "absent.yml"))
	t.Setenv("RUNNER_UUID", "env-uuid")
	t.Setenv("RUNNER_API_URL", "http://env")
	t.Setenv("RUNNER_API_CLIENT_ID", "cid")
	t.Setenv("RUNNER_API_CLIENT_SECRET", "csecret")
	t.Setenv("RUNNER_MAX_CONCURRENT_RUNS", "7")

	// A resolvable key via RUNNER_PRIVATE_KEY_FILE.
	keyPath := filepath.Join(t.TempDir(), "key.pem")
	require.NoError(t, os.WriteFile(keyPath, []byte("FILE_PEM"), 0o600))
	t.Setenv("RUNNER_PRIVATE_KEY_FILE", keyPath)

	cfg, err := LoadConfig(testLog(), "build-v", false)
	require.NoError(t, err)
	require.Equal(t, "env-uuid", cfg.Uuid)
	require.Equal(t, "http://env", cfg.Api.Url)
	require.Equal(t, "FILE_PEM", cfg.PrivateKey)
	require.Equal(t, 7, cfg.MaxConcurrentRuns)
}

func TestLoadConfig_BadMaxConcurrent(t *testing.T) {
	t.Setenv("RUNNER_CONFIG_FILE", filepath.Join(t.TempDir(), "absent.yml"))
	t.Setenv("RUNNER_MAX_CONCURRENT_RUNS", "notanumber")
	_, err := LoadConfig(testLog(), "build-v", true)
	require.Error(t, err)
}

// polling mode with no resolvable key fails validation (this runner decrypts).
func TestLoadConfig_PollingMissingKey(t *testing.T) {
	writeConfig(t, "uuid: u\napi:\n  url: http://mesh\n  username: u\n  password: p\n")
	// Point the key file at a non-existent path so the inline fallback is empty.
	t.Setenv("RUNNER_PRIVATE_KEY_FILE", filepath.Join(t.TempDir(), "nope.pem"))
	_, err := LoadConfig(testLog(), "build-v", false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "private key is required")
}
