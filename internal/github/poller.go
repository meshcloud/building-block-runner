package github

import (
	"context"
	"fmt"
	"time"

	"github.com/meshcloud/building-block-runner/internal/meshapi"
	"github.com/meshcloud/building-block-runner/internal/report"
)

// pollWorkflow is the sync poller (package-local because it must first
// FIND the dispatched run then track JOBS, unlike ado's stage timeline). It:
//  1. finds the dispatched run (workflow_dispatch returns no run id — correlation is
//     heuristic): ≤findAttempts, each lists the 5 newest runs and picks the first with
//     created_at > triggerTime−30s; miss/listing-error ⇒ wait pollInterval, retry;
//  2. polls run+jobs every pollInterval until the run is completed or pollTimeout; job-step
//     batches are reported (with the trigger step prepended to the FIRST batch only), the
//     completed-job re-report quirk preserved;
//  3. reports the terminal update from the run conclusion.
//
// seenJobs is local per invocation (no handler state). ctx cancellation:
// report a terminal ABORTED (fallback FAILED, never SUCCEEDED) then return
// ctx.Err(). A reported FAILED/terminal is NOT a returned error; only a report transport
// failure returns non-nil.
func (h Handler) pollWorkflow(ctx context.Context, reporter report.Reporter, gc *githubClient, impl meshapi.GithubImplementation, runID, workflow, token string, triggerTime time.Time) error {
	found, aborted := h.findRun(ctx, gc, impl, workflow, token, triggerTime)
	if aborted {
		return h.reportAborted(ctx, reporter, runID)
	}
	if found == nil {
		return h.failRun(reporter, runID, fmt.Sprintf("Could not find the triggered workflow run after %d attempts", h.findAttempts))
	}

	seenJobs := map[int64]bool{}
	current := *found

	// Poll loop: while not completed, timeout-check first, then wait, then refresh run+jobs.
	for current.Status != runCompleted {
		if h.deps.Clock.Now().After(triggerTime.Add(h.pollTimeout)) {
			return h.failRun(reporter, runID, genericErrorMessage(pollTimeoutSystemDetail))
		}
		if !h.deps.Clock.Wait(ctx, h.pollInterval) {
			return h.reportAborted(ctx, reporter, runID)
		}

		run, err := gc.workflowRunByID(token, impl.Owner, impl.Repository, current.Id)
		if err != nil {
			h.deps.Log.Warn("failed to get workflow run status, will retry", "runId", runID, "err", err)
			continue
		}
		jobs, err := gc.workflowJobs(token, impl.Owner, impl.Repository, current.Id)
		if err != nil {
			h.deps.Log.Warn("failed to list workflow jobs, will retry", "runId", runID, "err", err)
			continue
		}
		current = run
		abort, err := h.reportJobs(reporter, runID, jobs, seenJobs)
		if err != nil {
			return err
		}
		if abort {
			// Backend-requested abort (T1): stop polling promptly and report terminal
			// ABORTED. First cut only -- cancelling the remote GitHub Actions run itself is
			// a provider-specific follow-up, not done here.
			return h.sendAborted(reporter, runID)
		}
	}

	// One last job snapshot (errors warn-swallowed, :310-321).
	if jobs, err := gc.workflowJobs(token, impl.Owner, impl.Repository, current.Id); err != nil {
		h.deps.Log.Warn("failed to get final job statuses", "runId", runID, "err", err)
	} else if _, err := h.reportJobs(reporter, runID, jobs, seenJobs); err != nil {
		return err
	}

	return h.reportFinal(reporter, runID, current)
}

