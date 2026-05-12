package tfrun

import (
	"errors"
	"flag"
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
	configFilename = "runner-config.yml"

	// privateKeyFile is the hardcoded path where the private key file is expected to be mounted
	// in the Docker container (e.g. as a Kubernetes secret volume mount).
	privateKeyFile = "/app/private.key"

	FLAG_CONFIG = "config"

	FLAG_TFTIMEOUT        = "timeoutMins"
	FLAG_WSTIMEOUT        = "wsTimeoutMins"
	FLAG_INITTIMEOUT      = "initTimeoutMins"
	FLAG_INSTALLDIR       = "tfInstallDir"
	FLAG_WORKDIR          = "workingDir"
	FLAG_COORDINATOR_URL  = "apiUrl"
	FLAG_COORDINATOR_USER = "apiUser"
	FLAG_COORDINATOR_PASS = "apiPassword"

	FLAG_COORDINATOR_CLIENT_ID     = "apiClientId"
	FLAG_COORDINATOR_CLIENT_SECRET = "apiClientSecret"

	FLAG_INSECURE_HOST_KEYS = "insecureHostKeys"
	FLAG_RUNNER_UUID        = "runnerUuid"
	FLAG_PRIVATE_KEY_FILE   = "privateKeyFile"
)

var (
	configFile      = flag.String(FLAG_CONFIG, configFilename, "path to the YAML configuration file")
	timeoutMins     = flag.Int(FLAG_TFTIMEOUT, 60, "Terraform command timeout in minutes")
	wsTimeoutMins   = flag.Int(FLAG_WSTIMEOUT, 5, "Terraform workspace operations timeout in minutes")
	initTimeoutMins = flag.Int(FLAG_INITTIMEOUT, 3, "Terraform init command timeout in minutes")

	tfInstallDir = flag.String(FLAG_INSTALLDIR, "/tmp/runner/tfbin", "Terraform binaries install directory")
	tfWorkingDir = flag.String(FLAG_WORKDIR, "/tmp/runner/wd", "Parent directory for all workers")
	apiUrl       = flag.String(FLAG_COORDINATOR_URL, "http://localhost:8080", "Block coordinator URL")
	apiUser      = flag.String(FLAG_COORDINATOR_USER, "", "Basic Authentication user to authenticate towards Block Coordinator API")
	apiPassword  = flag.String(FLAG_COORDINATOR_PASS, "", "Basic Authentication password to authenticate towards Block Coordinator API")

	apiClientId     = flag.String(FLAG_COORDINATOR_CLIENT_ID, "", "API key client ID to authenticate towards Block Coordinator API")
	apiClientSecret = flag.String(FLAG_COORDINATOR_CLIENT_SECRET, "", "API key client secret to authenticate towards Block Coordinator API")

	insecureHostKeys   = flag.Bool(FLAG_INSECURE_HOST_KEYS, false, "If set to true, known host key validation is off.")
	runnerUuid         = flag.String(FLAG_RUNNER_UUID, "", "UUID of the building block runner to filter runs for")
	privateKeyFilePath = flag.String(FLAG_PRIVATE_KEY_FILE, privateKeyFile, "Path to the private SSH key file to load")
)

