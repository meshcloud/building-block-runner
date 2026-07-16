package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// baseTestFileConfig mirrors how each runner type embeds BaseFileConfig inline alongside its own
// type-specific yaml keys, so the decode tests exercise the exact `,inline` shape the loaders use.
type baseTestFileConfig struct {
	BaseFileConfig `yaml:",inline"`
	Version        string `yaml:"version"`
}

// TestBaseFileConfig_InlineDecode confirms decodeMap (merge.go) honors yaml `,inline` for the
// embedded BaseFileConfig: the flat uuid:/api:/maxConcurrentRuns: keys and the type-specific
// version: key populate the embedded and outer fields from one decode.
func TestBaseFileConfig_InlineDecode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runner-config.yml")
	require.NoError(t, os.WriteFile(path, []byte(`
uuid: flat-uuid
version: flat-ver
maxConcurrentRuns: 9
api:
  url: https://flat.example
  username: flatuser
  password: flatpass
`), 0o600))

	fc := baseTestFileConfig{BaseFileConfig: DefaultBaseFileConfig()}
	l := NewLoader()
	found, err := l.Load(path, &fc)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "flat-uuid", fc.Uuid)
	require.Equal(t, "flat-ver", fc.Version)
	require.Equal(t, 9, fc.MaxConcurrentRuns)
	require.Equal(t, "https://flat.example", fc.Api.Url)
	require.Equal(t, "flatuser", fc.Api.Username)
	require.Equal(t, "flatpass", fc.Api.Password)
}

// TestBaseFileConfig_InlineDecode_BlockRunner confirms the embedded `blockrunner:` compat block
// decodes through the inline embed too.
func TestBaseFileConfig_InlineDecode_BlockRunner(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runner-config.yml")
	require.NoError(t, os.WriteFile(path, []byte(`
uuid: flat-uuid
blockrunner:
  uuid: block-uuid
  api:
    url: https://block.example
  auth:
    username: blockuser
    password: blockpass
`), 0o600))

	fc := baseTestFileConfig{BaseFileConfig: DefaultBaseFileConfig()}
	l := NewLoader()
	_, err := l.Load(path, &fc)
	require.NoError(t, err)
	require.Equal(t, "flat-uuid", fc.Uuid)
	require.Equal(t, "block-uuid", fc.BlockRunner.Uuid)
	require.Equal(t, "https://block.example", fc.BlockRunner.Api.Url)
	require.Equal(t, "blockuser", fc.BlockRunner.Auth.Username)
}

func TestDefaultBaseFileConfig(t *testing.T) {
	fc := DefaultBaseFileConfig()
	require.Equal(t, DefaultRunnerUuid, fc.Uuid)
	require.Equal(t, DefaultApiUrl, fc.Api.Url)
	require.Equal(t, DefaultApiUsername, fc.Api.Username)
	require.Equal(t, DefaultApiPassword, fc.Api.Password)
	require.Equal(t, DefaultMaxConcurrentRuns, fc.MaxConcurrentRuns)
}

// TestResolveBase_Defaults: with no file and no env, ResolveBase returns the compiled-in defaults.
func TestResolveBase_Defaults(t *testing.T) {
	fc := DefaultBaseFileConfig()
	base, err := ResolveBase(discardLog(), NewLoader(), &fc)
	require.NoError(t, err)
	require.Equal(t, DefaultRunnerUuid, base.Uuid)
	require.Equal(t, DefaultApiUrl, base.Api.Url)
	require.Equal(t, DefaultMaxConcurrentRuns, base.MaxConcurrentRuns)
}

// TestResolveBase_EnvOverrides: RUNNER_* env wins over the seeded/flat values, and
// RUNNER_MAX_CONCURRENT_RUNS is applied. This is the shared precedence every type relies on.
func TestResolveBase_EnvOverrides(t *testing.T) {
	t.Setenv("RUNNER_UUID", "uuid-from-env")
	t.Setenv("RUNNER_API_URL", "https://env.example")
	t.Setenv("RUNNER_API_USERNAME", "envuser")
	t.Setenv("RUNNER_API_PASSWORD", "envpass")
	t.Setenv("RUNNER_API_CLIENT_ID", "cid")
	t.Setenv("RUNNER_API_CLIENT_SECRET", "csecret")
	t.Setenv("RUNNER_MAX_CONCURRENT_RUNS", "7")

	fc := DefaultBaseFileConfig()
	base, err := ResolveBase(discardLog(), NewLoader(), &fc)
	require.NoError(t, err)
	require.Equal(t, "uuid-from-env", base.Uuid)
	require.Equal(t, "https://env.example", base.Api.Url)
	require.Equal(t, "envuser", base.Api.Username)
	require.Equal(t, "envpass", base.Api.Password)
	require.Equal(t, "cid", base.Api.ClientId)
	require.Equal(t, "csecret", base.Api.ClientSecret)
	require.Equal(t, 7, base.MaxConcurrentRuns)
}

// TestResolveBase_BlockOverridesFlatButEnvWins pins the full three-layer precedence:
// blockrunner: block over the flat/default uuid, then RUNNER_UUID env over both.
func TestResolveBase_BlockOverridesFlat(t *testing.T) {
	fc := DefaultBaseFileConfig()
	fc.Uuid = "flat-uuid"
	fc.BlockRunner.Uuid = "block-uuid"
	base, err := ResolveBase(discardLog(), NewLoader(), &fc)
	require.NoError(t, err)
	require.Equal(t, "block-uuid", base.Uuid)
}

func TestResolveBase_InvalidMaxConcurrent(t *testing.T) {
	t.Setenv("RUNNER_MAX_CONCURRENT_RUNS", "not-a-number")
	fc := DefaultBaseFileConfig()
	_, err := ResolveBase(discardLog(), NewLoader(), &fc)
	require.Error(t, err)
}
