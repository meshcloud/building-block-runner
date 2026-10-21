package executor

import (
	"context"
	"fmt"
	"uuid"

	"github.com/meshcloud/meshstack-cli/client"
)

const (
	failureStepId          = "validation"
	failureStepDisplayName = "Validation"
)

func ReportFailure(ctx context.Context, api client.MeshRunnerRunClient, sourceId uuid.UUID, message string) error {
	err := api.RegisterSource(ctx, client.MeshRunnerRunSourceRegistration{
		Source: client.MeshRunnerRunSource{Id: sourceId},
		Steps:  []client.MeshRunnerRunStepRegistration{{Id: failureStepId, DisplayName: failureStepDisplayName}},
	})
	if err != nil {
		return fmt.Errorf("register as source of the run: %w", err)
	}
	failed := string(StatusFailed)
	_, err = api.PatchStatus(ctx, sourceId, client.MeshRunnerRunStatusUpdate{
		Status: &failed,
		Steps: []client.MeshRunnerRunStepStatusUpdate{{
			Id:            failureStepId,
			DisplayName:   failureStepDisplayName,
			Status:        &failed,
			UserMessage:   &message,
			SystemMessage: &message,
		}},
	})
	if err != nil {
		return fmt.Errorf("report that the run failed: %w", err)
	}
	return nil
}
