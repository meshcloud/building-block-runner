package main

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/meshcloud/building-block-runner/internal/build"
	"github.com/meshcloud/building-block-runner/internal/dispatch"
	meshapi "github.com/meshcloud/building-block-runner/internal/meshapi"
	"github.com/meshcloud/building-block-runner/internal/runmode"
	"github.com/meshcloud/building-block-runner/internal/tf"
)

const (
	ENV_RUN_JSON_FILE_PATH = runmode.RunJsonFilePathEnv
	ENV_EXECUTION_MODE     = "EXECUTION_MODE"
)

func main() {
	os.Exit(run())
}

func run() int {
	// Runner type identity carried as an attribute — replaces the former "[TF RUNNER]"
	// log prefix; the local-dev-stack readiness marker keys on type=tf-block-runner
	// (cross-repo lock-step, CROSS_REPO_TODO.md).
	logger := runmode.NewLogger().With("type", "tf-block-runner")

	cfg, err := tf.ReadConfig(logger)
	if err != nil {
		logger.Error("cannot read config", "error", err)
		return 1
	}

	// tf detects single-run mode via EXECUTION_MODE only -- unlike the four (github/gitlab/
	// azdevops/manual), it has no SPRING_PROFILES_ACTIVE=kubernetes trigger to preserve.
	singleRun := isSingleRunMode()

	tfBin, err := tf.NewTfBin(cfg.TfInstallDir, os.Stdout)
	if err != nil {
		logger.Error("failed to init tf binary provider", "error", err)
		return 1
	}

	return runmode.Main(singleRun, runmode.Runner{
		Name:    "tf-block-runner",
		Version: build.Version,
		Log:     logger,
		SingleRun: func(ctx context.Context) int {
			return executeSingleRun(ctx, logger, cfg, tfBin)
		},
		Poll: func(ctx context.Context) int {
			return runPolling(ctx, logger, cfg, tfBin)
		},
	})
}

func isSingleRunMode() bool {
	mode := os.Getenv(ENV_EXECUTION_MODE)
	return mode == "single-run"
}

// executeSingleRun drives one single-run execution via tf.Handler.Execute (the same handler
// polling uses), through the shared runmode.SingleRunResultFromFile scaffold.
//
// A single-run failure used to always fall through to exit 0, so the k8s
// Job the controller dispatched was reported "succeeded" even when the run never got off the
// ground. Handler.Execute only returns an error for failures before the run's first
// potentially state-mutating step (workdir setup, run registration — see handler.go); once
// tofu init/apply has begun, Execute always returns nil, even on failure. That scoping matters
// operationally: the controller's Job template uses BackoffLimit:1 + RestartPolicy:Never
// (run-controller/controller/kubernetes.go), so a blanket non-zero exit on any failure would make
// k8s re-run a failed terraform run once — a second, automatic APPLY/DESTROY against real
// infrastructure. Re-triggering stateful terraform must stay a deliberate user action, so only
// the pre-flight failure class (which never touched terraform) exits non-zero here.
func executeSingleRun(ctx context.Context, logger *slog.Logger, cfg tf.TfRunnerConfig, tfBin *tf.TfBinaries) int {
	return runmode.SingleRunResultFromFile(ctx, logger, cfg.RunnerUuid, meshapi.RunnerTypeTerraform, func(ctx context.Context, cr dispatch.ClaimedRun) (bool, error) {
		// Only used here for runId/buildingBlock logging and the pre-flight MkdirAll;
		// Handler.Execute re-maps cr.Details internally.
		run, err := tf.RunDTOToInternal(cr.Details)
		if err != nil {
			return false, err
		}

		runLog := logger.With("runId", run.Id)
		runLog.Info("Executing single run", "buildingBlock", run.BuildingBlockName)

		// The engine (Worker.tfExecution) does not MkdirAll the parent working dir; the
		// polling path relies on NewDispatchRunner's MkdirAll (dispatchrunner.go), so
		// single-run must create it itself here (preserves the old ExecuteRun MkdirAll /
		// "creates workerDir if missing").
		if err := os.MkdirAll(cfg.TfParentWorkingDir, 0o777); err != nil {
			runLog.Error("Failed to create working dir", "error", err)
			return false, err
		}

		sm := &successMeter{}
		handler := tf.NewHandler(tf.HandlerConfig{
			WorkingDir:            cfg.TfParentWorkingDir,
			TfCommandTimeout:      time.Duration(cfg.TfCommandTimeoutMins) * time.Minute,
			InitTimeout:           time.Duration(cfg.InitTimeoutMins) * time.Minute,
			WsTimeout:             time.Duration(cfg.WsTimeoutMins) * time.Minute,
			RunnerUuid:            cfg.RunnerUuid,
			ApiBackend:            cfg.RunApiBackend,
			SkipHostKeyValidation: cfg.SkipHostKeyValidation,
		}, tf.HandlerDeps{
			TfBinaries: tfBin,
			Meter:      sm,
			Log:        runLog,
		})

		execErr := handler.Execute(ctx, cr)
		// success reflects a real terminal status (RunSucceeded fired via sm), not execErr:
		// execErr is pre-flight-only (see the doc comment above). runmode's
		// InstrumentSingleRunResult meters and pushes this on its own fresh runner_*
		// registry (PUT-on-fail, PUT+DELETE-on-success).
		return sm.succeeded, execErr
	})
}
