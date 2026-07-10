package manual

import (
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

func TestLoadConfig_Defaults(t *testing.T) {
	t.Setenv("RUNNER_CONFIG_FILE", filepath.Join(t.TempDir(), "absent.yml"))
	cfg, err := LoadConfig(testLog(), "build-v", false)
	require.NoError(t, err)
	require.Equal(t, defaultUuid, cfg.Uuid)
	require.Equal(t, defaultApiUrl, cfg.Api.Url)
	require.Equal(t, "build-v", cfg.Version)
	require.False(t, cfg.DebugMode)
	require.Equal(t, defaultMaxConcurrentRuns, cfg.MaxConcurrentRuns)
}

func TestLoadConfig_EnvOverrides(t *testing.T) {
	t.Setenv("RUNNER_CONFIG_FILE", filepath.Join(t.TempDir(), "absent.yml"))
	t.Setenv("RUNNER_UUID", "uuid-from-env")
	t.Setenv("RUNNER_API_URL", "https://env.example")
	t.Setenv("RUNNER_API_CLIENT_ID", "cid")
	t.Setenv("RUNNER_API_CLIENT_SECRET", "csecret")
	t.Setenv("VERSION", "ver-from-env")
	t.Setenv("RUNNER_MAX_CONCURRENT_RUNS", "7")

	cfg, err := LoadConfig(testLog(), "build-v", false)
	require.NoError(t, err)
	require.Equal(t, "uuid-from-env", cfg.Uuid)
	require.Equal(t, "https://env.example", cfg.Api.Url)
	require.Equal(t, "cid", cfg.Api.ClientId)
	require.Equal(t, "ver-from-env", cfg.Version) // VERSION overrides the build version
	require.Equal(t, 7, cfg.MaxConcurrentRuns)
}

// TestLoadConfig_BlockRunnerCompat pins §6.4: a mounted Kotlin-era `blockrunner:` file
// configures the persona, and the block wins over flat keys.
func TestLoadConfig_BlockRunnerCompat(t *testing.T) {
	writeConfig(t, `
uuid: flat-uuid
blockrunner:
  uuid: block-uuid
  version: block-ver
  debugMode: true
  api:
    url: https://block.example
  auth:
    username: blockuser
    password: blockpass
`)
	cfg, err := LoadConfig(testLog(), "build-v", false)
	require.NoError(t, err)
	require.Equal(t, "block-uuid", cfg.Uuid) // block overrides the flat key
	require.Equal(t, "block-ver", cfg.Version)
	require.Equal(t, "https://block.example", cfg.Api.Url)
	require.Equal(t, "blockuser", cfg.Api.Username)
	require.True(t, cfg.DebugMode)
}

// TestLoadConfig_FailsOnUnconsumedLegacyEnv pins §10.4/D7: a BLOCKRUNNER_* relaxed-binding
// holdover that no key consumes is a hard startup error, never a silent wrong-default boot.
func TestLoadConfig_FailsOnUnconsumedLegacyEnv(t *testing.T) {
	t.Setenv("RUNNER_CONFIG_FILE", filepath.Join(t.TempDir(), "absent.yml"))
	t.Setenv("BLOCKRUNNER_UUID", "spring-relaxed-binding")
	_, err := LoadConfig(testLog(), "build-v", false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "BLOCKRUNNER_UUID")
}

func TestLoadConfig_InvalidMaxConcurrent(t *testing.T) {
	t.Setenv("RUNNER_CONFIG_FILE", filepath.Join(t.TempDir(), "absent.yml"))
	t.Setenv("RUNNER_MAX_CONCURRENT_RUNS", "not-a-number")
	_, err := LoadConfig(testLog(), "build-v", false)
	require.Error(t, err)
}

// TestConfig_Validate covers the polling requirements and the single-run exemption.
func TestConfig_Validate(t *testing.T) {
	require.NoError(t, Config{}.Validate(true)) // single-run: no auth/uuid required

	require.Error(t, Config{}.Validate(false))                                     // missing uuid
	require.Error(t, Config{Uuid: "u"}.Validate(false))                            // missing api.url
	require.Error(t, Config{Uuid: "u", Api: config.Api{Url: "x"}}.Validate(false)) // missing auth
	require.NoError(t, Config{Uuid: "u", Api: config.Api{Url: "x", Username: "a", Password: "b"}}.Validate(false))
}
