package config

import (
	"log/slog"
	"os"
)

// EnvAlias is one candidate env var for resolving a config file path. Aliases are tried
// in order; the first one set wins. Deprecated marks a legacy spelling kept working for
// compatibility (D7) -- using it logs a warning so operators can migrate off it.
type EnvAlias struct {
	Var        string
	Deprecated bool
}

// Path resolves a config file path by trying aliases in order (first match wins),
// falling back to defaultFile when none is set. Every alias is marked consumed
// regardless of whether it was set, matching Env's contract, since a config-file-path
// var is a recognized/handled name either way.
func (l *Loader) Path(log *slog.Logger, defaultFile string, aliases ...EnvAlias) string {
	for _, alias := range aliases {
		l.markConsumed(alias.Var)
		v, ok := os.LookupEnv(alias.Var)
		if !ok || v == "" {
			continue
		}
		if alias.Deprecated {
			log.Warn("using deprecated env var for config file path; prefer the primary spelling", "var", alias.Var)
		}
		return v
	}
	return defaultFile
}

// EnvBinding binds one legacy/compat env var onto a string field, applied at the
// highest precedence (over both YAML layers) by Env.
type EnvBinding struct {
	Var    string
	Target *string
}

// Env applies bindings on top of whatever Load already decoded, logging each one used
// -- preserving today's "Using X from environment" lines verbatim (D7). A binding whose
// env var is unset or empty leaves Target untouched, so it never clears a value already
// loaded from YAML.
func (l *Loader) Env(log *slog.Logger, bindings ...EnvBinding) {
	for _, b := range bindings {
		l.markConsumed(b.Var)
		v, ok := os.LookupEnv(b.Var)
		if !ok || v == "" {
			continue
		}
		log.Info("using value from environment", "var", b.Var)
		*b.Target = v
	}
}
