package controller

import (
	"testing"
)

func createValidRunnerConfig(uuid string) RunnerConfig {
	return RunnerConfig{
		Uuid:               uuid,
		DisplayName:        "Test Runner " + uuid,
		OwnedByWorkspace:   "test-workspace",
		ImplementationType: "TERRAFORM",
		Api: ApiConfig{
			Username: "user",
			Password: "pass",
		},
		Crypto: CryptoConfig{
			PublicKey:  "public-key",
			PrivateKey: "private-key",
		},
		JobSpecTemplate: JobSpecTemplate{
			Image: "test-image:latest",
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
		Runners: []RunnerConfig{
			createValidRunnerConfig("uuid-1"),
		},
	}
}

func TestValidateConfig_ValidConfig(t *testing.T) {
	config := createValidConfig()
	if err := validateConfig(config); err != nil {
		t.Errorf("expected no error for valid config, got: %v", err)
	}
}

func TestValidateConfig_DuplicateRunnerUUIDs(t *testing.T) {
	config := createValidConfig()
	config.Runners = []RunnerConfig{
		createValidRunnerConfig("duplicate-uuid"),
		createValidRunnerConfig("unique-uuid"),
		createValidRunnerConfig("duplicate-uuid"),
	}

	if err := validateConfig(config); err == nil {
		t.Error("expected error for duplicate runner UUIDs")
	}
}

func TestValidateConfig_MultipleRunnersWithUniqueUUIDs(t *testing.T) {
	config := createValidConfig()
	config.Runners = []RunnerConfig{
		createValidRunnerConfig("uuid-1"),
		createValidRunnerConfig("uuid-2"),
		createValidRunnerConfig("uuid-3"),
	}

	if err := validateConfig(config); err != nil {
		t.Errorf("expected no error for unique UUIDs, got: %v", err)
	}
}

func TestValidateConfig_InvalidConfigs(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ControllerConfig)
	}{
		{
			name:   "empty namespace",
			mutate: func(c *ControllerConfig) { c.Namespace = "" },
		},
		{
			name:   "no runners",
			mutate: func(c *ControllerConfig) { c.Runners = []RunnerConfig{} },
		},
		{
			name:   "empty runner UUID",
			mutate: func(c *ControllerConfig) { c.Runners[0].Uuid = "" },
		},
		{
			name:   "empty runner display name",
			mutate: func(c *ControllerConfig) { c.Runners[0].DisplayName = "" },
		},
		{
			name:   "empty runner workspace",
			mutate: func(c *ControllerConfig) { c.Runners[0].OwnedByWorkspace = "" },
		},
		{
			name:   "empty implementation type",
			mutate: func(c *ControllerConfig) { c.Runners[0].ImplementationType = "" },
		},
		{
			name:   "invalid implementation type",
			mutate: func(c *ControllerConfig) { c.Runners[0].ImplementationType = "INVALID" },
		},
		{
			name:   "empty private key",
			mutate: func(c *ControllerConfig) { c.Runners[0].Crypto.PrivateKey = "" },
		},
		{
			name:   "empty public key",
			mutate: func(c *ControllerConfig) { c.Runners[0].Crypto.PublicKey = "" },
		},
		{
			name:   "empty image",
			mutate: func(c *ControllerConfig) { c.Runners[0].JobSpecTemplate.Image = "" },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := createValidConfig()
			tt.mutate(config)

			if err := validateConfig(config); err == nil {
				t.Errorf("expected validation error for %s", tt.name)
			}
		})
	}
}

func TestValidateConfig_ValidImplementationTypes(t *testing.T) {
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
			config.Runners[0].ImplementationType = implType

			err := validateConfig(config)

			if err != nil {
				t.Errorf("validateConfig() error = %v, expected no error for valid implementationType %s", err, implType)
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

	t.Run("valid api key auth on runner api", func(t *testing.T) {
		config := createValidConfig()
		config.Runners[0].Api.Username = ""
		config.Runners[0].Api.Password = ""
		config.Runners[0].Api.ClientId = "my-client-id"
		config.Runners[0].Api.ClientSecret = "my-client-secret"

		if err := validateConfig(config); err != nil {
			t.Errorf("expected no error for valid runner API key auth, got: %v", err)
		}
	})

	t.Run("both auth methods set on global api", func(t *testing.T) {
		config := createValidConfig()
		config.Api.ClientId = "my-client-id"
		config.Api.ClientSecret = "my-client-secret"

		err := validateConfig(config)
		if err == nil {
			t.Error("expected error when both auth methods are set on api")
		} else if !contains(err.Error(), "ambiguous authentication configuration") {
			t.Errorf("expected ambiguous auth error, got: %v", err)
		}
	})

	t.Run("both auth methods set on runner api", func(t *testing.T) {
		config := createValidConfig()
		config.Runners[0].Api.ClientId = "my-client-id"
		config.Runners[0].Api.ClientSecret = "my-client-secret"

		err := validateConfig(config)
		if err == nil {
			t.Error("expected error when both auth methods are set on runner api")
		} else if !contains(err.Error(), "ambiguous authentication configuration") {
			t.Errorf("expected ambiguous auth error, got: %v", err)
		}
	})
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
