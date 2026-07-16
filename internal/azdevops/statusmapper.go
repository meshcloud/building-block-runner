package azdevops

import (
	"fmt"

	"github.com/meshcloud/building-block-runner/internal/report"
)

// This file is the Go twin of AzureDevOpsStatusMapper.kt: pure functions,
// no state, kept as unit-tested (statusmapper_test.go) per D16's "real decision surface"
// carve-out -- these tables ARE the decision surface, not incidental plumbing.

// mapPipelineResult maps succeeded -> SUCCEEDED; failed, canceled, unknown, or absent
// (nil) -> FAILED.
func mapPipelineResult(result *pipelineRunResult) report.ExecutionStatus {
	if result != nil && *result == resultSucceeded {
		return report.SUCCEEDED
	}
	return report.FAILED
}

func mapResultUserMessage(result *pipelineRunResult) string {
	if result == nil {
		return "Azure DevOps pipeline completed with unknown status"
	}
	switch *result {
	case resultSucceeded:
		return "Azure DevOps pipeline completed successfully"
	case resultFailed:
		return "Azure DevOps pipeline failed"
	case resultCanceled:
		return "Azure DevOps pipeline was canceled"
	default:
		return "Azure DevOps pipeline completed with unknown status"
	}
}

// mapStateUserMessage takes state as a pointer purely to reproduce the Kotlin function's
// nullable parameter for the pin table (mapPipelineStateToUserMessage(state:
// PipelineRunState?)); pipelineRun.State itself is never absent in practice.
func mapStateUserMessage(state *pipelineRunState) string {
	if state == nil {
		return "Azure DevOps pipeline state is unknown"
	}
	switch *state {
	case stateInProgress:
		return "Azure DevOps pipeline is running"
	case stateCompleted:
		return "Azure DevOps pipeline has completed"
	default:
		return fmt.Sprintf("Azure DevOps pipeline state: %s", string(*state))
	}
}

// mapStageStatus includes the else-holes pinned as-is (D13): completed+
// succeededWithIssues and completed+absent(result) both map to IN_PROGRESS -- such a stage
// step never reaches a terminal status. An absent/unrecognized state also maps to
// IN_PROGRESS (tolerant).
func mapStageStatus(state timelineRecordState, result timelineRecordResult) report.ExecutionStatus {
	switch state {
	case trsPending, trsInProgress:
		return report.IN_PROGRESS
	case trsCompleted:
		switch result {
		case trrSucceeded, trrSkipped:
			return report.SUCCEEDED
		case trrFailed, trrCanceled, trrAbandoned:
			return report.FAILED
		default:
			return report.IN_PROGRESS
		}
	default:
		return report.IN_PROGRESS
	}
}

// mapStageUserMessage includes the else branch: completed+canceled/abandoned
// renders "<name>: completed" (state.value, not the result) and an absent state renders
// "<name> is in unknown state".
func mapStageUserMessage(name string, state timelineRecordState, result timelineRecordResult) string {
	switch {
	case state == trsPending:
		return fmt.Sprintf("%s is pending", name)
	case state == trsInProgress:
		return fmt.Sprintf("%s is running", name)
	case state == trsCompleted && result == trrSucceeded:
		return fmt.Sprintf("%s completed successfully", name)
	case state == trsCompleted && result == trrSkipped:
		return fmt.Sprintf("%s was skipped", name)
	case state == trsCompleted && result == trrFailed:
		return fmt.Sprintf("%s failed", name)
	case state == "":
		return fmt.Sprintf("%s is in unknown state", name)
	default:
		return fmt.Sprintf("%s: %s", name, string(state))
	}
}

// buildStageSystemMessage builds "Stage: <name>, State: <state|Unknown>[, Result:
// <result>][, Started: <t>][, Finished: <t>]".
func buildStageSystemMessage(name string, state timelineRecordState, result timelineRecordResult, startTime, finishTime string) string {
	stateStr := string(state)
	if stateStr == "" {
		stateStr = "Unknown"
	}
	msg := fmt.Sprintf("Stage: %s, State: %s", name, stateStr)
	if result != "" {
		msg += fmt.Sprintf(", Result: %s", string(result))
	}
	if startTime != "" {
		msg += fmt.Sprintf(", Started: %s", startTime)
	}
	if finishTime != "" {
		msg += fmt.Sprintf(", Finished: %s", finishTime)
	}
	return msg
}
