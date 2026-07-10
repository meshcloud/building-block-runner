package main

import (
	"fmt"
	"log"
	"os"

	"gopkg.in/yaml.v2"

	"github.com/meshcloud/building-block-runner/internal/k8sjob"
	meshapi "github.com/meshcloud/building-block-runner/internal/meshapi"
)

// UseTestClient short-circuits the startup registration PUT for local-dev/test wiring
// (moved from the former internal/controller.UseTestClient).
var UseTestClient = false

// controllerConfig holds the run-controller persona's full configuration -- the dissolution
// target of the former internal/controller.ControllerConfig (PLAN_DETAIL_05 §5): the
// Kubernetes-facing fields (namespace, job templates, tolerations, node selector, image
// pull secrets) now live in k8sjob.Config and are embedded inline so every existing yaml
// key parses byte-identically (D7); registration/crypto/api fields and the polling/capacity
// knobs stay here because they are the persona's own wiring concern (D11: only main wires).
type controllerConfig struct {
	k8sjob.Config          `yaml:",inline"`
	PollingIntervalSeconds int          `yaml:"pollingIntervalSeconds"` // Polling interval in seconds (default: 10)
	MaxConcurrentJobs      int          `yaml:"maxConcurrentJobs"`      // Max number of unfinished runner jobs this controller keeps in flight (default: 10; negative = unlimited)
	Api                    apiConfig    `yaml:"api"`                    // Global API config used by controller to fetch runs and register runners
	Uuid                   string       `yaml:"uuid"`                   // Unique identifier for this universal run controller
	OwnedByWorkspace       string       `yaml:"ownedByWorkspace"`       // The workspace that owns this runner (required for registration)
	DisplayName            string       `yaml:"displayName"`            // Human-readable display name for this controller (required for registration)
	Crypto                 cryptoConfig `yaml:"crypto"`                 // Cryptographic keys for secure communication
}

// apiConfig holds API connection and authentication details.
// Provide either (clientId + clientSecret) for API key auth or (username + password) for Basic auth.
type apiConfig struct {
	Url          string `yaml:"url"`          // API base URL
	Username     string `yaml:"username"`     // Basic auth username (mutually exclusive with clientId/clientSecret)
	Password     string `yaml:"password"`     // Basic auth password (mutually exclusive with clientId/clientSecret)
	ClientId     string `yaml:"clientId"`     // API key client ID (mutually exclusive with username/password)
	ClientSecret string `yaml:"clientSecret"` // API key client secret (mutually exclusive with username/password)
}

// NewAuthProvider returns the appropriate AuthProvider based on the configured credentials.
// API key auth takes precedence when both clientId and clientSecret are set.
func (c apiConfig) NewAuthProvider() meshapi.AuthProvider {
	if c.ClientId != "" && c.ClientSecret != "" {
		return meshapi.NewApiKeyAuth(c.Url, c.ClientId, c.ClientSecret)
	}
	return meshapi.BasicAuth{Username: c.Username, Password: c.Password}
}

// cryptoConfig holds cryptographic keys for secure communication.
type cryptoConfig struct {
	PublicKey  string `yaml:"publicKey"`  // Public key for encryption (used to update runner)
	PrivateKey string `yaml:"privateKey"` // Private key for decryption (used to decrypt encrypted secrets)
}

const (
	defaultConfigFile = "runner-config.yml"

	envConfigFile = "RUNCONTROLLER_CONFIG_FILE"

	// Standard runner API-key env vars (shared with the standalone block runners). When set, they
	// override the api.clientId / api.clientSecret values from runner-config.yml.
	envApiClientId     = "RUNNER_API_CLIENT_ID"
	envApiClientSecret = "RUNNER_API_CLIENT_SECRET"
)

func readControllerConfig(logger *log.Logger) *controllerConfig {
	configPath := os.Getenv(envConfigFile)
	if configPath == "" {
		configPath = defaultConfigFile
	}

	config, err := readInYmlConfig(configPath)
	if err != nil {
		logger.Fatalf("Failed to read config file %s: %v\n", configPath, err)
	}

	// Apply defaults for optional fields before validation/logging.
	// A zero value means "not configured"; a negative value is an explicit opt-out (unlimited).
	if config.MaxConcurrentJobs == 0 {
		config.MaxConcurrentJobs = k8sjob.DefaultMaxConcurrentJobs
	}

	// Environment overrides take precedence over the config file.
	applyApiKeyEnvOverrides(config, logger)

	if err := validateControllerConfig(config); err != nil {
		logger.Fatalf("Invalid configuration: %v\n", err)
	}

	logControllerConfig(logger, config)

	return config
}

