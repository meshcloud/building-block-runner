package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
)

// defaultControllerConfigFile is the controller type's per-impl yaml file name (no shared
// base layer: every controller field is either registration/crypto/api or Kubernetes-facing,
// none of it shared with the fit runner types' base runner-config.yml).
const defaultControllerConfigFile = "runner-config.yml"

// envShutdownGrace overrides the in-process superset drain window (seconds). Same spelling
// and units the fit runner types honor (internal/tf's config), so one knob tunes the drain
// grace whichever way a type is run.
const envShutdownGrace = "RUNNER_SHUTDOWN_GRACE"

// Crypto holds the controller's registration key pair: PublicKey lets meshStack encrypt
// secrets addressed to this runner, PrivateKey decrypts them back.
type Crypto struct {
	PublicKey  string `yaml:"publicKey"`
	PrivateKey string `yaml:"privateKey"`
}

// ControllerConfig is the run-controller type's full configuration -- the dissolution
// target of the former cmd/bbrunner controllerConfig: the Kubernetes-facing fields
// (namespace, job templates, tolerations, node selector, image pull secrets) live in
// K8sJobConfig (jobtemplate.go) and are embedded inline so every existing yaml key parses byte-identically;
// registration/crypto/api fields and the polling/capacity knobs stay here because they are
// the runner type's own wiring concern.
type ControllerConfig struct {
	K8sJobConfig           K8sJobConfig      `yaml:",inline"`
	Api                    Api               `yaml:"api"`
	Crypto                 Crypto            `yaml:"crypto"`
	Uuid                   string            `yaml:"uuid"`
	OwnedByWorkspace       string            `yaml:"ownedByWorkspace"`
	DisplayName            string            `yaml:"displayName"`
	PollingIntervalSeconds int               `yaml:"pollingIntervalSeconds"`
	MaxConcurrentJobs      int               `yaml:"maxConcurrentJobs"`
	ShutdownGraceSeconds   int               `yaml:"shutdownGraceSeconds"`
	BlockRunner            BlockRunnerCompat `yaml:"blockrunner"`
}

// LoadController assembles the controller type's config: compiled-in defaults < per-impl
// YAML < the `blockrunner:` compat block < env (RUNNER_API_* and RUNNER_SHUTDOWN_GRACE),
// mirroring the fit runner types' precedence chain (see gitlab's LoadConfig). It never
// exits the process -- validation failures are returned so the caller decides how to fail.
func LoadController(log *slog.Logger) (*ControllerConfig, error) {
	var cfg ControllerConfig

	loader := NewLoader()
	path := loader.Path(log, defaultControllerConfigFile,
		EnvAlias{Var: "RUNNER_CONFIG_FILE"},
		EnvAlias{Var: "RUNCONTROLLER_CONFIG_FILE", Deprecated: true},
	)
	if _, err := loader.Load(path, &cfg); err != nil {
		return nil, err
	}
	loader.WarnIgnoredLegacyYAMLBlocks(log)

	// blockrunner: block overrides the flat keys (a mounted Kotlin-era file fully
	// configures the runner type); the controller has no version field, so nil is passed
	// (ApplyShared no-ops a nil target).
	cfg.BlockRunner.ApplyShared(log, &cfg.Uuid, nil, &cfg.Api)

	// A zero value means "not configured"; a negative value is an explicit opt-out
	// (unlimited), so only the zero default is replaced.
	if cfg.MaxConcurrentJobs == 0 {
		cfg.MaxConcurrentJobs = DefaultMaxConcurrentJobs
	}

	loader.Env(log,
		EnvBinding{Var: "RUNNER_API_URL", Target: &cfg.Api.Url},
		EnvBinding{Var: "RUNNER_API_CLIENT_ID", Target: &cfg.Api.ClientId},
		EnvBinding{Var: "RUNNER_API_CLIENT_SECRET", Target: &cfg.Api.ClientSecret},
	)
	applyShutdownGraceEnvOverride(log, &cfg.ShutdownGraceSeconds)

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// applyShutdownGraceEnvOverride applies RUNNER_SHUTDOWN_GRACE (an integer number of
// seconds) on top of the loaded config, mirroring the fit runner types so one env knob
// tunes the in-process superset's drain window whichever way a type is run. A non-integer
// value is warned about and ignored (the file value / default stands), never fatal.
func applyShutdownGraceEnvOverride(log *slog.Logger, target *int) {
	v, ok := os.LookupEnv(envShutdownGrace)
	if !ok || v == "" {
		return
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		log.Warn("ignoring invalid RUNNER_SHUTDOWN_GRACE (not an integer number of seconds)", "value", v, "error", err)
		return
	}
	log.Info("using value from environment", "var", envShutdownGrace)
	*target = n
}

// validate checks the controller config, returning the first failure (never exits the
// process). api.url is checked before Api.Validate so its dedicated message ("api.url is
// required") wins over Api.Validate's auth-focused messages, matching the former
// validateControllerConfig ordering.
func (c *ControllerConfig) validate() error {
	if c.Api.Url == "" {
		return fmt.Errorf("api.url is required")
	}
	if err := c.Api.Validate("api", true); err != nil {
		return err
	}
	if c.Uuid == "" {
		return fmt.Errorf("uuid is required")
	}
	if c.OwnedByWorkspace == "" {
		return fmt.Errorf("ownedByWorkspace is required")
	}
	if c.DisplayName == "" {
		return fmt.Errorf("displayName is required")
	}
	if c.Crypto.PublicKey == "" {
		return fmt.Errorf("crypto.publicKey is required")
	}
	if c.Crypto.PrivateKey == "" {
		return fmt.Errorf("crypto.privateKey is required")
	}
	return c.K8sJobConfig.Validate()
}

// LogStartup logs the startup configuration as one structured record plus one line per
// configured implementation (the former [RUN CONTROLLER] banner is retired in favor of the
// runner type attribute carried by the injected logger).
func (c *ControllerConfig) LogStartup(log *slog.Logger) {
	maxConcurrent := "unlimited"
	if c.MaxConcurrentJobs >= 0 {
		maxConcurrent = fmt.Sprintf("%d", c.MaxConcurrentJobs)
	}

	attrs := []any{
		"namespace", c.K8sJobConfig.Namespace,
		"uuid", c.Uuid,
		"maxConcurrentJobs", maxConcurrent,
		"implementations", len(c.K8sJobConfig.Implementations),
	}
	if len(c.K8sJobConfig.ImagePullSecrets) > 0 {
		attrs = append(attrs, "imagePullSecrets", c.K8sJobConfig.ImagePullSecrets)
	}
	attrs = append(attrs, "apiUrl", c.Api.Url, "apiUsername", c.Api.Username)
	log.Info("controller configuration", attrs...)

	for implType, spec := range c.K8sJobConfig.Implementations {
		log.Info("implementation configured",
			"type", implType, "image", spec.Image, "customEnvVars", len(spec.Env))
	}
}
