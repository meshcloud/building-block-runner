package tf

import (
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/meshcloud/building-block-runner/internal/config"
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
		RunnerUuid:            c.Uuid,
		ApiBackendUrl:         c.Api.Url,
		SkipHostKeyValidation: c.SkipHostKeyValidation,
		TfCommandTimeout:      time.Duration(c.TfCommandTimeoutMins) * time.Minute,
		InitTimeout:           time.Duration(c.InitTimeoutMins) * time.Minute,
		WsTimeout:             time.Duration(c.WsTimeoutMins) * time.Minute,
	}
}

// Compiled-in execution-config defaults for the tf runner type. They mirror the superset
// constants in cmd/bbrunner/tf.go (supersetTf*), which now reference these so the two default
// sources cannot drift. Before this, the timeouts/dirs came ONLY from the shipped tf
// runner-config.yml; the shared fit runner-config.yml no longer carries them, so ReadConfig
// applies them here. All three timeouts MUST be non-zero: an unset TfCommandTimeout is a zero
// time.Duration, which context.WithTimeout treats as an already-expired deadline, failing every
// run at init (see the comment at cmd/bbrunner/tf.go).
const (
	DefaultTfCommandTimeoutMins = 60
	DefaultWsTimeoutMins        = 5
	DefaultInitTimeoutMins      = 3
	DefaultWorkingDir           = "/tmp/runner/wd"
	DefaultTfInstallDir         = "/tmp/runner/tfbin"
)

// DefaultShutdownGraceSeconds is the tf type's default SIGINT/SIGTERM drain window:
// how long dispatch.InProcess.Wait lets in-flight runs finish on their own before cancelling
// them and forcing a terminal ABORTED status. 30s matches a typical k8s
// terminationGracePeriodSeconds -- deliberately shorter than dispatch.DefaultShutdownGrace
// (120s), which is tuned for runner types whose handlers are not tf's potentially long-running
// tofu apply/destroy.
const DefaultShutdownGraceSeconds = 30

// TfRunnerConfig is the tf runner's runtime config: the dispatcher-owned shared section
// (config.BaseConfig -- uuid, api, maxConcurrentRuns) plus tf's type-specific execution knobs.
// tf now converges on the shared config machinery (config.Loader + config.ResolveBase) like the
// four HTTP types, decoding a separate tfFileConfig and building this value from it in ReadConfig.
type TfRunnerConfig struct {
	config.BaseConfig
	TfCommandTimeoutMins  int
	TfParentWorkingDir    string
	TfInstallDir          string
	SkipHostKeyValidation bool
	PrivateKey            string
	PrivateKeyFile        string
	WsTimeoutMins         int
	InitTimeoutMins       int
	// ShutdownGraceSeconds bounds the SIGINT/SIGTERM drain window before in-flight runs are
	// cancelled and reported ABORTED (default DefaultShutdownGraceSeconds; env
	// RUNNER_SHUTDOWN_GRACE, in seconds). Additive.
	ShutdownGraceSeconds int
	// Registration, when present, opts the standalone tf runner into a startup self-
	// registration PUT to the meshfed API. Absent (nil) => the runner never self-registers.
	Registration *TfRegistrationConfig
}

