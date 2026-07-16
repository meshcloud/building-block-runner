package main

// main_test.go pins bbrunner's no-subcommand routing: the linked type set (registry.go) and
// the detected run mode (runmode.DetectSingleRun) together decide whether a single linked
// type's fit/single-run bootstrap runs, or the controller does. The runner type identity
// (run-controller / tf-block-runner) is stamped downstream (internal/tf, internal/controller),
// so these tests assert the *routing decision*, not the header bytes (those are covered by the
// internal-package suites and the container smoke test).

import (
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"
)

// spyRoute wires a route whose bootstraps record which one was selected instead of booting a
// real runner type. Each bootstrap returns a distinct exit code so tests can assert the route.
func spyRoute(selected *string, registry map[runnerType]typeRegistration, singleRun bool, detectErr error) route {
	return route{
		registry: registry,
		controller: func() int {
			*selected = "controller"
			return 10
		},
		detectSingleRun: func() (bool, error) { return singleRun, detectErr },
		log:             slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func oneTypeRegistry(selected *string) map[runnerType]typeRegistration {
	return map[runnerType]typeRegistration{
		runnerTypeTf: {
			fitBootstrap: func() int {
				*selected = "fit"
				return 11
			},
			singleRunBootstrap: func() int {
				*selected = "singleRun"
				return 12
			},
		},
	}
}

func TestRoute_Run(t *testing.T) {
	tests := []struct {
		name         string
		registry     func(selected *string) map[runnerType]typeRegistration
		singleRun    bool
		detectErr    error
		wantSelected string // "" => no bootstrap invoked
		wantCode     int
	}{
		{
			name:         "single linked type, no run file => fitBootstrap",
			registry:     oneTypeRegistry,
			singleRun:    false,
			wantSelected: "fit",
			wantCode:     11,
		},
		{
			name:         "single linked type, run file present => singleRunBootstrap",
			registry:     oneTypeRegistry,
			singleRun:    true,
			wantSelected: "singleRun",
			wantCode:     12,
		},
		{
			name:         "single linked type, run file set but unreadable => exit 1, no bootstrap invoked",
			registry:     oneTypeRegistry,
			detectErr:    errors.New("run file missing"),
			wantSelected: "",
			wantCode:     1,
		},
		{
			name: "multi/zero linked types => controller",
			registry: func(selected *string) map[runnerType]typeRegistration {
				return map[runnerType]typeRegistration{}
			},
			singleRun:    false,
			wantSelected: "controller",
			wantCode:     10,
		},
		{
			name: "multi/zero linked types with run file set => exit 1, controller never invoked",
			registry: func(selected *string) map[runnerType]typeRegistration {
				return map[runnerType]typeRegistration{}
			},
			singleRun:    true,
			wantSelected: "",
			wantCode:     1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var selected string
			r := spyRoute(&selected, tc.registry(&selected), tc.singleRun, tc.detectErr)

			code := r.run()

			require.Equal(t, tc.wantCode, code)
			require.Equal(t, tc.wantSelected, selected, "wrong bootstrap invoked")
		})
	}
}

// TestRoute_WiresRealRegistry guards that the production wiring keeps every linked runner
// type registered with both bootstraps set, so a future edit can't drop a type silently.
func TestRoute_WiresRealRegistry(t *testing.T) {
	if len(typeRegistry) == 0 {
		t.Skip("k8s controller build (-tags k8s) links no in-process runner types; the superset build wires them")
	}

	for _, token := range []runnerType{runnerTypeTf, runnerTypeManual, runnerTypeGitlab, runnerTypeAzdevops, runnerTypeGithub} {
		reg, ok := typeRegistry[token]
		require.True(t, ok, "runner type %q must be registered", token)
		require.NotNil(t, reg.fitBootstrap, "runner type %q must wire a fitBootstrap", token)
		require.NotNil(t, reg.singleRunBootstrap, "runner type %q must wire a singleRunBootstrap", token)
	}
}
