package report

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/go-meshapi-client/meshapi"
)

func TestToStatusUpdate_RejectsMissingRunId(t *testing.T) {
	_, err := ToStatusUpdate(RunStatus{}, "runner-1", meshapi.RunTypeTerraform)
	assert.Error(t, err)
}

func TestToStatusUpdate_MapsTopLevelFields(t *testing.T) {
	summary := "all good"
	s := RunStatus{
		RunId:   "run-1",
		Status:  SUCCEEDED,
		Summary: &summary,
	}

	dto, err := ToStatusUpdate(s, "runner-1", meshapi.RunTypeTerraform)
	require.NoError(t, err)

	assert.Equal(t, "run-1", dto.BlockRunId)
	assert.Equal(t, "runner-1", dto.Source)
	assert.Equal(t, meshapi.RunTypeTerraform, dto.Type)
	require.NotNil(t, dto.Status)
	assert.Equal(t, "SUCCEEDED", *dto.Status)
	assert.Same(t, &summary, dto.Summary)
	assert.Empty(t, dto.Steps)
	assert.Empty(t, dto.Artifact)
}

func TestToStatusUpdate_MapsStepsAndOutputs(t *testing.T) {
	userMsg := "running init"
	sysMsg := "terraform init\n...\n"
	s := RunStatus{
		RunId:  "run-1",
		Status: IN_PROGRESS,
		Steps: []StepStatus{
			{
				Name:          "init",
				DisplayName:   "Init",
				Status:        SUCCEEDED,
				UserMessage:   &userMsg,
				SystemMessage: &sysMsg,
				Outputs: map[string]Output{
					"secret": {Value: "shh", Type: "string", Sensitive: true},
				},
			},
			{Name: "apply", DisplayName: "Apply", Status: PENDING},
		},
	}

	dto, err := ToStatusUpdate(s, "runner-1", meshapi.RunTypeTerraform)
	require.NoError(t, err)
	require.Len(t, dto.Steps, 2)

	init := dto.Steps[0]
	assert.Equal(t, "init", init.Id)
	assert.Equal(t, "Init", init.DisplayName)
	require.NotNil(t, init.Status)
	assert.Equal(t, "SUCCEEDED", *init.Status)
	assert.Same(t, &userMsg, init.UserMessage)
	assert.Same(t, &sysMsg, init.SystemMessage)
	require.Contains(t, init.Outputs, "secret")
	assert.Equal(t, meshapi.OutputDTO{Value: "shh", Type: "string", Sensitive: true}, init.Outputs["secret"])

	apply := dto.Steps[1]
	assert.Equal(t, "apply", apply.Id)
	assert.Empty(t, apply.Outputs)
}

func TestToStatusUpdate_EncodesArtifactAsBase64(t *testing.T) {
	s := RunStatus{RunId: "run-1", Status: SUCCEEDED, Artifact: []byte("plan-bytes")}

	dto, err := ToStatusUpdate(s, "runner-1", meshapi.RunTypeTerraform)
	require.NoError(t, err)

	assert.Equal(t, "cGxhbi1ieXRlcw==", dto.Artifact)
}

func TestToStatusUpdate_UsesTheGivenRunType(t *testing.T) {
	s := RunStatus{RunId: "run-1", Status: SUCCEEDED}

	dto, err := ToStatusUpdate(s, "runner-1", meshapi.RunTypeManual)
	require.NoError(t, err)

	assert.Equal(t, meshapi.RunTypeManual, dto.Type)
}
