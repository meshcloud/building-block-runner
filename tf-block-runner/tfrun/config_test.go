package tfrun

import (
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
