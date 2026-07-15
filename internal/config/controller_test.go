package config

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newBufferLogger returns a slog logger writing to buf so tests can assert on rendered
// log lines (deprecation warnings, env-override notices, startup logging).
func newBufferLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(buf, nil))
}

// validControllerYAML is a minimal file that satisfies every required field, so a single
// override test only needs to blank/replace the one field under test.
const validControllerYAML = `
uuid: 11111111-1111-1111-1111-111111111111
ownedByWorkspace: my-workspace
displayName: my-controller
api:
  url: https://api.example.com
  username: bb-api
  password: guest
crypto:
  publicKey: pub-key
  privateKey: priv-key
namespace: my-namespace
implementations:
  TERRAFORM:
    image: terraform-runner:latest
`

func writeControllerConfig(t *testing.T, dir, contents string) {
	t.Helper()
	path := filepath.Join(dir, "runner-config.yml")
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))
}

// chdir switches the process working directory for the duration of the test (Path/Load
// resolve relative paths off cwd) and restores it on cleanup.
func chdir(t *testing.T, dir string) {
	t.Helper()
	t.Chdir(dir)
}

func TestLoadController_FileOnly(t *testing.T) {
	dir := t.TempDir()
	writeControllerConfig(t, dir, validControllerYAML)
	chdir(t, dir)

	var buf bytes.Buffer
	cfg, err := LoadController(newBufferLogger(&buf))
	require.NoError(t, err)

	assert.Equal(t, "11111111-1111-1111-1111-111111111111", cfg.Uuid)
	assert.Equal(t, "my-workspace", cfg.OwnedByWorkspace)
	assert.Equal(t, "my-controller", cfg.DisplayName)
	assert.Equal(t, "https://api.example.com", cfg.Api.Url)
	assert.Equal(t, "pub-key", cfg.Crypto.PublicKey)
	assert.Equal(t, "priv-key", cfg.Crypto.PrivateKey)
	assert.Equal(t, "my-namespace", cfg.K8sJobConfig.Namespace)
	assert.Equal(t, 10, cfg.MaxConcurrentJobs, "zero maxConcurrentJobs defaults to k8sjob.DefaultMaxConcurrentJobs")
}

func TestLoadController_ConfigFileEnvAliases(t *testing.T) {
	t.Run("neither set: default runner-config.yml in cwd is used", func(t *testing.T) {
		dir := t.TempDir()
		writeControllerConfig(t, dir, validControllerYAML)
		chdir(t, dir)

		var buf bytes.Buffer
		cfg, err := LoadController(newBufferLogger(&buf))
		require.NoError(t, err)
		assert.Equal(t, "my-workspace", cfg.OwnedByWorkspace)
	})

	t.Run("only the deprecated RUNCONTROLLER_CONFIG_FILE alias is set: used, and warns", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "legacy.yml")
		require.NoError(t, os.WriteFile(path, []byte(validControllerYAML), 0o600))
		t.Setenv("RUNCONTROLLER_CONFIG_FILE", path)

		var buf bytes.Buffer
		cfg, err := LoadController(newBufferLogger(&buf))
		require.NoError(t, err)
		assert.Equal(t, "my-workspace", cfg.OwnedByWorkspace)
		assert.Contains(t, buf.String(), "deprecated")
		assert.Contains(t, buf.String(), "RUNCONTROLLER_CONFIG_FILE")
		assert.Contains(t, buf.String(), "RUNNER_CONFIG_FILE")
	})

	t.Run("the canonical RUNNER_CONFIG_FILE wins when both are set, and no warning fires", func(t *testing.T) {
		dir := t.TempDir()
		primary := filepath.Join(dir, "primary.yml")
		legacy := filepath.Join(dir, "legacy.yml")
		require.NoError(t, os.WriteFile(primary, []byte(validControllerYAML), 0o600))
		require.NoError(t, os.WriteFile(legacy, []byte(strings.ReplaceAll(validControllerYAML, "my-controller", "legacy-controller")), 0o600))
		t.Setenv("RUNNER_CONFIG_FILE", primary)
		t.Setenv("RUNCONTROLLER_CONFIG_FILE", legacy)

		var buf bytes.Buffer
		cfg, err := LoadController(newBufferLogger(&buf))
		require.NoError(t, err)
		assert.Equal(t, "my-controller", cfg.DisplayName)
		assert.Empty(t, buf.String())
	})
}

func TestLoadController_ApiEnvOverrides(t *testing.T) {
	dir := t.TempDir()
	writeControllerConfig(t, dir, validControllerYAML)
	chdir(t, dir)

	t.Setenv("RUNNER_API_URL", "https://env.example.com")
	t.Setenv("RUNNER_API_CLIENT_ID", "env-client-id")
	t.Setenv("RUNNER_API_CLIENT_SECRET", "env-client-secret")

	var buf bytes.Buffer
	cfg, err := LoadController(newBufferLogger(&buf))
	require.NoError(t, err)

	assert.Equal(t, "https://env.example.com", cfg.Api.Url)
	assert.Equal(t, "env-client-id", cfg.Api.ClientId)
	assert.Equal(t, "env-client-secret", cfg.Api.ClientSecret)
}

