package config

import (
	"log/slog"
	"os"
	"strings"
)

// logLevelEnv is the additive (not a compat alias) env var controlling process log
// verbosity for every persona (D15/§5.2.6): debug|info|warn|error, case-insensitive,
// default info. At debug, the shared meshapi client logs full HTTP request/response
// wire detail.
const logLevelEnv = "LOG_LEVEL"

// LogLevel resolves LOG_LEVEL to a slog.Level. An unrecognized value logs a warning and
// falls back to info rather than failing startup -- this is an operator diagnostic
// knob, not a config correctness concern (unlike the legacy-env fail-fast guard).
func LogLevel(log *slog.Logger) slog.Level {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv(logLevelEnv)))
	switch raw {
	case "", "info":
		return slog.LevelInfo
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		log.Warn("unrecognized LOG_LEVEL value, falling back to info", "value", os.Getenv(logLevelEnv))
		return slog.LevelInfo
	}
}
