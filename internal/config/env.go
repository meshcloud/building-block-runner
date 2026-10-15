package config

import (
	"context"
	"log/slog"
	"os"

	"github.com/caarlos0/env/v11"
)

const envPrefix = "RUNNER_"

// applyEnvironment copies an envFallback value verbatim, as the expand option of env would rewrite a '$' in secrets.
func applyEnvironment(ctx context.Context, cfg *Config) error {
	environment := env.ToMap(os.Environ())
	fields, err := env.GetFieldParamsWithOptions(cfg, env.Options{Prefix: envPrefix, DefaultValueTagName: "envFallback"})
	if err != nil {
		return err
	}
	for _, field := range fields {
		switch {
		case environment[field.Key] != "":
			slog.InfoContext(ctx, "using environment variable", "name", field.Key)
		case field.HasDefaultValue && environment[field.DefaultValue] != "":
			slog.InfoContext(ctx, "using environment variable", "name", field.DefaultValue, "for", field.Key)
			environment[field.Key] = environment[field.DefaultValue]
		}
	}
	return env.ParseWithOptions(cfg, env.Options{Prefix: envPrefix, Environment: environment})
}
