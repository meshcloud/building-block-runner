package azuredevops

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"

	"github.com/meshcloud/meshstack-cli/client"

	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/executor"
)

func init() {
	executor.Register(config.RunnerTypeAzureDevOps, func(struct{}) (executor.Executor, error) { return Execute, nil })
}

func Execute(ctx context.Context, run executor.Run) error {
	trigger := run.Reporter.Step("azure-devops-trigger", "Trigger Azure DevOps Pipeline")
	if err := run.Reporter.Register(ctx); err != nil {
		return err
	}
	implementation := run.Spec.Definition.Spec.Implementation.AzureDevops
	if implementation == nil {
		return errors.New("the run has no Azure DevOps implementation")
	}
	err := trigger.Run(func() error {
		ado, started, err := triggerPipeline(ctx, run.MeshRunnerRun, *implementation)
		if err != nil {
			trigger.UserLogf("Could not trigger the Azure DevOps Pipeline")
			trigger.Logf("%v", err)
			return err
		}
		trigger.Logf("Triggered pipeline run %d. View run: %s", started.Id, started.webUrl())
		if implementation.Async {
			trigger.UserLogf("Triggered Azure DevOps Pipeline. Will wait for API updates on status...")
			run.Reporter.HandOver()
			return nil
		}
		trigger.UserLogf("Triggered Azure DevOps Pipeline. Polling for completion status...")
		return newPoller(ado, run.Reporter, trigger).await(ctx, started)
	})
	if ctx.Err() != nil {
		trigger.SetStatus(executor.StatusAborted)
	}
	return err
}

func triggerPipeline(ctx context.Context, run client.MeshRunnerRun, implementation client.MeshRunnerAzureDevopsImplementation) (*azureDevOps, pipelineRun, error) {
	ado, err := newAzureDevOps(implementation)
	if err != nil {
		return nil, pipelineRun{}, err
	}
	params, err := templateParameters(run)
	if err != nil {
		return nil, pipelineRun{}, err
	}
	request := triggerRequest{TemplateParameters: params}
	if implementation.RefName != nil {
		request.Resources = &resources{}
		request.Resources.Repositories.Self.RefName = *implementation.RefName
	}
	started, err := ado.trigger(ctx, request)
	return ado, started, err
}

// templateParameters leaves out the environment inputs, as a pipeline has no environment to receive them.
func templateParameters(run client.MeshRunnerRun) (map[string]string, error) {
	params := map[string]string{}
	for _, input := range run.Spec.BuildingBlock.Spec.Inputs {
		if input.Env {
			continue
		}
		value, err := parameterValue(input.Value)
		if err != nil {
			return nil, fmt.Errorf("input %s: %w", input.Key, err)
		}
		params[input.Key] = value
	}
	params["MESHSTACK_BEHAVIOR"] = run.Spec.Behavior
	return params, nil
}

func parameterValue(value any) (string, error) {
	switch v := value.(type) {
	case nil:
		return "", nil
	case string:
		return v, nil
	}
	encoded, err := json.Marshal(value)
	return string(encoded), err
}
