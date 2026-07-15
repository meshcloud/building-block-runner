package tf

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/meshcloud/building-block-runner/internal/config"
	meshapi "github.com/meshcloud/building-block-runner/internal/meshapi"
)

// execConfig carries the tf runner configuration that the run-execution path reads
// (worker/single-run/handler -> tfcmd/gitsource/authSsh), threaded explicitly in place of the
// former mutable package-level AppConfig global. It is a read-only value taken
// once at wiring time, so it is safe to copy and share across concurrent in-process runs.
type execConfig struct {
	RunnerUuid            string
	ApiBackendUrl         string
	SkipHostKeyValidation bool
	TfCommandTimeout      time.Duration
	InitTimeout           time.Duration
	WsTimeout             time.Duration
}

// exec derives the execution-path config subset from a full TfRunnerConfig. This is the single
// yaml boundary where the int-minute yaml fields are converted to time.Duration; everything
// downstream (execConfig -> TfCmdParams -> context.WithTimeout) carries the Duration as-is.
func (c TfRunnerConfig) exec() execConfig {
	return execConfig{
		RunnerUuid:            c.RunnerUuid,
		ApiBackendUrl:         c.RunApiBackend.Url,
		SkipHostKeyValidation: c.SkipHostKeyValidation,
		TfCommandTimeout:      time.Duration(c.TfCommandTimeoutMins) * time.Minute,
		InitTimeout:           time.Duration(c.InitTimeoutMins) * time.Minute,
		WsTimeout:             time.Duration(c.WsTimeoutMins) * time.Minute,
	}
}

// DefaultMaxConcurrentRuns is the tf type's in-process concurrency default on the
// dispatch.Loop path (now the only path): up to 3 concurrent runs, an intentional
// throughput improvement over the former single serial worker. Setting maxConcurrentRuns=1
// reproduces the exact historic serial cadence; a negative value means unlimited (bounded by
// the loop's per-cycle backstop).
const DefaultMaxConcurrentRuns = 3

// DefaultShutdownGraceSeconds is the tf type's default SIGINT/SIGTERM drain window:
// how long dispatch.InProcess.Wait lets in-flight runs finish on their own before cancelling
// them and forcing a terminal ABORTED status. 30s matches a typical k8s
// terminationGracePeriodSeconds -- deliberately shorter than dispatch.DefaultShutdownGrace
// (120s), which is tuned for runner types whose handlers are not tf's potentially long-running
// tofu apply/destroy.
const DefaultShutdownGraceSeconds = 30

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
	// MaxConcurrentRuns caps concurrent in-process runs on the dispatch.Loop path (default
	// DefaultMaxConcurrentRuns via ReadConfig; env RUNNER_MAX_CONCURRENT_RUNS). Additive.
	MaxConcurrentRuns int `yaml:"maxConcurrentRuns"`
	// ShutdownGraceSeconds bounds the SIGINT/SIGTERM drain window before in-flight runs are
	// cancelled and reported ABORTED (default DefaultShutdownGraceSeconds via ReadConfig; env
	// RUNNER_SHUTDOWN_GRACE, also in seconds). Additive.
	ShutdownGraceSeconds int `yaml:"shutdownGraceSeconds"`
	// Registration, when present, opts the standalone tf runner into a startup self-
	// registration PUT to the meshfed API (no WIF -- the standalone has no projected tokens).
	// Absent (nil) => the runner never self-registers, exactly as today. Additive.
	Registration *TfRegistrationConfig `yaml:"registration"`
}

// TfRegistrationConfig is the opt-in tf `registration:` section. It carries
// the same keys the controller yaml uses (displayName, ownedByWorkspace, publicKey) plus the
// runner capability. A standalone tf runner still requires a pre-created runner object in
// meshfed (the PUT returns 404 otherwise -- the frozen "create it via the meshStack UI"
// contract).
type TfRegistrationConfig struct {
	DisplayName      string `yaml:"displayName"`
	OwnedByWorkspace string `yaml:"ownedByWorkspace"`
	PublicKey        string `yaml:"publicKey"`
	// Capability is the runner's registered implementation type: a concrete type or ALL,
	// validated at startup via dispatch.ParseCapability.
	Capability string `yaml:"capability"`
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

	envConfigFile        = "RUNNER_CONFIG_FILE"
	envRunnerUuid        = "RUNNER_UUID"
	envApiUrl            = "RUNNER_API_URL"
	envAuthUsername      = "RUNNER_API_USERNAME"
	envAuthPassword      = "RUNNER_API_PASSWORD"
	envAuthClientId      = "RUNNER_API_CLIENT_ID"
	envAuthClientSecret  = "RUNNER_API_CLIENT_SECRET"
	envPrivateKeyFile    = "RUNNER_PRIVATE_KEY_FILE"
	envExecutionMode     = "EXECUTION_MODE"
	envRunJsonFilePath   = "RUN_JSON_FILE_PATH"
	envMaxConcurrentRuns = "RUNNER_MAX_CONCURRENT_RUNS"
	envShutdownGrace     = "RUNNER_SHUTDOWN_GRACE"
)

