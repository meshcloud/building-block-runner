package gitlab

import (
	"context"
	"errors"
	"fmt"

	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/executor"
)

func init() {
	executor.Register(config.RunnerTypeGitLab, func(struct{}) (executor.Executor, error) { return Execute, nil })
}

func Execute(ctx context.Context, run executor.Run) error {
	step := run.Reporter.Step("gl-trigger", "Trigger GitLab CI/CD")
	if err := run.Reporter.Register(ctx); err != nil {
		return err
	}
	err := step.Run(func() error {
		implementation := run.Spec.Definition.Spec.Implementation.GitlabCiCd
		if implementation == nil {
			return errors.New("the run has no GitLab implementation")
		}
		if err := triggerPipeline(ctx, step, run, *implementation); err != nil {
			return fmt.Errorf("could not trigger the GitLab pipeline: %w", err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	run.Reporter.HandOver()
	return nil
}
