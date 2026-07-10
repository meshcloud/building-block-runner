// Command azdevops is the azure-devops-block-runner persona binary (D1/D8): a fit binary
// linking only the azdevops handler and its deps (no k8s, no go-git, no tofu). With
// EXECUTION_MODE=single-run (or SPRING_PROFILES_ACTIVE=kubernetes) it runs one run from a
// mounted file and exits; otherwise it polls the meshfed API in-process. The same handler is
// also registered in the cmd/bbrunner superset (bbrunner azdevops). This is wiring only
// (package main, D11).
package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/meshcloud/building-block-runner/internal/azdevops"
	"github.com/meshcloud/building-block-runner/internal/build"
	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/meshapi"
)

func main() {
	os.Exit(run())
}

func run() int {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	log.Info("starting azure-devops-block-runner", "version", build.Version)

	singleRun := config.SingleRunMode(log)

	cfg, err := azdevops.LoadConfig(log, build.Version, singleRun)
	if err != nil {
		log.Error("cannot read config", "err", err)
		return 1
	}

	id := meshapi.Identity{Name: "azure-devops-block-runner", Version: cfg.Version}

	if singleRun {
		log.Info("running in single-run mode")
		return azdevops.RunSingleRun(context.Background(), log, cfg, id)
	}

	log.Info("running in polling mode")
	return runPolling(log, cfg, id)
}
