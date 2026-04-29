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
		ControllerId: "test-controller",
		Namespace:    "test-namespace",
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
			name:   "empty controller id",
			mutate: func(c *ControllerConfig) { c.ControllerId = "" },
		},
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
