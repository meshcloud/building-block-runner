package tf

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/meshcloud/meshstack-cli/client"

	"github.com/meshcloud/building-block-runner/v2/internal/config"
	tfconfig "github.com/meshcloud/building-block-runner/v2/internal/config/tf"
	"github.com/meshcloud/building-block-runner/v2/internal/dispatcher"
	"github.com/meshcloud/building-block-runner/v2/internal/executor"
)

func init() { executor.Register(config.RunnerTypeTf, newExecutor) }

func newExecutor(cfg tfconfig.Config) (executor.Executor, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	tfBinaries, err := NewBinaries(filepath.Join(cfg.CacheDir, "bin"), cfg.AbortGrace.Duration)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, run executor.Run) error {
		return Execute(ctx, run, cfg, tfBinaries)
	}, nil
}

func Execute(ctx context.Context, executorRun executor.Run, cfg tfconfig.Config, tfBinaries Binaries) error {
	run, err := newRun(executorRun)
	if err != nil {
		return err
	}
	runDir, err := os.Getwd()
	if err != nil {
		return err
	}
	r := run.Reporter
	async := run.terraform().Async
	steps := declareSteps(r, run.behavior, async)
	source := GitSource{repository: run.terraform(), runDir: runDir, step: steps.sources}
	tfCommand := newCmd(GenericCmd{
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
	slog.InfoContext(ctx, "executing the tf run", "buildingBlock", run.Spec.BuildingBlock.Spec.DisplayName, "async", async)
	if async {
		steps.sources.Logf("WARN: asynchronous Terraform is an advanced option for custom runners and workarounds. Prefer synchronous runs. For an asynchronous pipeline, use a GitHub workflow, Azure DevOps pipeline or GitLab pipeline implementation.")
	}
	return tfCommand.execute()
}

func newCmd(generic GenericCmd, behavior Behavior, artifacts client.MeshRunnerRunClient) Cmd {
	switch behavior {
	case behaviorDetect:
		return &PlanCommand{GenericCmd: generic, artifacts: artifacts}
	case behaviorDestroy:
		return &DestroyCommand{GenericCmd: generic}
	default:
		return &ApplyCommand{GenericCmd: generic, artifacts: artifacts}
	}
}
