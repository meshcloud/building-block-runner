package github

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"slices"
	"strconv"
	"time"

	"github.com/meshcloud/meshstack-cli/client/http"

	"github.com/meshcloud/building-block-runner/v2/internal/executor"
)

const (
	pollInterval = 10 * time.Second
	findAttempts = 12
	creationSkew = 30 * time.Second
)

type workflowRun struct {
	Id         int64     `json:"id"`
	Status     string    `json:"status"`
	Conclusion string    `json:"conclusion"`
	CreatedAt  time.Time `json:"created_at"`
	HtmlUrl    string    `json:"html_url"`
}

type workflowJob struct {
	Id         int64  `json:"id"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	HtmlUrl    string `json:"html_url"`
}

type jobStep struct {
	step   *executor.Step
	status executor.Status
}

func (w *workflow) await(ctx context.Context, runId int64) (executor.Status, error) {
	if runId == 0 {
		var err error
		if runId, err = w.findRun(ctx); err != nil {
			if ctx.Err() != nil {
				return executor.StatusAborted, err
			}
			return executor.StatusFailed, err
		}
	}
	slog.InfoContext(ctx, "polling the GitHub workflow run", "workflowRun", runId)
	jobs := map[int64]*jobStep{}
	tick := time.Tick(pollInterval)
	seen := false
	for polls := 1; ; polls++ {
		run, err := w.poll(ctx, runId, jobs)
		if httpErr, ok := errors.AsType[http.Error](err); ok && httpErr.IsUnauthorized() {
			if err = w.authenticate(ctx); err == nil {
				run, err = w.poll(ctx, runId, jobs)
			}
		}
		switch {
		case ctx.Err() != nil:
			return w.stop(ctx, runId, jobs, context.Cause(ctx))
		case !seen && polls < findAttempts && isNotFound(err):
			slog.WarnContext(ctx, "the GitHub workflow run is not visible yet, trying again", "workflowRun", runId, "error", err)
		case isPermanent(err):
			return executor.StatusFailed, fmt.Errorf("could not poll the GitHub workflow run %d: %w", runId, err)
		case err != nil:
			slog.WarnContext(ctx, "could not poll the GitHub workflow run, trying again", "workflowRun", runId, "error", err)
		default:
			seen = true
			if run.Status == "completed" {
				slog.InfoContext(ctx, "the GitHub workflow run completed", "workflowRun", runId, "conclusion", run.Conclusion)
				return runOutcome(run)
			}
		}
		select {
		case <-ctx.Done():
			return w.stop(ctx, runId, jobs, context.Cause(ctx))
		case <-tick:
		}
	}
}

// findRun finds the run that a dispatch started on a GitHub Enterprise Server that does not tell it.
func (w *workflow) findRun(ctx context.Context) (int64, error) {
	tick := time.Tick(pollInterval)
	for range findAttempts {
		runs, err := http.UnmarshalJsonBody[struct {
			WorkflowRuns []workflowRun `json:"workflow_runs"`
		}](w.api.http.DoRequest(ctx, http.MethodGet, w.repo.JoinPath("actions", "workflows", w.file, "runs"),
			append(w.api.options, http.WithUrlQuery(map[string]int{"per_page": 5}))...))
		if err != nil {
			slog.WarnContext(ctx, "could not list the GitHub workflow runs, trying again", "workflow", w.file, "error", err)
		} else if i := slices.IndexFunc(runs.WorkflowRuns, w.startedByDispatch); i >= 0 {
			return runs.WorkflowRuns[i].Id, nil
		}
		select {
		case <-ctx.Done():
			return 0, context.Cause(ctx)
		case <-tick:
		}
	}
	return 0, fmt.Errorf("found no run of the GitHub workflow %s that started after the dispatch", w.file)
}

func (w *workflow) startedByDispatch(run workflowRun) bool {
	return run.CreatedAt.After(w.dispatched.Add(-creationSkew))
}

func (w *workflow) poll(ctx context.Context, runId int64, jobs map[int64]*jobStep) (workflowRun, error) {
	runUrl := w.runUrl(runId)
	run, err := http.UnmarshalJsonBody[workflowRun](w.api.http.DoRequest(ctx, http.MethodGet, runUrl, w.api.options...))
	if err != nil {
		return run, err
	}
	listed, err := http.UnmarshalJsonBody[struct {
		Jobs []workflowJob `json:"jobs"`
	}](w.api.http.DoRequest(ctx, http.MethodGet, runUrl.JoinPath("jobs"), append(w.api.options, http.WithUrlQuery(map[string]int{"per_page": 100}))...))
	if err != nil {
		return run, err
	}
	for _, job := range listed.Jobs {
		w.reportJob(jobs, job)
	}
	return run, nil
}

func (w *workflow) reportJob(jobs map[int64]*jobStep, job workflowJob) {
	reported, seen := jobs[job.Id]
	if !seen {
		reported = &jobStep{step: w.reporter.Step("gh-workflow-job-"+strconv.FormatInt(job.Id, 10), "GitHub Job: "+job.Name)}
		reported.step.Logf("View the job: %s", job.HtmlUrl)
		jobs[job.Id] = reported
	}
	status := jobStatus(job)
	if status == reported.status {
		return
	}
	reported.status = status
	reported.step.SetStatus(status)
	if job.Status == "completed" {
		reported.step.Logf("The job completed with the conclusion %s.", job.Conclusion)
	}
}

func (w *workflow) runUrl(runId int64) *url.URL {
	return w.repo.JoinPath("actions", "runs", strconv.FormatInt(runId, 10))
}

func isNotFound(err error) bool {
	httpErr, ok := errors.AsType[http.Error](err)
	return ok && httpErr.IsNotFound()
}

func isPermanent(err error) bool {
	httpErr, ok := errors.AsType[http.Error](err)
	return ok && !httpErr.IsTemporary()
}

func (w *workflow) stop(ctx context.Context, runId int64, jobs map[int64]*jobStep, cause error) (executor.Status, error) {
	if err := http.IgnoreResponse(w.api.http.DoRequest(context.WithoutCancel(ctx), http.MethodPost, w.runUrl(runId).JoinPath("cancel"), w.api.options...)); err != nil {
		slog.WarnContext(ctx, "could not cancel the GitHub workflow run", "workflowRun", runId, "error", err)
	}
	for _, job := range jobs {
		if job.status == executor.StatusPending || job.status == executor.StatusInProgress {
			job.step.SetStatus(executor.StatusAborted)
		}
	}
	return executor.StatusAborted, cause
}

func jobStatus(job workflowJob) executor.Status {
	switch job.Status {
	case "completed":
		switch job.Conclusion {
		case "success", "skipped", "neutral":
			return executor.StatusSucceeded
		case "cancelled":
			return executor.StatusAborted
		}
		return executor.StatusFailed
	case "in_progress":
		return executor.StatusInProgress
	}
	return executor.StatusPending
}

func runOutcome(run workflowRun) (executor.Status, error) {
	if run.Conclusion == "success" {
		return executor.StatusSucceeded, nil
	}
	return executor.StatusFailed, fmt.Errorf("the GitHub workflow run %s completed with the conclusion %s", run.HtmlUrl, run.Conclusion)
}
