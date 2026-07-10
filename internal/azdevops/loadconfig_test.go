package azdevops

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/meshapi"
)

func writeConfig(t *testing.T, yaml string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "runner-config.yml")
	require.NoError(t, os.WriteFile(path, []byte(yaml), 0o600))
	t.Setenv("RUNNER_CONFIG_FILE", path)
}

func TestLoadConfig_Defaults_RequirePrivateKeyInPollingMode(t *testing.T) {
	t.Setenv("RUNNER_CONFIG_FILE", filepath.Join(t.TempDir(), "absent.yml"))
	// No env/yaml private key resolvable and the default /app/runner-private.pem path does
	// not exist in the test environment -- LoadConfig must fail fast (P5), not boot with an
	// empty key that will only surface as a decrypt failure per run.
	_, err := LoadConfig(testLog(), "build-v", false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "private key")
}

func TestLoadConfig_SingleRunMode_NoPrivateKeyRequired(t *testing.T) {
	t.Setenv("RUNNER_CONFIG_FILE", filepath.Join(t.TempDir(), "absent.yml"))
	cfg, err := LoadConfig(testLog(), "build-v", true)
	require.NoError(t, err)
	require.Equal(t, defaultUuid, cfg.Uuid)
	require.Equal(t, defaultApiUrl, cfg.Api.Url)
}

// TestLoadConfig_ShippedContainerConfig proves the actual per-impl config file the
// Dockerfile bakes in (containers/azure-devops-block-runner/runner-config.yml) loads
// cleanly AND that its baked dev private key is real, parseable PEM (config.ResolvePrivateKey
// -> meshapi.NewCertDecryptor) -- a regression guard for the single-line-PEM config-compat
// fix (internal/crypto.normalizePEM): the Kotlin classpath default ships this exact key with
// no newline after the BEGIN marker, which a bare pem.Decode rejects.
func TestLoadConfig_ShippedContainerConfig(t *testing.T) {
	t.Setenv("RUNNER_CONFIG_FILE", "../../containers/azure-devops-block-runner/runner-config.yml")
	cfg, err := LoadConfig(testLog(), "build-v", false)
	require.NoError(t, err)
	require.Equal(t, defaultUuid, cfg.Uuid)
	require.Equal(t, defaultApiUrl, cfg.Api.Url)
	require.NotEmpty(t, cfg.PrivateKey)

	_, err = meshapi.NewCertDecryptor(cfg.PrivateKey)
	require.NoError(t, err, "the baked dev private key must be valid, parseable PEM")
}

func TestLoadConfig_InlinePrivateKey(t *testing.T) {
	pem := mustReadTestKeyHandler(t)
	writeConfig(t, "privateKey: |\n  "+indentEachLine(pem, "  "))
	cfg, err := LoadConfig(testLog(), "build-v", false)
	require.NoError(t, err)
	require.Equal(t, pem, cfg.PrivateKey)
}

func TestLoadConfig_BlockRunnerCompat_PrivateKey(t *testing.T) {
	pem := mustReadTestKeyHandler(t)
	writeConfig(t, "blockrunner:\n  privateKey: |\n    "+indentEachLine(pem, "    "))
	cfg, err := LoadConfig(testLog(), "build-v", false)
	require.NoError(t, err)
	require.Equal(t, pem, cfg.PrivateKey)
}

// TestLoadConfig_BlockRunnerCompat_DebugModeIgnoredWithWarning pins that a manual-only
// blockrunner.debugMode key is inert but not silently dropped for the azdevops persona
// (BlockRunnerCompat.DebugMode's own doc comment already promised this; the azdevops port
// had not implemented it -- accumulated alias inventory audit, docs/DEPRECATIONS.md).
func TestLoadConfig_BlockRunnerCompat_DebugModeIgnoredWithWarning(t *testing.T) {
	pem := mustReadTestKeyHandler(t)
	writeConfig(t, "privateKey: |\n  "+indentEachLine(pem, "  ")+"\nblockrunner:\n  debugMode: true\n")

	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	_, err := LoadConfig(log, "build-v", false)
	require.NoError(t, err)
	require.Contains(t, buf.String(), "blockrunner.debugMode")
	require.Contains(t, buf.String(), "ignoring")
}

func TestLoadConfig_EnvOverrides(t *testing.T) {
	pem := mustReadTestKeyHandler(t)
	writeConfig(t, "privateKey: |\n  "+indentEachLine(pem, "  "))
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
	require.Equal(t, "ver-from-env", cfg.Version)
	require.Equal(t, 7, cfg.MaxConcurrentRuns)
}

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

func TestConfig_Validate(t *testing.T) {
	require.NoError(t, Config{}.Validate(true)) // single-run: nothing required

	require.Error(t, Config{}.Validate(false))                                     // missing uuid
	require.Error(t, Config{Uuid: "u"}.Validate(false))                            // missing api.url
	require.Error(t, Config{Uuid: "u", Api: config.Api{Url: "x"}}.Validate(false)) // missing auth
	require.Error(t, Config{Uuid: "u", Api: config.Api{Url: "x", Username: "a", Password: "b"}}.Validate(false),
		"missing private key")
	require.NoError(t, Config{
		Uuid: "u", Api: config.Api{Url: "x", Username: "a", Password: "b"}, PrivateKey: "pem",
	}.Validate(false))
}

// indentEachLine joins every line of s with "\n"+indent, so it can be embedded as a YAML
// block-literal value at the given indentation level.
func indentEachLine(s, indent string) string {
	out := ""
	for i, line := range splitLines(s) {
		if i > 0 {
			out += "\n" + indent
		}
		out += line
	}
	return out
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	lines = append(lines, s[start:])
	return lines
}
