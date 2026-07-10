package azdevops

import (
	"context"

	"github.com/meshcloud/building-block-runner/internal/report"
)

// pollCompletion drives a sync run to a terminal report (§2.2/§4.4). Semantics ported from
// AzureDevOpsPipelinePoller.kt, with two behavior-preserving Go translations (D15):
//
//  1. ctx-awareness: a cancelled ctx during the poll wait reports a TERMINAL status (ABORTED,
//     falling back to FAILED, never SUCCEEDED -- plan-05 H7 amendment, §4.4/§16.11) instead
//     of leaving the run IN_PROGRESS.
//  2. escalation semantics precisely mirror Kotlin's nested try/catch (§2.2, verified against
//     the source): a PATCH failure while sending a STAGE/fallback update during the loop is
//     silently absorbed exactly like a GET failure (Kotlin's shared catch(ex){ warn; continue }
//     -- see reportOrEscalate's doc for why only the timeout report and the final report get
//     the "one more attempt, then propagate" treatment).
func (h Handler) pollCompletion(ctx context.Context, client adoClient, reporter report.Reporter, runId string, initial pipelineRun) error {
	if isPipelineComplete(initial) {
		return h.reportOrEscalate(reporter, runId, finalUpdate(runId, initial))
	}

	deadline := h.deps.Clock.Now().Add(h.deps.pollBudget)
	current := initial
	reportedStages := map[string]bool{}
	lastReportedState := ""

	for !isPipelineComplete(current) {
		if !h.deps.Clock.Now().Before(deadline) {
			return h.reportOrEscalate(reporter, runId,
				failedUpdate(runId, failureMessage(pollTimeoutError{})))
		}

		select {
		case <-ctx.Done():
			return h.reportAbort(reporter, runId)
		case <-h.deps.Clock.After(h.deps.pollInterval):
		}

		next, err := client.GetPipelineRun(ctx, current.Id)
		if err != nil {
			h.deps.Log.Warn("failed to get pipeline run status, will retry", "err", err)
			continue
		}
		current = next

		records, timelineErr := client.GetTimeline(ctx, current.Id)
		if timelineErr == nil {
			if _, patchErr := reporter.Report(stageBatchUpdate(runId, current, records, reportedStages)); patchErr != nil {
				// Kotlin: a PATCH failure inside updatePipelineAndStageStatuses is caught by
				// the SAME catch block as a timeline-fetch failure -- fall through to the
				// identical fallback path/log message (§2.2).
				timelineErr = patchErr
			}
		}
		if timelineErr != nil {
			h.deps.Log.Warn("failed to get timeline records, will use basic status update", "err", timelineErr)
			if lastReportedState != string(current.State) {
				if _, patchErr := reporter.Report(stateOnlyUpdate(runId, current)); patchErr != nil {
					// Absorbed exactly like a GET failure -- Kotlin's shared catch(B) never
					// escalates a mid-loop PATCH failure to a run-FAILED report (verified
					// against AzureDevOpsPipelinePoller.kt:43-64: this failure mode falls
					// through to the outer "will retry" catch, not the outer-outer catch(A)
					// that guards only the final update).
					h.deps.Log.Warn("failed to send fallback status update, will retry", "err", patchErr)
				}
			}
		}
		// Kotlin sets lastReportedState unconditionally after the inner try/catch, on both the
		// success and fallback branches (AzureDevOpsPipelinePoller.kt:60) -- one assignment here
		// reproduces that (the fallback branch's own assignment would be redundant with this one).
		lastReportedState = string(current.State)
	}

	return h.reportOrEscalate(reporter, runId, finalUpdate(runId, current))
}

// reportOrEscalate is the twin of Kotlin's outermost try/catch(A) around
// pollPipelineCompletion's while loop (P-P5): it sends status; if that PATCH itself fails, it
// makes exactly one more attempt with the generic internal-error message wrapping that
// failure. A second failure propagates to Execute's caller (InProcess), matching Kotlin's
// uncaught propagation out of pollPipelineCompletion.
func (h Handler) reportOrEscalate(reporter report.Reporter, runId string, status report.RunStatus) error {
	if _, err := reporter.Report(status); err != nil {
		_, err2 := reporter.Report(failedUpdate(runId, failureMessage(err)))
		return err2
	}
	return nil
}

// reportAbort implements the plan-05 H7 amendment (§4.4/§16.11): report a TERMINAL status on
// ctx cancellation -- ABORTED first, falling back to FAILED if the endpoint rejects it, never
// SUCCEEDED -- so the coordinator never sees a stale IN_PROGRESS. A successfully-delivered
// terminal report is a handled run outcome (returns nil, A1); only a transport failure on
// BOTH attempts propagates.
func (h Handler) reportAbort(reporter report.Reporter, runId string) error {
	if _, err := reporter.Report(terminalAbortUpdate(runId)); err == nil {
		return nil
	}
	_, err := reporter.Report(terminalFailedUpdate(runId))
	return err
}

// pollTimeoutError is the Go twin of Kotlin's `Exception("Pipeline polling timeout after 30
// minutes")` (P-P1): a plain error (not an ExternalCallError) so failureMessage renders it
// through the generic internal-error form, exactly matching U-P8's timeout wording -- the
// intentionally-misleading-after-a-successful-trigger message, pinned as-is (D13, flag §16.3).
type pollTimeoutError struct{}

func (pollTimeoutError) Error() string { return "Pipeline polling timeout after 30 minutes" }
