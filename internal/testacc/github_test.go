package testacc

import (
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/meshcloud/meshstack-cli/client"
	"github.com/meshcloud/meshstack-cli/client/types/enum"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/v2/internal/executor"
	"github.com/meshcloud/building-block-runner/v2/internal/testacc/mockgithub"
	"github.com/meshcloud/building-block-runner/v2/internal/testacc/mockpipeline"
)

func TestAccGitHub(t *testing.T) {
	t.Parallel()
	s := requireSuite(t)
	newApp := func(t *testing.T, options mockgithub.Options) mockgithub.App {
		t.Helper()
		return mockgithub.New(t, s.Api.Runner, options)
	}

	t.Run("Async", func(t *testing.T) {
		t.Parallel()
		for name, c := range map[string]struct {
			outcome            executor.Status
			omitRunObjectInput bool
		}{
			"SUCCEEDED with the run object": {executor.StatusSucceeded, false},
			"SUCCEEDED with the run URL":    {executor.StatusSucceeded, true},
			"FAILED":                        {executor.StatusFailed, true},
		} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				app := newApp(t, mockgithub.Options{Outcome: c.outcome, Async: true})
				secret := rand.Text()
				vars := withSecret(app.Vars(t, c.omitRunObjectInput), secret)
				vars["permissions"] = []string{"BUILDINGBLOCK_LIST"}

				pc := deployPipelineCase(t, s, githubWorkflow, vars)

				pc.requireStatus(t, buildingBlockStatuses[c.outcome])
				pc.assertTriggerStep(t, succeeded)
				pc.assertMockStep(t, c.outcome)
				pc.assertHandedOver(t)
				trigger := app.RequireOneTrigger(t)
				assert.Equal(t, "main", trigger.Ref)
				if c.omitRunObjectInput {
					assert.True(t, strings.HasSuffix(trigger.Params["buildingBlockRunUrl"], pc.runUuid))
					assert.NotEmpty(t, trigger.Params["MESHSTACK_RUN_TOKEN"])
					assert.NotContains(t, trigger.Params, "buildingBlockRun")
					assert.NotEmpty(t, trigger.Params["MESHSTACK_API_TOKEN"])
					assert.NotEmpty(t, trigger.Params["MESHSTACK_ENDPOINT"])
					app.AssertListed(t, pc.BuildingBlockUuid)
					for input, value := range trigger.Params {
						assert.NotEqualf(t, secret, value, "input %s carries the sensitive input, which the workflow reads from the run URL", input)
					}
				} else {
					require.Contains(t, trigger.Params, "buildingBlockRun")
					assert.Len(t, trigger.Params, 1)
					runJson, err := base64.StdEncoding.DecodeString(trigger.Params["buildingBlockRun"])
					require.NoError(t, err)
					assert.NotContains(t, string(runJson), "appPem", "the run object leaves out the GitHub App key")
					run, err := mockpipeline.DecodeRun(runJson)
					require.NoError(t, err)
					assert.NotEmpty(t, run.Spec.RunToken)
					assert.Equal(t, map[string]string{"text": "hello", "secret": secret}, filterKeys(stringInputs(run), "text", "secret"))
				}
				pc.Runner.Records.AssertNotLoggedAtInfo(t, secret)
			})
		}
		t.Run("DESTROY", func(t *testing.T) {
			t.Parallel()
			app := newApp(t, mockgithub.Options{Outcome: executor.StatusSucceeded, Async: true})
			vars := withDeletion(withSecret(app.Vars(t, true), rand.Text()))
			vars["destroy_workflow"] = mockgithub.DestroyWorkflow

			c := deployPipelineCase(t, s, githubWorkflow, vars)

			trigger := c.destroyTrigger(t, app.Pipeline)
			assert.Equal(t, mockgithub.DestroyWorkflow, trigger.Workflow)
			assert.Equal(t, "main", trigger.Ref)
		})
	})

	t.Run("Sync", func(t *testing.T) {
		t.Parallel()
		for _, c := range []struct {
			name        string
			outcome     executor.Status
			blockStatus enum.Entry[client.BuildingBlockStatus]
			triggerStep string
			jobStep     string
			message     string
		}{
			{succeeded, executor.StatusSucceeded, client.BuildingBlockStatusSucceeded, succeeded, succeeded, "waiting for its run to complete"},
			{failed, executor.StatusFailed, client.BuildingBlockStatusFailed, failed, failed, "completed with the conclusion failure"},
			{"canceled in GitHub", executor.StatusAborted, client.BuildingBlockStatusFailed, failed, aborted, "completed with the conclusion cancelled"},
		} {
			t.Run(c.name, func(t *testing.T) {
				t.Parallel()
				app := newApp(t, mockgithub.Options{Outcome: c.outcome})

				pc := deployPipelineCase(t, s, githubWorkflow, withSecret(app.Vars(t, true), rand.Text()))

				pc.requireStatus(t, c.blockStatus)
				pc.assertTriggerStep(t, c.triggerStep)
				assert.Contains(t, pc.runLog, "## GitHub Job: "+mockgithub.Job+" "+c.jobStep, "the runner reports the jobs of the workflow run as steps")
				assert.Contains(t, pc.runLog, c.message)
				assert.NotContains(t, app.RequireOneTrigger(t).Params, "MESHSTACK_RUN_TOKEN", "a sync workflow does not report itself")
				assert.False(t, app.RequireOneRun(t).Canceled, "the runner canceled a workflow run that completed")
			})
		}
		t.Run("DESTROY", func(t *testing.T) {
			t.Parallel()
			app := newApp(t, mockgithub.Options{Outcome: executor.StatusSucceeded})
			vars := withDeletion(withSecret(app.Vars(t, true), rand.Text()))
			vars["destroy_workflow"] = mockgithub.DestroyWorkflow

			c := deployPipelineCase(t, s, githubWorkflow, vars)

			trigger := c.destroyTrigger(t, app.Pipeline)
			assert.Equal(t, mockgithub.DestroyWorkflow, trigger.Workflow)
			assert.Equal(t, "main", trigger.Ref)
			c.assertDestroySucceeded(t)
		})
		t.Run("dispatch without run details, run found by time", func(t *testing.T) {
			t.Parallel()
			app := newApp(t, mockgithub.Options{Outcome: executor.StatusSucceeded, WithoutRunDetails: true})

			c := deployPipelineCase(t, s, githubWorkflow, withSecret(app.Vars(t, true), rand.Text()))

			c.requireStatus(t, client.BuildingBlockStatusSucceeded)
			c.assertTriggerStep(t, succeeded)
			assert.Contains(t, c.runLog, mockgithub.Job)
		})
		t.Run("installation token expires during the run", func(t *testing.T) {
			t.Parallel()
			app := newApp(t, mockgithub.Options{Outcome: executor.StatusSucceeded, TokenLifetime: 15 * time.Second})

			c := deployPipelineCase(t, s, githubWorkflow, withSecret(app.Vars(t, true), rand.Text()))

			c.requireStatus(t, client.BuildingBlockStatusSucceeded)
			assert.Greater(t, app.IssuedTokens(t), 1, "the runner did not renew the installation token")
		})
		t.Run("workflow run not found", func(t *testing.T) {
			t.Parallel()
			app := newApp(t, mockgithub.Options{Outcome: executor.StatusSucceeded, RunNotFound: true})

			c := deployPipelineCase(t, s, githubWorkflow, withSecret(app.Vars(t, true), rand.Text()))

			c.requireStatus(t, client.BuildingBlockStatusFailed)
			assert.Contains(t, c.runLog, "404")
		})
		t.Run(aborted, func(t *testing.T) {
			t.Parallel()
			app := newApp(t, mockgithub.Options{})

			abortSync(t, s, githubWorkflow, app.Pipeline, withSecret(app.Vars(t, true), rand.Text()))
		})
	})

	t.Run("WithoutActionsWrite", func(t *testing.T) {
		t.Parallel()
		app := newApp(t, mockgithub.Options{Outcome: executor.StatusSucceeded, Async: true, WithoutActionsWrite: true})

		c := deployPipelineCase(t, s, githubWorkflow, withSecret(app.Vars(t, true), rand.Text()))

		c.requireStatus(t, client.BuildingBlockStatusFailed)
		c.assertTriggerStep(t, failed)
		assert.Contains(t, c.runLog, "actions")
		assert.Empty(t, app.Triggers(t))
	})

	t.Run("UnexpectedInputs", func(t *testing.T) {
		t.Parallel()
		app := newApp(t, mockgithub.Options{Outcome: executor.StatusSucceeded, Async: true, DeclaredInputs: []string{"buildingBlockRun"}})

		c := deployPipelineCase(t, s, githubWorkflow, withSecret(app.Vars(t, true), rand.Text()))

		c.requireStatus(t, client.BuildingBlockStatusFailed)
		c.assertTriggerStep(t, failed)
		assert.Contains(t, c.runLog, "buildingBlockRunUrl", "the run log names the input that the workflow does not declare")
		assert.Empty(t, app.Triggers(t))
	})
}

func stringInputs(run client.MeshRunnerRun) map[string]string {
	values := map[string]string{}
	for _, input := range run.Spec.BuildingBlock.Spec.Inputs {
		if text, isString := input.Value.(string); isString {
			values[input.Key] = text
		}
	}
	return values
}
