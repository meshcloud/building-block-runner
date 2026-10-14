package controller

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	meshapi "github.com/meshcloud/building-block-runner/go-meshapi-client/meshapi"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v2"
)

var nopLogger = log.New(io.Discard, "", 0)

func createValidImplementations() map[string]JobSpecTemplate {
	return map[string]JobSpecTemplate{
		"TERRAFORM": {
			Image: "tf-block-runner:latest",
		},
	}
}

// createValidBasicAuthConfig returns a config that passes validation using Basic auth
// (username + password). Tests exercising API key auth override these fields explicitly.
func createValidBasicAuthConfig() *ControllerConfig {
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
	config := createValidBasicAuthConfig()
	if err := validateConfig(config); err != nil {
		t.Errorf("expected no error for valid config, got: %v", err)
	}
}

func TestReadConfig_ApiUrlFromEnvironment(t *testing.T) {
	configFile := filepath.Join(t.TempDir(), "runner-config.yml")
	configYaml, err := yaml.Marshal(createValidBasicAuthConfig())
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(configFile, configYaml, 0o600))
	t.Setenv(envConfigFile, configFile)
	t.Setenv(envApiUrl, "http://env:8080")
	prev := AppConfig
	t.Cleanup(func() { AppConfig = prev })

	config := ReadConfig(nopLogger)

	if config.Api.Url != "http://env:8080" {
		t.Errorf("expected api url from env, got: %q", config.Api.Url)
	}
}

