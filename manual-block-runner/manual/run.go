package manual

import (
	"context"
	"fmt"
	"log/slog"
	"uuid"

	"github.com/meshcloud/building-block-runner/internal/http/meshapi"
)

const (
	stepId          = "manual"
	stepDisplayName = "Manual Block Run"
	succeeded       = "SUCCEEDED"
)

func Execute(ctx context.Context, run meshapi.MeshRunnerRun, api meshapi.MeshRunnerRunClient, sourceId uuid.UUID) error {
	runId := run.Metadata.Uuid
	err := api.RegisterSource(ctx, meshapi.MeshRunnerRunSourceRegistration{
		Source: meshapi.MeshRunnerRunSource{Id: sourceId},
		Steps:  []meshapi.MeshRunnerRunStepRegistration{{Id: stepId, DisplayName: stepDisplayName}},
	})
	if err != nil {
		return fmt.Errorf("register as source of run %s: %w", runId, err)
	}

	_, err = api.PatchStatus(ctx, sourceId, meshapi.MeshRunnerRunStatusUpdate{
		Status: new(succeeded),
		Steps: []meshapi.MeshRunnerRunStepStatusUpdate{{
			Id:          stepId,
			DisplayName: stepDisplayName,
			Status:      new(succeeded),
			Outputs:     outputsFrom(run.Spec.BuildingBlock.Spec.Inputs),
		}},
	})
	if err != nil {
		return fmt.Errorf("report run %s: %w", runId, err)
	}

	slog.Info("reported run as succeeded", "run", runId)
	return nil
}

func outputsFrom(inputs []meshapi.MeshRunnerRunInput) map[string]meshapi.MeshRunnerRunOutput {
	outputs := make(map[string]meshapi.MeshRunnerRunOutput, len(inputs))
	for _, input := range inputs {
		outputs[input.Key] = meshapi.MeshRunnerRunOutput{
			Value:     input.Value,
			Type:      outputType(input.Type),
			Sensitive: input.IsSensitive,
		}
	}
	return outputs
}

func outputType(inputType string) string {
	switch inputType {
	case "FILE", "SINGLE_SELECT":
		return "STRING"
	case "LIST", "MULTI_SELECT":
		return "CODE"
	default:
		return inputType
	}
}
