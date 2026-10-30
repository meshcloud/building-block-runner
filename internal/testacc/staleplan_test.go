package testacc

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"os"
	"path/filepath"
	"testing"

	"github.com/meshcloud/meshstack-cli/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/v2/internal/testacc/harness"
	"github.com/meshcloud/building-block-runner/v2/internal/testacc/runnerproc"
)

func TestAccStalePlan(t *testing.T) {
	t.Parallel()
	s := requireSuite(t)
	stateDir := s.WorkDir(t, "state")

	d := harness.Deploy(t, s, harness.Case{
		Files:              caseFiles(t),
		Bootstrap:          tfblockBootstrap(t),
		ImplementationType: "TERRAFORM",
		Runner:             runnerproc.Options{ForType: "tf"},
		Vars:               map[string]any{"static_inputs": map[string]string{"state_dir": stateDir}, "approve_manual_triggers": true},
	})
	d.RequireStatus(t, client.BuildingBlockStatusSucceeded)

	applyUuid, _, approve := triggerForApproval(t, d)
	bumpStateSerial(t, stateDir, d.BuildingBlockUuid)
	approve()

	d.AwaitRun(t, applyUuid, succeeded, failed)
	runLog := d.RunLog(t, applyUuid)
	require.Equalf(t, failed, d.ReadRun(t, applyUuid).Status, "run log:\n%s", runLog)
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