func TestValidateConfig_MultipleImplementations(t *testing.T) {
	config := createValidBasicAuthConfig()
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
			config := createValidBasicAuthConfig()
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
			config := createValidBasicAuthConfig()
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
		config := createValidBasicAuthConfig()
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
		config := createValidBasicAuthConfig()
		config.Api.ClientId = "my-client-id"
		config.Api.ClientSecret = "my-client-secret"

		if err := validateConfig(config); err != nil {
			t.Errorf("expected no error when both auth methods are set (api key wins), got: %v", err)
		}
	})

	t.Run("missing api url", func(t *testing.T) {
		config := createValidBasicAuthConfig()
		config.Api.Url = ""

		err := validateConfig(config)
		if err == nil {
			t.Error("expected error for missing api url")
		}
	})

	t.Run("no auth configured", func(t *testing.T) {
		config := createValidBasicAuthConfig()
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

func TestApplyApiKeyEnvOverrides(t *testing.T) {
	t.Run("sets clientId/clientSecret from environment when unset in file", func(t *testing.T) {
		t.Setenv(envApiClientId, "env-client-id")
		t.Setenv(envApiClientSecret, "env-client-secret")

		config := createValidBasicAuthConfig()
		applyApiKeyEnvOverrides(config, nopLogger)

		if config.Api.ClientId != "env-client-id" {
			t.Errorf("expected clientId from env, got: %q", config.Api.ClientId)
		}
		if config.Api.ClientSecret != "env-client-secret" {
			t.Errorf("expected clientSecret from env, got: %q", config.Api.ClientSecret)
		}
	})

	t.Run("env takes precedence over config file values", func(t *testing.T) {
		t.Setenv(envApiClientId, "env-client-id")
		t.Setenv(envApiClientSecret, "env-client-secret")

		config := createValidBasicAuthConfig()
		config.Api.ClientId = "file-client-id"
		config.Api.ClientSecret = "file-client-secret"
		applyApiKeyEnvOverrides(config, nopLogger)

		if config.Api.ClientId != "env-client-id" || config.Api.ClientSecret != "env-client-secret" {
			t.Errorf("expected env to override file, got: %q / %q", config.Api.ClientId, config.Api.ClientSecret)
		}
	})

	t.Run("empty env preserves config file values", func(t *testing.T) {
		t.Setenv(envApiClientId, "")
		t.Setenv(envApiClientSecret, "")

		config := createValidBasicAuthConfig()
		config.Api.ClientId = "file-client-id"
		config.Api.ClientSecret = "file-client-secret"
		applyApiKeyEnvOverrides(config, nopLogger)

		if config.Api.ClientId != "file-client-id" || config.Api.ClientSecret != "file-client-secret" {
			t.Errorf("expected file values preserved, got: %q / %q", config.Api.ClientId, config.Api.ClientSecret)
		}
	})

	t.Run("env api key takes precedence over a basic-auth-only config file", func(t *testing.T) {
		t.Setenv(envApiClientId, "env-client-id")
		t.Setenv(envApiClientSecret, "env-client-secret")

		// createValidBasicAuthConfig carries only username/password; the env-supplied API key should win.
		config := createValidBasicAuthConfig()
		applyApiKeyEnvOverrides(config, nopLogger)

		if err := validateConfig(config); err != nil {
			t.Fatalf("expected valid config with env API key over file basic auth, got: %v", err)
		}

		// The env values must have landed on the config...
		if config.Api.ClientId != "env-client-id" || config.Api.ClientSecret != "env-client-secret" {
			t.Fatalf("expected env API key on config, got: %q / %q", config.Api.ClientId, config.Api.ClientSecret)
		}
		// ...and the resolved auth provider must actually be API key auth, not the file's basic auth.
		if provider := config.Api.NewAuthProvider(""); provider == nil {
			t.Error("expected an auth provider, got nil")
		} else if _, ok := provider.(*meshapi.ApiKeyAuth); !ok {
			t.Errorf("expected API key auth to be selected over basic auth, got %T", provider)
		}
	})
}

func validKeycloakWif() *KeycloakWifConfig {
	return &KeycloakWifConfig{
		Url:          "https://id-dev.meshstack.io/",
		Realm:        "meshcloud-dev-wif",
		ClientId:     "run-controller",
		ClientSecret: "secret",
	}
}

func Test_KeycloakWif_IssuerFromRealmUrl(t *testing.T) {
	wif := validKeycloakWif()

	applyKeycloakWifDefaults(wif)

	require.Equal(t, "https://id-dev.meshstack.io/realms/meshcloud-dev-wif", wif.issuer())
	require.Equal(t, 90, wif.TokenLifetimeMinutes)
}

func Test_InstanceKeycloakWif_IssuerKeepsAuthPath(t *testing.T) {
	wif := validKeycloakWif()
	wif.Url = "https://sso.example.com/auth"
	wif.Realm = "example-wif"

	applyKeycloakWifDefaults(wif)

	require.Equal(t, "https://sso.example.com/auth/realms/example-wif", wif.issuer())
}

func Test_KeycloakWif_Validation(t *testing.T) {
	for name, testCase := range map[string]struct {
		change func(*KeycloakWifConfig)
		error  string
	}{
		"http url":          {func(c *KeycloakWifConfig) { c.Url = "http://keycloak-http/auth" }, "workloadIdentityFederation.keycloak.url must be an https URL"},
		"no url":            {func(c *KeycloakWifConfig) { c.Url = "" }, "workloadIdentityFederation.keycloak.url"},
		"no realm":          {func(c *KeycloakWifConfig) { c.Realm = "" }, "workloadIdentityFederation.keycloak.realm"},
		"no client secret":  {func(c *KeycloakWifConfig) { c.ClientSecret = "" }, "workloadIdentityFederation.keycloak.clientId and clientSecret"},
		"negative lifetime": {func(c *KeycloakWifConfig) { c.TokenLifetimeMinutes = -1 }, "workloadIdentityFederation.keycloak.tokenLifetimeMinutes"},
	} {
		t.Run(name, func(t *testing.T) {
			config := createValidBasicAuthConfig()
			config.WorkloadIdentityFederation = WorkloadIdentityFederationConfig{Issuer: WifIssuerKeycloak, Keycloak: validKeycloakWif()}
			testCase.change(config.WorkloadIdentityFederation.Keycloak)
			applyKeycloakWifDefaults(config.WorkloadIdentityFederation.Keycloak)

			require.ErrorContains(t, validateConfig(config), testCase.error)
		})
	}
}

func Test_WifIssuer_Validation(t *testing.T) {
	for name, testCase := range map[string]struct {
		wif   WorkloadIdentityFederationConfig
		error string
	}{
		"left out":                   {WorkloadIdentityFederationConfig{}, ""},
		"kubernetes":                 {WorkloadIdentityFederationConfig{Issuer: WifIssuerKubernetes}, ""},
		"off":                        {WorkloadIdentityFederationConfig{Issuer: WifIssuerOff}, ""},
		"keycloak":                   {WorkloadIdentityFederationConfig{Issuer: WifIssuerKeycloak, Keycloak: validKeycloakWif()}, ""},
		"keycloak without its block": {WorkloadIdentityFederationConfig{Issuer: WifIssuerKeycloak}, "workloadIdentityFederation.keycloak is required with issuer keycloak"},
		"keycloak block, no issuer":  {WorkloadIdentityFederationConfig{Keycloak: validKeycloakWif()}, "only valid with issuer keycloak, not kubernetes"},
		"keycloak block with off":    {WorkloadIdentityFederationConfig{Issuer: WifIssuerOff, Keycloak: validKeycloakWif()}, "only valid with issuer keycloak, not off"},
		"unknown issuer":             {WorkloadIdentityFederationConfig{Issuer: "gke"}, "workloadIdentityFederation.issuer must be kubernetes, keycloak or off"},
	} {
		t.Run(name, func(t *testing.T) {
			config := createValidBasicAuthConfig()
			config.WorkloadIdentityFederation = testCase.wif
			applyKeycloakWifDefaults(config.WorkloadIdentityFederation.Keycloak)

			err := validateConfig(config)

			if testCase.error == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, testCase.error)
			}
		})
	}
}
