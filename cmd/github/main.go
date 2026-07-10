// Command github is the github-block-runner persona binary (D1/D8): a fit binary linking
// only the github handler and the polling dispatcher (no k8s, no go-git, no tofu). With
// EXECUTION_MODE=single-run (or SPRING_PROFILES_ACTIVE=kubernetes) it runs one run from a
// mounted file and exits; otherwise it polls the meshfed API in-process. The same handler is
// also registered in the cmd/bbrunner superset (bbrunner github). Wiring only (package main,
// D11).
package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/meshcloud/building-block-runner/internal/build"
	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/github"
	"github.com/meshcloud/building-block-runner/internal/meshapi"
)

func main() {
	os.Exit(run())
}

func run() int {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	log.Info("starting github-block-runner", "version", build.Version)

	singleRun := config.SingleRunMode(log)

	cfg, err := github.LoadConfig(log, build.Version, singleRun)
	if err != nil {
		log.Error("cannot read config", "err", err)
		return 1
	}

	id := meshapi.Identity{Name: "github-block-runner", Version: cfg.Version}

	if singleRun {
		log.Info("running in single-run mode")
		return github.RunSingleRun(context.Background(), log, cfg, id)
	}

	log.Info("running in polling mode")
	return runPolling(log, cfg, id)
}
