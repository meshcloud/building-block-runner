package mockpipeline

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"
	"uuid"

	"github.com/meshcloud/meshstack-cli/client"
	"github.com/meshcloud/meshstack-cli/client/http"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/v2/internal/executor"
)

const (
	Step              = "Mock pipeline"
	reportTimeout     = time.Minute
	maxReportFailures = 10
)

type Options struct {
	// Outcome is the final status of a run, and empty for a run that stays in progress.
	Outcome     executor.Status
	Async       bool
	ReportDelay time.Duration
}

type Trigger struct {
	Workflow string
	Ref      string
	Params   map[string]string
}

// Pipeline reports to meshStack with the run token when async, as a real pipeline does; sync, the runner reports.
type Pipeline struct {
	Options

	meshStack client.Client

	mu                   sync.Mutex
	triggers             []Trigger
	runs                 []Run
	listedBuildingBlocks []string
	failures             chan error
}

func New(t *testing.T, meshStack client.Client, options Options) *Pipeline {
	t.Helper()
	p := &Pipeline{Options: options, meshStack: meshStack, failures: make(chan error, maxReportFailures)}
	t.Cleanup(func() {
		for len(p.failures) > 0 {
			t.Errorf("the mock pipeline could not report to meshStack: %v", <-p.failures)
		}
	})
	return p
}

func (p *Pipeline) Start(ctx context.Context, trigger Trigger, run client.MeshBuildingBlockRun, runErr error) Run {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.triggers = append(p.triggers, trigger)
	started := Run{Id: int64(len(p.runs) + 1), Created: time.Now().UTC(), outcome: p.Outcome}
	p.runs = append(p.runs, started)
	if p.Async {
		// The pipeline outlives the request that triggered it.
		ctx = context.WithoutCancel(ctx)
		go func() {
			if err := errors.Join(runErr, p.listBuildingBlocks(ctx, trigger.Params), p.report(ctx, run)); err != nil {
				p.failures <- err
			}
		}()
	}
	return started
}

func (p *Pipeline) AssertListed(t *testing.T, buildingBlockUuid string) {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	assert.Contains(t, p.listedBuildingBlocks, buildingBlockUuid, "the workflow lists its building block with MESHSTACK_API_TOKEN")
}

func (p *Pipeline) Triggers(t *testing.T) []Trigger {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.triggers)
}

func (p *Pipeline) RequireOneTrigger(t *testing.T) Trigger {
	t.Helper()
	triggers := p.Triggers(t)
	require.Len(t, triggers, 1)
	return triggers[0]
}

func (p *Pipeline) RequireOneRun(t *testing.T) Run {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	require.Len(t, p.runs, 1)
	return p.runs[0]
}

func (p *Pipeline) AwaitPoll(t *testing.T, timeout, interval time.Duration) {
	t.Helper()
	require.Eventually(t, func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		return slices.ContainsFunc(p.runs, func(run Run) bool { return run.Polls > 0 })
	}, timeout, interval, "the runner did not poll the pipeline run")
}

func (p *Pipeline) report(ctx context.Context, run client.MeshBuildingBlockRun) error {
	if run.Spec.RunToken == "" {
		return errors.New("the trigger carried no run token")
	}
	select {
	case <-time.After(p.ReportDelay):
	case <-ctx.Done():
		return ctx.Err()
	}
	ctx, cancel := context.WithTimeout(ctx, reportTimeout)
	defer cancel()
	runs := p.withToken(ctx, run.Spec.RunToken).BuildingBlockRun
	source := uuid.New().String()
	const step = "mock-pipeline"
	err := runs.RegisterSource(ctx, run.Metadata.Uuid, client.MeshBuildingBlockRunSourceRegistration{
		Source: client.MeshBuildingBlockRunSource{Id: source},
		Steps:  []client.MeshBuildingBlockRunStepRegistration{{Id: step, DisplayName: Step}},
	})
	if err != nil {
		return fmt.Errorf("register the source: %w", err)
	}
	_, err = runs.UpdateSourceSteps(ctx, run.Metadata.Uuid, source, client.MeshBuildingBlockRunStatusUpdate{
		Status: new(string(p.Outcome)),
		Steps: []client.MeshBuildingBlockRunStepStatusUpdate{{
			Id: step, DisplayName: Step, Status: new(string(p.Outcome)), UserMessage: new("The mock pipeline finished."),
		}},
	})
	return err
}

func (p *Pipeline) withToken(ctx context.Context, token string) client.Client {
	return client.New(ctx, p.meshStack.Endpoint, executor.NewHttpClient(), http.BearerToken(token))
}

// listBuildingBlocks reads meshStack with the run's API token as a workflow would, before the final status revokes it.
func (p *Pipeline) listBuildingBlocks(ctx context.Context, params map[string]string) error {
	token, found := params["MESHSTACK_API_TOKEN"]
	if !found {
		return nil
	}
	var listed []string
	for buildingBlock, err := range p.withToken(ctx, token).BuildingBlockV2.List(ctx, client.MeshBuildingBlockV2Filter{}) {
		if err != nil {
			return fmt.Errorf("list the building blocks with MESHSTACK_API_TOKEN: %w", err)
		}
		listed = append(listed, *buildingBlock.Metadata.Uuid)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.listedBuildingBlocks = append(p.listedBuildingBlocks, listed...)
	return nil
}
