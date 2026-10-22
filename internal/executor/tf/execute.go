package tf

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/meshcloud/meshstack-cli/client"

	"github.com/meshcloud/building-block-runner/internal/config"
	tfconfig "github.com/meshcloud/building-block-runner/internal/config/tf"
	"github.com/meshcloud/building-block-runner/internal/dispatcher"
	"github.com/meshcloud/building-block-runner/internal/executor"
)

func init() { executor.Register(config.RunnerTypeTf, newExecutor) }

func newExecutor(cfg tfconfig.Config) (executor.Executor, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	tfBinaries, err := NewTfBin(filepath.Join(cfg.CacheDir, "bin"), cfg.StopGrace.Duration, slog.NewLogLogger(slog.Default().Handler(), slog.LevelInfo).Writer())
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, run executor.Run) error {
		return Execute(ctx, run, cfg, tfBinaries)
	}, nil
}

func Execute(ctx context.Context, executorRun executor.Run, cfg tfconfig.Config, tfBinaries *TfBinaries) error {
	run, err := newRun(executorRun)
	if err != nil {
		return err
	}
	runDir, err := os.Getwd()
	if err != nil {
		return err
	}
	r := run.Reporter
	steps := declareSteps(r, run.terraform().Async, run.behavior)
	source := &GitSource{repository: run.terraform(), runDir: runDir, step: steps.sources}
	tfCommand := newTfCmd(GenericTfCmd{
		ctx:                  ctx,
		run:                  run,
		cfg:                  cfg,
		source:               source,
		moduleDir:            source.ModuleDir(),
		useWorkspaces:        true,
		keycloakWifTokenFile: os.Getenv(dispatcher.EnvKeycloakWifToken),
		bin:                  tfBinaries,
		reporter:             r,
		steps:                steps,
	}, run.behavior, run.Api)

	if err := r.Register(ctx); err != nil {
		return err
	}
	if run.terraform().Async {
		r.HandOver()
	}
	slog.InfoContext(ctx, "executing the tf run", "buildingBlock", run.Spec.BuildingBlock.Spec.DisplayName)
	return tfCommand.execute()
}

func newTfCmd(generic GenericTfCmd, behavior Behavior, artifacts client.MeshRunnerRunClient) TfCmd {
	switch behavior {
	case behaviorDetect:
		return &TfPlanCommand{GenericTfCmd: generic, artifacts: artifacts}
	case behaviorDestroy:
		return &TfDestroyCommand{GenericTfCmd: generic}
	default:
		return &TfApplyCommand{GenericTfCmd: generic, artifacts: artifacts}
	}
}
