package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFailOnUnconsumedLegacyEnv_NoMatchingPrefix_OK(t *testing.T) {
	l := NewLoader()
	assert.NoError(t, l.FailOnUnconsumedLegacyEnv("TEST_NEVER_MATCHES_ANYTHING_"))
}

func TestFailOnUnconsumedLegacyEnv_UnconsumedMatch_Errors(t *testing.T) {
	t.Setenv("BLOCKRUNNER_UUID", "some-uuid")

	l := NewLoader()
	err := l.FailOnUnconsumedLegacyEnv("BLOCKRUNNER_")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "BLOCKRUNNER_UUID")
}

func TestFailOnUnconsumedLegacyEnv_ConsumedViaEnvBinding_OK(t *testing.T) {
	t.Setenv("BLOCKRUNNER_UUID", "some-uuid")

	l := NewLoader()
	target := ""
	l.Env(discardLogger(), EnvBinding{Var: "BLOCKRUNNER_UUID", Target: &target})

	assert.NoError(t, l.FailOnUnconsumedLegacyEnv("BLOCKRUNNER_"))
}

func TestFailOnUnconsumedLegacyEnv_ConsumedViaPath_OK(t *testing.T) {
	t.Setenv("BLOCKRUNNER_CONFIG_FILE", "/some/path.yml")

	l := NewLoader()
	l.Path(discardLogger(), "runner-config.yml", EnvAlias{Var: "BLOCKRUNNER_CONFIG_FILE", Deprecated: true})

	assert.NoError(t, l.FailOnUnconsumedLegacyEnv("BLOCKRUNNER_"))
}

func TestFailOnUnconsumedLegacyEnv_MultipleOffenders_SortedInMessage(t *testing.T) {
	t.Setenv("BLOCKRUNNER_ZOO", "z")
	t.Setenv("BLOCKRUNNER_ALPHA", "a")

	l := NewLoader()
	err := l.FailOnUnconsumedLegacyEnv("BLOCKRUNNER_")
	require.Error(t, err)
	// ALPHA must be reported before ZOO (sorted), so the message is deterministic.
	assert.Regexp(t, "BLOCKRUNNER_ALPHA.*BLOCKRUNNER_ZOO", err.Error())
}

func TestFailOnUnconsumedLegacyEnv_NoPrefixesGiven_OK(t *testing.T) {
	t.Setenv("BLOCKRUNNER_UUID", "some-uuid")

	l := NewLoader()
	assert.NoError(t, l.FailOnUnconsumedLegacyEnv())
}
