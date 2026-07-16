package github

import (
	"log/slog"

	"github.com/meshcloud/building-block-runner/internal/config"
)

// fileConfig is the yaml surface the loader decodes: the shared BaseFileConfig
// (uuid/api/maxConcurrentRuns + the Kotlin-era `blockrunner:` compat block, embedded inline) plus
// the github-specific flat keys. A mounted Kotlin runner-config.yml populates the block; a
// Go-native file populates the flat keys; both keep working.
type fileConfig struct {
	config.BaseFileConfig `yaml:",inline"`
	Version               string `yaml:"version"`
	PrivateKey            string `yaml:"privateKey"`
	PrivateKeyFile        string `yaml:"privateKeyFile"`
}

// LoadConfig assembles the github type config with the full config precedence chain
// (defaults < blockrunner: block < flat keys < env), resolves the private key via
// config.ResolvePrivateKey (the full Kotlin PrivateKeyLoader order), and validates. The shared
// uuid/api/maxConcurrentRuns precedence is resolved by config.ResolveBase on this loader. In
// single-run mode auth + key are exempt (NoOp decryptor; run token carries auth).
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

	// Version is the github-only shared-block field (uuid/api are resolved by ResolveBase); the
	// block wins over the flat key, then VERSION env wins last (bound below). debugMode is a
	// manual-only key ⇒ warn-and-ignore here.
	fc.BlockRunner.ApplyShared(log, nil, &fc.Version, nil)
	if fc.BlockRunner.DebugMode != nil {
		log.Warn("ignoring manual-only blockrunner.debugMode key for the github runner", "key", "blockrunner.debugMode")
	}
	if fc.BlockRunner.PrivateKey != "" {
		config.WarnDeprecated(log, "blockrunner.privateKey", "privateKey")
		fc.PrivateKey = fc.BlockRunner.PrivateKey
	}
	if fc.BlockRunner.PrivateKeyFile != "" {
		config.WarnDeprecated(log, "blockrunner.privateKeyFile", "privateKeyFile or RUNNER_PRIVATE_KEY_FILE")
		fc.PrivateKeyFile = fc.BlockRunner.PrivateKeyFile
	}

	// The github-only VERSION env binding wins last; the shared RUNNER_* bindings + concurrency
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

	// Resolve the private key (RUNNER_PRIVATE_KEY_FILE > privateKeyFile > default path, then
	// inline fallback) — a first-class consumer of the shared resolver. Skipped in
	// single-run mode (NoOp decryptor).
	privateKey := ""
	if !singleRun {
		resolved, err := config.ResolvePrivateKey(log, fc.PrivateKeyFile, fc.PrivateKey)
		if err != nil {
			return Config{}, err
		}
		privateKey = resolved
	}

	out := Config{
		BaseConfig: base,
		Version:    fc.Version,
		PrivateKey: privateKey,
	}
	if err := out.Validate(singleRun); err != nil {
		return Config{}, err
	}
	return out, nil
}
