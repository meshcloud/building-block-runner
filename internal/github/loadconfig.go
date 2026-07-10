package github

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"

	"github.com/meshcloud/building-block-runner/internal/config"
)

// Shipped compiled-in defaults, matching the Kotlin github runner-config.yml `${VAR:default}`
// fallbacks (github-block-runner/src/main/resources/runner-config.yml:2-11) so a persona run
// with no config file and no env is effect-equivalent to the JVM default boot.
const (
	defaultUuid     = "606f54c8-ed3b-4a79-ad80-971dfb4eff21"
	defaultApiUrl   = "http://localhost:8302" // the mux GITHUB_WORKFLOW port (umbrella A11)
	defaultUsername = "bb-api"
	defaultPassword = "guest"
)

const envMaxConcurrentRuns = "RUNNER_MAX_CONCURRENT_RUNS"

// fileConfig is the yaml surface the loader decodes: Go-native flat keys plus the Kotlin-era
// `blockrunner:` compat block (§6.2). A mounted Kotlin runner-config.yml populates the
// block; a Go-native file populates the flat keys; both keep working.
type fileConfig struct {
	Uuid              string                   `yaml:"uuid"`
	Version           string                   `yaml:"version"`
	Api               config.Api               `yaml:"api"`
	PrivateKey        string                   `yaml:"privateKey"`
	PrivateKeyFile    string                   `yaml:"privateKeyFile"`
	MaxConcurrentRuns int                      `yaml:"maxConcurrentRuns"`
	BlockRunner       config.BlockRunnerCompat `yaml:"blockrunner"`
}

// LoadConfig assembles the github persona config with the full D7 precedence chain
// (defaults < blockrunner: block < flat keys, then env wins last), resolves the private key
// via config.ResolvePrivateKey (the full Kotlin PrivateKeyLoader order), and validates. In
// single-run mode auth + key are exempt (NoOp decryptor; run token carries auth).
func LoadConfig(log *slog.Logger, buildVersion string, singleRun bool) (Config, error) {
	cfg := fileConfig{
		Uuid:              defaultUuid,
		Version:           buildVersion,
		Api:               config.Api{Url: defaultApiUrl, Username: defaultUsername, Password: defaultPassword},
		MaxConcurrentRuns: defaultMaxConcurrentRuns,
	}

	loader := config.NewLoader()
	configPath := loader.Path(log, "runner-config.yml", config.EnvAlias{Var: "RUNNER_CONFIG_FILE"})
	if _, err := loader.Load("", configPath, &cfg); err != nil {
		return Config{}, err
	}

	// blockrunner: block overrides the flat keys (a mounted Kotlin-era file fully configures
	// the persona). debugMode is a manual-only key ⇒ warn-and-ignore here (§6.2).
	cfg.BlockRunner.ApplyShared(log, &cfg.Uuid, &cfg.Version, &cfg.Api)
	if cfg.BlockRunner.DebugMode != nil {
		log.Warn("ignoring manual-only blockrunner.debugMode key for the github runner", "key", "blockrunner.debugMode")
	}
	if cfg.BlockRunner.PrivateKey != "" {
		config.WarnDeprecated(log, "blockrunner.privateKey", "privateKey")
		cfg.PrivateKey = cfg.BlockRunner.PrivateKey
	}
	if cfg.BlockRunner.PrivateKeyFile != "" {
		config.WarnDeprecated(log, "blockrunner.privateKeyFile", "privateKeyFile or RUNNER_PRIVATE_KEY_FILE")
		cfg.PrivateKeyFile = cfg.BlockRunner.PrivateKeyFile
	}

	// Env bindings win last (D7); literal shipped spellings (§6.2), no relaxed-binding variants.
	loader.Env(log,
		config.EnvBinding{Var: "RUNNER_UUID", Target: &cfg.Uuid},
		config.EnvBinding{Var: "VERSION", Target: &cfg.Version, Deprecated: true, Canonical: "the compiled-in build version (ldflags)"},
		config.EnvBinding{Var: "RUNNER_API_URL", Target: &cfg.Api.Url},
		config.EnvBinding{Var: "RUNNER_API_USERNAME", Target: &cfg.Api.Username},
		config.EnvBinding{Var: "RUNNER_API_PASSWORD", Target: &cfg.Api.Password},
		config.EnvBinding{Var: "RUNNER_API_CLIENT_ID", Target: &cfg.Api.ClientId},
		config.EnvBinding{Var: "RUNNER_API_CLIENT_SECRET", Target: &cfg.Api.ClientSecret},
	)

	if err := applyMaxConcurrentRuns(log, &cfg.MaxConcurrentRuns); err != nil {
		return Config{}, err
	}

	if err := loader.FailOnUnconsumedLegacyEnv("BLOCKRUNNER_"); err != nil {
		return Config{}, err
	}

	// Resolve the private key (RUNNER_PRIVATE_KEY_FILE > privateKeyFile > default path, then
	// inline fallback) — a first-class consumer of the shared resolver (§6.2). Skipped in
	// single-run mode (NoOp decryptor).
	privateKey := ""
	if !singleRun {
		resolved, err := config.ResolvePrivateKey(log, cfg.PrivateKeyFile, cfg.PrivateKey)
		if err != nil {
			return Config{}, err
		}
		privateKey = resolved
	}

	out := Config{
		Uuid:              cfg.Uuid,
		Version:           cfg.Version,
		Api:               cfg.Api,
		PrivateKey:        privateKey,
		MaxConcurrentRuns: cfg.MaxConcurrentRuns,
	}
	if err := out.Validate(singleRun); err != nil {
		return Config{}, err
	}
	return out, nil
}

// applyMaxConcurrentRuns honors RUNNER_MAX_CONCURRENT_RUNS (additive — plan 05). A negative
// value means unlimited; a non-numeric value is a hard startup error (P5).
func applyMaxConcurrentRuns(log *slog.Logger, target *int) error {
	v, ok := os.LookupEnv(envMaxConcurrentRuns)
	if !ok || v == "" {
		return nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fmt.Errorf("%s=%q is not a valid integer: %w", envMaxConcurrentRuns, v, err)
	}
	log.Info("using value from environment", "var", envMaxConcurrentRuns)
	*target = n
	return nil
}
