package config

import (
	"log/slog"
	"os"
)

// EnvAlias is one candidate env var for resolving a config file path. Aliases are tried
// in order; the first one set wins. Deprecated marks a legacy spelling kept working for
// compatibility -- using it logs a warning so operators can migrate off it.
type EnvAlias struct {
	Var        string
	Deprecated bool
}

// Path resolves a config file path by trying aliases in order (first match wins),
// falling back to defaultFile when none is set. Every alias is marked consumed
// regardless of whether it was set, matching Env's contract, since a config-file-path
// var is a recognized/handled name either way.
func (l *Loader) Path(log *slog.Logger, defaultFile string, aliases ...EnvAlias) string {
	canonical := firstNonDeprecatedVar(aliases)
	for _, alias := range aliases {
		l.markConsumed(alias.Var)
		v, ok := os.LookupEnv(alias.Var)
		if !ok || v == "" {
			continue
		}
		if alias.Deprecated {
			WarnDeprecated(log, alias.Var, canonical)
		}
		return v
	}
	return defaultFile
}

// firstNonDeprecatedVar names the alias a deprecation warning should point operators at --
// the first non-deprecated candidate, since that is the one Path tries first. Callers that
// pass only deprecated aliases fall back to a generic description rather than fabricating a
// var name that doesn't exist.
func firstNonDeprecatedVar(aliases []EnvAlias) string {
	for _, a := range aliases {
		if !a.Deprecated {
			return a.Var
		}
	}
	return "the default config path"
}

// EnvBinding binds one env var onto a string field, applied at the highest precedence
// (over both YAML layers) by Env. Deprecated marks a binding that is a legacy/compat
// spelling kept working for compatibility -- using it logs a uniform deprecation
// warning (Canonical describes the replacement) instead of the plain "using value from
// environment" info line.
type EnvBinding struct {
	Var        string
	Target     *string
	Deprecated bool
	Canonical  string // human-readable replacement description; required when Deprecated
}

// Env applies bindings on top of whatever Load already decoded, logging each one used
// -- preserving today's "Using X from environment" lines verbatim. A binding whose
// env var is unset or empty leaves Target untouched, so it never clears a value already
// loaded from YAML.
func (l *Loader) Env(log *slog.Logger, bindings ...EnvBinding) {
	for _, b := range bindings {
		l.markConsumed(b.Var)
		v, ok := os.LookupEnv(b.Var)
		if !ok || v == "" {
			continue
		}
		if b.Deprecated {
			WarnDeprecated(log, b.Var, b.Canonical)
		} else {
			log.Info("using value from environment", "var", b.Var)
		}
		*b.Target = v
	}
}
