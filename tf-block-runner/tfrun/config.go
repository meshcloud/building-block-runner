package tfrun

import (
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"

	meshapi "github.com/meshcloud/building-block-runner/go-meshapi-client/meshapi"
	"gopkg.in/yaml.v2"
)

var AppConfig TfRunnerConfig

type TfRunnerConfig struct {
	TfCommandTimeoutMins  int          `yaml:"timeoutMins"`
	TfParentWorkingDir    string       `yaml:"workingDir"`
	TfInstallDir          string       `yaml:"tfInstallDir"`
	RunApiBackend         RunApiConfig `yaml:"api"`
	SkipHostKeyValidation bool         `yaml:"insecureHostKeys"`
	PrivateKey            string       `yaml:"privateKey"`
	PrivateKeyFile        string       `yaml:"privateKeyFile"`
	WsTimeoutMins         int          `yaml:"wsTimeoutMins"`
	InitTimeoutMins       int          `yaml:"initTimeoutMins"`
	RunnerUuid            string       `yaml:"runnerUuid"`
}

// RunApiConfig holds API connection and authentication details.
// Provide either (clientId + clientSecret) for API key auth or (user + password) for Basic auth.
type RunApiConfig struct {
	Url          string `yaml:"url"`
	User         string `yaml:"user"`
	Password     string `yaml:"password"`
	ClientId     string `yaml:"clientId"`
	ClientSecret string `yaml:"clientSecret"`
}

// NewAuthProvider returns the appropriate AuthProvider based on the configured credentials.
// API key auth takes precedence when both clientId and clientSecret are set.
// Returns nil when neither auth method is configured (valid in single-run mode, runToken covers it).
func (c RunApiConfig) NewAuthProvider() meshapi.AuthProvider {
	if c.ClientId != "" && c.ClientSecret != "" {
		return meshapi.NewApiKeyAuth(c.Url, c.ClientId, c.ClientSecret)
	}
	if c.User != "" && c.Password != "" {
		return meshapi.BasicAuth{Username: c.User, Password: c.Password}
	}
	return nil
}

const (
	defaultConfigFile     = "runner-config.yml"
	defaultPrivateKeyFile = "/app/runner-private.pem"

	envConfigFile       = "RUNNER_CONFIG_FILE"
	envRunnerUUID       = "RUNNER_UUID"
	envAPIURL           = "RUNNER_API_URL"
	envAuthUsername     = "RUNNER_API_USERNAME"
	envAuthPassword     = "RUNNER_API_PASSWORD"
	envAuthClientID     = "RUNNER_API_CLIENT_ID"
	envAuthClientSecret = "RUNNER_API_CLIENT_SECRET"
	envPrivateKeyFile   = "RUNNER_PRIVATE_KEY_FILE"
	envExecutionMode    = "EXECUTION_MODE"
	envRunJSONFilePath  = "RUN_JSON_FILE_PATH"
)

func ReadConfig(logger *log.Logger) error {
	// read configFile path from env var or use default
	configPath := os.Getenv(envConfigFile)
	if configPath == "" {
		configPath = defaultConfigFile
	}

	// read in and unmarshal config file (if present)
	if fileData, err := os.ReadFile(configPath); errors.Is(err, fs.ErrNotExist) {
		logger.Printf("config file %s does not exist, will use defaults and environment", configPath)
	} else if err != nil {
		return err
	} else if err := yaml.Unmarshal(fileData, &AppConfig); err != nil {
		return err
	}

	// apply environment variables (highest precedence)
	applyEnvVars(logger)

	// Try to load the private key from the configured file path (highest priority).
	// Uses RUNNER_PRIVATE_KEY_FILE env var path if set, otherwise the default /app/private.key.
	// Falls back to privateKey from runner-config.yml if the file is not found.
	applyPrivateKeyFile(AppConfig.PrivateKeyFile, &AppConfig, logger)

	// validate authentication configuration
	if err := validateAuthConfig(AppConfig); err != nil {
		return err
	}

	// validate RunnerUuid is set
	if err := validateRunnerUuid(AppConfig); err != nil {
		return err
	}

	logger.Printf("--------------------------------------------------------------------\n")
	logger.Printf("Starting as runner with UUID %s\n", AppConfig.RunnerUuid)
	logger.Printf("Using %s for saving TF binaries\n", AppConfig.TfInstallDir)
	logger.Printf("Using %s as working directory\n", AppConfig.TfParentWorkingDir)
	logger.Printf("Configured timeout for TF commands is %d mins \n", AppConfig.TfCommandTimeoutMins)
	logger.Printf("Configured timeout for TF workspace operations is %d mins \n", AppConfig.WsTimeoutMins)
	logger.Printf("Configured timeout for TF init command is %d mins \n", AppConfig.InitTimeoutMins)
	logger.Printf("Connecting to meshfed-api at %s\n", AppConfig.RunApiBackend.Url)
	if AppConfig.SkipHostKeyValidation {
		logger.Printf("(!) Skipping host key validation is considered insecure and should not be used in production.")
	}
	logger.Printf("--------------------------------------------------------------------\n")
	return nil
}