// applyApiKeyEnvOverrides applies the standard RUNNER_API_CLIENT_ID / RUNNER_API_CLIENT_SECRET
// environment variables on top of the loaded config. They take precedence over api.clientId /
// api.clientSecret from runner-config.yml, allowing API-key credentials to be injected via the
// environment (e.g. from a Kubernetes secret) without baking them into the config file. Empty env
// vars are ignored so they never clear a value set in the file.
//
// API key auth takes precedence over basic auth (see apiConfig.NewAuthProvider), so supplying these
// is sufficient to authenticate even when the config file still carries username/password.
func applyApiKeyEnvOverrides(config *controllerConfig, logger *log.Logger) {
	clientId := os.Getenv(envApiClientId)
	clientSecret := os.Getenv(envApiClientSecret)

	// API key auth needs both halves; supplying only one via the environment is almost always a
	// mistake (e.g. a typo'd secret name in the deployment), so warn loudly.
	if (clientId != "") != (clientSecret != "") {
		logger.Printf("Warning: only one of %s / %s is set in the environment; API key auth requires both. This is most likely a mistake.\n", envApiClientId, envApiClientSecret)
	}

	if clientId != "" {
		logger.Printf("Using %s from environment\n", envApiClientId)
		config.Api.ClientId = clientId
	}
	if clientSecret != "" {
		logger.Printf("Using %s from environment\n", envApiClientSecret)
		config.Api.ClientSecret = clientSecret
	}
}

// validateApiAuth checks that at least one complete authentication method is configured for an
// apiConfig. context is a human-readable prefix for error messages (e.g. "api").
//
// When both methods are fully configured, API key auth takes precedence (see
// apiConfig.NewAuthProvider) -- that is a valid configuration, not an error. This lets API key
// credentials supplied via configuration override a basic-auth default baked into the image.
func validateApiAuth(c apiConfig, context string) error {
	hasCompleteApiKeyAuth := c.ClientId != "" && c.ClientSecret != ""
	hasCompleteBasicAuth := c.Username != "" && c.Password != ""

	if hasCompleteApiKeyAuth || hasCompleteBasicAuth {
		return nil
	}

	if c.ClientId != "" || c.ClientSecret != "" {
		if c.ClientId == "" {
			return fmt.Errorf("%s.clientId is required when using API key auth", context)
		}
		return fmt.Errorf("%s.clientSecret is required when using API key auth", context)
	}
	if c.Username != "" || c.Password != "" {
		if c.Username == "" {
			return fmt.Errorf("%s.username is required when using Basic auth", context)
		}
		return fmt.Errorf("%s.password is required when using Basic auth", context)
	}
	return fmt.Errorf("%s: no authentication configured; set either username/password (Basic auth) or clientId/clientSecret (API key auth)", context)
}

func validateControllerConfig(config *controllerConfig) error {
	if err := config.Validate(); err != nil {
		return err
	}
	if config.Api.Url == "" {
		return fmt.Errorf("api.url is required")
	}
	if err := validateApiAuth(config.Api, "api"); err != nil {
		return err
	}
	if config.Uuid == "" {
		return fmt.Errorf("uuid is required")
	}
	if config.OwnedByWorkspace == "" {
		return fmt.Errorf("ownedByWorkspace is required")
	}
	if config.DisplayName == "" {
		return fmt.Errorf("displayName is required")
	}
	if config.Crypto.PublicKey == "" {
		return fmt.Errorf("crypto.publicKey is required")
	}
	if config.Crypto.PrivateKey == "" {
		return fmt.Errorf("crypto.privateKey is required")
	}
	return nil
}

// logControllerConfig logs the startup configuration.
func logControllerConfig(logger *log.Logger, config *controllerConfig) {
	logger.Println("--------------------------------------------------------------------")
	logger.Printf("Kubernetes namespace: %s\n", config.Namespace)
	if len(config.ImagePullSecrets) > 0 {
		logger.Printf("Image pull secrets: %v\n", config.ImagePullSecrets)
	}

	if UseTestClient {
		logger.Printf("(!) Test mode enabled - not connecting to API\n")
	} else {
		logger.Printf("API URL: %s\n", config.Api.Url)
		logger.Printf("API Username: %s\n", config.Api.Username)
	}

	logger.Printf("Controller UUID: %s\n", config.Uuid)
	if config.MaxConcurrentJobs < 0 {
		logger.Printf("Max concurrent jobs: unlimited\n")
	} else {
		logger.Printf("Max concurrent jobs: %d\n", config.MaxConcurrentJobs)
	}
	logger.Printf("Configured implementations: %d\n", len(config.Implementations))
	for implType, spec := range config.Implementations {
		logger.Printf("  %s: image=%s", implType, spec.Image)
		if len(spec.Env) > 0 {
			logger.Printf(" (%d custom env vars)", len(spec.Env))
		}
		logger.Println()
	}
	logger.Println("--------------------------------------------------------------------")
}

func readInYmlConfig(file string) (*controllerConfig, error) {
	fileData, err := os.ReadFile(file)
	if err != nil {
		return &controllerConfig{}, err
	}

	config := &controllerConfig{}
	err = yaml.Unmarshal(fileData, config)

	return config, err
}