// tfFileConfig is the yaml surface ReadConfig decodes: the shared BaseFileConfig
// (uuid/api/maxConcurrentRuns + the Kotlin-era `blockrunner:` compat block, embedded inline) plus
// tf's type-specific keys. tf has no `version:` key (it stamps build.Version).
type tfFileConfig struct {
	config.BaseFileConfig `yaml:",inline"`
	TfCommandTimeoutMins  int                   `yaml:"timeoutMins"`
	TfParentWorkingDir    string                `yaml:"workingDir"`
	TfInstallDir          string                `yaml:"tfInstallDir"`
	SkipHostKeyValidation bool                  `yaml:"insecureHostKeys"`
	PrivateKey            string                `yaml:"privateKey"`
	PrivateKeyFile        string                `yaml:"privateKeyFile"`
	WsTimeoutMins         int                   `yaml:"wsTimeoutMins"`
	InitTimeoutMins       int                   `yaml:"initTimeoutMins"`
	ShutdownGraceSeconds  int                   `yaml:"shutdownGraceSeconds"`
	Registration          *TfRegistrationConfig `yaml:"registration"`
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

const (
	defaultConfigFile     = "runner-config.yml"
	defaultPrivateKeyFile = "runner-private.pem"

	envConfigFile     = "RUNNER_CONFIG_FILE"
	envPrivateKeyFile = "RUNNER_PRIVATE_KEY_FILE"
	envShutdownGrace  = "RUNNER_SHUTDOWN_GRACE"
)

// ReadConfig loads the tf runner configuration from the config file (if present) overlaid by the
// RUNNER_* environment variables, validates it, and returns it. It converges on the shared config
// machinery like the four HTTP types: seed DefaultBaseFileConfig() + tf's compiled-in defaults,
// decode one fileConfig, then config.ResolveBase applies the shared uuid/api/maxConcurrentRuns
// precedence (blockrunner: block < RUNNER_* env) on the same loader (so FailOnUnconsumedLegacyEnv
// stays unified). Standing credentials are not gated: like the other four fit types, tf applies
// the compiled-in dev-local API defaults so a poll-mode dispatcher boots zero-config against the
// local meshfed-API; real deployments override them via env.
func ReadConfig(logger *slog.Logger) (TfRunnerConfig, error) {
	fc := tfFileConfig{
		BaseFileConfig:       config.DefaultBaseFileConfig(),
		TfCommandTimeoutMins: DefaultTfCommandTimeoutMins,
		WsTimeoutMins:        DefaultWsTimeoutMins,
		InitTimeoutMins:      DefaultInitTimeoutMins,
		TfParentWorkingDir:   DefaultWorkingDir,
		TfInstallDir:         DefaultTfInstallDir,
		ShutdownGraceSeconds: DefaultShutdownGraceSeconds,
	}

	loader := config.NewLoader()
	configPath := loader.Path(logger, defaultConfigFile, config.EnvAlias{Var: envConfigFile})
	if found, err := loader.Load(configPath, &fc); err != nil {
		return TfRunnerConfig{}, err
	} else if !found {
		logger.Info("config file does not exist, will use defaults and environment", "path", configPath)
	}
	loader.WarnIgnoredLegacyYAMLBlocks(logger)

	// tf has no version key, so ApplyShared (uuid/api) is fully handled inside ResolveBase.
	// The tf-only RUNNER_PRIVATE_KEY_FILE binding wins last; the shared RUNNER_* bindings +
	// concurrency are applied by ResolveBase on this same loader.
	loader.Env(logger,
		config.EnvBinding{Var: envPrivateKeyFile, Target: &fc.PrivateKeyFile},
	)
	base, err := config.ResolveBase(logger, loader, &fc.BaseFileConfig)
	if err != nil {
		return TfRunnerConfig{}, err
	}

	// RUNNER_SHUTDOWN_GRACE is a tf-specific int env; loader.Env binds strings only, so it is
	// read here. Invalid values warn-and-ignore (unlike RUNNER_MAX_CONCURRENT_RUNS, which is a
	// hard error via ResolveBase) -- the historic tf behavior.
	if v := os.Getenv(envShutdownGrace); v != "" {
		if n, err := strconv.Atoi(v); err != nil {
			logger.Warn("ignoring invalid RUNNER_SHUTDOWN_GRACE (not an integer number of seconds)", "value", v, "error", err)
		} else {
			logger.Info("using value from environment", "var", envShutdownGrace)
			fc.ShutdownGraceSeconds = n
		}
	}

	if err := loader.FailOnUnconsumedLegacyEnv("BLOCKRUNNER_"); err != nil {
		return TfRunnerConfig{}, err
	}

	cfg := TfRunnerConfig{
		BaseConfig:            base,
		TfCommandTimeoutMins:  fc.TfCommandTimeoutMins,
		TfParentWorkingDir:    fc.TfParentWorkingDir,
		TfInstallDir:          fc.TfInstallDir,
		SkipHostKeyValidation: fc.SkipHostKeyValidation,
		PrivateKey:            fc.PrivateKey,
		PrivateKeyFile:        fc.PrivateKeyFile,
		WsTimeoutMins:         fc.WsTimeoutMins,
		InitTimeoutMins:       fc.InitTimeoutMins,
		ShutdownGraceSeconds:  fc.ShutdownGraceSeconds,
		Registration:          fc.Registration,
	}

	// Load the private key from the configured file path (highest priority), falling back to the
	// default ./runner-private.pem and then the inline privateKey from the file. tf keeps its own
	// relative default path (not config.ResolvePrivateKey's /app/runner-private.pem).
	if cfg.PrivateKeyFile == "" {
		cfg.PrivateKeyFile = defaultPrivateKeyFile
	}
	applyPrivateKeyFile(cfg.PrivateKeyFile, &cfg, logger)

	// API key auth wins when both methods are configured (e.g. an env-supplied client id/secret on
	// top of a basic-auth default baked into runner-config.yml). Surface that so it's not surprising.
	if cfg.Api.ClientId != "" && cfg.Api.ClientSecret != "" &&
		(cfg.Api.Username != "" || cfg.Api.Password != "") {
		logger.Info("Both API key and Basic auth are configured; using API key auth and ignoring Basic auth (username/password)")
	}

	if err := validateRunnerUuid(cfg); err != nil {
		return TfRunnerConfig{}, err
	}

	logger.Info("Starting as runner",
		"uuid", cfg.Uuid,
		"tfInstallDir", cfg.TfInstallDir,
		"workingDir", cfg.TfParentWorkingDir,
		"tfCommandTimeoutMins", cfg.TfCommandTimeoutMins,
		"wsTimeoutMins", cfg.WsTimeoutMins,
		"initTimeoutMins", cfg.InitTimeoutMins,
		"meshfedApiUrl", cfg.Api.Url,
	)
	if cfg.SkipHostKeyValidation {
		logger.Warn("Skipping host key validation is considered insecure and should not be used in production.")
	}
	return cfg, nil
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

// validateRunnerUuid ensures that the runner UUID is configured and not empty.
func validateRunnerUuid(cfg TfRunnerConfig) error {
	if cfg.Uuid == "" {
		return errors.New("uuid is required and must not be empty. Set it via RUNNER_UUID environment variable or runner-config.yml")
	}
	return nil
}
