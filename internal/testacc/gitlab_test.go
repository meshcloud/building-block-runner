package testacc

import (
	"crypto/rand"
	"testing"

	"github.com/meshcloud/meshstack-cli/client"
	"github.com/stretchr/testify/assert"

	"github.com/meshcloud/building-block-runner/v2/internal/executor"
	"github.com/meshcloud/building-block-runner/v2/internal/testacc/mockgitlab"
	"github.com/meshcloud/building-block-runner/v2/internal/testacc/mockpipeline"
)

func TestAccGitLab(t *testing.T) {
	t.Parallel()
	s := requireSuite(t)
	newProject := func(t *testing.T, outcome executor.Status) mockgitlab.Project {
		t.Helper()
		return mockgitlab.New(t, s.Api.Runner, mockpipeline.Options{Outcome: outcome})
	}

	for _, outcome := range []executor.Status{executor.StatusSucceeded, executor.StatusFailed} {
		t.Run(string(outcome), func(t *testing.T) {
			t.Parallel()
			project := newProject(t, outcome)
			secret := rand.Text()

			c := deployPipelineCase(t, s, gitlabPipeline, withSecret(project.Vars(t), secret))

			c.requireStatus(t, buildingBlockStatuses[outcome])
			c.assertTriggerStep(t, succeeded)
			c.assertMockStep(t, outcome)
			c.assertHandedOver(t)
			trigger := project.RequireOneTrigger(t)
			assert.Equal(t, "main", trigger.Ref)
			assert.Equal(t, "APPLY", trigger.Params["variables[MESHSTACK_BEHAVIOR]"])
			assert.Equal(t, "hello", trigger.Params["inputs[text]"])
			assert.Equal(t, secret, trigger.Params["inputs[secret]"])
			assert.Equal(t, "hello env", trigger.Params["variables[ENV_TEXT]"])
			assert.Equal(t, map[string]any{"num": 42.0, "flag": true, "multi_select": []any{"a", "c"}},
				mockgitlab.ParsedInputs(t, trigger.Params, "num", "flag", "multi_select"))
			assert.Equal(t, "true", trigger.Params["variables[ENV_FLAG]"])
			assert.Equal(t, `["a","c"]`, trigger.Params["variables[ENV_MULTI_SELECT]"])
			assert.NotContains(t, trigger.Params["variables[MESHSTACK_RUN]"], project.TriggerToken, "the run object leaves out the pipeline trigger token")
			for _, link := range []string{"SELF_URL", "REGISTER_SOURCE_URL", "UPDATE_SOURCE_URL", "BASE_URL"} {
				assert.NotEmpty(t, trigger.Params["variables[MESHSTACK_"+link+"]"])
			}
			c.Runner.Records.AssertNotLoggedAtInfo(t, secret)
		})
	}

	t.Run("DESTROY", func(t *testing.T) {
		t.Parallel()
		project := newProject(t, executor.StatusSucceeded)

		c := deployPipelineCase(t, s, gitlabPipeline, withDeletion(withSecret(project.Vars(t), rand.Text())))

		trigger := c.destroyTrigger(t, project.Pipeline)
		assert.Equal(t, "DESTROY", trigger.Params["variables[MESHSTACK_BEHAVIOR]"])
		assert.Equal(t, "main", trigger.Ref)
	})

	t.Run("UnknownProject", func(t *testing.T) {
		t.Parallel()
		project := newProject(t, executor.StatusSucceeded)
		vars := withSecret(project.Vars(t), rand.Text())
		vars["project_id"] = "4711"

		c := deployPipelineCase(t, s, gitlabPipeline, vars)

		c.requireStatus(t, client.BuildingBlockStatusFailed)
		c.assertTriggerStep(t, failed)
		assert.Contains(t, c.runLog, "404")
		assert.Empty(t, project.Triggers(t))
	})
}
