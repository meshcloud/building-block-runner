package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
)

// DefaultMaxConcurrentRuns is the shared standalone in-process concurrency default for every
// runner type: a modest throughput improvement over the former serial single-worker cadence,
// overridable via RUNNER_MAX_CONCURRENT_RUNS. Before this it was re-declared per type
// (defaultMaxConcurrentRuns in internal/{manual,gitlab,azdevops}, DefaultMaxConcurrentRuns in
// internal/tf), all 3; github's old per-type default of 1 is intentionally dropped in favor of
// this shared 3 (docs/DEPRECATIONS.md).
const DefaultMaxConcurrentRuns = 3

const envMaxConcurrentRuns = "RUNNER_MAX_CONCURRENT_RUNS"

// BaseConfig is the dispatcher-owned shared runtime config every runner type embeds: the runner
// identity, the meshfed API/auth section, and the in-process concurrency cap. Type configs embed
// it (promoting Uuid / Api / MaxConcurrentRuns) and add only their type-specific fields, so the
// shared precedence (defaults < blockrunner: block < RUNNER_* env) lives once in ResolveBase
// instead of being copy-pasted into each type's loader.
type BaseConfig struct {
	// Uuid is this runner's uuid (RUNNER_UUID / blockrunner.uuid): the claim forRunnerUuid, the
	// status-source id, and the node-id header.
	Uuid string
	// Api is the shared meshfed connection/auth (url + Basic/API-key).
	Api Api
	// MaxConcurrentRuns caps in-process concurrent runs in polling mode (default
	// DefaultMaxConcurrentRuns; negative means unlimited). It is unused on the
	// superset/controller path -- that path reads ControllerConfig.MaxConcurrentJobs instead --
	// an accepted wart, kept here so every single-type dispatcher reads one shared field.
	MaxConcurrentRuns int
}

// BaseFileConfig is the shared yaml surface, embedded inline (`yaml:",inline"`) into each type's
// file-config struct so the flat `uuid:` / `api:` / `maxConcurrentRuns:` keys plus the Kotlin-era
// `blockrunner:` compat block decode uniformly across every type. Seed it from
// DefaultBaseFileConfig before Load so keys absent from the file keep their compiled-in defaults.
type BaseFileConfig struct {
	Uuid              string            `yaml:"uuid"`
	Api               Api               `yaml:"api"`
	MaxConcurrentRuns int               `yaml:"maxConcurrentRuns"`
	BlockRunner       BlockRunnerCompat `yaml:"blockrunner"`
}

// DefaultBaseFileConfig returns the shared compiled-in dev-local defaults so a type run with no
// config file and no env boots against the local meshStack: the single well-known local-dev
// runner uuid, the local meshfed-API endpoint + Basic-auth credentials, and the shared
// concurrency default. Real deployments override them via RUNNER_UUID / RUNNER_API_* env.
func DefaultBaseFileConfig() BaseFileConfig {
	return BaseFileConfig{
		Uuid:              DefaultRunnerUuid,
		Api:               Api{Url: DefaultApiUrl, Username: DefaultApiUsername, Password: DefaultApiPassword},
		MaxConcurrentRuns: DefaultMaxConcurrentRuns,
	}
}

// ResolveBase applies the shared precedence -- the `blockrunner:` compat block over the flat keys,
// then RUNNER_* env over both, then RUNNER_MAX_CONCURRENT_RUNS -- on the CALLER'S loader, so
// consumed-env tracking (FailOnUnconsumedLegacyEnv) stays unified across the shared and the
// type-specific keys. It mutates fc in place and returns the resolved BaseConfig. Version is
// type-specific (tf has none; the four HTTP types stamp a header from it): ResolveBase passes nil
// to ApplyShared, and the caller applies blockrunner.version / VERSION itself.
func ResolveBase(log *slog.Logger, loader *Loader, fc *BaseFileConfig) (BaseConfig, error) {
	fc.BlockRunner.ApplyShared(log, &fc.Uuid, nil, &fc.Api)

	loader.Env(log,
		EnvBinding{Var: "RUNNER_UUID", Target: &fc.Uuid},
		EnvBinding{Var: "RUNNER_API_URL", Target: &fc.Api.Url},
		EnvBinding{Var: "RUNNER_API_USERNAME", Target: &fc.Api.Username},
		EnvBinding{Var: "RUNNER_API_PASSWORD", Target: &fc.Api.Password},
		EnvBinding{Var: "RUNNER_API_CLIENT_ID", Target: &fc.Api.ClientId},
		EnvBinding{Var: "RUNNER_API_CLIENT_SECRET", Target: &fc.Api.ClientSecret},
	)

	if err := applyMaxConcurrentRuns(log, &fc.MaxConcurrentRuns); err != nil {
		return BaseConfig{}, err
	}

	return BaseConfig{Uuid: fc.Uuid, Api: fc.Api, MaxConcurrentRuns: fc.MaxConcurrentRuns}, nil
}

// applyMaxConcurrentRuns honors RUNNER_MAX_CONCURRENT_RUNS (additive): a negative value means
// unlimited (bounded per-cycle by the loop's backstop). A non-numeric value is a hard startup
// error, never a silent fallback. Moved here from the per-type loaders (formerly copy-pasted in
// internal/{manual,github,gitlab,azdevops}).
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
