package config

import (
	"log"
	"os"

	"github.com/caarlos0/env/v11"
)

const envPrefix = "RUNNER_"

// applyEnvironment copies an envFallback value verbatim, as the expand option of env would rewrite a '$' in secrets.
func applyEnvironment(cfg *Config, logger *log.Logger) error {
	environment := env.ToMap(os.Environ())
	fields, err := env.GetFieldParamsWithOptions(cfg, env.Options{Prefix: envPrefix, DefaultValueTagName: "envFallback"})
	if err != nil {
		return err
	}
	for _, field := range fields {
		switch {
		case environment[field.Key] != "":
			logger.Printf("Using %s from environment", field.Key)
		case field.HasDefaultValue && environment[field.DefaultValue] != "":
			logger.Printf("Using %s from environment for %s", field.DefaultValue, field.Key)
			environment[field.Key] = environment[field.DefaultValue]
		}
	}
	return env.ParseWithOptions(cfg, env.Options{Prefix: envPrefix, Environment: environment})
}
