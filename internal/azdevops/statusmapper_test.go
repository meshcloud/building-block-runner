package azdevops

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/meshcloud/building-block-runner/internal/report"
)

func resultPtr(r pipelineRunResult) *pipelineRunResult { return &r }

// Test_MapPipelineResult is S-P1.
func Test_MapPipelineResult(t *testing.T) {
	cases := []struct {
		name   string
		result *pipelineRunResult
		want   report.ExecutionStatus
	}{
		{"succeeded -> SUCCEEDED", resultPtr(resultSucceeded), report.SUCCEEDED},
		{"failed -> FAILED", resultPtr(resultFailed), report.FAILED},
		{"canceled -> FAILED", resultPtr(resultCanceled), report.FAILED},
		{"unknown -> FAILED", resultPtr(resultUnknownADO), report.FAILED},
		{"nil (absent) -> FAILED", nil, report.FAILED},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, mapPipelineResult(c.result))
		})
	}
}

// Test_MapResultUserMessage is S-P2.
func Test_MapResultUserMessage(t *testing.T) {
	cases := []struct {
		name   string
		result *pipelineRunResult
		want   string
	}{
		{"succeeded", resultPtr(resultSucceeded), "Azure DevOps pipeline completed successfully"},
		{"failed", resultPtr(resultFailed), "Azure DevOps pipeline failed"},
		{"canceled", resultPtr(resultCanceled), "Azure DevOps pipeline was canceled"},
		{"unknown", resultPtr(resultUnknownADO), "Azure DevOps pipeline completed with unknown status"},
		{"nil", nil, "Azure DevOps pipeline completed with unknown status"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, mapResultUserMessage(c.result))
		})
	}
}

func statePtr(s pipelineRunState) *pipelineRunState { return &s }

// Test_MapStateUserMessage is S-P3.
func Test_MapStateUserMessage(t *testing.T) {
	cases := []struct {
		name  string
		state *pipelineRunState
		want  string
	}{
		{"inProgress", statePtr(stateInProgress), "Azure DevOps pipeline is running"},
		{"completed", statePtr(stateCompleted), "Azure DevOps pipeline has completed"},
		{"nil", nil, "Azure DevOps pipeline state is unknown"},
		{"other (canceling)", statePtr(stateCanceling), "Azure DevOps pipeline state: canceling"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, mapStateUserMessage(c.state))
		})
	}
}

// Test_MapStageStatus is S-P4: the full cross-product incl. the else-holes (completed+
// succeededWithIssues and completed+absent -> IN_PROGRESS).
func Test_MapStageStatus(t *testing.T) {
	cases := []struct {
		name   string
		state  timelineRecordState
		result timelineRecordResult
		want   report.ExecutionStatus
	}{
		{"pending", trsPending, "", report.IN_PROGRESS},
		{"inProgress", trsInProgress, "", report.IN_PROGRESS},
		{"completed+succeeded", trsCompleted, trrSucceeded, report.SUCCEEDED},
		{"completed+skipped", trsCompleted, trrSkipped, report.SUCCEEDED},
		{"completed+failed", trsCompleted, trrFailed, report.FAILED},
		{"completed+canceled", trsCompleted, trrCanceled, report.FAILED},
		{"completed+abandoned", trsCompleted, trrAbandoned, report.FAILED},
		{"completed+succeededWithIssues (else-hole)", trsCompleted, trrSucceededWithIssues, report.IN_PROGRESS},
		{"completed+absent result (else-hole)", trsCompleted, "", report.IN_PROGRESS},
		{"absent state", "", "", report.IN_PROGRESS},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, mapStageStatus(c.state, c.result))
		})
	}
}

// Test_MapStageUserMessage is S-P5 incl. the else branch and the null-state message.
func Test_MapStageUserMessage(t *testing.T) {
	cases := []struct {
		name   string
		state  timelineRecordState
		result timelineRecordResult
		want   string
	}{
		{"pending", trsPending, "", "deploy is pending"},
		{"inProgress", trsInProgress, "", "deploy is running"},
		{"completed+succeeded", trsCompleted, trrSucceeded, "deploy completed successfully"},
		{"completed+skipped", trsCompleted, trrSkipped, "deploy was skipped"},
		{"completed+failed", trsCompleted, trrFailed, "deploy failed"},
		{"completed+canceled (else)", trsCompleted, trrCanceled, "deploy: completed"},
		{"completed+abandoned (else)", trsCompleted, trrAbandoned, "deploy: completed"},
		{"absent state", "", "", "deploy is in unknown state"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, mapStageUserMessage("deploy", c.state, c.result))
		})
	}
}

// Test_BuildStageSystemMessage is S-P6: all-fields, result-only, no-times, null-state.
func Test_BuildStageSystemMessage(t *testing.T) {
	cases := []struct {
		name          string
		state         timelineRecordState
		result        timelineRecordResult
		start, finish string
		want          string
	}{
		{"all fields", trsCompleted, trrSucceeded, "2024-01-01T00:00:00Z", "2024-01-01T00:05:00Z",
			"Stage: deploy, State: completed, Result: succeeded, Started: 2024-01-01T00:00:00Z, Finished: 2024-01-01T00:05:00Z"},
		{"result only", trsInProgress, trrSucceeded, "", "",
			"Stage: deploy, State: inProgress, Result: succeeded"},
		{"no times, no result", trsPending, "", "", "",
			"Stage: deploy, State: pending"},
		{"null state", "", "", "", "", "Stage: deploy, State: Unknown"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := buildStageSystemMessage("deploy", c.state, c.result, c.start, c.finish)
			assert.Equal(t, c.want, got)
		})
	}
}
