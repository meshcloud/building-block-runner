package config

import (
	"bytes"
	"log/slog"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLogLevel(t *testing.T) {
	tests := []struct {
		name string
		env  string
		want slog.Level
	}{
		{"unset defaults to info", "", slog.LevelInfo},
		{"info explicit", "info", slog.LevelInfo},
		{"debug", "debug", slog.LevelDebug},
		{"DEBUG uppercase", "DEBUG", slog.LevelDebug},
		{"warn", "warn", slog.LevelWarn},
		{"warning alias", "warning", slog.LevelWarn},
		{"error", "error", slog.LevelError},
		{"padded whitespace", "  debug  ", slog.LevelDebug},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.env == "" {
				require.NoError(t, os.Unsetenv("LOG_LEVEL"))
			} else {
				t.Setenv("LOG_LEVEL", tt.env)
			}
			assert.Equal(t, tt.want, LogLevel(discardLogger()))
		})
	}
}

func TestLogLevel_Unrecognized_FallsBackToInfoAndWarns(t *testing.T) {
	t.Setenv("LOG_LEVEL", "verbose")

	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))

	assert.Equal(t, slog.LevelInfo, LogLevel(log))
	assert.Contains(t, buf.String(), "unrecognized LOG_LEVEL")
}
