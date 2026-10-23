package azuredevops

import (
	"cmp"
	"context"
	"errors"
	"log/slog"
	gohttp "net/http"
	"slices"
	"time"

	"github.com/meshcloud/meshstack-cli/client/http"

	"github.com/meshcloud/building-block-runner/internal/executor"
)

const (
	pollInterval  = 10 * time.Second
	cancelTimeout = 30 * time.Second
)

type poller struct {
	ado      *azureDevOps
	reporter *executor.Reporter
	trigger  *executor.Step

	stages map[string]*stage
}

type stage struct {
	step          *executor.Step
	status        executor.Status
	state, result string
}

func newPoller(ado *azureDevOps, reporter *executor.Reporter, trigger *executor.Step) *poller {
	return &poller{ado: ado, reporter: reporter, trigger: trigger, stages: map[string]*stage{}}
}

func (p *poller) await(ctx context.Context, run pipelineRun) error {
	for run.State != "completed" {
		select {
		case <-ctx.Done():
			p.cancel(ctx, run.Id)
			return context.Cause(ctx)
		case <-time.After(pollInterval):
		}
		polled, err := p.ado.run(ctx, run.Id)
		if err != nil {
			if isPermanent(err) {
				p.trigger.UserLogf("Could not read the status of the Azure DevOps pipeline run")
				p.trigger.Logf("%v", err)
				return err
			}
			slog.WarnContext(ctx, "could not poll the pipeline run, retrying", "pipelineRun", run.Id, "error", err)
			continue
		}
		run = polled
		p.updateStages(ctx, run.Id)
	}
	return p.complete(run)
}

func (p *poller) complete(run pipelineRun) error {
	p.trigger.Logf("Pipeline run %d completed with state: %s, result: %s. View run: %s", run.Id, run.State, run.Result, run.webUrl())
	if run.Result == "succeeded" {
		p.settleStages(executor.StatusSucceeded)
		p.trigger.UserLogf("Azure DevOps pipeline completed successfully")
		return nil
	}
	p.settleStages(executor.StatusAborted)
	message := "Azure DevOps pipeline completed with unknown status"
	switch run.Result {
	case "failed":
		message = "Azure DevOps pipeline failed"
	case "canceled":
		message = "Azure DevOps pipeline was canceled"
	}
	p.trigger.UserLogf("%s", message)
	return errors.New(message)
}

func (p *poller) cancel(ctx context.Context, runId int64) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cancelTimeout)
	defer cancel()
	p.settleStages(executor.StatusAborted)
	if err := p.ado.cancel(ctx, runId); err != nil {
		p.trigger.Logf("Could not cancel pipeline run %d: %v", runId, err)
		return
	}
	p.trigger.Logf("Canceled pipeline run %d", runId)
}

func (p *poller) updateStages(ctx context.Context, runId int64) {
	records, err := p.ado.timeline(ctx, runId)
	if err != nil {
		slog.WarnContext(ctx, "could not read the timeline of the pipeline run", "pipelineRun", runId, "error", err)
		return
	}
	records = slices.DeleteFunc(records, func(r timelineRecord) bool { return r.Type != "Stage" || r.ParentId != "" })
	slices.SortStableFunc(records, func(a, b timelineRecord) int { return cmp.Compare(a.Order, b.Order) })
	for _, record := range records {
		p.updateStage(record)
	}
}

func (p *poller) updateStage(record timelineRecord) {
	name := cmp.Or(record.Name, "Unknown Stage")
	s, seen := p.stages[record.Id]
	if !seen {
		s = &stage{step: p.reporter.Step("ado-stage-"+record.Id, "Stage: "+name)}
		p.stages[record.Id] = s
	}
	if seen && s.state == record.State && s.result == record.Result {
		return
	}
	s.state, s.result = record.State, record.Result
	var message string
	s.status, message = stageUpdate(name, record.State, record.Result)
	s.step.SetStatus(s.status)
	s.step.UserLogf("%s", message)
	s.step.Logf("State: %s, Result: %s, Started: %s, Finished: %s",
		cmp.Or(record.State, "unknown"), cmp.Or(record.Result, "none"), cmp.Or(record.StartTime, "-"), cmp.Or(record.FinishTime, "-"))
}

// settleStages ends the stages that are still running, as the Reporter fails a run that ends with a running step.
func (p *poller) settleStages(status executor.Status) {
	for _, s := range p.stages {
		if s.status == executor.StatusPending || s.status == executor.StatusInProgress {
			s.status = status
			s.step.SetStatus(status)
		}
	}
}

func stageUpdate(name, state, result string) (executor.Status, string) {
	other := name + ": " + cmp.Or(state, "unknown") + " " + result
	switch state {
	case "pending":
		return executor.StatusPending, name + " is pending"
	case "completed":
		switch result {
		case "succeeded":
			return executor.StatusSucceeded, name + " completed successfully"
		case "succeededWithIssues":
			return executor.StatusSucceeded, name + " completed with issues"
		case "skipped":
			return executor.StatusSucceeded, name + " was skipped"
		case "failed":
			return executor.StatusFailed, name + " failed"
		case "abandoned":
			return executor.StatusFailed, other
		case "canceled":
			return executor.StatusAborted, name + " was canceled"
		}
	case "inProgress":
		return executor.StatusInProgress, name + " is running"
	}
	return executor.StatusInProgress, other
}

func isPermanent(err error) bool {
	httpErr, ok := errors.AsType[http.Error](err)
	return ok && httpErr.IsClientError() &&
		httpErr.StatusCode != gohttp.StatusRequestTimeout && httpErr.StatusCode != gohttp.StatusTooManyRequests
}
