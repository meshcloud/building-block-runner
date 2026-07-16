// bbrunner is the single entrypoint shipped for every runner type and the run-controller
// image. There is no subcommand: the linked type set and the run mode are both auto-detected.
//
//   - Linked type set is fixed at build time by the type_<type>/k8s build tags (registry.go /
//     registry_register.go): exactly one type linked (a `type_X` build) => that type's own
//     bootstrap; zero types linked (`-tags k8s`, the lean run-controller image) or all five
//     (no tags, the local-dev superset) => the controller (auto-detects its dispatcher:
//     KubernetesJobDispatcher in-cluster, InProcess superset outside a cluster).
//   - Run mode is auto-detected from RUN_JSON_FILE_PATH (runmode.DetectSingleRun): a mounted,
//     non-empty run file => single-run; unset => polling. Single-run only makes sense on a
//     single-linked-type build (a mounted run file on a multi/zero-type build could never be
//     served unambiguously), so requesting it there is a fail-fast error, not a silent poll.
//
// This is wiring only (package main). Each runner type bootstrap constructs its own
// slog.Logger carrying a runner type attribute (the former [RUN CONTROLLER] / [TF RUNNER]
// prefixes are retired) whether run as the controller or as the one linked type.
package main

import (
	"log/slog"
	"os"

	"github.com/meshcloud/building-block-runner/internal/catrust"
	"github.com/meshcloud/building-block-runner/internal/meshapi"
	"github.com/meshcloud/building-block-runner/internal/runmode"
)

// runnerType is a linked runner type's fit token, keying typeRegistry (registry.go). Its
// value equals the canonical meshapi.Identity.Name that runner type stamps on its runner
// headers (frozen) -- the identity itself is stamped downstream (internal/tf,
// internal/controller), not injected here.
type runnerType string

const (
	runnerTypeTf       runnerType = "tf"       // fit tf-block-runner type
	runnerTypeManual   runnerType = "manual"   // fit manual-block-runner type
	runnerTypeGitlab   runnerType = "gitlab"   // fit gitlab-block-runner type
	runnerTypeAzdevops runnerType = "azdevops" // fit azure-devops-block-runner type
	runnerTypeGithub   runnerType = "github"   // fit github-block-runner type
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	// Wired here, ahead of route: every runner type's outbound HTTPS (RunClient,
	// RunnerClient, ApiKeyAuth) already funnels through meshapi's sharedHTTPClient, so this
	// one call is the single choke point for CUSTOM_CA_CERTS_PATH trust across all builds. A
	// bad mount fails fast rather than silently running with an incomplete trust store.
	pool, err := catrust.RootCAs(logger)
	if err != nil {
		logger.Error("failed to build root CA pool", "error", err)
		os.Exit(1)
	}
	meshapi.ConfigureRootCAs(pool)

	os.Exit(route{
		registry:        typeRegistry,
		controller:      runController,
		detectSingleRun: runmode.DetectSingleRun,
		log:             logger,
	}.run())
}

// route resolves the linked type set + detected run mode to a bootstrap and returns the
// process exit code. Dependencies are injected so the routing is unit-testable without
// booting a real runner type.
type route struct {
	// registry is the build's linked runner types (registry.go), keyed by fit token.
	registry map[runnerType]typeRegistration
	// controller is the 0-or-many-linked-types bootstrap: the run-controller/superset.
	controller func() int
	// detectSingleRun resolves the run mode from RUN_JSON_FILE_PATH.
	detectSingleRun func() (bool, error)
	log             *slog.Logger
}

// run implements the routing described in the package doc above.
func (r route) run() int {
	singleRun, err := r.detectSingleRun()
	if err != nil {
		r.log.Error("failed to detect run mode", "error", err)
		return 1
	}

	if len(r.registry) != 1 {
		// 0 linked (-tags k8s) or 5 linked (untagged superset): both always poll via the
		// controller; a mounted run file can never be served unambiguously there.
		if singleRun {
			r.log.Error("RUN_JSON_FILE_PATH is set but this build links zero or multiple runner types; single-run requires a single-type build")
			return 1
		}
		return r.controller()
	}

	for _, reg := range r.registry {
		if singleRun {
			return reg.singleRunBootstrap()
		}
		return reg.fitBootstrap()
	}
	panic("unreachable: len(r.registry) == 1")
}
