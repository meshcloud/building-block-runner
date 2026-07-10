package manual

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"

	"github.com/meshcloud/building-block-runner/internal/config"
)

// Shipped compiled-in defaults, matching the Kotlin runner-config.yml `${VAR:default}`
// fallbacks byte-for-byte (manual-block-runner/src/main/resources/runner-config.yml) so a
// persona run with no config file and no env is effect-equivalent to the JVM default boot.
const (
	defaultUuid     = "d943b032-7836-4fef-a4a0-158817beecf3"
	defaultApiUrl   = "http://localhost:8301" // the mux MANUAL port (umbrella A11)
	defaultUsername = "bb-api"
	defaultPassword = "guest"
	defaultVersion  = "dev"
)

const envMaxConcurrentRuns = "RUNNER_MAX_CONCURRENT_RUNS"

// fileConfig is the yaml surface the persona loader decodes: the Go-native flat keys plus
// the Kotlin-era `blockrunner:` compat block (§6.4). A mounted Kotlin runner-config.yml
// populates the block; a Go-native file populates the flat keys; both keep working.
type fileConfig struct {
	Uuid              string                   `yaml:"uuid"`
	Version           string                   `yaml:"version"`
	Api               config.Api               `yaml:"api"`
	DebugMode         bool                     `yaml:"debugMode"`
	MaxConcurrentRuns int                      `yaml:"maxConcurrentRuns"`
	BlockRunner       config.BlockRunnerCompat `yaml:"blockrunner"`
}

// LoadConfig assembles the manual persona config with the full D7 precedence chain:
// compiled-in defaults < the `blockrunner:` compat block < flat Go-native keys is resolved
// by normalizing the block over the flat keys, then env bindings win last. It fails fast
// (P5) on an unconsumed BLOCKRUNNER_*-prefixed env var (a Spring relaxed-binding holdover,
// §10.4) and — in polling mode — on an unusable config. buildVersion is the ldflags build
// version, overridden by VERSION / blockrunner.version when set (§6.2).
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
	// the persona); DebugMode is the manual-only key read directly off the block.
	cfg.BlockRunner.ApplyShared(log, &cfg.Uuid, &cfg.Version, &cfg.Api)
	if cfg.BlockRunner.DebugMode != nil {
		config.WarnDeprecated(log, "blockrunner.debugMode", "debugMode")
		cfg.DebugMode = *cfg.BlockRunner.DebugMode
	}
	// manual decrypts nothing, so a mounted Kotlin-era key is inert here -- warn-and-ignore
	// (mirrors github's blockrunner.debugMode handling) rather than silently dropping it,
	// so an operator relying on the key for a *different* persona in the same file notices.
	if cfg.BlockRunner.PrivateKey != "" {
		log.Warn("ignoring blockrunner.privateKey key for the manual runner; the manual runner never decrypts", "key", "blockrunner.privateKey")
	}
	if cfg.BlockRunner.PrivateKeyFile != "" {
		log.Warn("ignoring blockrunner.privateKeyFile key for the manual runner; the manual runner never decrypts", "key", "blockrunner.privateKeyFile")
	}

	// Env bindings win last (D7). Names are the literal shipped spellings (§6.2); no Spring
	// relaxed-binding variants are honored.
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

	out := Config{
		Uuid:              cfg.Uuid,
		Version:           cfg.Version,
		Api:               cfg.Api,
		DebugMode:         cfg.DebugMode,
		MaxConcurrentRuns: cfg.MaxConcurrentRuns,
	}
	if err := out.Validate(singleRun); err != nil {
		return Config{}, err
	}
	return out, nil
}

// applyMaxConcurrentRuns honors RUNNER_MAX_CONCURRENT_RUNS (new, additive — plan 05, A2):
// a negative value means unlimited (bounded per-cycle by the loop's backstop). A non-numeric
// value is a hard startup error (P5), never a silent fallback.
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
