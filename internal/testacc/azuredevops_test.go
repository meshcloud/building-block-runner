package testacc

import (
	"crypto/rand"
	"strings"
	"testing"

	"github.com/meshcloud/meshstack-cli/client"
	"github.com/meshcloud/meshstack-cli/client/types/enum"
	"github.com/stretchr/testify/assert"

	"github.com/meshcloud/building-block-runner/v2/internal/executor"
	"github.com/meshcloud/building-block-runner/v2/internal/testacc/mockazuredevops"
	"github.com/meshcloud/building-block-runner/v2/internal/testacc/mockpipeline"
)

func TestAccAzureDevOps(t *testing.T) {
	t.Parallel()
	s := requireSuite(t)
	newPipeline := func(t *testing.T, options mockpipeline.Options) mockazuredevops.Pipeline {
		t.Helper()
		return mockazuredevops.New(t, s.Api.Runner, options)
	}

	t.Run("Async", func(t *testing.T) {
		t.Parallel()
		for _, outcome := range []executor.Status{executor.StatusSucceeded, executor.StatusFailed} {
			t.Run(string(outcome), func(t *testing.T) {
				t.Parallel()
				pipeline := newPipeline(t, mockpipeline.Options{Outcome: outcome, Async: true})
				secret := rand.Text()

				c := deployPipelineCase(t, s, azureDevOps, withSecret(pipeline.Vars(t), secret))

				c.requireStatus(t, buildingBlockStatuses[outcome])
				c.assertTriggerStep(t, succeeded)
				c.assertMockStep(t, outcome)
				c.assertHandedOver(t)
				trigger := pipeline.RequireOneTrigger(t)
				assert.Equal(t, "refs/heads/main", trigger.Ref)
				assert.Equal(t, map[string]string{"text": "hello", "secret": secret, "MESHSTACK_BEHAVIOR": "APPLY"},
					filterKeys(trigger.Params, "text", "secret", "MESHSTACK_BEHAVIOR"))
				assert.True(t, strings.HasSuffix(trigger.Params["MESHSTACK_RUN_URL"], c.runUuid))
				assert.NotEmpty(t, trigger.Params["MESHSTACK_RUN_TOKEN"])
				assert.NotContains(t, trigger.Params, "ENV_TEXT", "a pipeline has no environment for an environment input")
				c.Runner.Records.AssertNotLoggedAtInfo(t, secret)
			})
		}
		t.Run("DESTROY", func(t *testing.T) {
			t.Parallel()
			pipeline := newPipeline(t, mockpipeline.Options{Outcome: executor.StatusSucceeded, Async: true})

			c := deployPipelineCase(t, s, azureDevOps, withDeletion(withSecret(pipeline.Vars(t), rand.Text())))

			trigger := c.destroyTrigger(t, pipeline.Pipeline)
			assert.Equal(t, "DESTROY", trigger.Params["MESHSTACK_BEHAVIOR"])
			assert.Equal(t, "refs/heads/main", trigger.Ref)
		})
	})

	t.Run("Sync", func(t *testing.T) {
		t.Parallel()
		for _, c := range []struct {
			name        string
			outcome     executor.Status
			blockStatus enum.Entry[client.BuildingBlockStatus]
			triggerStep string
			stageStep   string
			message     string
		}{
			{succeeded, executor.StatusSucceeded, client.BuildingBlockStatusSucceeded, succeeded, succeeded, "Azure DevOps pipeline completed successfully"},
			{failed, executor.StatusFailed, client.BuildingBlockStatusFailed, failed, failed, "Azure DevOps pipeline failed"},
			{"canceled in Azure DevOps", executor.StatusAborted, client.BuildingBlockStatusFailed, failed, aborted, "Azure DevOps pipeline was canceled"},
		} {
			t.Run(c.name, func(t *testing.T) {
				t.Parallel()
				pipeline := newPipeline(t, mockpipeline.Options{Outcome: c.outcome})

				pc := deployPipelineCase(t, s, azureDevOps, withSecret(pipeline.Vars(t), rand.Text()))

				pc.requireStatus(t, c.blockStatus)
				pc.assertTriggerStep(t, c.triggerStep)
				assert.Contains(t, pc.runLog, "## Stage: "+mockazuredevops.Stage+" "+c.stageStep, "the runner reports the stages of the pipeline run as steps")
				assert.Contains(t, pc.runLog, "## Stage: "+mockazuredevops.SkippedStage+" "+succeeded)
				assert.Contains(t, pc.runLog, mockazuredevops.SkippedStage+" was skipped")
				assert.Contains(t, pc.runLog, c.message)
				assert.NotContains(t, pipeline.RequireOneTrigger(t).Params, "MESHSTACK_RUN_TOKEN", "a sync pipeline does not report itself")
				assert.False(t, pipeline.RequireOneRun(t).Canceled, "the runner canceled a pipeline run that completed")
			})
		}
		t.Run("DESTROY", func(t *testing.T) {
			t.Parallel()
			pipeline := newPipeline(t, mockpipeline.Options{Outcome: executor.StatusSucceeded})

			c := deployPipelineCase(t, s, azureDevOps, withDeletion(withSecret(pipeline.Vars(t), rand.Text())))

			trigger := c.destroyTrigger(t, pipeline.Pipeline)
			assert.Equal(t, "DESTROY", trigger.Params["MESHSTACK_BEHAVIOR"])
			assert.Equal(t, "refs/heads/main", trigger.Ref)
			c.assertDestroySucceeded(t)
		})
		t.Run(aborted, func(t *testing.T) {
			t.Parallel()
			pipeline := newPipeline(t, mockpipeline.Options{})

			abortSync(t, s, azureDevOps, pipeline.Pipeline, withSecret(pipeline.Vars(t), rand.Text()))
		})
	})

	t.Run("Unauthorized", func(t *testing.T) {
		t.Parallel()
		pipeline := newPipeline(t, mockpipeline.Options{Outcome: executor.StatusSucceeded, Async: true})
		vars := withSecret(pipeline.Vars(t), rand.Text())
		vars["pat"] = rand.Text()

		c := deployPipelineCase(t, s, azureDevOps, vars)

		c.requireStatus(t, client.BuildingBlockStatusFailed)
		c.assertTriggerStep(t, failed)
		assert.Contains(t, c.runLog, "401")
		assert.Empty(t, pipeline.Triggers(t))
	})
}
