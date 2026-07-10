package main

import (
	"fmt"
	"log/slog"
	"os"

	"gopkg.in/yaml.v2"

	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/k8sjob"
	meshapi "github.com/meshcloud/building-block-runner/internal/meshapi"
)

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

	// envConfigFile is the canonical config-file-path override -- the same spelling every
	// fit persona binary honors via internal/config.EnvAlias{Var: "RUNNER_CONFIG_FILE"}
	// (D7: one config knob, one name, shared across personas).
	envConfigFile = "RUNNER_CONFIG_FILE"
	// envConfigFileDeprecated is the pre-consolidation controller-only spelling (accumulated
	// alias inventory row 1, docs/DEPRECATIONS.md) -- kept working, deprecation-logged;
	// envConfigFile wins when both are set.
	envConfigFileDeprecated = "RUNCONTROLLER_CONFIG_FILE"

	// Standard runner API-key env vars (shared with the standalone block runners). When set, they
	// override the api.clientId / api.clientSecret values from runner-config.yml.
	envApiClientId     = "RUNNER_API_CLIENT_ID"
	envApiClientSecret = "RUNNER_API_CLIENT_SECRET"
)

// resolveControllerConfigFile applies the RUNNER_CONFIG_FILE > RUNCONTROLLER_CONFIG_FILE
// (deprecated) > defaultConfigFile precedence. It routes the deprecation warning through the
// shared config.WarnDeprecated helper (§7.1) so the wording stays uniform with every other
// alias; the controller persona's config loading (readInYmlConfig/yaml.v2) predates the shared
// internal/config.Loader and is out of this cleanup pass's scope to rewrite wholesale -- only
// the alias gap itself (docs/DEPRECATIONS.md row 1) is in scope.
func resolveControllerConfigFile(logger *slog.Logger) string {
	if v := os.Getenv(envConfigFile); v != "" {
		return v
	}
	if v := os.Getenv(envConfigFileDeprecated); v != "" {
		config.WarnDeprecated(logger, envConfigFileDeprecated, envConfigFile)
		return v
	}
	return defaultConfigFile
}

func readControllerConfig(logger *slog.Logger) *controllerConfig {
	configPath := resolveControllerConfigFile(logger)

	cfg, err := readInYmlConfig(configPath)
	if err != nil {
		logger.Error("failed to read config file", "path", configPath, "error", err)
		os.Exit(1)
	}

	// Apply defaults for optional fields before validation/logging.
	// A zero value means "not configured"; a negative value is an explicit opt-out (unlimited).
	if cfg.MaxConcurrentJobs == 0 {
		cfg.MaxConcurrentJobs = k8sjob.DefaultMaxConcurrentJobs
	}

	// Environment overrides take precedence over the config file.
	applyApiKeyEnvOverrides(cfg, logger)

	if err := validateControllerConfig(cfg); err != nil {
		logger.Error("invalid configuration", "error", err)
		os.Exit(1)
	}

	logControllerConfig(logger, cfg)

	return cfg
}

// applyApiKeyEnvOverrides applies the standard RUNNER_API_CLIENT_ID / RUNNER_API_CLIENT_SECRET
// environment variables on top of the loaded config. They take precedence over api.clientId /
// api.clientSecret from runner-config.yml, allowing API-key credentials to be injected via the
// environment (e.g. from a Kubernetes secret) without baking them into the config file. Empty env
// vars are ignored so they never clear a value set in the file.
//
// API key auth takes precedence over basic auth (see apiConfig.NewAuthProvider), so supplying these
// is sufficient to authenticate even when the config file still carries username/password.
func applyApiKeyEnvOverrides(cfg *controllerConfig, logger *slog.Logger) {
	clientId := os.Getenv(envApiClientId)
	clientSecret := os.Getenv(envApiClientSecret)

	// API key auth needs both halves; supplying only one via the environment is almost always a
	// mistake (e.g. a typo'd secret name in the deployment), so warn loudly.
	if (clientId != "") != (clientSecret != "") {
		logger.Warn("only one of the API key env vars is set; API key auth requires both -- this is most likely a mistake",
			"clientIdVar", envApiClientId, "clientSecretVar", envApiClientSecret)
	}

	if clientId != "" {
		logger.Info("using API client id from environment", "var", envApiClientId)
		cfg.Api.ClientId = clientId
	}
	if clientSecret != "" {
		logger.Info("using API client secret from environment", "var", envApiClientSecret)
		cfg.Api.ClientSecret = clientSecret
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

// logControllerConfig logs the startup configuration as one structured record plus one line
// per configured implementation (slog, §8: the former [RUN CONTROLLER] banner is retired in
// favor of the persona attribute carried by the injected logger).
func logControllerConfig(logger *slog.Logger, cfg *controllerConfig) {
	maxConcurrent := "unlimited"
	if cfg.MaxConcurrentJobs >= 0 {
		maxConcurrent = fmt.Sprintf("%d", cfg.MaxConcurrentJobs)
	}

	attrs := []any{
		"namespace", cfg.Namespace,
		"uuid", cfg.Uuid,
		"maxConcurrentJobs", maxConcurrent,
		"implementations", len(cfg.Implementations),
	}
	if len(cfg.ImagePullSecrets) > 0 {
		attrs = append(attrs, "imagePullSecrets", cfg.ImagePullSecrets)
	}
	attrs = append(attrs, "apiUrl", cfg.Api.Url, "apiUsername", cfg.Api.Username)
	logger.Info("controller configuration", attrs...)

	for implType, spec := range cfg.Implementations {
		logger.Info("implementation configured",
			"type", implType, "image", spec.Image, "customEnvVars", len(spec.Env))
	}
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