func ReadConfig(logger *log.Logger) error {
	// Parse flags first so --config can override the default path.
	flag.Parse()

	// read in and unmarshal config file (if present)
	if fileData, err := os.ReadFile(*configFile); errors.Is(err, fs.ErrNotExist) {
		logger.Printf("config file %s does not exist, will use defaults and environment", *configFile)
	} else if err != nil {
		return err
	} else if err := yaml.Unmarshal(fileData, &AppConfig); err != nil {
		return err
	}

	// parse program args into config struct as fallback
	applyFlags()

	// apply environment variables (highest precedence)
	applyEnvVars(logger)

	// Try to load the private key from the configured file path (highest priority).
	// Uses BLOCKRUNNER_PRIVATE_KEY_FILE env var path if set, otherwise the default /app/private.key.
	// Falls back to privateKey from runner-config.yml or BLOCKRUNNER_PRIVATEKEY env variable if the file is not found.
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

// We apply flags with precendece over file config, but only of the flag was actively set.
// flags' default values are only applied, if the config value would be null otherwise.
func applyFlags() {
	isFlagSet := func(flagName string) (isSet bool) {
		flag.Visit(func(flag *flag.Flag) {
			if flag.Name == flagName {
				isSet = true
			}
		})
		return
	}

	if isFlagSet(FLAG_TFTIMEOUT) || AppConfig.TfCommandTimeoutMins == 0 {
		AppConfig.TfCommandTimeoutMins = *timeoutMins
	}

	if isFlagSet(FLAG_WSTIMEOUT) || AppConfig.WsTimeoutMins == 0 {
		AppConfig.WsTimeoutMins = *wsTimeoutMins
	}

	if isFlagSet(FLAG_INITTIMEOUT) || AppConfig.InitTimeoutMins == 0 {
		AppConfig.InitTimeoutMins = *initTimeoutMins
	}

	if isFlagSet(FLAG_INSTALLDIR) || AppConfig.TfInstallDir == "" {
		AppConfig.TfInstallDir = *tfInstallDir
	}

	if isFlagSet(FLAG_WORKDIR) || AppConfig.TfParentWorkingDir == "" {
		AppConfig.TfParentWorkingDir = *tfWorkingDir
	}

	if isFlagSet(FLAG_COORDINATOR_URL) || AppConfig.RunApiBackend.Url == "" {
		AppConfig.RunApiBackend.Url = *apiUrl
	}

	if isFlagSet(FLAG_COORDINATOR_USER) || AppConfig.RunApiBackend.User == "" {
		AppConfig.RunApiBackend.User = *apiUser
	}

	if isFlagSet(FLAG_COORDINATOR_PASS) || AppConfig.RunApiBackend.Password == "" {
		AppConfig.RunApiBackend.Password = *apiPassword
	}

	if isFlagSet(FLAG_COORDINATOR_CLIENT_ID) || AppConfig.RunApiBackend.ClientId == "" {
		AppConfig.RunApiBackend.ClientId = *apiClientId
	}

	if isFlagSet(FLAG_COORDINATOR_CLIENT_SECRET) || AppConfig.RunApiBackend.ClientSecret == "" {
		AppConfig.RunApiBackend.ClientSecret = *apiClientSecret
	}

	if isFlagSet(FLAG_INSECURE_HOST_KEYS) {
		AppConfig.SkipHostKeyValidation = *insecureHostKeys
	}

	if isFlagSet(FLAG_RUNNER_UUID) || AppConfig.RunnerUuid == "" {
		AppConfig.RunnerUuid = *runnerUuid
	}

	if isFlagSet(FLAG_PRIVATE_KEY_FILE) || AppConfig.PrivateKeyFile == "" {
		AppConfig.PrivateKeyFile = *privateKeyFilePath
	}
}

// applyEnvVars applies environment variables with BLOCKRUNNER_ prefix
// Environment variables take precedence over all other configuration sources
func applyEnvVars(logger *log.Logger) {
	if envUuid := os.Getenv("BLOCKRUNNER_UUID"); envUuid != "" {
		logger.Printf("Using BLOCKRUNNER_UUID from environment: %s\n", envUuid)
		AppConfig.RunnerUuid = envUuid
	}

	if envApiUrl := os.Getenv("BLOCKRUNNER_API_URL"); envApiUrl != "" {
		logger.Printf("Using BLOCKRUNNER_API_URL from environment\n")
		AppConfig.RunApiBackend.Url = envApiUrl
	}

	if envUsername := os.Getenv("BLOCKRUNNER_AUTH_USERNAME"); envUsername != "" {
		logger.Printf("Using BLOCKRUNNER_AUTH_USERNAME from environment\n")
		AppConfig.RunApiBackend.User = envUsername
	}

	if envPassword := os.Getenv("BLOCKRUNNER_AUTH_PASSWORD"); envPassword != "" {
		logger.Printf("Using BLOCKRUNNER_AUTH_PASSWORD from environment\n")
		AppConfig.RunApiBackend.Password = envPassword
	}

	if envClientId := os.Getenv("BLOCKRUNNER_AUTH_CLIENT_ID"); envClientId != "" {
		logger.Printf("Using BLOCKRUNNER_AUTH_CLIENT_ID from environment\n")
		AppConfig.RunApiBackend.ClientId = envClientId
	}

	if envClientSecret := os.Getenv("BLOCKRUNNER_AUTH_CLIENT_SECRET"); envClientSecret != "" {
		logger.Printf("Using BLOCKRUNNER_AUTH_CLIENT_SECRET from environment\n")
		AppConfig.RunApiBackend.ClientSecret = envClientSecret
	}

	if envPrivateKey := os.Getenv("BLOCKRUNNER_PRIVATEKEY"); envPrivateKey != "" {
		logger.Printf("Using BLOCKRUNNER_PRIVATEKEY from environment\n")
		AppConfig.PrivateKey = envPrivateKey
	}

	if envPrivateKeyFile := os.Getenv("BLOCKRUNNER_PRIVATE_KEY_FILE"); envPrivateKeyFile != "" {
		logger.Printf("Using BLOCKRUNNER_PRIVATE_KEY_FILE from environment\n")
		AppConfig.PrivateKeyFile = envPrivateKeyFile
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
	executionMode := os.Getenv("EXECUTION_MODE")
	runJsonFilePath := os.Getenv("RUN_JSON_FILE_PATH")
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
		return fmt.Errorf("runnerUuid is required and must not be empty. Set it via configuration file or --runnerUuid flag")
	}
	return nil
}
