package main

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

// bbrunner is the controller/superset entrypoint. It is shipped
// AS the run-controller image and its default invocation (no subcommand) is the controller:
//
//	bbrunner            -> the run-controller/superset controller bootstrap (auto-detects its
//	                       dispatcher: KubernetesJobDispatcher in-cluster, InProcess superset
//	                       outside a cluster)
//	bbrunner <type>  -> that runner type forced in-process (local-dev / mux replacement),
//	                       the same wiring the standalone cmd/<type> binary runs
//	bbrunner <unknown>  -> usage error, non-zero exit (never a silent default)
//
// This is wiring only (package main). Each runner type bootstrap constructs its own
// slog.Logger carrying a runner type attribute (the former [RUN CONTROLLER] / [TF RUNNER]
// prefixes are retired) whether run as the default controller or forced in-process via a
// subcommand.

// runnerType is a fit-runner-type subcommand token. Its value equals the canonical
// meshapi.Identity.Name that runner type stamps on its runner headers (frozen) — the identity
// itself is stamped downstream (internal/tf, internal/controller), not injected here.
type runnerType string

const (
	runnerTypeTf       runnerType = "tf"       // fit tf-block-runner type, forced in-process (local-dev)
	runnerTypeManual   runnerType = "manual"   // fit manual-block-runner type
	runnerTypeGitlab   runnerType = "gitlab"   // fit gitlab-block-runner type
	runnerTypeAzdevops runnerType = "azdevops" // fit azure-devops-block-runner type
	runnerTypeGithub   runnerType = "github"   // fit github-block-runner type
)

func main() {
	os.Exit(newDispatcher().run(os.Args[1:]))
}

// dispatcher resolves a CLI invocation to a runner type bootstrap and returns the process exit code.
// Bootstraps are injected so the routing is unit-testable without booting a real runner type.
type dispatcher struct {
	// controller is the default (no-subcommand) bootstrap: the run-controller/superset.
	controller func() int
	// fit maps a subcommand token to that runner type's in-process bootstrap.
	fit map[runnerType]func() int
	// usage is where the actionable error for an unknown/misused invocation is written.
	usage io.Writer
}

// newDispatcher wires the runner type bootstraps from typeRegistry (registry.go): each linked
// type's own tag-gated file (tf.go, manual.go, gitlab.go, azdevops.go, github.go) registers
// itself via an init(), so this only ever sees the types actually linked into the current
// build -- a build tag that excludes a type simply drops it from d.fit, never leaving a
// dangling reference ("leaner run-controller image via build tags").
func newDispatcher() dispatcher {
	fit := make(map[runnerType]func() int, len(typeRegistry))
	for token, reg := range typeRegistry {
		fit[token] = reg.fitBootstrap
	}
	return dispatcher{
		controller: runController,
		fit:        fit,
		usage:      os.Stderr,
	}
}

func (d dispatcher) run(args []string) int {
	switch len(args) {
	case 0:
		// Default: the auto-detecting controller/superset.
		return d.controller()
	case 1:
		if bootstrap, ok := d.fit[runnerType(args[0])]; ok {
			return bootstrap()
		}
		d.printUsage(fmt.Sprintf("unknown runner type %q", args[0]))
		return 2
	default:
		d.printUsage(fmt.Sprintf("unexpected arguments after type: %v", args[1:]))
		return 2
	}
}

func (d dispatcher) printUsage(reason string) {
	tokens := make([]string, 0, len(d.fit))
	for p := range d.fit {
		tokens = append(tokens, string(p))
	}
	sort.Strings(tokens)
	msg := fmt.Sprintf("bbrunner: %s\n", reason) +
		"usage:\n" +
		"  bbrunner            run the controller/superset (default)\n" +
		fmt.Sprintf("  bbrunner <type>  force one runner type in-process; one of: %s\n", strings.Join(tokens, ", "))
	_, _ = io.WriteString(d.usage, msg)
}
