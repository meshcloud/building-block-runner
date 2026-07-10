package gitlab

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"

	"github.com/meshcloud/building-block-runner/internal/config"
)

// Shipped compiled-in defaults, matching the Kotlin runner-config.yml `${VAR:default}`
// fallbacks byte-for-byte (gitlab-block-runner/src/main/resources/runner-config.yml) so a
// persona run with no config file and no env is effect-equivalent to the JVM default boot.
const (
	defaultUuid     = "bfe76555-7a69-48e8-8cc0-8e02eb76fc22"
	defaultApiUrl   = "http://localhost:8303" // the mux GITLAB_PIPELINE port (umbrella A11)
	defaultUsername = "bb-api"
	defaultPassword = "guest"

	// defaultBaseConfigFile is the shared top-level base layer (umbrella §5.4/§10.5, plan
	// 06B §6.3): it holds the well-known dev private key common to gitlab/azdevops/github.
	// gitlab is the first port to actually wire a non-empty base layer -- manual (06A)
	// needs none, so its LoadConfig calls Loader.Load with an empty basePath.
	defaultBaseConfigFile = "containers/runner-config.yml"
)

const envMaxConcurrentRuns = "RUNNER_MAX_CONCURRENT_RUNS"

// fileConfig is the yaml surface the persona loader decodes: the Go-native flat keys plus
// the Kotlin-era `blockrunner:` compat block (umbrella §5.4). A mounted Kotlin
// runner-config.yml populates the block; a Go-native file populates the flat keys; both
// keep working.
type fileConfig struct {
	Uuid              string                   `yaml:"uuid"`
	Version           string                   `yaml:"version"`
	Api               config.Api               `yaml:"api"`
	PrivateKey        string                   `yaml:"privateKey"`
	PrivateKeyFile    string                   `yaml:"privateKeyFile"`
	MaxConcurrentRuns int                      `yaml:"maxConcurrentRuns"`
	BlockRunner       config.BlockRunnerCompat `yaml:"blockrunner"`
}

// LoadConfig assembles the gitlab persona config with the full D7 precedence chain:
// compiled-in defaults < shared base YAML < per-impl YAML < the `blockrunner:` compat
// block < env (the block sits above the flat keys within the file layer, matching 06A
// §6.4 -- a mounted Kotlin-era file must fully configure the persona). It fails fast (P5)
// on an unconsumed BLOCKRUNNER_*-prefixed env var and -- in polling mode -- on an unusable
// config, which now includes a resolvable private key (every GitLab run decrypts the
// pipeline trigger token, §6.1). buildVersion is the ldflags build version, overridden by
// VERSION / blockrunner.version when set.
func LoadConfig(log *slog.Logger, buildVersion string, singleRun bool) (Config, error) {
	cfg := fileConfig{
		Uuid:              defaultUuid,
		Version:           buildVersion,
		Api:               config.Api{Url: defaultApiUrl, Username: defaultUsername, Password: defaultPassword},
		MaxConcurrentRuns: defaultMaxConcurrentRuns,
	}

	loader := config.NewLoader()
	basePath := loader.Path(log, defaultBaseConfigFile, config.EnvAlias{Var: "RUNNER_BASE_CONFIG_FILE"})
	implPath := loader.Path(log, "runner-config.yml", config.EnvAlias{Var: "RUNNER_CONFIG_FILE"})
	if _, err := loader.Load(basePath, implPath, &cfg); err != nil {
		return Config{}, err
	}

	// blockrunner: block overrides the flat keys (a mounted Kotlin-era file fully
	// configures the persona); PrivateKey/PrivateKeyFile are the gitlab-specific keys read
	// directly off the block (manual ignores them, 06A §17 row 8).
	cfg.BlockRunner.ApplyShared(log, &cfg.Uuid, &cfg.Version, &cfg.Api)
	if cfg.BlockRunner.DebugMode != nil {
		log.Warn("ignoring manual-only blockrunner.debugMode key for the gitlab runner", "key", "blockrunner.debugMode")
	}
	if cfg.BlockRunner.PrivateKey != "" {
		config.WarnDeprecated(log, "blockrunner.privateKey", "privateKey")
		cfg.PrivateKey = cfg.BlockRunner.PrivateKey
	}
	if cfg.BlockRunner.PrivateKeyFile != "" {
		config.WarnDeprecated(log, "blockrunner.privateKeyFile", "privateKeyFile or RUNNER_PRIVATE_KEY_FILE")
		cfg.PrivateKeyFile = cfg.BlockRunner.PrivateKeyFile
	}

	// Env bindings win last (D7). Names are the literal shipped spellings (§6.2); no
	// Spring relaxed-binding variants are honored.
	loader.Env(log,
		config.EnvBinding{Var: "RUNNER_UUID", Target: &cfg.Uuid},
		config.EnvBinding{Var: "VERSION", Target: &cfg.Version, Deprecated: true, Canonical: "the compiled-in build version (ldflags)"},
		config.EnvBinding{Var: "RUNNER_API_URL", Target: &cfg.Api.Url},
		config.EnvBinding{Var: "RUNNER_API_USERNAME", Target: &cfg.Api.Username},
		config.EnvBinding{Var: "RUNNER_API_PASSWORD", Target: &cfg.Api.Password},
		config.EnvBinding{Var: "RUNNER_API_CLIENT_ID", Target: &cfg.Api.ClientId},
		config.EnvBinding{Var: "RUNNER_API_CLIENT_SECRET", Target: &cfg.Api.ClientSecret},
		config.EnvBinding{Var: "RUNNER_PRIVATE_KEY_FILE", Target: &cfg.PrivateKeyFile},
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
		MaxConcurrentRuns: cfg.MaxConcurrentRuns,
	}

	// The private key is resolved (not just carried as a path/inline value) only in
	// polling mode: single-run mode uses the NoOp decryptor, and RESOLVE-ing here would
	// otherwise demand a key file exist even when nothing will ever decrypt with it.
	if !singleRun {
		pem, err := config.ResolvePrivateKey(log, cfg.PrivateKeyFile, cfg.PrivateKey)
		if err != nil {
			return Config{}, fmt.Errorf("resolving private key: %w", err)
		}
		out.PrivateKeyPEM = pem
	}

	if err := out.Validate(singleRun); err != nil {
		return Config{}, err
	}
	return out, nil
}

// applyMaxConcurrentRuns honors RUNNER_MAX_CONCURRENT_RUNS (plan 05, A2): a negative value
// means unlimited (bounded per-cycle by the loop's backstop). A non-numeric value is a
// hard startup error (P5), never a silent fallback.
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
