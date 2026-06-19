package controller

import (
	"fmt"
	"log"
	"os"

	meshapi "github.com/meshcloud/building-block-runner/go-meshapi-client/meshapi"
	"gopkg.in/yaml.v2"
)

var AppConfig *ControllerConfig = nil

// DiscoveredOidcIssuer holds the OIDC issuer URL discovered from Kubernetes at runtime
var DiscoveredOidcIssuer string = ""

// ControllerConfig holds the main configuration for the run controller
type ControllerConfig struct {
	Namespace              string                     `yaml:"namespace"`              // Kubernetes namespace where jobs are created
	ImagePullSecrets       []string                   `yaml:"imagePullSecrets"`       // Image pull secrets for runner jobs (optional)
	PollingIntervalSeconds int                        `yaml:"pollingIntervalSeconds"` // Polling interval in seconds (default: 10)
	Api                    ApiConfig                  `yaml:"api"`                    // Global API config used by controller to fetch runs and register runners
	Uuid                   string                     `yaml:"uuid"`                   // Unique identifier for this universal run controller
	OwnedByWorkspace       string                     `yaml:"ownedByWorkspace"`       // The workspace that owns this runner (required for registration)
	DisplayName            string                     `yaml:"displayName"`            // Human-readable display name for this controller (required for registration)
	Crypto                 CryptoConfig               `yaml:"crypto"`                 // Cryptographic keys for secure communication
	Tolerations            []TolerationConfig         `yaml:"tolerations"`            // Pod tolerations applied to all runner jobs (e.g. for spot instances)
	NodeSelector           map[string]string          `yaml:"nodeSelector"`           // Node selector applied to all runner jobs
	Implementations        map[string]JobSpecTemplate `yaml:"implementations"`        // Kubernetes job templates keyed by implementation type (e.g. TERRAFORM, GITHUB_WORKFLOW)
}

// ApiConfig holds API connection and authentication details.
// Provide either (clientId + clientSecret) for API key auth or (username + password) for Basic auth.
type ApiConfig struct {
	Url          string `yaml:"url"`          // API base URL
	Username     string `yaml:"username"`     // Basic auth username (mutually exclusive with clientId/clientSecret)
	Password     string `yaml:"password"`     // Basic auth password (mutually exclusive with clientId/clientSecret)
	ClientId     string `yaml:"clientId"`     // API key client ID (mutually exclusive with username/password)
	ClientSecret string `yaml:"clientSecret"` // API key client secret (mutually exclusive with username/password)
}

// NewAuthProvider returns the appropriate AuthProvider based on the configured credentials.
// API key auth takes precedence when both clientId and clientSecret are set.
// fallbackURL is used when the ApiConfig itself has no Url set (e.g. per-runner configs).
func (c ApiConfig) NewAuthProvider(fallbackURL string) meshapi.AuthProvider {
	url := c.Url
	if url == "" {
		url = fallbackURL
	}
	if c.ClientId != "" && c.ClientSecret != "" {
		return meshapi.NewApiKeyAuth(url, c.ClientId, c.ClientSecret)
	}
	return meshapi.BasicAuth{Username: c.Username, Password: c.Password}
}

// CryptoConfig holds cryptographic keys for secure communication
type CryptoConfig struct {
	PublicKey  string `yaml:"publicKey"`  // Public key for encryption (used to update runner)
	PrivateKey string `yaml:"privateKey"` // Private key for decryption (used to decrypt encrypted secrets)
}

// JobSpecTemplate defines the Kubernetes job specification for a runner
// All configuration is passed via environment variables
type JobSpecTemplate struct {
	Image             string             `yaml:"image"`             // Container image to use for the runner
	Command           []string           `yaml:"command"`           // Optional: Override container command for custom entrypoint wrapper
	Args              []string           `yaml:"args"`              // Optional: Override container args for custom entrypoint wrapper
	Env               map[string]string  `yaml:"env"`               // Additional environment variables to pass to the runner
	Resources         ResourcesConfig    `yaml:"resources"`         // Container resource requests and limits
	ExtraVolumes      []ExtraVolume      `yaml:"extraVolumes"`      // Additional volumes to mount (e.g., for trusted certs)
	ExtraVolumeMounts []ExtraVolumeMount `yaml:"extraVolumeMounts"` // Additional volume mounts
}

// TolerationConfig defines a pod toleration for scheduling runner jobs on tainted nodes.
// Operator defaults to "Equal" when Value is set, and "Exists" when only Key is set.
// Effect can be "NoSchedule", "PreferNoSchedule", or "NoExecute".
type TolerationConfig struct {
	Key               string  `yaml:"key"`
	Operator          string  `yaml:"operator"`
	Value             string  `yaml:"value"`
	Effect            string  `yaml:"effect"`
	TolerationSeconds *int64  `yaml:"tolerationSeconds"` // Only meaningful for "NoExecute" effect
}

// ExtraVolume defines an additional volume with support for ConfigMap, Secret, or EmptyDir sources
// Only one of ConfigMap, Secret, or EmptyDir should be set
type ExtraVolume struct {
	Name      string               `yaml:"name"`      // Volume name
	ConfigMap *ConfigMapVolumeSpec `yaml:"configMap"` // ConfigMap volume source (optional)
	Secret    *SecretVolumeSpec    `yaml:"secret"`    // Secret volume source (optional)
	EmptyDir  *EmptyDirVolumeSpec  `yaml:"emptyDir"`  // EmptyDir volume source (optional)
}

