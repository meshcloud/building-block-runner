package main

// main_test.go pins bbrunner's argv routing (PLAN_DETAIL_04 §5 step 2): the default (no
// subcommand) invocation is the controller/superset; a known fit token forces that persona
// in-process; anything else is a usage error with a non-zero exit (P5 — never a silent default).
// The persona identity (run-controller / tf-block-runner) is stamped downstream (internal/tf,
// internal/controller — §4.2), so these tests assert the *routing decision*, not the header bytes
// (those are covered by the internal-package suites and the step-5 container smoke).

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// spyDispatcher builds a dispatcher whose bootstraps record which persona was selected instead of
// booting one. Each bootstrap returns a distinct exit code so the caller can assert the route.
func spyDispatcher(selected *string) (dispatcher, *bytes.Buffer) {
	usage := &bytes.Buffer{}
	return dispatcher{
		controller: func() int { *selected = "controller"; return 10 },
		fit: map[persona]func() int{
			personaTf: func() int { *selected = "tf"; return 11 },
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
			name:         "tf subcommand forces the tf persona in-process",
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
			name:      "trailing garbage after a valid persona is rejected",
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
			require.Equal(t, tc.wantSelected, selected, "wrong persona bootstrap invoked")
			if tc.wantUsage {
				require.NotZero(t, code, "usage errors must exit non-zero")
				out := usage.String()
				require.Contains(t, out, "bbrunner:", "usage error must be actionable")
				require.Contains(t, out, "tf", "usage must list the available fit personas")
			} else {
				require.Empty(t, usage.String(), "a valid route must not print usage")
			}
		})
	}
}

// TestNewDispatcher_WiresRealBootstraps guards that the production wiring keeps the tf fit token
// registered and a controller default set (so a future edit can't drop a persona silently).
func TestNewDispatcher_WiresRealBootstraps(t *testing.T) {
	d := newDispatcher()
	require.NotNil(t, d.controller, "the default controller bootstrap must be wired")
	_, ok := d.fit[personaTf]
	require.True(t, ok, "the tf fit persona subcommand must be registered")
	_, ok = d.fit[personaManual]
	require.True(t, ok, "the manual fit persona subcommand must be registered (phase 6a)")
	_, ok = d.fit[personaGitlab]
	require.True(t, ok, "the gitlab fit persona subcommand must be registered (phase 6b)")
	_, ok = d.fit[personaAzdevops]
	require.True(t, ok, "the azdevops fit persona subcommand must be registered (phase 6c)")
}

// TestDispatcher_UsageListsTokensSorted keeps the usage line deterministic (sorted tokens) so the
// message is stable for operators and does not flake as the fit map grows in phase 6.
func TestDispatcher_UsageListsTokensSorted(t *testing.T) {
	usage := &bytes.Buffer{}
	d := dispatcher{
		controller: func() int { return 0 },
		fit: map[persona]func() int{
			persona("zzz"): func() int { return 0 },
			personaTf:      func() int { return 0 },
		},
		usage: usage,
	}

	require.Equal(t, 2, d.run([]string{"nope"}))
	line := usage.String()
	require.Less(t, strings.Index(line, "tf"), strings.Index(line, "zzz"), "fit tokens must be listed sorted")
}
