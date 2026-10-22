package testacc

import (
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/meshcloud/meshstack-cli/client"
	"github.com/meshcloud/meshstack-cli/client/types/enum"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func withSecret(vars map[string]any, secret string) map[string]any {
	vars["secret"] = secret
	return vars
}

func TestAccGitHubAsync(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct {
		outcome            string
		omitRunObjectInput bool
	}{
		"SUCCEEDED with the run object": {statusSucceeded, false},
		"SUCCEEDED with the run URL":    {statusSucceeded, true},
		"FAILED":                        {statusFailed, true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s := requireExecutor(t, githubWorkflow)
			app := s.pipelines.newGitHubApp(t, githubOptions{outcome: c.outcome, async: true})
			secret := rand.Text()

			pc := s.deployPipelineCase(t, githubWorkflow, withSecret(app.vars(c.omitRunObjectInput), secret))

			pc.requireStatus(t, buildingBlockStatuses[c.outcome])
			pc.assertTriggerStep(t, statusSucceeded)
			pc.assertMockStep(t, c.outcome)
			pc.assertHandedOver(t)
			trigger := app.requireOneTrigger(t)
			assert.Equal(t, "main", trigger.ref)
			if c.omitRunObjectInput {
				assert.True(t, strings.HasSuffix(trigger.params["buildingBlockRunUrl"], pc.runUuid))
				assert.NotEmpty(t, trigger.params["MESHSTACK_RUN_TOKEN"])
				assert.NotContains(t, trigger.params, "buildingBlockRun")
				for input, value := range trigger.params {
					assert.NotEqualf(t, secret, value, "input %s carries the sensitive input, which the workflow reads from the run URL", input)
				}
			} else {
				require.Contains(t, trigger.params, "buildingBlockRun")
				assert.Len(t, trigger.params, 1)
				runJson, err := base64.StdEncoding.DecodeString(trigger.params["buildingBlockRun"])
				require.NoError(t, err)
				assert.NotContains(t, string(runJson), "appPem", "the run object leaves out the GitHub App key")
				run, err := decodeRun(runJson)
				require.NoError(t, err)
				assert.NotEmpty(t, run.Spec.RunToken)
				assert.Equal(t, map[string]string{"text": "hello", "secret": secret}, filterKeys(runInputs(run), "text", "secret"))
			}
			pc.runner.assertNotLoggedAtInfo(t, secret)
		})
	}
}

func TestAccGitHubSync(t *testing.T) {
	t.Parallel()
	for _, outcome := range []string{statusSucceeded, statusFailed} {
		t.Run(outcome, func(t *testing.T) {
			t.Parallel()
			s := requireExecutor(t, githubWorkflow)
			app := s.pipelines.newGitHubApp(t, githubOptions{outcome: outcome})

			c := s.deployPipelineCase(t, githubWorkflow, withSecret(app.vars(true), rand.Text()))

			c.requireStatus(t, buildingBlockStatuses[outcome])
			c.assertTriggerStep(t, outcome)
			assert.Contains(t, c.runLog, githubJob, "the runner reports the jobs of the workflow run as steps")
			trigger := app.requireOneTrigger(t)
			assert.NotContains(t, trigger.params, "MESHSTACK_RUN_TOKEN", "a sync workflow does not report itself")
		})
	}
	t.Run("dispatch without run details, run found by time", func(t *testing.T) {
		t.Parallel()
		s := requireExecutor(t, githubWorkflow)
		app := s.pipelines.newGitHubApp(t, githubOptions{outcome: statusSucceeded, withoutRunDetails: true})

		c := s.deployPipelineCase(t, githubWorkflow, withSecret(app.vars(true), rand.Text()))

		c.requireStatus(t, client.BuildingBlockStatusSucceeded)
		c.assertTriggerStep(t, statusSucceeded)
		assert.Contains(t, c.runLog, githubJob)
	})
	t.Run(statusAborted, func(t *testing.T) {
		t.Parallel()
		s := requireExecutor(t, githubWorkflow)
		app := s.pipelines.newGitHubApp(t, githubOptions{})

		s.abortSync(t, githubWorkflow, app.pipeline, withSecret(app.vars(true), rand.Text()))
	})
}

func TestAccGitHubWithoutActionsWrite(t *testing.T) {
	t.Parallel()
	s := requireExecutor(t, githubWorkflow)
	app := s.pipelines.newGitHubApp(t, githubOptions{outcome: statusSucceeded, async: true, withoutActionsWrite: true})

	c := s.deployPipelineCase(t, githubWorkflow, withSecret(app.vars(true), rand.Text()))

	c.requireStatus(t, client.BuildingBlockStatusFailed)
	c.assertTriggerStep(t, statusFailed)
	assert.Contains(t, c.runLog, "actions")
	assert.Empty(t, app.received())
}

func TestAccGitHubUnexpectedInputs(t *testing.T) {
	t.Parallel()
	s := requireExecutor(t, githubWorkflow)
	app := s.pipelines.newGitHubApp(t, githubOptions{outcome: statusSucceeded, async: true, declaredInputs: []string{"buildingBlockRun"}})

	c := s.deployPipelineCase(t, githubWorkflow, withSecret(app.vars(true), rand.Text()))

	c.requireStatus(t, client.BuildingBlockStatusFailed)
	c.assertTriggerStep(t, statusFailed)
	assert.Contains(t, c.runLog, "buildingBlockRunUrl", "the run log names the input that the workflow does not declare")
	assert.Empty(t, app.received())
}

