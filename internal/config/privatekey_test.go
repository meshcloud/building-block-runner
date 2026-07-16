package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolvePrivateKey(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, "env.pem")
	yamlFile := filepath.Join(dir, "yaml.pem")
	require.NoError(t, os.WriteFile(envFile, []byte("ENV-KEY"), 0o600))
	require.NoError(t, os.WriteFile(yamlFile, []byte("YAML-KEY"), 0o600))

	t.Run("env RUNNER_PRIVATE_KEY_FILE wins", func(t *testing.T) {
		t.Setenv(envPrivateKeyFile, envFile)
		got, err := ResolvePrivateKey(discardLog(), yamlFile, "INLINE")
		require.NoError(t, err)
		require.Equal(t, "ENV-KEY", got)
	})

	t.Run("yaml privateKeyFile used when no env", func(t *testing.T) {
		t.Setenv(envPrivateKeyFile, "")
		got, err := ResolvePrivateKey(discardLog(), yamlFile, "INLINE")
		require.NoError(t, err)
		require.Equal(t, "YAML-KEY", got)
	})

	t.Run("missing resolved file falls back to inline", func(t *testing.T) {
		t.Setenv(envPrivateKeyFile, filepath.Join(dir, "nope.pem"))
		got, err := ResolvePrivateKey(discardLog(), "", "INLINE")
		require.NoError(t, err)
		require.Equal(t, "INLINE", got)
	})

	t.Run("default path missing falls back to inline", func(t *testing.T) {
		t.Setenv(envPrivateKeyFile, "")
		got, err := ResolvePrivateKey(discardLog(), "", "INLINE")
		require.NoError(t, err)
		require.Equal(t, "INLINE", got)
	})

	t.Run("unreadable existing file is a hard error", func(t *testing.T) {
		t.Setenv(envPrivateKeyFile, dir) // a directory: exists but not readable as a file
		_, err := ResolvePrivateKey(discardLog(), "", "INLINE")
		require.Error(t, err)
	})
}
