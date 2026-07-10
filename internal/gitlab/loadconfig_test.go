package gitlab

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

// absentBase points RUNNER_BASE_CONFIG_FILE at a file that doesn't exist, so tests aren't
// accidentally sensitive to this repo's real containers/runner-config.yml.
func absentBase(t *testing.T) {
	t.Helper()
	t.Setenv("RUNNER_BASE_CONFIG_FILE", filepath.Join(t.TempDir(), "absent-base.yml"))
}

func TestLoadConfig_Defaults_SingleRun(t *testing.T) {
	absentBase(t)
	t.Setenv("RUNNER_CONFIG_FILE", filepath.Join(t.TempDir(), "absent.yml"))
	cfg, err := LoadConfig(testLog(), "build-v", true)
	require.NoError(t, err)
	require.Equal(t, defaultUuid, cfg.Uuid)
	require.Equal(t, defaultApiUrl, cfg.Api.Url)
	require.Equal(t, "build-v", cfg.Version)
	require.Equal(t, defaultMaxConcurrentRuns, cfg.MaxConcurrentRuns)
	require.Empty(t, cfg.PrivateKeyPEM, "single-run mode never resolves a private key")
}

// TestLoadConfig_PollingMode_ResolvesBaseKey pins §6.3: the shared base layer's private
// key is used when no operator override exists (T2 resolution order, deferred to the
// default path only when neither env nor a mounted file resolves).
func TestLoadConfig_PollingMode_ResolvesBaseKey(t *testing.T) {
	baseDir := t.TempDir()
	basePath := filepath.Join(baseDir, "base.yml")
	require.NoError(t, os.WriteFile(basePath, []byte("privateKey: \"the-dev-key\"\n"), 0o600))
	t.Setenv("RUNNER_BASE_CONFIG_FILE", basePath)
	t.Setenv("RUNNER_CONFIG_FILE", filepath.Join(t.TempDir(), "absent-impl.yml"))
	// Point the loader's default-path fallback at a file we know is absent so
	// ResolvePrivateKey falls through to the inline base key rather than a real
	// /app/runner-private.pem on the test machine.
	t.Setenv("RUNNER_PRIVATE_KEY_FILE", filepath.Join(t.TempDir(), "no-such-key-file.pem"))

	cfg, err := LoadConfig(testLog(), "build-v", false)
	require.NoError(t, err)
	require.Equal(t, "the-dev-key", cfg.PrivateKeyPEM)
}

// TestLoadConfig_PollingMode_NoKeyIsAnError pins §6.1: gitlab always needs a resolvable
// key in polling mode (unlike manual).
func TestLoadConfig_PollingMode_NoKeyIsAnError(t *testing.T) {
	absentBase(t)
	t.Setenv("RUNNER_CONFIG_FILE", filepath.Join(t.TempDir(), "absent.yml"))
	t.Setenv("RUNNER_PRIVATE_KEY_FILE", filepath.Join(t.TempDir(), "no-such-key-file.pem"))
	t.Setenv("RUNNER_API_CLIENT_ID", "cid")
	t.Setenv("RUNNER_API_CLIENT_SECRET", "csecret")

	_, err := LoadConfig(testLog(), "build-v", false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "private key")
}

func TestLoadConfig_EnvOverrides(t *testing.T) {
	absentBase(t)
	t.Setenv("RUNNER_CONFIG_FILE", filepath.Join(t.TempDir(), "absent.yml"))
	t.Setenv("RUNNER_UUID", "uuid-from-env")
	t.Setenv("RUNNER_API_URL", "https://env.example")
	t.Setenv("RUNNER_API_CLIENT_ID", "cid")
	t.Setenv("RUNNER_API_CLIENT_SECRET", "csecret")
	t.Setenv("VERSION", "ver-from-env")
	t.Setenv("RUNNER_MAX_CONCURRENT_RUNS", "7")

	cfg, err := LoadConfig(testLog(), "build-v", true) // single-run: skip the key requirement
	require.NoError(t, err)
	require.Equal(t, "uuid-from-env", cfg.Uuid)
	require.Equal(t, "https://env.example", cfg.Api.Url)
	require.Equal(t, "cid", cfg.Api.ClientId)
	require.Equal(t, "ver-from-env", cfg.Version)
	require.Equal(t, 7, cfg.MaxConcurrentRuns)
}

