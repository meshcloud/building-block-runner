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
	configFilename = "application.yml"

	FLAG_TFTIMEOUT        = "timeoutMins"
	FLAG_WSTIMEOUT        = "wsTimeoutMins"
	FLAG_INITTIMEOUT      = "initTimeoutMins"
	FLAG_INSTALLDIR       = "tfInstallDir"
	FLAG_WORKDIR          = "workingDir"
	FLAG_COORDINATOR_URL  = "api_url"
	FLAG_COORDINATOR_USER = "api_user"
	FLAG_COORDINATOR_PASS = "api_password"

	FLAG_COORDINATOR_CLIENT_ID     = "api_client_id"
	FLAG_COORDINATOR_CLIENT_SECRET = "api_client_secret"

	FLAG_INSECURE_HOST_KEYS = "insecure_hostkeys"
	FLAG_RUNNER_UUID        = "runnerUuid"
)

var (
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

	insecureHostKeys = flag.Bool(FLAG_INSECURE_HOST_KEYS, false, "If set to true, known host key validation is off.")
	runnerUuid       = flag.String(FLAG_RUNNER_UUID, "", "UUID of the building block runner to filter runs for")
)

func ReadConfig(logger *log.Logger) error {
	// read in and unmarshal config file (if present)
	if fileData, err := os.ReadFile(configFilename); errors.Is(err, fs.ErrNotExist) {
		logger.Printf("config file %s does not exist, will use defaults and environment", configFilename)
	} else if err != nil {
		return err
	} else if err := yaml.Unmarshal(fileData, &AppConfig); err != nil {
		return err
	}

	// parse program args into config struct as fallback
	flag.Parse()
	applyFlags()

	// apply environment variables (highest precedence)
	applyEnvVars(logger)

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
