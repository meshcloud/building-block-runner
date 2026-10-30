package testacc

import (
	"strings"
	"testing"
	"time"

	"github.com/meshcloud/meshstack-cli/client"
	"github.com/stretchr/testify/assert"

	"github.com/meshcloud/building-block-runner/v2/internal/executor"
	"github.com/meshcloud/building-block-runner/v2/internal/testacc/harness"
	"github.com/meshcloud/building-block-runner/v2/internal/testacc/mockazuredevops"
	"github.com/meshcloud/building-block-runner/v2/internal/testacc/mockgithub"
	"github.com/meshcloud/building-block-runner/v2/internal/testacc/mockgitlab"
	"github.com/meshcloud/building-block-runner/v2/internal/testacc/mockpipeline"
	"github.com/meshcloud/building-block-runner/v2/internal/testacc/runnerproc"
)

// TestAccAsyncTf plays the workaround of asynchronous Terraform: the module starts a pipeline that reports the run itself.
func TestAccAsyncTf(t *testing.T) {
	t.Parallel()
	s := requireSuite(t)
	// The runner hands over only after the apply, so a pipeline that reports at once would end the run first.
	options := mockpipeline.Options{Outcome: executor.StatusSucceeded, Async: true, ReportDelay: 5 * time.Second}

	for _, tc := range []struct {
		pipeline    string
		ref         string
		runUrlParam string
		start       func(t *testing.T) (p *mockpipeline.Pipeline, triggerUrl, triggerToken string)
	}{
		{pipeline: "gitlab", ref: "main", runUrlParam: "variables[MESHSTACK_RUN_URL]", start: func(t *testing.T) (*mockpipeline.Pipeline, string, string) {
			t.Helper()
			project := mockgitlab.New(t, s.Api.Runner, options)
			return project.Pipeline, project.TriggerUrl(t), project.TriggerToken
		}},
		{pipeline: "github", ref: "main", runUrlParam: "buildingBlockRunUrl", start: func(t *testing.T) (*mockpipeline.Pipeline, string, string) {
			t.Helper()
			app := mockgithub.New(t, s.Api.Runner, mockgithub.Options{Options: options})
			return app.Pipeline, app.DispatchUrl(t), app.InstallationToken
		}},
		{pipeline: "azuredevops", ref: "refs/heads/main", runUrlParam: "MESHSTACK_RUN_URL", start: func(t *testing.T) (*mockpipeline.Pipeline, string, string) {
			t.Helper()
			p := mockazuredevops.New(t, s.Api.Runner, options)
			return p.Pipeline, p.RunsUrl(t), p.Pat
		}},
	} {
		t.Run(tc.pipeline, func(t *testing.T) {
			t.Parallel()
			p, triggerUrl, triggerToken := tc.start(t)

			pc := newPipelineCase(t, harness.Deploy(t, s, harness.Case{
				Files:              caseFiles(t),
				Bootstrap:          tfblockBootstrap(t),
				ImplementationType: "TERRAFORM",
				Runner:             runnerproc.Options{ForType: "tf"},
				Vars: map[string]any{
					"async":                          true,
					"use_mesh_http_backend_fallback": false,
					"static_inputs":                  map[string]string{"pipeline": tc.pipeline, "trigger_url": triggerUrl, "trigger_token": triggerToken},
				},
			}))

			pc.requireStatus(t, client.BuildingBlockStatusSucceeded)
			assert.Contains(t, pc.runLog, "## Prepare Run "+succeeded)
			assert.Contains(t, pc.runLog, "WARN: asynchronous Terraform")
			assert.NotContains(t, pc.runLog, "Run Terraform Apply", "an async run reports its single trigger step")
			pc.assertMockStep(t, executor.StatusSucceeded)
			pc.assertHandedOver(t)
			pc.Runner.Records.Await(t, "of the run that it stays IN_PROGRESS", func(r runnerproc.Record) bool {
				return r.Attr("run") == pc.runUuid && sendsFinalStatus(inProgress)(r)
			})
			trigger := p.RequireOneTrigger(t)
			assert.Equal(t, tc.ref, trigger.Ref)
			assert.True(t, strings.HasSuffix(trigger.Params[tc.runUrlParam], pc.runUuid))

			destroy := pc.destroyTrigger(t, p)
			assert.NotEqual(t, trigger.Params[tc.runUrlParam], destroy.Params[tc.runUrlParam],
				"the apply of the DESTROY run triggers the pipeline with the deletion run")
		})
	}
}