// TestLoadConfig_BlockRunnerCompat pins §6.4: a mounted Kotlin-era `blockrunner:` file
// configures the persona, including the gitlab-specific privateKey/privateKeyFile keys
// (manual ignores them; gitlab consumes them, 06A §17 row 8).
func TestLoadConfig_BlockRunnerCompat(t *testing.T) {
	absentBase(t)
	writeConfig(t, `
uuid: flat-uuid
blockrunner:
  uuid: block-uuid
  version: block-ver
  api:
    url: https://block.example
  auth:
    username: blockuser
    password: blockpass
  privateKey: "block-inline-key"
`)
	t.Setenv("RUNNER_PRIVATE_KEY_FILE", filepath.Join(t.TempDir(), "no-such-key-file.pem"))
	cfg, err := LoadConfig(testLog(), "build-v", false)
	require.NoError(t, err)
	require.Equal(t, "block-uuid", cfg.Uuid)
	require.Equal(t, "block-ver", cfg.Version)
	require.Equal(t, "https://block.example", cfg.Api.Url)
	require.Equal(t, "blockuser", cfg.Api.Username)
	require.Equal(t, "block-inline-key", cfg.PrivateKeyPEM)
}

// TestLoadConfig_BlockRunnerCompat_DebugModeIgnoredWithWarning pins that a manual-only
// blockrunner.debugMode key is inert but not silently dropped for the gitlab persona
// (BlockRunnerCompat.DebugMode's own doc comment already promised this; the gitlab port
// had not implemented it -- accumulated alias inventory audit, docs/DEPRECATIONS.md).
func TestLoadConfig_BlockRunnerCompat_DebugModeIgnoredWithWarning(t *testing.T) {
	absentBase(t)
	writeConfig(t, "blockrunner:\n  debugMode: true\n")
	t.Setenv("RUNNER_PRIVATE_KEY_FILE", filepath.Join(t.TempDir(), "no-such-key-file.pem"))

	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	_, err := LoadConfig(log, "build-v", true)
	require.NoError(t, err)
	require.Contains(t, buf.String(), "blockrunner.debugMode")
	require.Contains(t, buf.String(), "ignoring")
}

func TestLoadConfig_FailsOnUnconsumedLegacyEnv(t *testing.T) {
	absentBase(t)
	t.Setenv("RUNNER_CONFIG_FILE", filepath.Join(t.TempDir(), "absent.yml"))
	t.Setenv("BLOCKRUNNER_UUID", "spring-relaxed-binding")
	_, err := LoadConfig(testLog(), "build-v", true)
	require.Error(t, err)
	require.Contains(t, err.Error(), "BLOCKRUNNER_UUID")
}

func TestLoadConfig_InvalidMaxConcurrent(t *testing.T) {
	absentBase(t)
	t.Setenv("RUNNER_CONFIG_FILE", filepath.Join(t.TempDir(), "absent.yml"))
	t.Setenv("RUNNER_MAX_CONCURRENT_RUNS", "not-a-number")
	_, err := LoadConfig(testLog(), "build-v", true)
	require.Error(t, err)
}

func TestLoadConfig_MalformedYaml(t *testing.T) {
	absentBase(t)
	writeConfig(t, "\tthis: : is not: valid: yaml\n  - broken")
	_, err := LoadConfig(testLog(), "v", true)
	require.Error(t, err)
}

// TestConfig_Validate covers the polling requirements (incl. the private-key requirement)
// and the single-run exemption.
func TestConfig_Validate(t *testing.T) {
	require.NoError(t, Config{}.Validate(true)) // single-run: nothing required

	require.Error(t, Config{}.Validate(false))                                     // missing uuid
	require.Error(t, Config{Uuid: "u"}.Validate(false))                            // missing api.url
	require.Error(t, Config{Uuid: "u", Api: config.Api{Url: "x"}}.Validate(false)) // missing auth
	require.Error(t, Config{Uuid: "u", Api: config.Api{Url: "x", Username: "a", Password: "b"}}.Validate(false),
		"missing private key")
	require.NoError(t, Config{
		Uuid: "u", Api: config.Api{Url: "x", Username: "a", Password: "b"}, PrivateKeyPEM: "pem",
	}.Validate(false))
}
