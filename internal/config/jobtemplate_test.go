package config

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

func createValidConfig() K8sJobConfig {
	return K8sJobConfig{
		Namespace:       "test-namespace",
		Implementations: createValidImplementations(),
	}
}

func TestConfig_Validate_ValidConfig(t *testing.T) {
	cfg := createValidConfig()
	if err := cfg.Validate(); err != nil {
		t.Errorf("expected no error for valid config, got: %v", err)
	}
}

func TestConfig_Validate_MultipleImplementations(t *testing.T) {
	cfg := createValidConfig()
	cfg.Implementations = map[string]JobSpecTemplate{
		"TERRAFORM":             {Image: "tf-runner:latest"},
		"GITHUB_WORKFLOW":       {Image: "gh-runner:latest"},
		"GITLAB_PIPELINE":       {Image: "gl-runner:latest"},
		"AZURE_DEVOPS_PIPELINE": {Image: "ado-runner:latest"},
		"MANUAL":                {Image: "manual-runner:latest"},
	}

	if err := cfg.Validate(); err != nil {
		t.Errorf("expected no error for all implementation types, got: %v", err)
	}
}

func TestConfig_Validate_InvalidConfigs(t *testing.T) {
	tests := []struct {
		name           string
		mutate         func(*K8sJobConfig)
		expectedErrSub string
	}{
		{
			name:           "empty namespace",
			mutate:         func(c *K8sJobConfig) { c.Namespace = "" },
			expectedErrSub: "namespace is required",
		},
		{
			name:           "empty implementations map",
			mutate:         func(c *K8sJobConfig) { c.Implementations = map[string]JobSpecTemplate{} },
			expectedErrSub: "at least one implementation handler",
		},
		{
			name:           "nil implementations map",
			mutate:         func(c *K8sJobConfig) { c.Implementations = nil },
			expectedErrSub: "at least one implementation handler",
		},
		{
			name: "invalid implementation type key",
			mutate: func(c *K8sJobConfig) {
				c.Implementations = map[string]JobSpecTemplate{
					"INVALID_TYPE": {Image: "some-image:latest"},
				}
			},
			expectedErrSub: "implementations key 'INVALID_TYPE' is invalid",
		},
		{
			name: "ALL not valid as handler key",
			mutate: func(c *K8sJobConfig) {
				c.Implementations = map[string]JobSpecTemplate{
					"ALL": {Image: "some-image:latest"},
				}
			},
			expectedErrSub: "implementations key 'ALL' is invalid",
		},
		{
			name: "empty image in implementation spec",
			mutate: func(c *K8sJobConfig) {
				c.Implementations = map[string]JobSpecTemplate{
					"TERRAFORM": {Image: ""},
				}
			},
			expectedErrSub: "implementations.TERRAFORM.image is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := createValidConfig()
			tt.mutate(&cfg)

			err := cfg.Validate()
			if err == nil {
				t.Fatalf("expected validation error for %s", tt.name)
			}
			if tt.expectedErrSub != "" && !strings.Contains(err.Error(), tt.expectedErrSub) {
				t.Errorf("expected error containing %q, got: %v", tt.expectedErrSub, err)
			}
		})
	}
}

func TestConfig_Validate_ValidImplementationTypeKeys(t *testing.T) {
	validTypes := []string{
		"TERRAFORM",
		"GITHUB_WORKFLOW",
		"GITLAB_PIPELINE",
		"AZURE_DEVOPS_PIPELINE",
		"MANUAL",
	}

	for _, implType := range validTypes {
		t.Run(implType, func(t *testing.T) {
			cfg := createValidConfig()
			cfg.Implementations = map[string]JobSpecTemplate{
				implType: {Image: "test-image:latest"},
			}

			if err := cfg.Validate(); err != nil {
				t.Errorf("Validate() error = %v, expected no error for valid key %s", err, implType)
			}
		})
	}
}