func TestLoadController_ShutdownGraceEnvOverride(t *testing.T) {
	t.Run("valid value overrides shutdownGraceSeconds", func(t *testing.T) {
		dir := t.TempDir()
		writeControllerConfig(t, dir, validControllerYAML)
		chdir(t, dir)
		t.Setenv("RUNNER_SHUTDOWN_GRACE", "45")

		var buf bytes.Buffer
		cfg, err := LoadController(newBufferLogger(&buf))
		require.NoError(t, err)
		assert.Equal(t, 45, cfg.ShutdownGraceSeconds)
	})

	t.Run("invalid value is ignored with a warning", func(t *testing.T) {
		dir := t.TempDir()
		writeControllerConfig(t, dir, validControllerYAML)
		chdir(t, dir)
		t.Setenv("RUNNER_SHUTDOWN_GRACE", "not-a-number")

		var buf bytes.Buffer
		cfg, err := LoadController(newBufferLogger(&buf))
		require.NoError(t, err)
		assert.Equal(t, 0, cfg.ShutdownGraceSeconds)
		assert.Contains(t, buf.String(), "RUNNER_SHUTDOWN_GRACE")
		assert.Contains(t, buf.String(), "ignoring invalid")
	})

	t.Run("unset leaves the file/default value untouched", func(t *testing.T) {
		dir := t.TempDir()
		writeControllerConfig(t, dir, validControllerYAML)
		chdir(t, dir)

		var buf bytes.Buffer
		cfg, err := LoadController(newBufferLogger(&buf))
		require.NoError(t, err)
		assert.Equal(t, 0, cfg.ShutdownGraceSeconds)
	})
}

func TestLoadController_MaxConcurrentJobsDefault(t *testing.T) {
	t.Run("unset in file defaults to k8sjob.DefaultMaxConcurrentJobs", func(t *testing.T) {
		dir := t.TempDir()
		writeControllerConfig(t, dir, validControllerYAML)
		chdir(t, dir)

		var buf bytes.Buffer
		cfg, err := LoadController(newBufferLogger(&buf))
		require.NoError(t, err)
		assert.Equal(t, 10, cfg.MaxConcurrentJobs)
	})

	t.Run("explicit value from file is preserved", func(t *testing.T) {
		dir := t.TempDir()
		writeControllerConfig(t, dir, validControllerYAML+"maxConcurrentJobs: 5\n")
		chdir(t, dir)

		var buf bytes.Buffer
		cfg, err := LoadController(newBufferLogger(&buf))
		require.NoError(t, err)
		assert.Equal(t, 5, cfg.MaxConcurrentJobs)
	})

	t.Run("explicit negative value (unlimited) is preserved, not replaced by the default", func(t *testing.T) {
		dir := t.TempDir()
		writeControllerConfig(t, dir, validControllerYAML+"maxConcurrentJobs: -1\n")
		chdir(t, dir)

		var buf bytes.Buffer
		cfg, err := LoadController(newBufferLogger(&buf))
		require.NoError(t, err)
		assert.Equal(t, -1, cfg.MaxConcurrentJobs)
	})
}

func TestLoadController_RequiredFieldErrors(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(yaml string) string
		wantErr string
	}{
		{
			name:    "missing api.url",
			mutate:  func(y string) string { return strings.ReplaceAll(y, "url: https://api.example.com", "url: \"\"") },
			wantErr: "api.url is required",
		},
		{
			name:    "missing api auth (password blanked, no clientId/clientSecret)",
			mutate:  func(y string) string { return strings.ReplaceAll(y, "password: guest", "password: \"\"") },
			wantErr: "api.password is required when using Basic auth",
		},
		{
			name: "missing uuid",
			mutate: func(y string) string {
				return strings.ReplaceAll(y, "uuid: 11111111-1111-1111-1111-111111111111", "uuid: \"\"")
			},
			wantErr: "uuid is required",
		},
		{
			name: "missing ownedByWorkspace",
			mutate: func(y string) string {
				return strings.ReplaceAll(y, "ownedByWorkspace: my-workspace", "ownedByWorkspace: \"\"")
			},
			wantErr: "ownedByWorkspace is required",
		},
		{
			name:    "missing displayName",
			mutate:  func(y string) string { return strings.ReplaceAll(y, "displayName: my-controller", "displayName: \"\"") },
			wantErr: "displayName is required",
		},
		{
			name:    "missing crypto.publicKey",
			mutate:  func(y string) string { return strings.ReplaceAll(y, "publicKey: pub-key", "publicKey: \"\"") },
			wantErr: "crypto.publicKey is required",
		},
		{
			name:    "missing crypto.privateKey",
			mutate:  func(y string) string { return strings.ReplaceAll(y, "privateKey: priv-key", "privateKey: \"\"") },
			wantErr: "crypto.privateKey is required",
		},
		{
			name:    "missing namespace (K8sJobConfig.Validate)",
			mutate:  func(y string) string { return strings.ReplaceAll(y, "namespace: my-namespace", "namespace: \"\"") },
			wantErr: "namespace is required",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeControllerConfig(t, dir, tc.mutate(validControllerYAML))
			chdir(t, dir)

			var buf bytes.Buffer
			_, err := LoadController(newBufferLogger(&buf))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestLoadController_LogStartup(t *testing.T) {
	dir := t.TempDir()
	writeControllerConfig(t, dir, validControllerYAML)
	chdir(t, dir)

	var buf bytes.Buffer
	logger := newBufferLogger(&buf)
	cfg, err := LoadController(logger)
	require.NoError(t, err)

	buf.Reset()
	cfg.LogStartup(logger)
	out := buf.String()
	assert.Contains(t, out, "controller configuration")
	assert.Contains(t, out, "my-namespace")
	assert.Contains(t, out, "implementation configured")
	assert.Contains(t, out, "terraform-runner:latest")
}