// findRun implements step 1. It returns (run, aborted): run is nil after findAttempts
// misses (the not-found path), aborted is true when ctx cancelled mid-wait. Listing errors
// are warn-swallowed and retried within the budget (never fatal), so there is no error
// return.
func (h Handler) findRun(ctx context.Context, gc *githubClient, impl meshapi.GithubImplementation, workflow, token string, triggerTime time.Time) (*workflowRun, bool) {
	cutoff := triggerTime.Add(-h.findBuffer)
	for attempt := 0; attempt < h.findAttempts; attempt++ {
		runs, err := gc.listWorkflowRuns(token, impl.Owner, impl.Repository, workflow)
		if err != nil {
			h.deps.Log.Warn("failed to find workflow run on attempt, will retry", "attempt", attempt+1, "err", err)
		} else {
			if run := firstRunAfter(runs, cutoff); run != nil {
				return run, false
			}
		}
		if !h.deps.Clock.Wait(ctx, h.pollInterval) {
			return nil, true
		}
	}
	return nil, false
}

// firstRunAfter picks the first run whose created_at is after cutoff (triggerTime−30s). An
// unparsable created_at is skipped (Kotlin: Instant.from throws into the find-retry path —
// here it just does not match, and the same retry follows).
func firstRunAfter(runs []workflowRun, cutoff time.Time) *workflowRun {
	for i := range runs {
		created, err := time.Parse(time.RFC3339, runs[i].CreatedAt)
		if err != nil {
			continue
		}
		if created.After(cutoff) {
			return &runs[i]
		}
	}
	return nil
}

// reportJobs emits one IN_PROGRESS batch for the new-or-completed jobs. New jobs
// are recorded in seenJobs; completed jobs are re-reported on every poll (a quirk
// pinned as-is). The gh-trigger step is prepended ONLY in the first batch (when every
// reported job is new, i.e. seenJobs size == batch size after recording). It returns the
// reporter's abort signal (T1) so the poll loop can stop promptly on a backend-requested
// abort.
func (h Handler) reportJobs(reporter report.Reporter, runID string, jobs []workflowJob, seenJobs map[int64]bool) (bool, error) {
	var batch []workflowJob
	for _, job := range jobs {
		isNew := !seenJobs[job.Id]
		if isNew {
			seenJobs[job.Id] = true
		}
		if isNew || job.Status == jobCompleted {
			batch = append(batch, job)
		}
	}
	if len(batch) == 0 {
		return false, nil
	}

	steps := make([]report.StepStatus, 0, len(batch)+1)
	if len(seenJobs) == len(batch) {
		// First batch: prepend the trigger step (:393-407).
		steps = append(steps, report.StepStatus{
			Name:          StepId,
			Status:        report.SUCCEEDED,
			UserMessage:   ptr("GitHub workflow triggered successfully"),
			SystemMessage: ptr("Workflow started, monitoring individual jobs"),
		})
	}
	for _, job := range batch {
		steps = append(steps, jobStep(job))
	}

	return reporter.Report(report.RunStatus{RunId: runID, Status: report.IN_PROGRESS, Steps: steps})
}

// jobStep maps one job to its step: id gh-workflow-job-<id>, display "GitHub Job:
// <name>", the status/message mapping.
func jobStep(job workflowJob) report.StepStatus {
	status := report.IN_PROGRESS
	if job.Status == jobCompleted {
		if job.Conclusion == "success" {
			status = report.SUCCEEDED
		} else {
			status = report.FAILED
		}
	}

	user := jobUserMessage(job)
	system := jobSystemMessage(job)
	return report.StepStatus{
		Name:          jobStepIdPrefix + fmt.Sprint(job.Id),
		DisplayName:   jobDisplayNamePrefix + job.Name,
		Status:        status,
		UserMessage:   ptr(user),
		SystemMessage: ptr(system),
	}
}

func jobUserMessage(job workflowJob) string {
	switch {
	case job.Status == jobCompleted && job.Conclusion == "success":
		return fmt.Sprintf("Job '%s' completed successfully", job.Name)
	case job.Status == jobCompleted && job.Conclusion == "failure":
		return fmt.Sprintf("Job '%s' failed", job.Name)
	case job.Status == jobCompleted && job.Conclusion == "cancelled":
		return fmt.Sprintf("Job '%s' was cancelled", job.Name)
	case job.Status == jobCompleted && job.Conclusion == "skipped":
		return fmt.Sprintf("Job '%s' was skipped", job.Name)
	case job.Status == jobInProgress:
		return fmt.Sprintf("Job '%s' is running", job.Name)
	case job.Status == jobQueued:
		return fmt.Sprintf("Job '%s' is queued", job.Name)
	default:
		return fmt.Sprintf("Job '%s' status: %s", job.Name, string(job.Status))
	}
}

