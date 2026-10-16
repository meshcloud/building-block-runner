package manual

import (
	"context"
	"fmt"
	"log/slog"
	"uuid"

	"github.com/meshcloud/meshstack-cli/client"
)

const (
	stepId          = "manual"
	stepDisplayName = "Manual Block Run"
	succeeded       = "SUCCEEDED"
)

func Execute(ctx context.Context, run client.MeshRunnerRun, api client.MeshRunnerRunClient, sourceId uuid.UUID) error {
	runId := run.Metadata.Uuid
	err := api.RegisterSource(ctx, client.MeshRunnerRunSourceRegistration{
		Source: client.MeshRunnerRunSource{Id: sourceId},
		Steps:  []client.MeshRunnerRunStepRegistration{{Id: stepId, DisplayName: stepDisplayName}},
	})
	if err != nil {
		return fmt.Errorf("register as source of run %s: %w", runId, err)
	}

	_, err = api.PatchStatus(ctx, sourceId, client.MeshRunnerRunStatusUpdate{
		Status: new(succeeded),
		Steps: []client.MeshRunnerRunStepStatusUpdate{{
			Id:          stepId,
			DisplayName: stepDisplayName,
			Status:      new(succeeded),
			Outputs:     outputsFrom(run.Spec.BuildingBlock.Spec.Inputs),
		}},
	})
	if err != nil {
		return fmt.Errorf("report run %s: %w", runId, err)
	}

	slog.InfoContext(ctx, "reported run as succeeded")
	return nil
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
