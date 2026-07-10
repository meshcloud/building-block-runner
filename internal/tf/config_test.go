package tf

import (
	"bytes"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateAuthConfig(t *testing.T) {
	tests := []struct {
		name        string
		config      TfRunnerConfig
		wantErr     bool
		errContains string
	}{
		{
			name: "valid basic auth",
			config: TfRunnerConfig{
				RunApiBackend: RunApiConfig{
					User:     "test-user",
					Password: "test-password",
				},
			},
			wantErr: false,
		},
		{
			name: "valid api key auth",
			config: TfRunnerConfig{
				RunApiBackend: RunApiConfig{
					ClientId:     "my-client-id",
					ClientSecret: "my-client-secret",
				},
			},
			wantErr: false,
		},
		{
			name: "no authentication methods",
			config: TfRunnerConfig{
				RunApiBackend: RunApiConfig{
					User:     "",
					Password: "",
				},
			},
			wantErr:     true,
			errContains: "authentication required in polling mode",
		},
		{
			name: "only user without password",
			config: TfRunnerConfig{
				RunApiBackend: RunApiConfig{
					User:     "test-user",
					Password: "",
				},
			},
			wantErr:     true,
			errContains: "authentication required in polling mode",
		},
		{
			name: "only password without user",
			config: TfRunnerConfig{
				RunApiBackend: RunApiConfig{
					User:     "",
					Password: "test-password",
				},
			},
			wantErr:     true,
			errContains: "authentication required in polling mode",
		},
		{
			name: "only clientId without clientSecret",
			config: TfRunnerConfig{
				RunApiBackend: RunApiConfig{
					ClientId: "my-client-id",
				},
			},
			wantErr:     true,
			errContains: "authentication required in polling mode",
		},
		{
			name: "only clientSecret without clientId",
			config: TfRunnerConfig{
				RunApiBackend: RunApiConfig{
					ClientSecret: "my-client-secret",
				},
			},
			wantErr:     true,
			errContains: "authentication required in polling mode",
		},
		{
			// API key auth takes precedence over basic auth (see RunApiConfig.NewAuthProvider), so
			// having both fully configured is valid — e.g. env-supplied API key credentials layered
			// over the basic-auth default baked into runner-config.yml.
			name: "both basic auth and api key auth configured (api key wins)",
			config: TfRunnerConfig{
				RunApiBackend: RunApiConfig{
					User:         "test-user",
					Password:     "test-password",
					ClientId:     "my-client-id",
					ClientSecret: "my-client-secret",
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateAuthConfig(tt.config)
			if tt.wantErr {
				require.ErrorContains(t, err, tt.errContains)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestValidateRunnerUuid_ValidUuid(t *testing.T) {
	t.Run("valid uuid", func(t *testing.T) {
		require.NoError(t, validateRunnerUuid(TfRunnerConfig{
			RunnerUuid: "a1b2c3d4-e5f6-4a5b-8c9d-0e1f2a3b4c5d",
		}))
	})
	t.Run("invalid uuid", func(t *testing.T) {
		require.ErrorContains(t, validateRunnerUuid(TfRunnerConfig{}), "runnerUuid is required")
	})
}

func TestApplyEnvVars(t *testing.T) {
	nopLogger := slog.New(slog.NewTextHandler(io.Discard, nil))

	t.Run("applies RUNNER_UUID from environment", func(t *testing.T) {
		t.Setenv(envRunnerUuid, "test-uuid-123")
		AppConfig = TfRunnerConfig{}
		applyEnvVars(nopLogger)
		require.Equal(t, "test-uuid-123", AppConfig.RunnerUuid)
	})

	t.Run("applies RUNNER_API_URL from environment", func(t *testing.T) {
		t.Setenv(envApiUrl, "https://api.example.com")
		AppConfig = TfRunnerConfig{}
		applyEnvVars(nopLogger)
		require.Equal(t, "https://api.example.com", AppConfig.RunApiBackend.Url)
	})

	t.Run("applies RUNNER_API_USERNAME from environment", func(t *testing.T) {
		t.Setenv(envAuthUsername, "testuser")
		AppConfig = TfRunnerConfig{}
		applyEnvVars(nopLogger)
		require.Equal(t, "testuser", AppConfig.RunApiBackend.User)
	})

	t.Run("applies RUNNER_API_PASSWORD from environment", func(t *testing.T) {
		t.Setenv(envAuthPassword, "testpass")
		AppConfig = TfRunnerConfig{}
		applyEnvVars(nopLogger)
		require.Equal(t, "testpass", AppConfig.RunApiBackend.Password)
	})

	t.Run("applies RUNNER_API_CLIENT_ID from environment", func(t *testing.T) {
		t.Setenv(envAuthClientId, "client-123")
		AppConfig = TfRunnerConfig{}
		applyEnvVars(nopLogger)
		require.Equal(t, "client-123", AppConfig.RunApiBackend.ClientId)
	})

	t.Run("applies RUNNER_API_CLIENT_SECRET from environment", func(t *testing.T) {
		t.Setenv(envAuthClientSecret, "secret-456")
		AppConfig = TfRunnerConfig{}
		applyEnvVars(nopLogger)
		require.Equal(t, "secret-456", AppConfig.RunApiBackend.ClientSecret)
	})

	t.Run("applies RUNNER_PRIVATE_KEY_FILE from environment", func(t *testing.T) {
		t.Setenv(envPrivateKeyFile, "/path/to/key.pem")
		AppConfig = TfRunnerConfig{}
		applyEnvVars(nopLogger)
		require.Equal(t, "/path/to/key.pem", AppConfig.PrivateKeyFile)
	})

	t.Run("uses default private key file when env var empty and not configured", func(t *testing.T) {
		t.Setenv(envPrivateKeyFile, "")
		AppConfig = TfRunnerConfig{PrivateKeyFile: ""}
		applyEnvVars(nopLogger)
		require.Equal(t, defaultPrivateKeyFile, AppConfig.PrivateKeyFile)
	})

	t.Run("preserves private key file when env var empty but already configured", func(t *testing.T) {
		t.Setenv(envPrivateKeyFile, "")
		AppConfig = TfRunnerConfig{PrivateKeyFile: "/configured/key.pem"}
		applyEnvVars(nopLogger)
		require.Equal(t, "/configured/key.pem", AppConfig.PrivateKeyFile)
	})

	t.Run("multiple env vars together", func(t *testing.T) {
		t.Setenv(envRunnerUuid, "uuid-multi")
		t.Setenv(envApiUrl, "https://api-multi.example.com")
		t.Setenv(envAuthUsername, "user-multi")
		t.Setenv(envAuthPassword, "pass-multi")
		AppConfig = TfRunnerConfig{}
		applyEnvVars(nopLogger)

		require.Equal(t, "uuid-multi", AppConfig.RunnerUuid)
		require.Equal(t, "https://api-multi.example.com", AppConfig.RunApiBackend.Url)
		require.Equal(t, "user-multi", AppConfig.RunApiBackend.User)
		require.Equal(t, "pass-multi", AppConfig.RunApiBackend.Password)
	})

	t.Run("env vars override config file values", func(t *testing.T) {
		t.Setenv(envRunnerUuid, "env-uuid")
		t.Setenv(envApiUrl, "https://env-api.example.com")
		t.Setenv(envAuthUsername, "env-user")
		t.Setenv(envAuthPassword, "env-pass")
		t.Setenv(envAuthClientId, "env-client-id")
		t.Setenv(envAuthClientSecret, "env-client-secret")
		t.Setenv(envPrivateKeyFile, "/env/key.pem")

		// Simulate config file values already set
		AppConfig = TfRunnerConfig{
			RunnerUuid: "config-uuid",
			RunApiBackend: RunApiConfig{
				Url:          "https://config-api.example.com",
				User:         "config-user",
				Password:     "config-pass",
				ClientId:     "config-client-id",
				ClientSecret: "config-client-secret",
			},
			PrivateKeyFile: "/config/key.pem",
		}

		applyEnvVars(nopLogger)

		// All values should be overridden by env vars
		require.Equal(t, "env-uuid", AppConfig.RunnerUuid)
		require.Equal(t, "https://env-api.example.com", AppConfig.RunApiBackend.Url)
		require.Equal(t, "env-user", AppConfig.RunApiBackend.User)
		require.Equal(t, "env-pass", AppConfig.RunApiBackend.Password)
		require.Equal(t, "env-client-id", AppConfig.RunApiBackend.ClientId)
		require.Equal(t, "env-client-secret", AppConfig.RunApiBackend.ClientSecret)
		require.Equal(t, "/env/key.pem", AppConfig.PrivateKeyFile)
	})

	t.Run("empty env var preserves config file value", func(t *testing.T) {
		t.Setenv(envRunnerUuid, "")
		t.Setenv(envApiUrl, "")
		t.Setenv(envAuthUsername, "")
		t.Setenv(envAuthPassword, "")
		t.Setenv(envAuthClientId, "")
		t.Setenv(envAuthClientSecret, "")
		t.Setenv(envPrivateKeyFile, "")

		// Simulate config file values already set
		AppConfig = TfRunnerConfig{
			RunnerUuid: "config-uuid",
			RunApiBackend: RunApiConfig{
				Url:          "https://config-api.example.com",
				User:         "config-user",
				Password:     "config-pass",
				ClientId:     "config-client-id",
				ClientSecret: "config-client-secret",
			},
			PrivateKeyFile: "/config/key.pem",
		}

		applyEnvVars(nopLogger)

		// All values should be preserved from config
		require.Equal(t, "config-uuid", AppConfig.RunnerUuid)
		require.Equal(t, "https://config-api.example.com", AppConfig.RunApiBackend.Url)
		require.Equal(t, "config-user", AppConfig.RunApiBackend.User)
		require.Equal(t, "config-pass", AppConfig.RunApiBackend.Password)
		require.Equal(t, "config-client-id", AppConfig.RunApiBackend.ClientId)
		require.Equal(t, "config-client-secret", AppConfig.RunApiBackend.ClientSecret)
		require.Equal(t, "/config/key.pem", AppConfig.PrivateKeyFile)
	})

	t.Run("partial env var override (mix of empty and set vars)", func(t *testing.T) {
		t.Setenv(envRunnerUuid, "env-uuid")
		t.Setenv(envApiUrl, "")
		t.Setenv(envAuthUsername, "env-user")
		t.Setenv(envAuthPassword, "")

		AppConfig = TfRunnerConfig{
			RunnerUuid: "config-uuid",
			RunApiBackend: RunApiConfig{
				Url:      "https://config-api.example.com",
				User:     "config-user",
				Password: "config-pass",
			},
		}

		applyEnvVars(nopLogger)

		// Only env vars that are set should override
		require.Equal(t, "env-uuid", AppConfig.RunnerUuid)
		require.Equal(t, "https://config-api.example.com", AppConfig.RunApiBackend.Url)
		require.Equal(t, "env-user", AppConfig.RunApiBackend.User)
		require.Equal(t, "config-pass", AppConfig.RunApiBackend.Password)
	})

	t.Run("logs env var names without values", func(t *testing.T) {
		t.Setenv(envRunnerUuid, "uuid-secret-ish")
		t.Setenv(envApiUrl, "https://api.example.com")
		t.Setenv(envAuthUsername, "user@example.com")
		t.Setenv(envAuthPassword, "super-secret-password")
		t.Setenv(envAuthClientId, "client-id-value")
		t.Setenv(envAuthClientSecret, "super-secret-client-secret")
		t.Setenv(envPrivateKeyFile, "/very/secret/path.pem")

		AppConfig = TfRunnerConfig{}

		var logBuffer bytes.Buffer
		bufferLogger := slog.New(slog.NewTextHandler(&logBuffer, nil))
		applyEnvVars(bufferLogger)

		logOutput := logBuffer.String()
		require.Contains(t, logOutput, envRunnerUuid)
		require.Contains(t, logOutput, envApiUrl)
		require.Contains(t, logOutput, envAuthUsername)
		require.Contains(t, logOutput, envAuthPassword)
		require.Contains(t, logOutput, envAuthClientId)
		require.Contains(t, logOutput, envAuthClientSecret)
		require.Contains(t, logOutput, envPrivateKeyFile)
		require.NotContains(t, logOutput, "uuid-secret-ish")
		require.NotContains(t, logOutput, "https://api.example.com")
		require.NotContains(t, logOutput, "user@example.com")
		require.NotContains(t, logOutput, "super-secret-password")
		require.NotContains(t, logOutput, "client-id-value")
		require.NotContains(t, logOutput, "super-secret-client-secret")
		require.NotContains(t, logOutput, "/very/secret/path.pem")
	})
}

func TestApplyPrivateKeyFile(t *testing.T) {
	nopLogger := slog.New(slog.NewTextHandler(io.Discard, nil))

	t.Run("loads key from file", func(t *testing.T) {
		keyFile := filepath.Join(t.TempDir(), "private.key")
		require.NoError(t, os.WriteFile(keyFile, []byte("-----BEGIN PRIVATE KEY-----\ntest\n-----END PRIVATE KEY-----\n"), 0600))

		cfg := TfRunnerConfig{}
		applyPrivateKeyFile(keyFile, &cfg, nopLogger)

		require.Equal(t, "-----BEGIN PRIVATE KEY-----\ntest\n-----END PRIVATE KEY-----\n", cfg.PrivateKey)
	})

	t.Run("silently skips missing file", func(t *testing.T) {
		cfg := TfRunnerConfig{}
		applyPrivateKeyFile(filepath.Join(t.TempDir(), "does-not-exist.key"), &cfg, nopLogger)

		require.Empty(t, cfg.PrivateKey)
	})

	t.Run("does not overwrite existing value when file is missing", func(t *testing.T) {
		cfg := TfRunnerConfig{PrivateKey: "existing-key"}
		applyPrivateKeyFile(filepath.Join(t.TempDir(), "does-not-exist.key"), &cfg, nopLogger)

		require.Equal(t, "existing-key", cfg.PrivateKey)
	})

	t.Run("file overrides existing key from config", func(t *testing.T) {
		keyFile := filepath.Join(t.TempDir(), "private.key")
		require.NoError(t, os.WriteFile(keyFile, []byte("key-from-file"), 0600))

		cfg := TfRunnerConfig{PrivateKey: "key-from-config"}
		applyPrivateKeyFile(keyFile, &cfg, nopLogger)

		require.Equal(t, "key-from-file", cfg.PrivateKey)
	})
}
