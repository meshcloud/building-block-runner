package tfrun

import (
	"io"
	"log"
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
			name: "both basic auth and api key auth configured",
			config: TfRunnerConfig{
				RunApiBackend: RunApiConfig{
					User:         "test-user",
					Password:     "test-password",
					ClientId:     "my-client-id",
					ClientSecret: "my-client-secret",
				},
			},
			wantErr:     true,
			errContains: "ambiguous authentication configuration",
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

func TestApplyPrivateKeyFile(t *testing.T) {
	nopLogger := log.New(io.Discard, "", 0)

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
