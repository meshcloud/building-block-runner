package manual

import (
	"log/slog"

	"github.com/meshcloud/building-block-runner/internal/config"
)

// fileConfig is the yaml surface the runner type loader decodes: the shared BaseFileConfig
// (uuid/api/maxConcurrentRuns + the Kotlin-era `blockrunner:` compat block, embedded inline) plus
// the manual-specific flat keys. A mounted Kotlin runner-config.yml populates the block; a
// Go-native file populates the flat keys; both keep working.
type fileConfig struct {
	config.BaseFileConfig `yaml:",inline"`
	Version               string `yaml:"version"`
	DebugMode             bool   `yaml:"debugMode"`
}

// LoadConfig assembles the manual type config with the full config precedence chain:
// compiled-in defaults < the `blockrunner:` compat block < flat Go-native keys < env. The shared
// uuid/api/maxConcurrentRuns precedence is resolved by config.ResolveBase on this loader; the
// manual-only version/debugMode is resolved here. It fails fast on an unconsumed
// BLOCKRUNNER_*-prefixed env var (a Spring relaxed-binding holdover) and — in polling mode —
// on an unusable config. buildVersion is the ldflags build version, overridden by VERSION /
// blockrunner.version when set.
func LoadConfig(log *slog.Logger, buildVersion string, singleRun bool) (Config, error) {
	fc := fileConfig{
		BaseFileConfig: config.DefaultBaseFileConfig(),
		Version:        buildVersion,
	}

	loader := config.NewLoader()
	configPath := loader.Path(log, "runner-config.yml", config.EnvAlias{Var: "RUNNER_CONFIG_FILE"})
	if _, err := loader.Load(configPath, &fc); err != nil {
		return Config{}, err
	}
	loader.WarnIgnoredLegacyYAMLBlocks(log)

	// Version is the manual-only shared-block field (uuid/api are resolved by ResolveBase);
	// the block wins over the flat key, then VERSION env wins last (bound below).
	fc.BlockRunner.ApplyShared(log, nil, &fc.Version, nil)
	if fc.BlockRunner.DebugMode != nil {
		config.WarnDeprecated(log, "blockrunner.debugMode", "debugMode")
		fc.DebugMode = *fc.BlockRunner.DebugMode
	}
	// manual decrypts nothing, so a mounted Kotlin-era key is inert here -- warn-and-ignore
	// (mirrors github's blockrunner.debugMode handling) rather than silently dropping it,
	// so an operator relying on the key for a *different* type in the same file notices.
	if fc.BlockRunner.PrivateKey != "" {
		log.Warn("ignoring blockrunner.privateKey key for the manual runner; the manual runner never decrypts", "key", "blockrunner.privateKey")
	}
	if fc.BlockRunner.PrivateKeyFile != "" {
		log.Warn("ignoring blockrunner.privateKeyFile key for the manual runner; the manual runner never decrypts", "key", "blockrunner.privateKeyFile")
	}

	// The manual-only VERSION env binding wins last; the shared RUNNER_* bindings + concurrency
	// are applied by ResolveBase on this same loader.
	loader.Env(log,
		config.EnvBinding{Var: "VERSION", Target: &fc.Version, Deprecated: true, Canonical: "the compiled-in build version (ldflags)"},
	)
	base, err := config.ResolveBase(log, loader, &fc.BaseFileConfig)
	if err != nil {
		return Config{}, err
	}

	if err := loader.FailOnUnconsumedLegacyEnv("BLOCKRUNNER_"); err != nil {
		return Config{}, err
	}

	out := Config{
		BaseConfig: base,
		Version:    fc.Version,
		DebugMode:  fc.DebugMode,
	}
	if err := out.Validate(singleRun); err != nil {
		return Config{}, err
	}
	return out, nil
}
