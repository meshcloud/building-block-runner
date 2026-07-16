package azdevops

import (
	"log/slog"

	"github.com/meshcloud/building-block-runner/internal/config"
)

// fileConfig is the yaml surface the runner type loader decodes: the shared BaseFileConfig
// (uuid/api/maxConcurrentRuns + the Kotlin-era `blockrunner:` compat block, embedded inline) plus
// the azdevops-specific flat keys. A mounted Kotlin runner-config.yml populates the block (incl.
// its baked dev privateKey); a Go-native file populates the flat keys; both keep working.
type fileConfig struct {
	config.BaseFileConfig `yaml:",inline"`
	Version               string `yaml:"version"`
	PrivateKey            string `yaml:"privateKey"`
	PrivateKeyFile        string `yaml:"privateKeyFile"`
}

// LoadConfig assembles the azure-devops type config with the full config precedence chain
// (defaults < blockrunner: compat block < flat keys < env). The shared uuid/api/maxConcurrentRuns
// precedence is resolved by config.ResolveBase on this loader. It fails fast on an unconsumed
// BLOCKRUNNER_*-prefixed env var and, in polling mode, on an unusable config (incl. an
// unresolvable private key). buildVersion is the ldflags build version, overridden by VERSION /
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

	// Version is the azdevops-only shared-block field (uuid/api are resolved by ResolveBase); the
	// block wins over the flat key, then VERSION env wins last (bound below).
	// PrivateKey/PrivateKeyFile are read directly off the block.
	fc.BlockRunner.ApplyShared(log, nil, &fc.Version, nil)
	if fc.BlockRunner.DebugMode != nil {
		log.Warn("ignoring manual-only blockrunner.debugMode key for the azdevops runner", "key", "blockrunner.debugMode")
	}
	if fc.BlockRunner.PrivateKey != "" {
		config.WarnDeprecated(log, "blockrunner.privateKey", "privateKey")
		fc.PrivateKey = fc.BlockRunner.PrivateKey
	}
	if fc.BlockRunner.PrivateKeyFile != "" {
		config.WarnDeprecated(log, "blockrunner.privateKeyFile", "privateKeyFile or RUNNER_PRIVATE_KEY_FILE")
		fc.PrivateKeyFile = fc.BlockRunner.PrivateKeyFile
	}

	// The azdevops-only VERSION env binding wins last; the shared RUNNER_* bindings + concurrency
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

	// PrivateKeyLoader resolution order (RUNNER_PRIVATE_KEY_FILE env > yaml privateKeyFile >
	// default path, falling back to the inline yaml privateKey) -- resolved after every other
	// override so it sees the final PrivateKeyFile/PrivateKey values.
	resolvedKey, err := config.ResolvePrivateKey(log, fc.PrivateKeyFile, fc.PrivateKey)
	if err != nil {
		return Config{}, err
	}

	out := Config{
		BaseConfig: base,
		Version:    fc.Version,
		PrivateKey: resolvedKey,
	}
	if err := out.Validate(singleRun); err != nil {
		return Config{}, err
	}
	return out, nil
}
