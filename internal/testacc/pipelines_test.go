package testacc

import (
	"testing"

	"github.com/meshcloud/meshstack-cli/client"
	"github.com/meshcloud/meshstack-cli/client/types/enum"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/v2/internal/executor"
	"github.com/meshcloud/building-block-runner/v2/internal/testacc/harness"
	"github.com/meshcloud/building-block-runner/v2/internal/testacc/mockpipeline"
	"github.com/meshcloud/building-block-runner/v2/internal/testacc/runnerproc"
)

type pipelineKind struct {
	implementationType string
	forType            string
}

var (
	githubWorkflow = pipelineKind{"GITHUB_WORKFLOW", "github"}
	gitlabPipeline = pipelineKind{"GITLAB_PIPELINE", "gitlab"}
	azureDevOps    = pipelineKind{"AZURE_DEVOPS_PIPELINE", "azureDevOps"}

	buildingBlockStatuses = map[executor.Status]enum.Entry[client.BuildingBlockStatus]{
		executor.StatusSucceeded: client.BuildingBlockStatusSucceeded,
		executor.StatusFailed:    client.BuildingBlockStatusFailed,
	}
)

type pipelineCase struct {
	harness.Deployment

	runUuid string
	runLog  string
}

func deployPipeline(t *testing.T, s harness.Suite, kind pipelineKind, vars map[string]any) harness.Deployment {
	t.Helper()
	return harness.Deploy(t, s, harness.Case{
		Files:              caseFiles(t),
		ImplementationType: kind.implementationType,
		Runner:             runnerproc.Options{ForType: kind.forType},
		Vars:               vars,
	})
}

func deployPipelineCase(t *testing.T, s harness.Suite, kind pipelineKind, vars map[string]any) pipelineCase {
	t.Helper()
	return newPipelineCase(t, deployPipeline(t, s, kind, vars))
}

func newPipelineCase(t *testing.T, d harness.Deployment) pipelineCase {
	t.Helper()
	runUuid := d.LatestRunUuid(t)
	return pipelineCase{Deployment: d, runUuid: runUuid, runLog: d.RunLog(t, runUuid)}
}

func withSecret(vars map[string]any, secret string) map[string]any {
	vars["secret"] = secret
	return vars
}

func withDeletion(vars map[string]any) map[string]any {
	vars["deletion_mode"] = "DELETE"
	return vars
}

func (c pipelineCase) requireStatus(t *testing.T, status enum.Entry[client.BuildingBlockStatus]) {
	t.Helper()
	require.Equalf(t, status, c.ReadBuildingBlock(t).Status.Status, "run log:\n%s", c.runLog)
}

func (c pipelineCase) assertTriggerStep(t *testing.T, status string) {
	t.Helper()
	logs, err := c.Api.BuildingBlockRun.GetLogs(t.Context(), c.runUuid)
	require.NoError(t, err)
	require.NotEmptyf(t, logs.Steps, "run log:\n%s", c.runLog)
	assert.Equalf(t, status, logs.Steps[0].Status, "run log:\n%s", c.runLog)
}

func (c pipelineCase) assertMockStep(t *testing.T, status executor.Status) {
	t.Helper()
	assert.Contains(t, c.runLog, "## "+mockpipeline.Step+" "+string(status))
}

func (c pipelineCase) assertHandedOver(t *testing.T) {
	t.Helper()
	c.Runner.Records.Await(t, "of the runner that the run finished", runFinished(c.runUuid))
	runnerproc.AssertNoRecord(t, c.Runner.Records.OfRun(t, c.runUuid), "of the runner that the run failed", runFailed)
}

func (c pipelineCase) destroyTrigger(t *testing.T, p *mockpipeline.Pipeline) mockpipeline.Trigger {
	t.Helper()
	c.DestroyBuildingBlock(t)
	triggers := p.Triggers(t)
	require.Len(t, triggers, 2, "the deletion triggers the pipeline a second time")
	return triggers[1]
}

func (c pipelineCase) assertDestroySucceeded(t *testing.T) {
	t.Helper()
	c.Runner.Records.Await(t, "of the deletion run that it sends SUCCEEDED", func(r runnerproc.Record) bool {
		return r.Attr("run") != "" && r.Attr("run") != c.runUuid && sendsFinalStatus(succeeded)(r)
	})
}

func abortSync(t *testing.T, s harness.Suite, kind pipelineKind, p *mockpipeline.Pipeline, vars map[string]any) {
	t.Helper()
	vars["wait_for_completion"] = false
	d := deployPipeline(t, s, kind, vars)
	p.AwaitPoll(t, runnerproc.RunTimeout, runnerproc.PollInterval)
	runUuid := d.LatestRunUuid(t)

	d.AbortRun(t, runUuid)

	d.AwaitBuildingBlock(t, client.BuildingBlockStatusAborted)
	d.Runner.Records.Await(t, "of the runner that the run finished", runFinished(runUuid))
	runnerproc.AssertNoRecord(t, d.Runner.Records.OfRun(t, runUuid), "of the runner that the run failed", runFailed)
	assert.True(t, p.RequireOneRun(t).Canceled, "the runner did not cancel the pipeline run")
}

func filterKeys(values map[string]string, keys ...string) map[string]string {
	filtered := map[string]string{}
	for _, key := range keys {
		if value, found := values[key]; found {
			filtered[key] = value
		}
	}
	return filtered
}
