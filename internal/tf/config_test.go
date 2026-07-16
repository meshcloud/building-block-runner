package tf

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/internal/config"
)

func TestValidateRunnerUuid(t *testing.T) {
	t.Run("valid uuid", func(t *testing.T) {
		require.NoError(t, validateRunnerUuid(TfRunnerConfig{
			BaseConfig: config.BaseConfig{Uuid: "a1b2c3d4-e5f6-4a5b-8c9d-0e1f2a3b4c5d"},
		}))
	})
	t.Run("missing uuid", func(t *testing.T) {
		require.ErrorContains(t, validateRunnerUuid(TfRunnerConfig{}), "uuid is required")
	})
}

// TestReadConfig_TfEnvHandling covers tf's type-specific env conversion (now on the shared loader):
// RUNNER_MAX_CONCURRENT_RUNS (shared, hard error on non-numeric via config.ResolveBase),
// RUNNER_SHUTDOWN_GRACE (tf-only int, warn-and-ignore on non-numeric), and RUNNER_PRIVATE_KEY_FILE.
func TestReadConfig_TfEnvHandling(t *testing.T) {
	// A minimal file so ReadConfig has a uuid and validates.
	writeMinimal := func(t *testing.T) {
		t.Helper()
		path := filepath.Join(t.TempDir(), "runner-config.yml")
		require.NoError(t, os.WriteFile(path, []byte("uuid: u\n"), 0o600))
		t.Setenv(envConfigFile, path)
	}

	t.Run("applies RUNNER_MAX_CONCURRENT_RUNS", func(t *testing.T) {
		writeMinimal(t)
		t.Setenv("RUNNER_MAX_CONCURRENT_RUNS", "7")
		cfg, err := ReadConfig(discardLogger())
		require.NoError(t, err)
		require.Equal(t, 7, cfg.MaxConcurrentRuns)
	})

	t.Run("invalid RUNNER_MAX_CONCURRENT_RUNS is a hard error", func(t *testing.T) {
		writeMinimal(t)
		t.Setenv("RUNNER_MAX_CONCURRENT_RUNS", "not-a-number")
		_, err := ReadConfig(discardLogger())
		require.Error(t, err)
	})

	t.Run("applies RUNNER_SHUTDOWN_GRACE", func(t *testing.T) {
		writeMinimal(t)
		t.Setenv(envShutdownGrace, "5")
		cfg, err := ReadConfig(discardLogger())
		require.NoError(t, err)
		require.Equal(t, 5, cfg.ShutdownGraceSeconds)
	})

	t.Run("invalid RUNNER_SHUTDOWN_GRACE warns and keeps the default", func(t *testing.T) {
		writeMinimal(t)
		t.Setenv(envShutdownGrace, "not-a-number")
		cfg, err := ReadConfig(discardLogger())
		require.NoError(t, err)
		require.Equal(t, DefaultShutdownGraceSeconds, cfg.ShutdownGraceSeconds)
	})

	t.Run("RUNNER_PRIVATE_KEY_FILE loads the key file contents", func(t *testing.T) {
		writeMinimal(t)
		keyPath := filepath.Join(t.TempDir(), "key.pem")
		require.NoError(t, os.WriteFile(keyPath, []byte("KEY-PEM"), 0o600))
		t.Setenv(envPrivateKeyFile, keyPath)
		cfg, err := ReadConfig(discardLogger())
		require.NoError(t, err)
		require.Equal(t, keyPath, cfg.PrivateKeyFile)
		require.Equal(t, "KEY-PEM", cfg.PrivateKey)
	})
}

func TestApplyPrivateKeyFile(t *testing.T) {
	t.Run("loads key from file", func(t *testing.T) {
		keyFile := filepath.Join(t.TempDir(), "private.key")
		require.NoError(t, os.WriteFile(keyFile, []byte("-----BEGIN PRIVATE KEY-----\ntest\n-----END PRIVATE KEY-----\n"), 0600))

		cfg := TfRunnerConfig{}
		applyPrivateKeyFile(keyFile, &cfg, discardLogger())

		require.Equal(t, "-----BEGIN PRIVATE KEY-----\ntest\n-----END PRIVATE KEY-----\n", cfg.PrivateKey)
	})

	t.Run("silently skips missing file", func(t *testing.T) {
		cfg := TfRunnerConfig{}
		applyPrivateKeyFile(filepath.Join(t.TempDir(), "does-not-exist.key"), &cfg, discardLogger())

		require.Empty(t, cfg.PrivateKey)
	})

	t.Run("does not overwrite existing value when file is missing", func(t *testing.T) {
		cfg := TfRunnerConfig{PrivateKey: "existing-key"}
		applyPrivateKeyFile(filepath.Join(t.TempDir(), "does-not-exist.key"), &cfg, discardLogger())

		require.Equal(t, "existing-key", cfg.PrivateKey)
	})

	t.Run("file overrides existing key from config", func(t *testing.T) {
		keyFile := filepath.Join(t.TempDir(), "private.key")
		require.NoError(t, os.WriteFile(keyFile, []byte("key-from-file"), 0600))

		cfg := TfRunnerConfig{PrivateKey: "key-from-config"}
		applyPrivateKeyFile(keyFile, &cfg, discardLogger())

		require.Equal(t, "key-from-file", cfg.PrivateKey)
	})
}