// jobSystemMessage builds "Job ID: <id>, Status: <status>[, Conclusion: <c>][, Started:
// <t>][, Completed: <t>], View job: <html_url>" (:370-382).
func jobSystemMessage(job workflowJob) string {
	s := fmt.Sprintf("Job ID: %d, Status: %s", job.Id, string(job.Status))
	if job.Conclusion != "" {
		s += ", Conclusion: " + job.Conclusion
	}
	if job.StartedAt != "" {
		s += ", Started: " + job.StartedAt
	}
	if job.CompletedAt != "" {
		s += ", Completed: " + job.CompletedAt
	}
	s += ", View job: " + job.HtmlUrl
	return s
}

// reportFinal reports the terminal update from the run conclusion: success⇒SUCCEEDED,
// everything else⇒FAILED; steps = the gh-trigger step only.
func (h Handler) reportFinal(reporter report.Reporter, runID string, run workflowRun) error {
	status := report.FAILED
	if run.Conclusion == "success" {
		status = report.SUCCEEDED
	}

	user := "GitHub workflow completed with unknown status"
	switch run.Conclusion {
	case "success":
		user = "GitHub workflow completed successfully"
	case "failure":
		user = "GitHub workflow failed"
	case "cancelled":
		user = "GitHub workflow was cancelled"
	case "timed_out":
		user = "GitHub workflow timed out"
	}

	system := fmt.Sprintf("Workflow run %d completed with status: %s, conclusion: %s. View run: %s",
		run.Id, string(run.Status), run.Conclusion, run.HtmlUrl)

	_, err := reporter.Report(report.RunStatus{
		RunId:  runID,
		Status: status,
		Steps: []report.StepStatus{{
			Name:          StepId,
			Status:        status,
			UserMessage:   ptr(user),
			SystemMessage: ptr(system),
		}},
	})
	return err
}

// reportAborted reports the graceful-shutdown terminal update via sendAborted, then returns
// ctx.Err() (or context.Canceled) so a single-run process exits non-zero and k8s reschedules
// -- the shutdown-signal abort path.
func (h Handler) reportAborted(ctx context.Context, reporter report.Reporter, runID string) error {
	if err := h.sendAborted(reporter, runID); err != nil {
		return err
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return context.Canceled
}

// sendAborted sends the terminal ABORTED update, falling back to FAILED if the endpoint
// rejects the ABORTED transition, NEVER SUCCEEDED -- so the coordinator never sees a stale
// IN_PROGRESS. It returns the report transport error (nil on success); callers decide what a
// successful send means for their own return value (shutdown: non-nil to trigger a
// reschedule via reportAborted; backend-requested abort (T1): nil, a handled terminal
// outcome).
func (h Handler) sendAborted(reporter report.Reporter, runID string) error {
	msg := "The building block run was aborted because the runner is shutting down."
	_, err := reporter.Report(report.RunStatus{
		RunId:  runID,
		Status: report.ABORTED,
		Steps: []report.StepStatus{{
			Name:          StepId,
			Status:        report.ABORTED,
			UserMessage:   ptr("GitHub workflow monitoring aborted"),
			SystemMessage: ptr(msg),
		}},
	})
	if err == nil {
		return nil
	}
	// ABORTED rejected ⇒ fall back to FAILED (never SUCCEEDED).
	_, ferr := reporter.Report(report.RunStatus{
		RunId:  runID,
		Status: report.FAILED,
		Steps: []report.StepStatus{{
			Name:          StepId,
			Status:        report.FAILED,
			UserMessage:   ptr(failUserMessage),
			SystemMessage: ptr(msg),
		}},
	})
	return ferr
}