func TestAccGitLab(t *testing.T) {
	t.Parallel()
	for _, outcome := range []string{statusSucceeded, statusFailed} {
		t.Run(outcome, func(t *testing.T) {
			t.Parallel()
			s := requireExecutor(t, gitlabPipeline)
			project := s.pipelines.newGitLabProject(t, outcome)
			secret := rand.Text()

			c := s.deployPipelineCase(t, gitlabPipeline, withSecret(project.vars(), secret))

			c.requireStatus(t, buildingBlockStatuses[outcome])
			c.assertTriggerStep(t, statusSucceeded)
			c.assertMockStep(t, outcome)
			c.assertHandedOver(t)
			trigger := project.requireOneTrigger(t)
			assert.Equal(t, "main", trigger.ref)
			assert.Equal(t, "APPLY", trigger.params["variables[MESHSTACK_BEHAVIOR]"])
			assert.Equal(t, "hello", trigger.params["inputs[text]"])
			assert.Equal(t, secret, trigger.params["inputs[secret]"])
			assert.Equal(t, "hello env", trigger.params["variables[ENV_TEXT]"])
			assert.NotContains(t, trigger.params["variables[MESHSTACK_RUN]"], project.triggerToken, "the run object leaves out the pipeline trigger token")
			for _, link := range []string{"SELF_URL", "REGISTER_SOURCE_URL", "UPDATE_SOURCE_URL", "BASE_URL"} {
				assert.NotEmpty(t, trigger.params["variables[MESHSTACK_"+link+"]"])
			}
			c.runner.assertNotLoggedAtInfo(t, secret)
		})
	}
}

func TestAccGitLabUnknownProject(t *testing.T) {
	t.Parallel()
	s := requireExecutor(t, gitlabPipeline)
	project := s.pipelines.newGitLabProject(t, statusSucceeded)
	vars := withSecret(project.vars(), rand.Text())
	vars["project_id"] = "4711"

	c := s.deployPipelineCase(t, gitlabPipeline, vars)

	c.requireStatus(t, client.BuildingBlockStatusFailed)
	c.assertTriggerStep(t, statusFailed)
	assert.Contains(t, c.runLog, "404")
	assert.Empty(t, project.received())
}

func TestAccAzureDevOpsAsync(t *testing.T) {
	t.Parallel()
	for _, outcome := range []string{statusSucceeded, statusFailed} {
		t.Run(outcome, func(t *testing.T) {
			t.Parallel()
			s := requireExecutor(t, azureDevOps)
			pipeline := s.pipelines.newAzureDevOpsPipeline(t, outcome, true)
			secret := rand.Text()

			c := s.deployPipelineCase(t, azureDevOps, withSecret(pipeline.vars(), secret))

			c.requireStatus(t, buildingBlockStatuses[outcome])
			c.assertTriggerStep(t, statusSucceeded)
			c.assertMockStep(t, outcome)
			c.assertHandedOver(t)
			trigger := pipeline.requireOneTrigger(t)
			assert.Equal(t, "refs/heads/main", trigger.ref)
			assert.Equal(t, map[string]string{"text": "hello", "secret": secret, "MESHSTACK_BEHAVIOR": "APPLY"},
				filterKeys(trigger.params, "text", "secret", "MESHSTACK_BEHAVIOR"))
			assert.True(t, strings.HasSuffix(trigger.params["MESHSTACK_RUN_URL"], c.runUuid))
			assert.NotEmpty(t, trigger.params["MESHSTACK_RUN_TOKEN"])
			c.runner.assertNotLoggedAtInfo(t, secret)
		})
	}
}

func TestAccAzureDevOpsSync(t *testing.T) {
	t.Parallel()
	for _, outcome := range []string{statusSucceeded, statusFailed} {
		t.Run(outcome, func(t *testing.T) {
			t.Parallel()
			s := requireExecutor(t, azureDevOps)
			pipeline := s.pipelines.newAzureDevOpsPipeline(t, outcome, false)

			c := s.deployPipelineCase(t, azureDevOps, withSecret(pipeline.vars(), rand.Text()))

			c.requireStatus(t, buildingBlockStatuses[outcome])
			c.assertTriggerStep(t, outcome)
			assert.Contains(t, c.runLog, azureDevOpsStage, "the runner reports the stages of the pipeline run as steps")
			assert.NotContains(t, pipeline.requireOneTrigger(t).params, "MESHSTACK_RUN_TOKEN", "a sync pipeline does not report itself")
		})
	}
	t.Run(statusAborted, func(t *testing.T) {
		t.Parallel()
		s := requireExecutor(t, azureDevOps)
		pipeline := s.pipelines.newAzureDevOpsPipeline(t, "", false)

		s.abortSync(t, azureDevOps, pipeline.pipeline, withSecret(pipeline.vars(), rand.Text()))
	})
}

func TestAccAzureDevOpsUnauthorized(t *testing.T) {
	t.Parallel()
	s := requireExecutor(t, azureDevOps)
	pipeline := s.pipelines.newAzureDevOpsPipeline(t, statusSucceeded, true)
	vars := withSecret(pipeline.vars(), rand.Text())
	vars["pat"] = rand.Text()

	c := s.deployPipelineCase(t, azureDevOps, vars)

	c.requireStatus(t, client.BuildingBlockStatusFailed)
	c.assertTriggerStep(t, statusFailed)
	assert.Contains(t, c.runLog, "401")
	assert.Empty(t, pipeline.received())
}

var buildingBlockStatuses = map[string]enum.Entry[client.BuildingBlockStatus]{
	statusSucceeded: client.BuildingBlockStatusSucceeded,
	statusFailed:    client.BuildingBlockStatusFailed,
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
