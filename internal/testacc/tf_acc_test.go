package testacc

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/meshcloud/meshstack-cli/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const statusInProgress = "IN_PROGRESS"

func tfBlockCase(vars map[string]any) testCase {
	return testCase{name: "tfblock", implementationType: "TERRAFORM", runner: tfRunner, vars: vars}
}

func TestAccChildKilled(t *testing.T) {
	t.Parallel()
	s := requireSuite(t)

	d := s.deploy(t, tfBlockCase(map[string]any{
		// $PPID is the run process the runner started.
		"pre_run_script": `if [ "$1" = APPLY ]; then kill -9 "$PPID"; fi`,
	}))

	bb := d.readBuildingBlock(t)
	runUuid := d.latestRunUuid(t)
	runLog := d.runLog(t, runUuid)
	require.Equalf(t, client.BuildingBlockStatusFailed, bb.Status.Status, "run log:\n%s", runLog)
	assert.Contains(t, runLog, "before it reported its status")
	assertRecord(t, d.runner.records.ofRun(runUuid), "of the runner that reports FAILED for the run", func(r record) bool {
		return r.attr("msg") == "run failed" && strings.Contains(r.attr("message"), "exit code")
	})
}

func TestAccAsyncHandOver(t *testing.T) {
	t.Parallel()
	s := requireSuite(t)

	d := s.deploy(t, tfBlockCase(map[string]any{"async": true, "wait_for_completion": false, "purge_on_delete": true}))

	handOver := d.runner.records.await(t, "of the run that it hands over", func(r record) bool {
		return r.attr("msg") == "sending the final status" && r.attr("status") == statusInProgress
	})
	runUuid := handOver.attr("run")
	d.runner.records.await(t, "of the runner that the run finished", func(r record) bool {
		return r.attr("run") == runUuid && r.attr("msg") == "run finished"
	})
	assert.Equal(t, statusInProgress, d.readRun(t, runUuid).Status)
	assert.Equal(t, client.BuildingBlockStatusInProgress, d.readBuildingBlock(t).Status.Status)
}

func TestAccBackendWorkspace(t *testing.T) {
	t.Parallel()
	s := requireSuite(t)
	stateDir := s.workDir(t, "state")
	workspaceDir := filepath.Join(stateDir, "ws")
	c := tfBlockCase(map[string]any{"static_inputs": map[string]string{"state_dir": stateDir}})
	c.module = "backend"

	d := s.deploy(t, c)

	bb := d.readBuildingBlock(t)
	require.Equalf(t, client.BuildingBlockStatusSucceeded, bb.Status.Status, "run log:\n%s", d.runLog(t, d.latestRunUuid(t)))
	workspaces, err := os.ReadDir(workspaceDir)
	require.NoError(t, err)
	require.Len(t, workspaces, 1)
	assert.True(t, strings.HasSuffix(workspaces[0].Name(), d.buildingBlockUuid), "workspace %s does not end with the building block uuid", workspaces[0].Name())

	d.bootstrap.destroy(t, buildingBlockResource)
	assert.NoDirExists(t, filepath.Join(workspaceDir, workspaces[0].Name()))
}

func TestAccStalePlan(t *testing.T) {
	t.Parallel()
	s := requireSuite(t)
	stateDir := s.workDir(t, "state")
	c := tfBlockCase(map[string]any{"static_inputs": map[string]string{"state_dir": stateDir}, "approve_manual_triggers": true})
	c.module = "staleplan"

	d := s.deploy(t, c)
	require.Equalf(t, client.BuildingBlockStatusSucceeded, d.readBuildingBlock(t).Status.Status, "run log:\n%s", d.runLog(t, d.latestRunUuid(t)))

	applyUuid, approve := d.triggerForApproval(t)
	bumpStateSerial(t, stateDir, d.buildingBlockUuid)
	approve()

	d.awaitRun(t, applyUuid, runTimeout, statusSucceeded, statusFailed)
	runLog := d.runLog(t, applyUuid)
	require.Equalf(t, statusFailed, d.readRun(t, applyUuid).Status, "run log:\n%s", runLog)
	assert.Contains(t, runLog, "Saved plan is stale")
	assert.Contains(t, runLog, "Please re-run the dry-run")
}

// bumpStateSerial makes tofu reject a plan made before as stale.
func bumpStateSerial(t *testing.T, stateDir, buildingBlockUuid string) {
	t.Helper()
	states, err := filepath.Glob(filepath.Join(stateDir, "ws", "*"+buildingBlockUuid, "terraform.tfstate"))
	require.NoError(t, err)
	require.Len(t, states, 1)
	encoded, err := os.ReadFile(states[0])
	require.NoError(t, err)
	var state map[string]jsontext.Value
	require.NoError(t, json.Unmarshal(encoded, &state))
	var serial int
	require.NoError(t, json.Unmarshal(state["serial"], &serial))
	state["serial"], err = json.Marshal(serial + 1)
	require.NoError(t, err)
	encoded, err = json.Marshal(state)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(states[0], encoded, 0o600))
}

func TestAccTerraform155(t *testing.T) {
	t.Parallel()
	s := requireSuite(t)
	c := tfBlockCase(map[string]any{"pre_run_script": "terraform version"})
	c.terraformVersion = "1.5.5"

	d := s.deploy(t, c)

	bb := d.readBuildingBlock(t)
	runLog := d.runLog(t, d.latestRunUuid(t))
	require.Equalf(t, client.BuildingBlockStatusSucceeded, bb.Status.Status, "run log:\n%s", runLog)
	assert.Equal(t, "hello", outputValues(bb)["text"])
	assert.Contains(t, runLog, "Terraform v1.5.5")
}

func TestAccInitFails(t *testing.T) {
	t.Parallel()
	s := requireSuite(t)
	// The destroy run would fail its init too.
	c := tfBlockCase(map[string]any{"purge_on_delete": true})
	c.module = "initfail"

	d := s.deploy(t, c)

	bb := d.readBuildingBlock(t)
	runLog := d.runLog(t, d.latestRunUuid(t))
	require.Equalf(t, client.BuildingBlockStatusFailed, bb.Status.Status, "run log:\n%s", runLog)
	assert.Contains(t, runLog, "meshcloud/does-not-exist")
	assert.Contains(t, runLog, "Check provider and / or backend config")
}

func TestAccGitRef(t *testing.T) {
	t.Parallel()
	s := requireSuite(t)
	repositoryUrl := s.commitModule(t, "tfblock")
	repository := strings.TrimPrefix(repositoryUrl, "file://")
	runGit(t, repository, "tag", "v1")
	commitText := func(text string) string {
		require.NoError(t, os.WriteFile(filepath.Join(repository, "text.txt"), []byte(text+"\n"), 0o600))
		return commitAll(t, repository, text)
	}
	commit := commitText("commit")
	commitText("main")

	for _, ref := range []struct{ name, ref, text string }{
		{name: "tag", ref: "v1", text: "hello"},
		{name: "commit", ref: commit, text: "commit"},
	} {
		t.Run(ref.name, func(t *testing.T) {
			t.Parallel()

			d := s.deploy(t, tfBlockCase(map[string]any{"repository_url": repositoryUrl, "ref_name": ref.ref}))

			bb := d.readBuildingBlock(t)
			runLog := d.runLog(t, d.latestRunUuid(t))
			require.Equalf(t, client.BuildingBlockStatusSucceeded, bb.Status.Status, "run log:\n%s", runLog)
			assert.Equal(t, ref.text, outputValues(bb)["text"])
			assert.Contains(t, runLog, "Checking out ref "+ref.ref)
		})
	}
}
