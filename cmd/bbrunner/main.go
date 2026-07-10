package main

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

// bbrunner is the controller/superset entrypoint (D1/D2, PLAN_DETAIL_04 §4.1). It is shipped
// AS the run-controller image and its default invocation (no subcommand) is the controller:
//
//	bbrunner            -> the run-controller/superset controller bootstrap (phase-4:
//	                       KubernetesJobDispatcher only; auto-detect + InProcessDispatcher
//	                       arrive in phase 5)
//	bbrunner <persona>  -> that fit persona forced in-process (local-dev / mux replacement),
//	                       the same wiring the standalone cmd/<persona> binary runs
//	bbrunner <unknown>  -> usage error, non-zero exit (P5 — never a silent default)
//
// This is wiring only (package main, D11-exempt). Each persona bootstrap keeps its own logger
// prefix ([RUN CONTROLLER] / [TF RUNNER], §4.1) whether run as the default controller or forced
// in-process via a subcommand.

// persona is a fit-persona subcommand token. Its value equals the canonical
// meshapi.Identity.Name that persona stamps on its runner headers (frozen, §4.2) — the identity
// itself is stamped downstream (internal/tf, internal/controller), not injected here.
type persona string

const (
	personaTf       persona = "tf"       // fit tf-block-runner persona, forced in-process (local-dev)
	personaManual   persona = "manual"   // fit manual-block-runner persona (phase 6a)
	personaGitlab   persona = "gitlab"   // fit gitlab-block-runner persona (phase 6b)
	personaAzdevops persona = "azdevops" // fit azure-devops-block-runner persona (phase 6c)
	// phase 6: personaGithub.
)

func main() {
	os.Exit(newDispatcher().run(os.Args[1:]))
}

// dispatcher resolves a CLI invocation to a persona bootstrap and returns the process exit code.
// Bootstraps are injected so the routing is unit-testable without booting a real persona.
type dispatcher struct {
	// controller is the default (no-subcommand) bootstrap: the run-controller/superset.
	controller func() int
	// fit maps a subcommand token to that fit persona's in-process bootstrap.
	fit map[persona]func() int
	// usage is where the actionable error for an unknown/misused invocation is written.
	usage io.Writer
}

// newDispatcher wires the real persona bootstraps. Adding a phase-6 fit persona is one entry
// here plus its own cmd/<persona>/main.go (§11).
func newDispatcher() dispatcher {
	return dispatcher{
		controller: runController,
		fit: map[persona]func() int{
			personaTf:       runTfPolling,
			personaManual:   runManualPolling,
			personaGitlab:   runGitlabPolling,
			personaAzdevops: runAzdevopsPolling,
		},
		usage: os.Stderr,
	}
}

func (d dispatcher) run(args []string) int {
	switch len(args) {
	case 0:
		// Default: the auto-detecting controller/superset (phase-4: k8s dispatch only).
		return d.controller()
	case 1:
		if bootstrap, ok := d.fit[persona(args[0])]; ok {
			return bootstrap()
		}
		d.printUsage(fmt.Sprintf("unknown persona %q", args[0]))
		return 2
	default:
		d.printUsage(fmt.Sprintf("unexpected arguments after persona: %v", args[1:]))
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
		fmt.Sprintf("  bbrunner <persona>  force one persona in-process; one of: %s\n", strings.Join(tokens, ", "))
	_, _ = io.WriteString(d.usage, msg)
}
