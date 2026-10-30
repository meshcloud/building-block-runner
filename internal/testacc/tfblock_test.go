package testacc

import (
	"os"
	"path/filepath"
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

// slowClaimLatency outlasts the minute that the meshStack client waits for response headers.
const slowClaimLatency = 75 * time.Second

func TestAccTfBlock(t *testing.T) {
	t.Parallel()
	s := requireSuite(t)
	tfBlockCase := func(t *testing.T, vars map[string]any) harness.Case {
		t.Helper()
		return harness.Case{Files: caseFiles(t), ImplementationType: "TERRAFORM", Runner: runnerproc.Options{ForType: "tf"}, Vars: vars}
	}

	t.Run("ChildKilled", func(t *testing.T) {
		t.Parallel()

		d := harness.Deploy(t, s, tfBlockCase(t, map[string]any{
			// $PPID is the run process the runner started.
			"pre_run_script": `if [ "$1" = APPLY ]; then kill -9 "$PPID"; fi`,
		}))

		runLog := d.RequireStatus(t, client.BuildingBlockStatusFailed)
		assert.Contains(t, runLog, "before it reported its status")
		runnerproc.AssertRecord(t, d.Runner.Records.OfRun(t, d.LatestRunUuid(t)), "of the runner that reports FAILED for the run", func(r runnerproc.Record) bool {
			return runFailed(r) && strings.Contains(r.Attr("message"), "exit code")
		})
	})

	t.Run("Terraform155", func(t *testing.T) {
		t.Parallel()
		c := tfBlockCase(t, map[string]any{"pre_run_script": "terraform version"})
		c.TerraformVersion = "1.5.5"

		d := harness.Deploy(t, s, c)

		runLog := d.RequireStatus(t, client.BuildingBlockStatusSucceeded)
		assert.Equal(t, "hello", d.Output[string](t, "text"))
		assert.Contains(t, runLog, "Terraform v1.5.5")
	})

	t.Run("GitRef", func(t *testing.T) {
		t.Parallel()
		repositoryUrl := s.CommitModule(t, caseModule(t))
		repository := strings.TrimPrefix(repositoryUrl, "file://")
		harness.RunGit(t, repository, "tag", "v1")
		commitText := func(text string) string {
			require.NoError(t, os.WriteFile(filepath.Join(repository, "text.txt"), []byte(text+"\n"), 0o600))
			return harness.CommitAll(t, repository, text)
		}
		commit := commitText("commit")
		harness.RunGit(t, repository, "branch", "feature", commit)
		commitText("main")

		for _, ref := range []struct{ name, ref, text string }{
			{name: "tag", ref: "v1", text: "hello"},
			{name: "commit", ref: commit, text: "commit"},
			{name: "branch", ref: "feature", text: "commit"},
		} {
			t.Run(ref.name, func(t *testing.T) {
				t.Parallel()

				d := harness.Deploy(t, s, tfBlockCase(t, map[string]any{"repository_url": repositoryUrl, "ref_name": ref.ref}))

				runLog := d.RequireStatus(t, client.BuildingBlockStatusSucceeded)
				assert.Equal(t, ref.text, d.Output[string](t, "text"))
				assert.Contains(t, runLog, "Checking out ref "+ref.ref)
			})
		}
	})

	// Pins the known hazard that meshfed commits a claim before it answers, so a claim that times out loses its run.
	t.Run("SlowClaimAnswerLosesRun", func(t *testing.T) {
		t.Parallel()
		proxy := s.StartApiProxy(t)
		c := tfBlockCase(t, map[string]any{"wait_for_completion": false, "purge_on_delete": true})
		c.Runner.ApiUrl = proxy.Url
		c.WithoutBuildingBlock = true

		d := harness.Deploy(t, s, c)
		d.Runner.Records.Await(t, "of the runner that it claims", func(r runnerproc.Record) bool { return r.Attr("msg") == "claiming runs" })
		unblockClaims := proxy.AddToxic(t, "timeout", toxiproxy.Upstream, map[string]any{"timeout": 0})
		d = d.ApplyBuildingBlock(t)
		require.Eventually(t, func() bool { return d.ReadBuildingBlock(t).Status.LatestRunUuid != nil }, time.Minute, runnerproc.PollInterval)
		runUuid := d.LatestRunUuid(t)
		delayAnswers := proxy.AddToxic(t, "latency", toxiproxy.Downstream, map[string]any{"latency": slowClaimLatency.Milliseconds()})
		unblockClaims()
		d.Runner.Records.Await(t, "of the runner that its claim timed out", func(r runnerproc.Record) bool {
			return r.Attr("msg") == "claiming a run failed" && strings.Contains(r.Attr("error"), "timeout awaiting response headers")
		})
		delayAnswers()

		started := func(r runnerproc.Record) bool { return r.Attr("msg") == "starting run" }
		assert.Never(t, func() bool { return slices.ContainsFunc(d.Runner.Records.OfRun(t, runUuid), started) }, 30*time.Second, runnerproc.PollInterval,
			"the runner started the run whose claim answer it missed")
		assert.Equal(t, inProgress, d.ReadRun(t, runUuid).Status)
		assert.Equal(t, client.BuildingBlockStatusInProgress, d.ReadBuildingBlock(t).Status.Status)
	})
}
