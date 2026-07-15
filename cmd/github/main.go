// Command github is the github-block-runner type binary: a fit binary linking
// only the github handler and the polling dispatcher (no k8s, no go-git, no tofu). With
// EXECUTION_MODE=single-run (or SPRING_PROFILES_ACTIVE=kubernetes) it runs one run from a
// mounted file and exits; otherwise it polls the meshfed API in-process. The same handler is
// also registered in the cmd/bbrunner superset (bbrunner github). Wiring only (package main).
package main

import (
	"context"
	"os"

	"github.com/meshcloud/building-block-runner/internal/build"
	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/github"
	"github.com/meshcloud/building-block-runner/internal/meshapi"
	"github.com/meshcloud/building-block-runner/internal/runmode"
)

func main() {
	os.Exit(run())
}

func run() int {
	log := runmode.NewLogger()

	singleRun := config.SingleRunMode(log)

	cfg, err := github.LoadConfig(log, build.Version, singleRun)
	if err != nil {
		log.Error("cannot read config", "err", err)
		return 1
	}

	id := meshapi.Identity{Name: "github-block-runner", Version: cfg.Version}

	return runmode.Main(singleRun, runmode.Runner{
		Name:    "github-block-runner",
		Version: cfg.Version,
		Log:     log,
		SingleRun: func(ctx context.Context) int {
			return github.RunSingleRun(ctx, log, cfg, id)
		},
		Poll: func(ctx context.Context) int {
			return runPolling(ctx, log, cfg, id)
		},
	})
}
