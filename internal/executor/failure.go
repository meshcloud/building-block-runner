package executor

import (
	"context"
	"fmt"

	"github.com/meshcloud/meshstack-cli/client"
)

const (
	failureStepId          = "validation"
	failureStepDisplayName = "Validation"
)

func ReportFailure(ctx context.Context, api client.MeshBuildingBlockRunClient, runUuid, sourceId, message string) error {
	err := api.RegisterSource(ctx, runUuid, client.MeshBuildingBlockRunSourceRegistration{
		Source: client.MeshBuildingBlockRunSource{Id: sourceId},
		Steps:  []client.MeshBuildingBlockRunStepRegistration{{Id: failureStepId, DisplayName: failureStepDisplayName}},
	})
	if err != nil {
		return fmt.Errorf("register as source of the run: %w", err)
	}
	_, err = api.UpdateSourceSteps(ctx, runUuid, sourceId, client.MeshBuildingBlockRunStatusUpdate{
		Status: new(string(StatusFailed)),
		Steps: []client.MeshBuildingBlockRunStepStatusUpdate{{
			Id:            failureStepId,
			DisplayName:   failureStepDisplayName,
			Status:        new(string(StatusFailed)),
			UserMessage:   new(message),
			SystemMessage: new(message),
		}},
	})
	if err != nil {
		return fmt.Errorf("report that the run failed: %w", err)
	}
	return nil
}
