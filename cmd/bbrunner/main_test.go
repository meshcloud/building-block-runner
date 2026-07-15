package main

// main_test.go pins bbrunner's argv routing: the default (no
// subcommand) invocation is the controller/superset; a known fit token forces that type
// in-process; anything else is a usage error with a non-zero exit (never a silent default).
// The runner type identity (run-controller / tf-block-runner) is stamped downstream (internal/tf,
// internal/controller), so these tests assert the *routing decision*, not the header bytes
// (those are covered by the internal-package suites and the container smoke test).

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// spyDispatcher builds a dispatcher whose bootstraps record which type was selected instead of
// booting one. Each bootstrap returns a distinct exit code so the caller can assert the route.
func spyDispatcher(selected *string) (dispatcher, *bytes.Buffer) {
	usage := &bytes.Buffer{}
	return dispatcher{
		controller: func() int { *selected = "controller"; return 10 },
		fit: map[runnerType]func() int{
			runnerTypeTf: func() int { *selected = "tf"; return 11 },
		},
		usage: usage,
	}, usage
}

func TestDispatcher_Run(t *testing.T) {
	tests := []struct {
		name         string
		args         []string
		wantSelected string // "" => no bootstrap invoked (usage error)
		wantCode     int
		wantUsage    bool
	}{
		{
			name:         "no subcommand runs the controller/superset by default",
			args:         nil,
			wantSelected: "controller",
			wantCode:     10,
		},
		{
			name:         "tf subcommand forces the tf type in-process",
			args:         []string{"tf"},
			wantSelected: "tf",
			wantCode:     11,
		},
		{
			name:      "unknown subcommand is a usage error, not a silent default",
			args:      []string{"manual"},
			wantCode:  2,
			wantUsage: true,
		},
		{
			name:      "trailing garbage after a valid type is rejected",
			args:      []string{"tf", "extra"},
			wantCode:  2,
			wantUsage: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var selected string
			d, usage := spyDispatcher(&selected)

			code := d.run(tc.args)

			require.Equal(t, tc.wantCode, code)
			require.Equal(t, tc.wantSelected, selected, "wrong runner type bootstrap invoked")
			if tc.wantUsage {
				require.NotZero(t, code, "usage errors must exit non-zero")
				out := usage.String()
				require.Contains(t, out, "bbrunner:", "usage error must be actionable")
				require.Contains(t, out, "tf", "usage must list the available runner types")
			} else {
				require.Empty(t, usage.String(), "a valid route must not print usage")
			}
		})
	}
}

// TestNewDispatcher_WiresRealBootstraps guards that the production wiring keeps the tf fit token
// registered and a controller default set (so a future edit can't drop a runner type silently).
func TestNewDispatcher_WiresRealBootstraps(t *testing.T) {
	if len(typeRegistry) == 0 {
		t.Skip("k8s controller build (-tags k8s) links no in-process runner types; the superset build wires them")
	}
	d := newDispatcher()
	require.NotNil(t, d.controller, "the default controller bootstrap must be wired")
	_, ok := d.fit[runnerTypeTf]
	require.True(t, ok, "the tf runner type subcommand must be registered")
	_, ok = d.fit[runnerTypeManual]
	require.True(t, ok, "the manual runner type subcommand must be registered (phase 6a)")
	_, ok = d.fit[runnerTypeGitlab]
	require.True(t, ok, "the gitlab runner type subcommand must be registered (phase 6b)")
	_, ok = d.fit[runnerTypeAzdevops]
	require.True(t, ok, "the azdevops runner type subcommand must be registered (phase 6c)")
	_, ok = d.fit[runnerTypeGithub]
	require.True(t, ok, "the github runner type subcommand must be registered (phase 6d)")
}

// TestDispatcher_UsageListsTokensSorted keeps the usage line deterministic (sorted tokens) so the
// message is stable for operators and does not flake as the fit map grows.
func TestDispatcher_UsageListsTokensSorted(t *testing.T) {
	usage := &bytes.Buffer{}
	d := dispatcher{
		controller: func() int { return 0 },
		fit: map[runnerType]func() int{
			runnerType("zzz"): func() int { return 0 },
			runnerTypeTf:      func() int { return 0 },
		},
		usage: usage,
	}

	require.Equal(t, 2, d.run([]string{"nope"}))
	line := usage.String()
	require.Less(t, strings.Index(line, "tf"), strings.Index(line, "zzz"), "fit tokens must be listed sorted")
}
