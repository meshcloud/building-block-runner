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
			name: "no authentication methods",
			config: TfRunnerConfig{
				RunApiBackend: RunApiConfig{
					User:     "",
					Password: "",
				},
			},
			wantErr:     true,
			errContains: "basic authentication required",
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
			errContains: "basic authentication required",
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
			errContains: "basic authentication required",
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
