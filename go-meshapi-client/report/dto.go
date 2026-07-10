package report

import (
	"encoding/base64"
	"fmt"
	"time"

	"github.com/meshcloud/building-block-runner/go-meshapi-client/meshapi"
)

// ToStatusUpdate maps a RunStatus onto the wire DTO tf's status PATCH sends, parametrized by
// source (the reporting runner's uuid) and t (the meshapi.RunType) instead of the tfrun
// predecessor's hardcoded RunnerUuid global and RunTypeTerraform constant — the same mapping
// now serves every RunType producing full step output.
func ToStatusUpdate(s RunStatus, source string, t meshapi.RunType) (meshapi.RunStatusUpdateDTO, error) {
	if s.RunId == "" {
		return meshapi.RunStatusUpdateDTO{}, fmt.Errorf("run status has no RunId, cannot build a status update")
	}

	statusStr := s.Status.String()

	var steps []meshapi.StepStatusUpdateDTO
	if len(s.Steps) > 0 {
		steps = make([]meshapi.StepStatusUpdateDTO, len(s.Steps))
		for i, step := range s.Steps {
			var outputs map[string]meshapi.OutputDTO
			if len(step.Outputs) > 0 {
				outputs = make(map[string]meshapi.OutputDTO, len(step.Outputs))
				for k, v := range step.Outputs {
					outputs[k] = meshapi.OutputDTO{
						Value:     v.Value,
						Type:      v.Type,
						Sensitive: v.Sensitive,
					}
				}
			}

			stepStatusStr := step.Status.String()
			steps[i] = meshapi.StepStatusUpdateDTO{
				Id:            step.Name,
				DisplayName:   step.DisplayName,
				Status:        &stepStatusStr,
				UserMessage:   step.UserMessage,
				SystemMessage: step.SystemMessage,
				Outputs:       outputs,
			}
		}
	}

	// Artifact: encode binary plan as base64 if present.
	artifact := ""
	if len(s.Artifact) > 0 {
		artifact = base64.StdEncoding.EncodeToString(s.Artifact)
	}

	return meshapi.RunStatusUpdateDTO{
		BlockRunId: s.RunId,
		Source:     source,
		Type:       t,
		Status:     &statusStr,
		CreatedOn:  time.Now(),
		Summary:    s.Summary,
		Steps:      steps,
		Artifact:   artifact,
	}, nil
}
