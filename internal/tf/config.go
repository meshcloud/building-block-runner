package tf

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"

	"gopkg.in/yaml.v2"

	meshapi "github.com/meshcloud/building-block-runner/internal/meshapi"
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
	defaultPrivateKeyFile = "runner-private.pem"

	envConfigFile       = "RUNNER_CONFIG_FILE"
	envRunnerUuid       = "RUNNER_UUID"
	envApiUrl           = "RUNNER_API_URL"
	envAuthUsername     = "RUNNER_API_USERNAME"
	envAuthPassword     = "RUNNER_API_PASSWORD"
	envAuthClientId     = "RUNNER_API_CLIENT_ID"
	envAuthClientSecret = "RUNNER_API_CLIENT_SECRET"
	envPrivateKeyFile   = "RUNNER_PRIVATE_KEY_FILE"
	envExecutionMode    = "EXECUTION_MODE"
	envRunJsonFilePath  = "RUN_JSON_FILE_PATH"
)

func ReadConfig(logger *slog.Logger) error {
	// read configFile path from env var or use default
	configPath := os.Getenv(envConfigFile)
	if configPath == "" {
		configPath = defaultConfigFile
	}

	// read in and unmarshal config file (if present)
	if fileData, err := os.ReadFile(configPath); errors.Is(err, fs.ErrNotExist) {
		logger.Info("config file does not exist, will use defaults and environment", "path", configPath)
	} else if err != nil {
		return err
	} else if err := yaml.Unmarshal(fileData, &AppConfig); err != nil {
		return err
	}

	// apply environment variables (highest precedence)
	applyEnvVars(logger)

	// Try to load the private key from the configured file path (highest priority).
	// Uses RUNNER_PRIVATE_KEY_FILE env var path if set, otherwise the default ./runner-private.pem.
	// Falls back to privateKey from runner-config.yml if the file is not found.
	applyPrivateKeyFile(AppConfig.PrivateKeyFile, &AppConfig, logger)

	// validate authentication configuration
	if err := validateAuthConfig(AppConfig); err != nil {
		return err
	}

	// API key auth wins when both methods are configured (e.g. an env-supplied client id/secret on
	// top of a basic-auth default baked into runner-config.yml). Surface that so it's not surprising.
	if AppConfig.RunApiBackend.ClientId != "" && AppConfig.RunApiBackend.ClientSecret != "" &&
		(AppConfig.RunApiBackend.User != "" || AppConfig.RunApiBackend.Password != "") {
		logger.Info("Both API key and Basic auth are configured; using API key auth and ignoring Basic auth (user/password)")
	}

	// validate RunnerUuid is set
	if err := validateRunnerUuid(AppConfig); err != nil {
		return err
	}

	logger.Info("Starting as runner",
		"uuid", AppConfig.RunnerUuid,
		"tfInstallDir", AppConfig.TfInstallDir,
		"workingDir", AppConfig.TfParentWorkingDir,
		"tfCommandTimeoutMins", AppConfig.TfCommandTimeoutMins,
		"wsTimeoutMins", AppConfig.WsTimeoutMins,
		"initTimeoutMins", AppConfig.InitTimeoutMins,
		"meshfedApiUrl", AppConfig.RunApiBackend.Url,
	)
	if AppConfig.SkipHostKeyValidation {
		logger.Warn("Skipping host key validation is considered insecure and should not be used in production.")
	}
	return nil
}

// applyEnvVars applies environment variables with RUNNER_ prefix and sets defaults for unset values.
// Environment variables take precedence over config file values.
func applyEnvVars(logger *slog.Logger) {
	if envUuid := os.Getenv(envRunnerUuid); envUuid != "" {
		logger.Info("Using value from environment", "var", envRunnerUuid)
		AppConfig.RunnerUuid = envUuid
	}

	if apiUrl := os.Getenv(envApiUrl); apiUrl != "" {
		logger.Info("Using value from environment", "var", envApiUrl)
		AppConfig.RunApiBackend.Url = apiUrl
	}

	if username := os.Getenv(envAuthUsername); username != "" {
		logger.Info("Using value from environment", "var", envAuthUsername)
		AppConfig.RunApiBackend.User = username
	}

	if password := os.Getenv(envAuthPassword); password != "" {
		logger.Info("Using value from environment", "var", envAuthPassword)
		AppConfig.RunApiBackend.Password = password
	}

	if clientId := os.Getenv(envAuthClientId); clientId != "" {
		logger.Info("Using value from environment", "var", envAuthClientId)
		AppConfig.RunApiBackend.ClientId = clientId
	}

	if clientSecret := os.Getenv(envAuthClientSecret); clientSecret != "" {
		logger.Info("Using value from environment", "var", envAuthClientSecret)
		AppConfig.RunApiBackend.ClientSecret = clientSecret
	}

	if privateKeyFile := os.Getenv(envPrivateKeyFile); privateKeyFile != "" {
		logger.Info("Using value from environment", "var", envPrivateKeyFile)
		AppConfig.PrivateKeyFile = privateKeyFile
	} else if AppConfig.PrivateKeyFile == "" {
		// Use default private key file path if not configured via config file or env var
		AppConfig.PrivateKeyFile = defaultPrivateKeyFile
	}
}

// applyPrivateKeyFile loads the private key from path and sets cfg.PrivateKey.
// It is silently skipped when the file does not exist.
// Other read errors are logged as warnings but do not fail startup.
func applyPrivateKeyFile(path string, cfg *TfRunnerConfig, logger *slog.Logger) {
	if path == "" {
		return
	}
	keyData, err := os.ReadFile(path)
	if err == nil {
		logger.Info("Loaded private key", "path", path)
		cfg.PrivateKey = string(keyData)
	} else if !errors.Is(err, fs.ErrNotExist) {
		logger.Warn("could not read private key file", "path", path, "error", err)
	}
}

// validateAuthConfig ensures proper authentication configuration.
// In Kubernetes mode (single-run with RUN_JSON_FILE_PATH), auth credentials are not required
// as the run is provided via file mounted from a K8S secret and contains a runToken.
// In polling mode, either basic auth (user+password) or API key auth (clientId+clientSecret) is required.
//
// When both methods are fully configured, API key auth takes precedence (see
// RunApiConfig.NewAuthProvider) — that is a valid configuration, not an error. This lets API key
// credentials supplied via the environment override a basic-auth default baked into runner-config.yml.
func validateAuthConfig(config TfRunnerConfig) error {
	hasCompleteBasicAuth := config.RunApiBackend.User != "" && config.RunApiBackend.Password != ""
	hasCompleteApiKeyAuth := config.RunApiBackend.ClientId != "" && config.RunApiBackend.ClientSecret != ""

	// Check if we're in single-run mode
	executionMode := os.Getenv(envExecutionMode)
	runJsonFilePath := os.Getenv(envRunJsonFilePath)
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

// validateRunnerUuid ensures that the runner UUID is configured and not empty.
func validateRunnerUuid(config TfRunnerConfig) error {
	if config.RunnerUuid == "" {
		return fmt.Errorf("runnerUuid is required and must not be empty. Set it via RUNNER_UUID environment variable or runner-config.yml")
	}
	return nil
}
