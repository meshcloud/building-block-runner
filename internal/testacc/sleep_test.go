package testacc

import (
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/meshcloud/meshstack-cli/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/v2/internal/testacc/harness"
	"github.com/meshcloud/building-block-runner/v2/internal/testacc/runnerproc"
	"github.com/meshcloud/building-block-runner/v2/internal/testacc/toxiproxy"
)

const blockedLongerThanKeepalive = 75 * time.Second

func TestAccSleep(t *testing.T) {
	t.Parallel()
	s := requireSuite(t)
	sleepCase := func(t *testing.T, runner runnerproc.Options, vars map[string]any) harness.Case {
		t.Helper()
		defaults := map[string]any{"sleep_seconds": 0, "approve_manual_triggers": false, "wait_for_completion": true, "pre_run_script": echoBehaviorScript}
		maps.Copy(defaults, vars)
		return harness.Case{Files: caseFiles(t), ImplementationType: "TERRAFORM", Runner: runner, Vars: defaults}
	}

	t.Run("Timeout", func(t *testing.T) {
		t.Parallel()
		const timeout = "30s"

		d := harness.Deploy(t, s, sleepCase(t, runnerproc.Options{ForType: "tf", Timeout: timeout}, map[string]any{"sleep_seconds": 600}))

		runLog := d.RequireStatus(t, client.BuildingBlockStatusFailed)
		assert.Contains(t, runLog, "the run exceeded the timeout of "+timeout)
	})

	t.Run("Abort", func(t *testing.T) {
		t.Parallel()

		d := harness.Deploy(t, s, sleepCase(t, runnerproc.Options{ForType: "tf"}, map[string]any{"sleep_seconds": 600, "wait_for_completion": false}))

		runUuid := d.Runner.Records.AwaitRunInStep(t, stepExecuteTf)
		d.AbortRun(t, runUuid)
		d.AwaitBuildingBlock(t, client.BuildingBlockStatusAborted)
		runnerproc.AssertRecord(t, d.Runner.Records.OfRun(t, runUuid), "of the run that it sends ABORTED", sendsFinalStatus(aborted))

		_, err := s.Api.BuildingBlockV2.TriggerRun(t.Context(), d.BuildingBlockUuid)
		require.NoError(t, err)
		creating := d.Runner.Records.Await(t, "of the next run that it got the state lock and creates the resource", func(r runnerproc.Record) bool {
			return r.Attr("run") != runUuid && r.Attr("step") == stepExecuteTf && strings.Contains(r.Attr("msg"), "terraform_data.sleep: Creating...")
		})
		d.Runner.Records.Await(t, "of tofu that it got the interrupt of the aborted run", func(r runnerproc.Record) bool {
			return r.Attr("run") == runUuid && r.Attr("step") == stepExecuteTf && strings.Contains(r.Attr("msg"), "Interrupt received")
		})
		d.AbortRun(t, creating.Attr("run"))
		d.AwaitBuildingBlock(t, client.BuildingBlockStatusAborted)
	})

	t.Run("PlanApproval", func(t *testing.T) {
		t.Parallel()

		d := harness.Deploy(t, s, sleepCase(t, runnerproc.Options{ForType: "tf"}, map[string]any{"approve_manual_triggers": true}))
		d.RequireStatus(t, client.BuildingBlockStatusSucceeded)

		applyUuid, detectUuid, approve := triggerForApproval(t, d)
		approve()

		d.AwaitRun(t, applyUuid, succeeded, failed)
		runLog := d.RunLog(t, applyUuid)
		require.Equalf(t, succeeded, d.ReadRun(t, applyUuid).Status, "run log:\n%s", runLog)
		assert.Contains(t, runLog, "Using the provided plan artifact from the dry-run")
		assert.Contains(t, runLog, "goes to the meshStack http backend")
		assert.Contains(t, runLog, "Resources: 0 added, 1 changed, 0 destroyed.", "the plan updates the resource that the http backend kept from the first apply")
		assert.Contains(t, runLog, "behavior APPLY")
		assert.Contains(t, d.RunLog(t, detectUuid), "behavior DETECT")
	})

	t.Run("AbortLearnedAfterBlockedUpdates", func(t *testing.T) {
		t.Parallel()
		proxy := s.StartApiProxy(t)

		d := harness.Deploy(t, s, sleepCase(t, runnerproc.Options{ForType: "tf", ApiUrl: proxy.Url}, map[string]any{
			"sleep_seconds": 600, "wait_for_completion": false, "pre_run_script": nil,
		}))

		runUuid := d.Runner.Records.AwaitRunInStep(t, stepExecuteTf)
		unblock := proxy.AddToxic(t, "timeout", toxiproxy.Upstream, map[string]any{"timeout": 0})
		d.AbortRun(t, runUuid)
		time.Sleep(blockedLongerThanKeepalive)
		learned := func(r runnerproc.Record) bool { return r.Attr("msg") == "meshStack aborted the run, cancelling it" }
		require.False(t, slices.ContainsFunc(d.Runner.Records.OfRun(t, runUuid), learned), "the run learned of the abort while meshStack was unreachable")
		runnerproc.AssertRecord(t, d.Runner.Records.OfRun(t, runUuid), "of a status update that failed", func(r runnerproc.Record) bool { return r.Attr("msg") == "status update failed" })
		unblock()

		d.AwaitBuildingBlock(t, client.BuildingBlockStatusAborted)
		records := d.Runner.Records.OfRun(t, runUuid)
		runnerproc.AssertRecord(t, records, "of the run that it learned of the abort", learned)
		runnerproc.AssertRecord(t, records, "of the run that it sends ABORTED", sendsFinalStatus(aborted))
		d.DestroyBuildingBlock(t)
	})
}
