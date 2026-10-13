package tfrun

import (
	"context"
	"io"
	"log"
	"time"
	"uuid"

	"github.com/meshcloud/building-block-runner/internal/http/meshapi"
)

const EP_State = "%s/api/terraform/state/workspace/%s/buildingBlock/%s"

type RunApi struct {
	client   meshapi.MeshRunnerRunClient
	runUuid  uuid.UUID
	sourceId uuid.UUID
}

func NewRunApi(client meshapi.MeshRunnerRunClient, runUuid, sourceId uuid.UUID) RunApi {
	return RunApi{client: client, runUuid: runUuid, sourceId: sourceId}
}

func (api RunApi) DownloadPredecessorArtifact(ctx context.Context, w io.Writer) error {
	return api.client.DownloadPlanArtifact(ctx, w)
}

func (api RunApi) UploadArtifact(ctx context.Context, path string) error {
	return api.client.UploadArtifact(ctx, path)
}

func (api RunApi) Register(ctx context.Context, runStatus *RunStatus) error {
	steps := make([]meshapi.MeshRunnerRunStepRegistration, 0, len(runStatus.Steps))
	for _, s := range runStatus.Steps {
		steps = append(steps, meshapi.MeshRunnerRunStepRegistration{Id: s.Name, DisplayName: s.DisplayName})
	}
	err := api.client.RegisterSource(ctx, meshapi.MeshRunnerRunSourceRegistration{
		Source: meshapi.MeshRunnerRunSource{Id: api.sourceId},
		Steps:  steps,
	})
	if err != nil {
		return err
	}
	log.Printf("[RUNNER] Registered source for run %s", api.runUuid)
	return nil
}

func (api RunApi) UpdateState(ctx context.Context, status *RunStatus) (abort bool, err error) {
	result, err := api.client.PatchStatus(ctx, api.sourceId, api.statusUpdate(status))
	return result.Abort, err
}

// statusUpdate sends steps as null when there are none, which keeps the steps meshStack has.
func (api RunApi) statusUpdate(status *RunStatus) meshapi.MeshRunnerRunStatusUpdate {
	var steps []meshapi.MeshRunnerRunStepStatusUpdate
	for _, step := range status.Steps {
		outputs := make(map[string]meshapi.MeshRunnerRunOutput, len(step.Outputs))
		for name, output := range step.Outputs {
			outputs[name] = meshapi.MeshRunnerRunOutput{Value: output.Value, Type: string(output.Type), Sensitive: output.Sensitive}
		}
		steps = append(steps, meshapi.MeshRunnerRunStepStatusUpdate{
			Id:            step.Name,
			DisplayName:   step.DisplayName,
			Status:        new(step.Status.str()),
			UserMessage:   step.UserMessage,
			SystemMessage: step.SystemMessage,
			Outputs:       outputs,
		})
	}
	return meshapi.MeshRunnerRunStatusUpdate{
		BlockRunId:      api.runUuid,
		Source:          api.sourceId,
		Type:            meshapi.MeshRunnerImplementationTypeTerraform,
		Status:          new(status.Status.str()),
		CreatedOn:       time.Now(),
		Summary:         status.Summary,
		Steps:           steps,
		ChangesDetected: status.ChangesDetected,
	}
}
