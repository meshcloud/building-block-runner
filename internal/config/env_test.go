package config

import (
	"bytes"
	"log/slog"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func unsetEnv(t *testing.T, vars ...string) {
	t.Helper()
	for _, v := range vars {
		require.NoError(t, os.Unsetenv(v))
	}
}

func TestPath_NoAliasSet_ReturnsDefault(t *testing.T) {
	unsetEnv(t, "TEST_PATH_PRIMARY", "TEST_PATH_LEGACY")

	l := NewLoader()
	got := l.Path(discardLogger(), "runner-config.yml",
		EnvAlias{Var: "TEST_PATH_PRIMARY"},
		EnvAlias{Var: "TEST_PATH_LEGACY", Deprecated: true},
	)
	assert.Equal(t, "runner-config.yml", got)
}

func TestPath_PrimarySet_Wins(t *testing.T) {
	t.Setenv("TEST_PATH_PRIMARY", "/primary/path.yml")
	t.Setenv("TEST_PATH_LEGACY", "/legacy/path.yml")

	l := NewLoader()
	got := l.Path(discardLogger(), "runner-config.yml",
		EnvAlias{Var: "TEST_PATH_PRIMARY"},
		EnvAlias{Var: "TEST_PATH_LEGACY", Deprecated: true},
	)
	assert.Equal(t, "/primary/path.yml", got)
}

func TestPath_OnlyDeprecatedAliasSet_UsedAndWarns(t *testing.T) {
	unsetEnv(t, "TEST_PATH_PRIMARY")
	t.Setenv("TEST_PATH_LEGACY", "/legacy/path.yml")

	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))

	l := NewLoader()
	got := l.Path(log, "runner-config.yml",
		EnvAlias{Var: "TEST_PATH_PRIMARY"},
		EnvAlias{Var: "TEST_PATH_LEGACY", Deprecated: true},
	)
	assert.Equal(t, "/legacy/path.yml", got)
	assert.Contains(t, buf.String(), "deprecated")
}

func TestPath_AliasesMarkedConsumed(t *testing.T) {
	unsetEnv(t, "TEST_PATH_PRIMARY")

	l := NewLoader()
	l.Path(discardLogger(), "runner-config.yml", EnvAlias{Var: "TEST_PATH_PRIMARY"})
	assert.True(t, l.consumed["TEST_PATH_PRIMARY"])
}

func TestEnv_SetsTarget_AndLogs(t *testing.T) {
	t.Setenv("TEST_ENV_BINDING", "value-from-env")

	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))

	target := "compiled-in"
	l := NewLoader()
	l.Env(log, EnvBinding{Var: "TEST_ENV_BINDING", Target: &target})

	assert.Equal(t, "value-from-env", target)
	assert.Contains(t, buf.String(), "TEST_ENV_BINDING")
}

func TestEnv_UnsetOrEmpty_LeavesTargetUntouched(t *testing.T) {
	unsetEnv(t, "TEST_ENV_BINDING_UNSET")
	t.Setenv("TEST_ENV_BINDING_EMPTY", "")

	target1 := "from-yaml-1"
	target2 := "from-yaml-2"
	l := NewLoader()
	l.Env(discardLogger(),
		EnvBinding{Var: "TEST_ENV_BINDING_UNSET", Target: &target1},
		EnvBinding{Var: "TEST_ENV_BINDING_EMPTY", Target: &target2},
	)

	assert.Equal(t, "from-yaml-1", target1)
	assert.Equal(t, "from-yaml-2", target2)
}

func TestEnv_MarksBindingsConsumedRegardless(t *testing.T) {
	unsetEnv(t, "TEST_ENV_BINDING_NEVER_SET")

	target := ""
	l := NewLoader()
	l.Env(discardLogger(), EnvBinding{Var: "TEST_ENV_BINDING_NEVER_SET", Target: &target})

	assert.True(t, l.consumed["TEST_ENV_BINDING_NEVER_SET"])
}
