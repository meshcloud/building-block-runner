package controller

import (
	"strings"
	"testing"
)

func createValidImplementations() map[string]JobSpecTemplate {
	return map[string]JobSpecTemplate{
		"TERRAFORM": {
			Image: "tf-block-runner:latest",
		},
	}
}

func createValidConfig() *ControllerConfig {
	return &ControllerConfig{
		Namespace: "test-namespace",
		Api: ApiConfig{
			Url:      "http://localhost:8080",
			Username: "api-user",
			Password: "api-pass",
		},
		Uuid:             "46b7c17a-61f0-4062-9601-5785e60ce11f",
		OwnedByWorkspace: "test-workspace",
		DisplayName:      "Test Controller",
		Crypto: CryptoConfig{
			PublicKey:  "public-key",
			PrivateKey: "private-key",
		},
		Implementations: createValidImplementations(),
	}
}

func TestValidateConfig_ValidConfig(t *testing.T) {
	config := createValidConfig()
	if err := validateConfig(config); err != nil {
		t.Errorf("expected no error for valid config, got: %v", err)
	}
}

func TestValidateConfig_MultipleImplementations(t *testing.T) {
	config := createValidConfig()
	config.Implementations = map[string]JobSpecTemplate{
		"TERRAFORM":             {Image: "tf-runner:latest"},
		"GITHUB_WORKFLOW":       {Image: "gh-runner:latest"},
		"GITLAB_PIPELINE":       {Image: "gl-runner:latest"},
		"AZURE_DEVOPS_PIPELINE": {Image: "ado-runner:latest"},
		"MANUAL":                {Image: "manual-runner:latest"},
	}

	if err := validateConfig(config); err != nil {
		t.Errorf("expected no error for all implementation types, got: %v", err)
	}
}

func TestValidateConfig_InvalidConfigs(t *testing.T) {
	tests := []struct {
		name           string
		mutate         func(*ControllerConfig)
		expectedErrSub string
	}{
		{
			name:           "empty namespace",
			mutate:         func(c *ControllerConfig) { c.Namespace = "" },
			expectedErrSub: "namespace is required",
		},
		{
			name:           "missing uuid",
			mutate:         func(c *ControllerConfig) { c.Uuid = "" },
			expectedErrSub: "uuid is required",
		},
		{
			name:           "missing ownedByWorkspace",
			mutate:         func(c *ControllerConfig) { c.OwnedByWorkspace = "" },
			expectedErrSub: "ownedByWorkspace is required",
		},
		{
			name:           "missing displayName",
			mutate:         func(c *ControllerConfig) { c.DisplayName = "" },
			expectedErrSub: "displayName is required",
		},
		{
			name:           "missing crypto public key",
			mutate:         func(c *ControllerConfig) { c.Crypto.PublicKey = "" },
			expectedErrSub: "crypto.publicKey is required",
		},
		{
			name:           "missing crypto private key",
			mutate:         func(c *ControllerConfig) { c.Crypto.PrivateKey = "" },
			expectedErrSub: "crypto.privateKey is required",
		},
		{
			name:           "empty implementations map",
			mutate:         func(c *ControllerConfig) { c.Implementations = map[string]JobSpecTemplate{} },
			expectedErrSub: "at least one implementation handler",
		},
		{
			name:           "nil implementations map",
			mutate:         func(c *ControllerConfig) { c.Implementations = nil },
			expectedErrSub: "at least one implementation handler",
		},
		{
			name: "invalid implementation type key",
			mutate: func(c *ControllerConfig) {
				c.Implementations = map[string]JobSpecTemplate{
					"INVALID_TYPE": {Image: "some-image:latest"},
				}
			},
			expectedErrSub: "implementations key 'INVALID_TYPE' is invalid",
		},
		{
			name: "ALL not valid as handler key",
			mutate: func(c *ControllerConfig) {
				c.Implementations = map[string]JobSpecTemplate{
					"ALL": {Image: "some-image:latest"},
				}
			},
			expectedErrSub: "implementations key 'ALL' is invalid",
		},
		{
			name: "empty image in implementation spec",
			mutate: func(c *ControllerConfig) {
				c.Implementations = map[string]JobSpecTemplate{
					"TERRAFORM": {Image: ""},
				}
			},
			expectedErrSub: "implementations.TERRAFORM.image is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := createValidConfig()
			tt.mutate(config)

			err := validateConfig(config)
			if err == nil {
				t.Errorf("expected validation error for %s", tt.name)
				return
			}
			if tt.expectedErrSub != "" && !strings.Contains(err.Error(), tt.expectedErrSub) {
				t.Errorf("expected error containing %q, got: %v", tt.expectedErrSub, err)
			}
		})
	}
}

func TestValidateConfig_ValidImplementationTypeKeys(t *testing.T) {
	validTypes := []string{
		"TERRAFORM",
		"GITHUB_WORKFLOW",
		"GITLAB_PIPELINE",
		"AZURE_DEVOPS_PIPELINE",
		"MANUAL",
	}

	for _, implType := range validTypes {
		t.Run(implType, func(t *testing.T) {
			config := createValidConfig()
			config.Implementations = map[string]JobSpecTemplate{
				implType: {Image: "test-image:latest"},
			}

			err := validateConfig(config)
			if err != nil {
				t.Errorf("validateConfig() error = %v, expected no error for valid key %s", err, implType)
			}
		})
	}
}

func TestValidateConfig_ApiKeyAuth(t *testing.T) {
	t.Run("valid api key auth on global api", func(t *testing.T) {
		config := createValidConfig()
		config.Api.Username = ""
		config.Api.Password = ""
		config.Api.ClientId = "my-client-id"
		config.Api.ClientSecret = "my-client-secret"

		if err := validateConfig(config); err != nil {
			t.Errorf("expected no error for valid API key auth, got: %v", err)
		}
	})

	t.Run("both auth methods set on global api (api key wins)", func(t *testing.T) {
		// API key auth takes precedence over basic auth (see ApiConfig.NewAuthProvider), so having
		// both fully configured is valid rather than ambiguous — e.g. API key credentials layered
		// over a basic-auth default baked into the image.
		config := createValidConfig()
		config.Api.ClientId = "my-client-id"
		config.Api.ClientSecret = "my-client-secret"

		if err := validateConfig(config); err != nil {
			t.Errorf("expected no error when both auth methods are set (api key wins), got: %v", err)
		}
	})

	t.Run("missing api url", func(t *testing.T) {
		config := createValidConfig()
		config.Api.Url = ""

		err := validateConfig(config)
		if err == nil {
			t.Error("expected error for missing api url")
		}
	})

	t.Run("no auth configured", func(t *testing.T) {
		config := createValidConfig()
		config.Api.Username = ""
		config.Api.Password = ""

		err := validateConfig(config)
		if err == nil {
			t.Error("expected error when no auth method is configured")
		} else if !strings.Contains(err.Error(), "no authentication configured") {
			t.Errorf("expected no-auth error, got: %v", err)
		}
	})
}
