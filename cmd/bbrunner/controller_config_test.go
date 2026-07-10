package main

import (
	"bytes"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// newBufferLogger returns a slog logger writing to buf so tests can assert on the rendered
// deprecation warning (§8.4 construction-only retarget: log.New -> slog handler).
func newBufferLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(buf, nil))
}

// TestResolveControllerConfigFile pins the accumulated alias inventory's row 1
// (docs/DEPRECATIONS.md): RUNNER_CONFIG_FILE is canonical, RUNCONTROLLER_CONFIG_FILE is a
// deprecated, still-honored alias, and the default applies when neither is set.
func TestResolveControllerConfigFile(t *testing.T) {
	t.Run("neither set uses the default", func(t *testing.T) {
		var buf bytes.Buffer
		logger := newBufferLogger(&buf)
		got := resolveControllerConfigFile(logger)
		require.Equal(t, defaultConfigFile, got)
		require.Empty(t, buf.String())
	})

	t.Run("only the deprecated alias is set: used, and warns", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "legacy.yml")
		t.Setenv("RUNCONTROLLER_CONFIG_FILE", path)

		var buf bytes.Buffer
		logger := newBufferLogger(&buf)
		got := resolveControllerConfigFile(logger)
		require.Equal(t, path, got)
		require.Contains(t, buf.String(), "deprecated")
		require.Contains(t, buf.String(), "RUNCONTROLLER_CONFIG_FILE")
		require.Contains(t, buf.String(), "RUNNER_CONFIG_FILE")
	})

	t.Run("the canonical var wins when both are set, and no warning fires", func(t *testing.T) {
		primary := filepath.Join(t.TempDir(), "primary.yml")
		legacy := filepath.Join(t.TempDir(), "legacy.yml")
		t.Setenv("RUNNER_CONFIG_FILE", primary)
		t.Setenv("RUNCONTROLLER_CONFIG_FILE", legacy)

		var buf bytes.Buffer
		logger := newBufferLogger(&buf)
		got := resolveControllerConfigFile(logger)
		require.Equal(t, primary, got)
		require.Empty(t, buf.String())
	})
}
