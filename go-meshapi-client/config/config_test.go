package config

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testConfig struct {
	Name    string        `yaml:"name"`
	Enabled bool          `yaml:"enabled"`
	Retries int           `yaml:"retries"`
	Nested  testNested    `yaml:"nested"`
	Api     testAliasedAP `yaml:"api"`
}

type testNested struct {
	A string `yaml:"a"`
	B string `yaml:"b"`
}

// testAliasedAP exercises interpolation on a nested field.
type testAliasedAP struct {
	Url string `yaml:"url"`
}

func writeFile(t *testing.T, dir, name, contents string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))
	return path
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError + 1}))
}

func TestLoad_TwoFileDeepMerge(t *testing.T) {
	dir := t.TempDir()
	base := writeFile(t, dir, "base.yml", `
name: base-name
enabled: false
retries: 3
nested:
  a: base-a
  b: base-b
`)
	impl := writeFile(t, dir, "impl.yml", `
name: impl-name
nested:
  a: impl-a
`)

	l := NewLoader()
	var cfg testConfig
	found, err := l.Load(base, impl, &cfg)
	require.NoError(t, err)
	assert.True(t, found)

	// per-impl overrides the base key-wise...
	assert.Equal(t, "impl-name", cfg.Name)
	assert.Equal(t, "impl-a", cfg.Nested.A)
	// ...while keys absent from the per-impl layer inherit the base (nested merge, not
	// wholesale replacement of the "nested" map).
	assert.Equal(t, "base-b", cfg.Nested.B)
	assert.False(t, cfg.Enabled)
	assert.Equal(t, 3, cfg.Retries)
}

func TestLoad_BaseOnly(t *testing.T) {
	dir := t.TempDir()
	base := writeFile(t, dir, "base.yml", "name: only-base\nretries: 7\n")

	l := NewLoader()
	var cfg testConfig
	found, err := l.Load(base, filepath.Join(dir, "does-not-exist.yml"), &cfg)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "only-base", cfg.Name)
	assert.Equal(t, 7, cfg.Retries)
}

func TestLoad_PerImplOnly(t *testing.T) {
	dir := t.TempDir()
	impl := writeFile(t, dir, "impl.yml", "name: only-impl\n")

	l := NewLoader()
	var cfg testConfig
	found, err := l.Load(filepath.Join(dir, "does-not-exist.yml"), impl, &cfg)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "only-impl", cfg.Name)
}

func TestLoad_NeitherLayerExists_NotFatal(t *testing.T) {
	dir := t.TempDir()
	cfg := testConfig{Name: "compiled-in-default"}

	l := NewLoader()
	found, err := l.Load(filepath.Join(dir, "a.yml"), filepath.Join(dir, "b.yml"), &cfg)
	require.NoError(t, err)
	assert.False(t, found)
	// compiled-in defaults survive untouched -- Load never zeroes `into` when nothing
	// on disk applies.
	assert.Equal(t, "compiled-in-default", cfg.Name)
}

func TestLoad_EmptyPerImplPath_SkipsLayer(t *testing.T) {
	dir := t.TempDir()
	base := writeFile(t, dir, "base.yml", "name: base-only\n")

	l := NewLoader()
	var cfg testConfig
	found, err := l.Load(base, "", &cfg)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "base-only", cfg.Name)
}

func TestLoad_PreservesCompiledInDefaultsForAbsentKeys(t *testing.T) {
	dir := t.TempDir()
	base := writeFile(t, dir, "base.yml", "name: from-yaml\n")

	l := NewLoader()
	cfg := testConfig{Retries: 42} // compiled-in default, no yaml key for it
	found, err := l.Load(base, "", &cfg)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "from-yaml", cfg.Name)
	assert.Equal(t, 42, cfg.Retries, "compiled-in default must survive a yaml layer that doesn't mention the key")
}

func TestLoad_InvalidYaml_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	base := writeFile(t, dir, "base.yml", "name: [unterminated\n")

	l := NewLoader()
	var cfg testConfig
	_, err := l.Load(base, "", &cfg)
	require.Error(t, err)
}

func TestLoad_PerImplInvalidYaml_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	base := writeFile(t, dir, "base.yml", "name: ok\n")
	impl := writeFile(t, dir, "impl.yml", "name: [unterminated\n")

	l := NewLoader()
	var cfg testConfig
	_, err := l.Load(base, impl, &cfg)
	require.Error(t, err)
}

func TestLoad_DecodeError_TargetTypeMismatch(t *testing.T) {
	dir := t.TempDir()
	// `retries` is a string here but the target struct's field is an int -- yaml.v2
	// refuses that coercion, exercising decodeMap's error path.
	base := writeFile(t, dir, "base.yml", "retries: not-a-number\n")

	l := NewLoader()
	var cfg testConfig
	_, err := l.Load(base, "", &cfg)
	require.Error(t, err)
}

func TestLoad_ReadError_NotAFile(t *testing.T) {
	// A directory at the config path fails with a non-ENOENT error, exercising
	// readLayer's "real read error" branch (distinct from "file does not exist").
	dir := t.TempDir()
	l := NewLoader()
	var cfg testConfig
	_, err := l.Load(dir, "", &cfg)
	require.Error(t, err)
}

func TestLoad_Interpolation_ResolvesSetVar(t *testing.T) {
	t.Setenv("TEST_INTERP_URL", "https://interpolated.example")
	dir := t.TempDir()
	base := writeFile(t, dir, "base.yml", "api:\n  url: ${TEST_INTERP_URL}\n")

	l := NewLoader()
	var cfg testConfig
	_, err := l.Load(base, "", &cfg)
	require.NoError(t, err)
	assert.Equal(t, "https://interpolated.example", cfg.Api.Url)
}

func TestLoad_Interpolation_UnsetVarResolvesEmpty(t *testing.T) {
	require.NoError(t, os.Unsetenv("TEST_INTERP_UNSET_XYZ"))
	dir := t.TempDir()
	base := writeFile(t, dir, "base.yml", "name: ${TEST_INTERP_UNSET_XYZ}\n")

	l := NewLoader()
	var cfg testConfig
	_, err := l.Load(base, "", &cfg)
	require.NoError(t, err)
	assert.Empty(t, cfg.Name)
}

func TestLoad_Interpolation_MarksVarConsumedEvenWhenUnset(t *testing.T) {
	require.NoError(t, os.Unsetenv("BLOCKRUNNER_SOME_LEGACY_KEY"))
	dir := t.TempDir()
	base := writeFile(t, dir, "base.yml", "name: ${BLOCKRUNNER_SOME_LEGACY_KEY}\n")

	l := NewLoader()
	var cfg testConfig
	_, err := l.Load(base, "", &cfg)
	require.NoError(t, err)

	// Set the var now (simulating an operator setting it) and confirm the fail-fast
	// guard does not flag it: the ${VAR} reference in the YAML is the consumption.
	t.Setenv("BLOCKRUNNER_SOME_LEGACY_KEY", "anything")
	assert.NoError(t, l.FailOnUnconsumedLegacyEnv("BLOCKRUNNER_"))
}