// ConfigMapVolumeSpec defines a ConfigMap volume source
type ConfigMapVolumeSpec struct {
	Name string `yaml:"name"` // ConfigMap name
}

// SecretVolumeSpec defines a Secret volume source
type SecretVolumeSpec struct {
	SecretName string `yaml:"secretName"` // Secret name
}

// EmptyDirVolumeSpec defines an EmptyDir volume source
type EmptyDirVolumeSpec struct {
	SizeLimit string `yaml:"sizeLimit"` // Optional size limit (e.g., "1Gi")
}

// ExtraVolumeMount defines an additional volume mount
type ExtraVolumeMount struct {
	Name      string `yaml:"name"`      // Volume name (must match volume)
	MountPath string `yaml:"mountPath"` // Path to mount in container
	ReadOnly  bool   `yaml:"readOnly"`  // Mount as read-only
}

// ResourcesConfig defines CPU and memory resource requests and limits for the runner container
type ResourcesConfig struct {
	Requests ResourceSpec `yaml:"requests"` // Resource requests (guaranteed minimum)
	Limits   ResourceSpec `yaml:"limits"`   // Resource limits (maximum allowed)
}

// ResourceSpec defines CPU and memory values
type ResourceSpec struct {
	Cpu    string `yaml:"cpu"`    // CPU in Kubernetes format (e.g., "100m", "1")
	Memory string `yaml:"memory"` // Memory in Kubernetes format (e.g., "256Mi", "1Gi")
}

const (
	defaultConfigFile = "runner-config.yml"

	envConfigFile = "RUNCONTROLLER_CONFIG_FILE"
)

func ReadConfig(logger *log.Logger) *ControllerConfig {
	configPath := os.Getenv(envConfigFile)
	if configPath == "" {
		configPath = defaultConfigFile
	}

	// Read configuration from file
	config, err := ReadInYmlConfig(configPath)
	if err != nil {
		logger.Fatalf("Failed to read config file %s: %v\n", configPath, err)
	}

	// Validate configuration
	if err := validateConfig(config); err != nil {
		logger.Fatalf("Invalid configuration: %v\n", err)
	}

	// Log startup configuration
	logConfig(logger, config)

	AppConfig = config
	return config
}

// validateConfig ensures the configuration is valid and complete
// validateApiAuth checks that exactly one authentication method is configured for an ApiConfig.
// context is a human-readable prefix for error messages (e.g. "api" or "runner[0].api").
func validateApiAuth(c ApiConfig, context string) error {
	hasBasicAuth := c.Username != "" || c.Password != ""
	hasApiKeyAuth := c.ClientId != "" || c.ClientSecret != ""

	if hasBasicAuth && hasApiKeyAuth {
		return fmt.Errorf("%s: ambiguous authentication configuration: both Basic auth (username/password) and API key auth (clientId/clientSecret) are set; configure only one method", context)
	}
	if hasBasicAuth {
		if c.Username == "" {
			return fmt.Errorf("%s.username is required when using Basic auth", context)
		}
		if c.Password == "" {
			return fmt.Errorf("%s.password is required when using Basic auth", context)
		}
	}
	if hasApiKeyAuth {
		if c.ClientId == "" {
			return fmt.Errorf("%s.clientId is required when using API key auth", context)
		}
		if c.ClientSecret == "" {
			return fmt.Errorf("%s.clientSecret is required when using API key auth", context)
		}
	}
	if !hasBasicAuth && !hasApiKeyAuth {
		return fmt.Errorf("%s: no authentication configured; set either username/password (Basic auth) or clientId/clientSecret (API key auth)", context)
	}
	return nil
}

func validateConfig(config *ControllerConfig) error {
	if config.Namespace == "" {
		return fmt.Errorf("namespace is required")
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
	if len(config.Implementations) == 0 {
		return fmt.Errorf("at least one implementation handler must be configured under 'implementations'")
	}
	for key, spec := range config.Implementations {
		if !isValidHandlerImplementationType(key) {
			return fmt.Errorf("implementations key '%s' is invalid; valid values are: TERRAFORM, GITHUB_WORKFLOW, GITLAB_PIPELINE, AZURE_DEVOPS_PIPELINE, MANUAL", key)
		}
		if spec.Image == "" {
			return fmt.Errorf("implementations.%s.image is required", key)
		}
	}
	return nil
}

// logConfig logs the startup configuration
func logConfig(logger *log.Logger, config *ControllerConfig) {
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

func ReadInYmlConfig(file string) (*ControllerConfig, error) {
	fileData, err := os.ReadFile(file)
	if err != nil {
		return &ControllerConfig{}, err
	}

	config := &ControllerConfig{}
	err = yaml.Unmarshal(fileData, config)

	return config, err
}

// isValidHandlerImplementationType checks if the given implementation type is a valid handler key.
// Note: "ALL" is a registration concept only and cannot be used as a handler key.
func isValidHandlerImplementationType(implType string) bool {
	switch meshapi.RunnerImplementationType(implType) {
	case meshapi.RunnerTypeTerraform,
		meshapi.RunnerTypeGitHubWorkflow,
		meshapi.RunnerTypeGitLabPipeline,
		meshapi.RunnerTypeAzureDevOpsPipeline,
		meshapi.RunnerTypeManual:
		return true
	default:
		return false
	}
}