// applyEnvVars applies environment variables with RUNNER_ prefix and sets defaults for unset values.
// Environment variables take precedence over config file values.
func applyEnvVars(logger *log.Logger) {
	if envUuid := os.Getenv(envRunnerUUID); envUuid != "" {
		logger.Printf("Using %s from environment: %s\n", envRunnerUUID, envUuid)
		AppConfig.RunnerUuid = envUuid
	}

	if envApiUrl := os.Getenv(envAPIURL); envApiUrl != "" {
		logger.Printf("Using %s from environment\n", envAPIURL)
		AppConfig.RunApiBackend.Url = envApiUrl
	}

	if envUsername := os.Getenv(envAuthUsername); envUsername != "" {
		logger.Printf("Using %s from environment\n", envAuthUsername)
		AppConfig.RunApiBackend.User = envUsername
	}

	if envPassword := os.Getenv(envAuthPassword); envPassword != "" {
		logger.Printf("Using %s from environment\n", envAuthPassword)
		AppConfig.RunApiBackend.Password = envPassword
	}

	if envClientId := os.Getenv(envAuthClientID); envClientId != "" {
		logger.Printf("Using %s from environment\n", envAuthClientID)
		AppConfig.RunApiBackend.ClientId = envClientId
	}

	if envClientSecret := os.Getenv(envAuthClientSecret); envClientSecret != "" {
		logger.Printf("Using %s from environment\n", envAuthClientSecret)
		AppConfig.RunApiBackend.ClientSecret = envClientSecret
	}

	if envPrivateKeyFile := os.Getenv(envPrivateKeyFile); envPrivateKeyFile != "" {
		logger.Printf("Using %s from environment\n", envPrivateKeyFile)
		AppConfig.PrivateKeyFile = envPrivateKeyFile
	} else if AppConfig.PrivateKeyFile == "" {
		// Use default private key file path if not configured via config file or env var
		AppConfig.PrivateKeyFile = defaultPrivateKeyFile
	}
}

// applyPrivateKeyFile loads the private key from path and sets cfg.PrivateKey.
// It is silently skipped when the file does not exist.
// Other read errors are logged as warnings but do not fail startup.
func applyPrivateKeyFile(path string, cfg *TfRunnerConfig, logger *log.Logger) {
	if path == "" {
		return
	}
	keyData, err := os.ReadFile(path)
	if err == nil {
		logger.Printf("Loaded private key from %s\n", path)
		cfg.PrivateKey = string(keyData)
	} else if !errors.Is(err, fs.ErrNotExist) {
		logger.Printf("Warning: could not read private key file %s: %v\n", path, err)
	}
}

// validateAuthConfig ensures proper authentication configuration.
// In Kubernetes mode (single-run with RUN_JSON_FILE_PATH), auth credentials are not required
// as the run is provided via file mounted from a K8S secret and contains a runToken.
// In polling mode, either basic auth (user+password) or API key auth (clientId+clientSecret) is required.
func validateAuthConfig(config TfRunnerConfig) error {
	hasCompleteBasicAuth := config.RunApiBackend.User != "" && config.RunApiBackend.Password != ""
	hasCompleteApiKeyAuth := config.RunApiBackend.ClientId != "" && config.RunApiBackend.ClientSecret != ""

	if hasCompleteBasicAuth && hasCompleteApiKeyAuth {
		return fmt.Errorf("ambiguous authentication configuration: both Basic auth (user/password) and API key auth (clientId/clientSecret) are set; configure only one method")
	}

	// Check if we're in single-run mode
	executionMode := os.Getenv(envExecutionMode)
	runJsonFilePath := os.Getenv(envRunJSONFilePath)
	isSingleRunMode := executionMode == "single-run"

	// In single-run mode, RUN_JSON_FILE_PATH is required
	if isSingleRunMode {
		if runJsonFilePath == "" {
			return fmt.Errorf("RUN_JSON_FILE_PATH environment variable is required in single-run mode")
		}
		// In single-run mode with RUN_JSON_FILE_PATH, auth credentials are not required
		return nil
	}

	if !hasCompleteBasicAuth && !hasCompleteApiKeyAuth {
		return fmt.Errorf("authentication required in polling mode: set either user+password (Basic auth) or clientId+clientSecret (API key auth)")
	}

	return nil
}

// validateRunnerUuid ensures that the runner UUID is configured and not empty
func validateRunnerUuid(config TfRunnerConfig) error {
	if config.RunnerUuid == "" {
		return fmt.Errorf("runnerUuid is required and must not be empty. Set it via RUNNER_UUID environment variable or runner-config.yml")
	}
	return nil
}
