package manual

import (
	"context"
	"log/slog"

	"github.com/meshcloud/meshstack-cli/client"
	"github.com/meshcloud/meshstack-cli/client/types/enum"

	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/executor"
)

func init() {
	executor.Register(config.RunnerTypeManual, func(struct{}) (executor.Executor, error) { return Execute, nil })
}

func Execute(ctx context.Context, run executor.Run) error {
	step := run.Reporter.Step("manual", "Manual Block Run")
	if err := run.Reporter.Register(ctx); err != nil {
		return err
	}
	return step.Run(func() error {
		step.SetOutputs(outputsFrom(run.Spec.BuildingBlock.Spec.Inputs))
		slog.InfoContext(ctx, "the manual run echoes its inputs as outputs", "count", len(run.Spec.BuildingBlock.Spec.Inputs))
		return nil
	})
}

func outputsFrom(inputs []client.MeshRunnerRunInput) map[string]client.MeshRunnerRunOutput {
	outputs := make(map[string]client.MeshRunnerRunOutput, len(inputs))
	for _, input := range inputs {
		outputs[input.Key] = client.MeshRunnerRunOutput{
			Value:     input.Value,
			Type:      outputType(input.Type),
			Sensitive: input.IsSensitive,
		}
	}
	return outputs
}

func outputType(inputType enum.Entry[client.MeshBuildingBlockIOType]) string {
	switch inputType {
	case client.MeshBuildingBlockIOTypeFile, client.MeshBuildingBlockIOTypeSingleSelect:
		return client.MeshBuildingBlockIOTypeString.String()
	case client.MeshBuildingBlockIOTypeList, client.MeshBuildingBlockIOTypeMultiSelect:
		return client.MeshBuildingBlockIOTypeCode.String()
	default:
		return inputType.String()
	}
}
