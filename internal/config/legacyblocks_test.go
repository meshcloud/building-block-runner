package config

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWarnIgnoredLegacyYAMLBlocks_WarnsOnSpringBlocks pins the warn-on-ignore behavior:
// a loaded config file carrying a top-level Spring/JVM block (logging:/server:/spring:) --
// the shape a customer-mounted Kotlin-era runner-config.yml still has -- now logs one
// warn-and-ignore line per block (docs/DEPRECATIONS.md §4), rather than yaml.Unmarshal
// silently dropping it.
func TestWarnIgnoredLegacyYAMLBlocks_WarnsOnSpringBlocks(t *testing.T) {
	dir := t.TempDir()
	base := writeFile(t, dir, `
name: from-base
logging:
  level:
    root: INFO
server:
  port: 8080
spring:
  profiles:
    active: kubernetes
`)

	l := NewLoader()
	var cfg testConfig
	_, err := l.Load(base, &cfg)
	require.NoError(t, err)

	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	l.WarnIgnoredLegacyYAMLBlocks(log)

	out := buf.String()
	assert.Contains(t, out, "ignoring unsupported legacy config block")
	assert.Contains(t, out, "block=logging")
	assert.Contains(t, out, "block=server")
	assert.Contains(t, out, "block=spring")
	assert.Contains(t, out, DeprecationDoc)
	// It is warn-only, not fail-fast: Load still succeeds and the recognized keys decode.
	assert.Equal(t, "from-base", cfg.Name)
}

// TestWarnIgnoredLegacyYAMLBlocks_SilentWhenAbsent confirms a clean config (no Spring/JVM
// block) warns nothing -- the check must not fire on ordinary config.
func TestWarnIgnoredLegacyYAMLBlocks_SilentWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	base := writeFile(t, dir, "name: clean\n")

	l := NewLoader()
	var cfg testConfig
	_, err := l.Load(base, &cfg)
	require.NoError(t, err)

	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	l.WarnIgnoredLegacyYAMLBlocks(log)

	assert.Empty(t, buf.String(), "a config with no Spring/JVM blocks must warn nothing")
}