// ReadConfig loads the tf runner configuration from the config file (if present) overlaid by the
// RUNNER_* environment variables, validates it, and returns it. It no longer mutates a package
// global: callers thread the returned value down the worker/handler paths.
func ReadConfig(logger *slog.Logger) (TfRunnerConfig, error) {
	var cfg TfRunnerConfig

	loader := config.NewLoader()
	configPath := loader.Path(logger, defaultConfigFile, config.EnvAlias{Var: envConfigFile})
	if found, err := loader.Load(configPath, "", &cfg); err != nil {
		return TfRunnerConfig{}, err
	} else if !found {
		logger.Info("config file does not exist, will use defaults and environment", "path", configPath)
	}

	// Default the in-process concurrency (before env, so an explicit env/file value wins).
	// Zero means "unset" -> the default; operators set it explicitly (incl. 1 for the
	// historic serial cadence, or a negative value for unlimited).
	if cfg.MaxConcurrentRuns == 0 {
		cfg.MaxConcurrentRuns = DefaultMaxConcurrentRuns
	}

	// Same "zero means unset" defaulting as MaxConcurrentRuns above.
	if cfg.ShutdownGraceSeconds == 0 {
		cfg.ShutdownGraceSeconds = DefaultShutdownGraceSeconds
	}

	// apply environment variables (highest precedence)
	applyEnvVars(&cfg, logger)

	// Try to load the private key from the configured file path (highest priority).
	// Uses RUNNER_PRIVATE_KEY_FILE env var path if set, otherwise the default ./runner-private.pem.
	// Falls back to privateKey from runner-config.yml if the file is not found.
	applyPrivateKeyFile(cfg.PrivateKeyFile, &cfg, logger)

	// validate authentication configuration
	if err := validateAuthConfig(cfg); err != nil {
		return TfRunnerConfig{}, err
	}

	// API key auth wins when both methods are configured (e.g. an env-supplied client id/secret on
	// top of a basic-auth default baked into runner-config.yml). Surface that so it's not surprising.
	if cfg.RunApiBackend.ClientId != "" && cfg.RunApiBackend.ClientSecret != "" &&
		(cfg.RunApiBackend.User != "" || cfg.RunApiBackend.Password != "") {
		logger.Info("Both API key and Basic auth are configured; using API key auth and ignoring Basic auth (user/password)")
	}

	// validate RunnerUuid is set
	if err := validateRunnerUuid(cfg); err != nil {
		return TfRunnerConfig{}, err
	}

	logger.Info("Starting as runner",
		"uuid", cfg.RunnerUuid,
		"tfInstallDir", cfg.TfInstallDir,
		"workingDir", cfg.TfParentWorkingDir,
		"tfCommandTimeoutMins", cfg.TfCommandTimeoutMins,
		"wsTimeoutMins", cfg.WsTimeoutMins,
		"initTimeoutMins", cfg.InitTimeoutMins,
		"meshfedApiUrl", cfg.RunApiBackend.Url,
	)
	if cfg.SkipHostKeyValidation {
		logger.Warn("Skipping host key validation is considered insecure and should not be used in production.")
	}
	return cfg, nil
}

// applyEnvVars applies environment variables with RUNNER_ prefix and sets defaults for unset values.
// Environment variables take precedence over config file values.
func applyEnvVars(cfg *TfRunnerConfig, logger *slog.Logger) {
	if envUuid := os.Getenv(envRunnerUuid); envUuid != "" {
		logger.Info("Using value from environment", "var", envRunnerUuid)
		cfg.RunnerUuid = envUuid
	}

	if apiUrl := os.Getenv(envApiUrl); apiUrl != "" {
		logger.Info("Using value from environment", "var", envApiUrl)
		cfg.RunApiBackend.Url = apiUrl
	}

	if username := os.Getenv(envAuthUsername); username != "" {
		logger.Info("Using value from environment", "var", envAuthUsername)
		cfg.RunApiBackend.User = username
	}

	if password := os.Getenv(envAuthPassword); password != "" {
		logger.Info("Using value from environment", "var", envAuthPassword)
		cfg.RunApiBackend.Password = password
	}

	if clientId := os.Getenv(envAuthClientId); clientId != "" {
		logger.Info("Using value from environment", "var", envAuthClientId)
		cfg.RunApiBackend.ClientId = clientId
	}

	if clientSecret := os.Getenv(envAuthClientSecret); clientSecret != "" {
		logger.Info("Using value from environment", "var", envAuthClientSecret)
		cfg.RunApiBackend.ClientSecret = clientSecret
	}

	if v := os.Getenv(envMaxConcurrentRuns); v != "" {
		if n, err := strconv.Atoi(v); err != nil {
			logger.Warn("ignoring invalid RUNNER_MAX_CONCURRENT_RUNS (not an integer)", "value", v, "error", err)
		} else {
			logger.Info("Using value from environment", "var", envMaxConcurrentRuns)
			cfg.MaxConcurrentRuns = n
		}
	}

	if v := os.Getenv(envShutdownGrace); v != "" {
		if n, err := strconv.Atoi(v); err != nil {
			logger.Warn("ignoring invalid RUNNER_SHUTDOWN_GRACE (not an integer number of seconds)", "value", v, "error", err)
		} else {
			logger.Info("Using value from environment", "var", envShutdownGrace)
			cfg.ShutdownGraceSeconds = n
		}
	}

	if privateKeyFile := os.Getenv(envPrivateKeyFile); privateKeyFile != "" {
		logger.Info("Using value from environment", "var", envPrivateKeyFile)
		cfg.PrivateKeyFile = privateKeyFile
	} else if cfg.PrivateKeyFile == "" {
		// Use default private key file path if not configured via config file or env var
		cfg.PrivateKeyFile = defaultPrivateKeyFile
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
