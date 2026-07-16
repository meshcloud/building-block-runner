package azdevops

import (
	"fmt"
	"sort"

	"github.com/meshcloud/building-block-runner/internal/report"
)

// This file builds the report.RunStatus values the handler/poller feed into the unified
// report.Reporter: each function returns exactly the changed/new steps for one
// Report call -- the reporter itself is stateless, so all dedup bookkeeping
// (reportedStages, lastReportedState) lives in the caller (poll.go), matching Kotlin's
// mutable-set-threaded-through-calls shape translated into a loop-local map (D15).

func str(s string) *string { return &s }

// registerStatus is the Register() call: one step, PENDING (the eventReporter fills in the
// PENDING status itself; DisplayName is what matters here for the registration body).
func registerStatus(runId string) report.RunStatus {
	return report.RunStatus{
		RunId: runId,
		Steps: []report.StepStatus{{Name: StepId, DisplayName: triggerDisplayName}},
	}
}

// triggerSuccessUpdate: run IN_PROGRESS, step SUCCEEDED, sync/async message variant.
func triggerSuccessUpdate(runId string, pr pipelineRun, async bool) report.RunStatus {
	extra := "Polling for completion status..."
	if async {
		extra = "Will wait for API updates on status..."
	}
	webUrl := pr.webURL()
	user := fmt.Sprintf("Triggered Azure DevOps Pipeline. %s", extra)
	system := fmt.Sprintf("Triggered pipeline run %d. View run: %s. %s", pr.Id, webUrl, extra)

	return report.RunStatus{
		RunId:  runId,
		Status: report.IN_PROGRESS,
		Steps: []report.StepStatus{{
			Name: StepId, Status: report.SUCCEEDED, UserMessage: str(user), SystemMessage: str(system),
		}},
	}
}

// stateOnlyUpdate: run+step IN_PROGRESS, the mapped state message, `state.value`
// wire rendering (empty-stage-list path -- sent unconditionally every call the caller
// makes it from, no dedup here).
func stateOnlyUpdate(runId string, pr pipelineRun) report.RunStatus {
	user := mapStateUserMessage(&pr.State)
	webUrl := pr.webURL()
	system := fmt.Sprintf("Pipeline run %d state: %s. View run: %s", pr.Id, string(pr.State), webUrl)

	return report.RunStatus{
		RunId:  runId,
		Status: report.IN_PROGRESS,
		Steps: []report.StepStatus{{
			Name: StepId, Status: report.IN_PROGRESS, UserMessage: str(user), SystemMessage: str(system),
		}},
	}
}

// stageBatchUpdate filters STAGE records with no parent, sorted by
// order; empty -> delegates to stateOnlyUpdate; otherwise re-includes the trigger
// step first (SUCCEEDED, the no-polling-suffix message pair, distinct from
// triggerSuccessUpdate's) plus every stage not yet reported OR currently COMPLETED
// (the one-way dedup -- COMPLETED stages are re-sent on every subsequent call).
// reportedStages is mutated in place (the loop-local dedup set, caller-owned).
func stageBatchUpdate(runId string, pr pipelineRun, records []timelineRecord, reportedStages map[string]bool) report.RunStatus {
	stages := make([]timelineRecord, 0, len(records))
	for _, r := range records {
		if r.Type == timelineTypeStage && r.ParentId == "" {
			stages = append(stages, r)
		}
	}
	if len(stages) == 0 {
		return stateOnlyUpdate(runId, pr)
	}
	sort.SliceStable(stages, func(i, j int) bool { return stages[i].Order < stages[j].Order })

	webUrl := pr.webURL()
	steps := make([]report.StepStatus, 0, len(stages)+1)
	steps = append(steps, report.StepStatus{
		Name:          StepId,
		Status:        report.SUCCEEDED,
		UserMessage:   str("Triggered Azure DevOps Pipeline"),
		SystemMessage: str(fmt.Sprintf("Pipeline run %d. View run: %s", pr.Id, webUrl)),
	})

	for _, stage := range stages {
		if reportedStages[stage.Id] && stage.State != trsCompleted {
			continue
		}
		name := stage.Name
		if name == "" {
			name = "Unknown Stage"
		}
		steps = append(steps, report.StepStatus{
			Name:          stageStepId(stage.Id),
			DisplayName:   stageDisplayName(name),
			Status:        mapStageStatus(stage.State, stage.Result),
			UserMessage:   str(mapStageUserMessage(name, stage.State, stage.Result)),
			SystemMessage: str(buildStageSystemMessage(name, stage.State, stage.Result, stage.StartTime, stage.FinishTime)),
		})
		reportedStages[stage.Id] = true
	}

	return report.RunStatus{RunId: runId, Status: report.IN_PROGRESS, Steps: steps}
}

// finalUpdate: only the trigger step, status = the mapped result (can flip
// SUCCEEDED->FAILED at the step level), the enum-NAME rendering for state/result (distinct
// from the polling messages' wire-value rendering).
func finalUpdate(runId string, pr pipelineRun) report.RunStatus {
	status := mapPipelineResult(pr.Result)
	user := mapResultUserMessage(pr.Result)
	webUrl := pr.webURL()
	system := fmt.Sprintf("Pipeline run %d completed with state: %s, result: %s. View run: %s",
		pr.Id, pr.State.enumName(), resultEnumNameOrNull(pr.Result), webUrl)

	return report.RunStatus{
		RunId:  runId,
		Status: status,
		Steps: []report.StepStatus{{
			Name: StepId, Status: status, UserMessage: str(user), SystemMessage: str(system),
		}},
	}
}

// failedUpdate: run+step FAILED, the fixed user message, message is the
// system-message text built by failureMessage.
func failedUpdate(runId, message string) report.RunStatus {
	return report.RunStatus{
		RunId:  runId,
		Status: report.FAILED,
		Steps: []report.StepStatus{{
			Name: StepId, Status: report.FAILED,
			UserMessage: str("Could not trigger the Azure DevOps Pipeline"), SystemMessage: str(message),
		}},
	}
}

// terminalAbortUpdate/terminalFailedUpdate build the graceful-shutdown terminal reports:
// ABORTED first, FAILED as the fallback if the endpoint
// rejects ABORTED -- never SUCCEEDED.
func terminalAbortUpdate(runId string) report.RunStatus {
	return report.RunStatus{
		RunId:  runId,
		Status: report.ABORTED,
		Steps:  []report.StepStatus{{Name: StepId, Status: report.ABORTED}},
	}
}

func terminalFailedUpdate(runId string) report.RunStatus {
	return report.RunStatus{
		RunId:  runId,
		Status: report.FAILED,
		Steps:  []report.StepStatus{{Name: StepId, Status: report.FAILED}},
	}
}
