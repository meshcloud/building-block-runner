package azdevops

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"

	"github.com/meshcloud/building-block-runner/internal/config"
)

// Shipped compiled-in defaults, matching the Kotlin runner-config.yml `${VAR:default}`
// fallbacks byte-for-byte (azure-devops-block-runner/src/main/resources/runner-config.yml,
// §2.6) so a persona run with no config file and no env is effect-equivalent to the JVM
// default boot.
const (
	defaultUuid     = "a9786b14-ecfe-44dd-b04c-2bcfd326aa23"
	defaultApiUrl   = "http://localhost:8304" // the mux AZURE_DEVOPS_PIPELINE port (umbrella A11)
	defaultUsername = "bb-api"
	defaultPassword = "guest"
	defaultVersion  = "dev"
)

const envMaxConcurrentRuns = "RUNNER_MAX_CONCURRENT_RUNS"

// fileConfig is the yaml surface the persona loader decodes: the Go-native flat keys plus
// the Kotlin-era `blockrunner:` compat block (§6.2/§6.4). A mounted Kotlin runner-config.yml
// populates the block (incl. its baked dev privateKey); a Go-native file populates the flat
// keys; both keep working.
type fileConfig struct {
	Uuid              string                   `yaml:"uuid"`
	Version           string                   `yaml:"version"`
	Api               config.Api               `yaml:"api"`
	PrivateKey        string                   `yaml:"privateKey"`
	PrivateKeyFile    string                   `yaml:"privateKeyFile"`
	MaxConcurrentRuns int                      `yaml:"maxConcurrentRuns"`
	BlockRunner       config.BlockRunnerCompat `yaml:"blockrunner"`
}

// LoadConfig assembles the azure-devops persona config with the full D7 precedence chain
// (defaults < blockrunner: compat block-normalized-over-flat-keys < env). It fails fast (P5)
// on an unconsumed BLOCKRUNNER_*-prefixed env var and, in polling mode, on an unusable
// config (incl. an unresolvable private key). buildVersion is the ldflags build version,
// overridden by VERSION / blockrunner.version when set (§6.2).
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

	// blockrunner: block overrides the flat keys (a mounted Kotlin-era file must fully
	// configure the persona, §6.4); PrivateKey/PrivateKeyFile are read directly off the
	// block (BlockRunnerCompat.ApplyShared only normalizes the cross-persona fields).
	cfg.BlockRunner.ApplyShared(log, &cfg.Uuid, &cfg.Version, &cfg.Api)
	if cfg.BlockRunner.DebugMode != nil {
		log.Warn("ignoring manual-only blockrunner.debugMode key for the azdevops runner", "key", "blockrunner.debugMode")
	}
	if cfg.BlockRunner.PrivateKey != "" {
		config.WarnDeprecated(log, "blockrunner.privateKey", "privateKey")
		cfg.PrivateKey = cfg.BlockRunner.PrivateKey
	}
	if cfg.BlockRunner.PrivateKeyFile != "" {
		config.WarnDeprecated(log, "blockrunner.privateKeyFile", "privateKeyFile or RUNNER_PRIVATE_KEY_FILE")
		cfg.PrivateKeyFile = cfg.BlockRunner.PrivateKeyFile
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

	// PrivateKeyLoader resolution order (RUNNER_PRIVATE_KEY_FILE env > yaml
	// privateKeyFile > default path, falling back to the inline yaml privateKey, 06A §6.5)
	// -- resolved after every other override so it sees the final PrivateKeyFile/PrivateKey
	// values.
	resolvedKey, err := config.ResolvePrivateKey(log, cfg.PrivateKeyFile, cfg.PrivateKey)
	if err != nil {
		return Config{}, err
	}

	out := Config{
		Uuid:              cfg.Uuid,
		Version:           cfg.Version,
		Api:               cfg.Api,
		PrivateKey:        resolvedKey,
		MaxConcurrentRuns: cfg.MaxConcurrentRuns,
	}
	if err := out.Validate(singleRun); err != nil {
		return Config{}, err
	}
	return out, nil
}

// applyMaxConcurrentRuns honors RUNNER_MAX_CONCURRENT_RUNS (new, additive -- plan 05, A2): a
// negative value means unlimited (bounded per-cycle by the loop's backstop). A non-numeric
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
