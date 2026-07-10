package manual

import (
	"context"
	"log/slog"

	"github.com/meshcloud/building-block-runner/internal/meshapi"
	"github.com/meshcloud/building-block-runner/internal/report"
)

// executeDebug reproduces DebugBlockRunnerService (dev-only helper): three IN_PROGRESS
// updates (waits between), then a final update whose status is a coin flip between
// SUCCEEDED and FAILED. Every update carries two steps — "manual" (SUCCEEDED, fixed
// messages) and "additionalDebugStep" (PENDING with no outputs on the non-final updates,
// SUCCEEDED with outputs on the final one). The debug outputs echo the RAW input type (no
// toOutputType), a Kotlin quirk pinned as-is (M-P4, §2.2). Sleep cadence and RNG
// distribution are not contracts (umbrella §3.2); only the update sequence/shape is.
func (h Handler) executeDebug(ctx context.Context, reporter report.Reporter, runId string, inputs []meshapi.BuildingBlockInputSpecDTO, log *slog.Logger) error {
	for i := 0; i < 3; i++ {
		if _, err := reporter.Report(h.debugUpdate(runId, report.IN_PROGRESS, false, inputs, log)); err != nil {
			return err
		}
		h.deps.Clock.Wait(ctx, debugWaitDelay)
	}

	finalStatus := report.SUCCEEDED
	if h.deps.Rand() >= 0.5 {
		finalStatus = report.FAILED
	}
	_, err := reporter.Report(h.debugUpdate(runId, finalStatus, true, inputs, log))
	return err
}

// debugUpdate builds one debug SourceUpdate (Kotlin makeUpdate).
func (h Handler) debugUpdate(runId string, status report.ExecutionStatus, isLast bool, inputs []meshapi.BuildingBlockInputSpecDTO, log *slog.Logger) report.RunStatus {
	debugStep := report.StepStatus{
		Name:          debugStepId,
		Status:        report.PENDING,
		UserMessage:   ptr(debugUserMessage),
		SystemMessage: ptr(debugSystemMessage),
	}
	if isLast {
		debugStep.Status = report.SUCCEEDED
		// Debug outputs echo the raw input type verbatim (no toOutputType — the M-P4 quirk).
		debugStep.Outputs = echoOutputs(inputs, rawType, log)
	}

	return report.RunStatus{
		RunId:  runId,
		Status: status,
		Steps: []report.StepStatus{
			{
				Name:          StepId,
				Status:        report.SUCCEEDED,
				UserMessage:   ptr(debugUserMessage),
				SystemMessage: ptr(debugSystemMessage),
			},
			debugStep,
		},
	}
}

// rawType is the identity type mapping used by debug outputs (the M-P4 quirk).
func rawType(t string) (string, bool) { return t, true }

func ptr[T any](v T) *T { return &v }
