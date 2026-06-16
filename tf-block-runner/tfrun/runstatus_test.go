package tfrun

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_firstStep_correctlyInitializes(t *testing.T) {
	runStatus := makeTestRunStatus()
	runStatus.firstStep()
	assert.Equal(t, 0, runStatus.CurrentStepIndex, "")
	assert.Equal(t, "step1", runStatus.currentStepStatus().Name)
}

func Test_failRunAndNotFinishedSteps_correctlyFailsThisAndSuccessorSteps(t *testing.T) {
	runStatus := makeTestRunStatus()
	runStatus.firstStep()
	runStatus.currentStepStatus().Status = SUCCEEDED
	runStatus.nextStep()
	runStatus.failRunAndNotFinishedSteps()

	assert.Equal(t, 1, runStatus.CurrentStepIndex, "")
	assert.Equal(t, SUCCEEDED, runStatus.Steps[0].Status)
	assert.Equal(t, FAILED, runStatus.Steps[1].Status)
	assert.Equal(t, FAILED, runStatus.Steps[2].Status)
	assert.Equal(t, "Aborted due to failure in an earlier step", *runStatus.Steps[2].SystemMessage)
}

func makeTestRunStatus() *RunStatus {
	return &RunStatus{
		RunId:            "runId",
		Status:           IN_PROGRESS,
		CurrentStepIndex: -1,
		Summary:          nil,
		Steps: []*StepStatus{
			{
				Name:          "step1",
				DisplayName:   "Step 1",
				Status:        PENDING,
				UserMessage:   nil,
				SystemMessage: nil,
				Outputs:       map[string]*TfOutput{},
				LogStartIdx:   0,
			},
			{
				Name:          "step2",
				DisplayName:   "Step 2",
				Status:        PENDING,
				UserMessage:   nil,
				SystemMessage: nil,
				Outputs:       map[string]*TfOutput{},
				LogStartIdx:   0,
			},
			{
				Name:          "step3",
				DisplayName:   "Step 3",
				Status:        PENDING,
				UserMessage:   nil,
				SystemMessage: nil,
				Outputs:       map[string]*TfOutput{},
				LogStartIdx:   0,
			},
		},
	}
}
