package testacc

import (
	"testing"

	"github.com/meshcloud/meshstack-cli/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/v2/internal/executor"
	"github.com/meshcloud/building-block-runner/v2/internal/testacc/harness"
	"github.com/meshcloud/building-block-runner/v2/internal/testacc/runnerproc"
)

const (
	stepExecuteTf    = "execute_tf"
	stepPreRunScript = "pre_run_script"

	echoBehaviorScript = `echo "behavior $1" | tee -a "$MESHSTACK_USER_MESSAGE"`
)

var (
	succeeded  = string(executor.StatusSucceeded)
	failed     = string(executor.StatusFailed)
	aborted    = string(executor.StatusAborted)
	inProgress = string(executor.StatusInProgress)
)

func sendsFinalStatus(status string) func(runnerproc.Record) bool {
	return func(r runnerproc.Record) bool {
		return r.Attr("msg") == "sending the final status" && r.Attr("status") == status
	}
}

func runFinished(runUuid string) func(runnerproc.Record) bool {
	return func(r runnerproc.Record) bool { return r.Attr("run") == runUuid && r.Attr("msg") == "run finished" }
}

func runFailed(r runnerproc.Record) bool {
	return r.Attr("msg") == "run failed"
}

func triggerForApproval(t *testing.T, d harness.Deployment) (applyUuid, detectUuid string, approve func()) {
	t.Helper()
	block, err := d.Api.BuildingBlockV2.Read(t.Context(), d.BuildingBlockUuid)
	require.NoError(t, err)
	require.NotNil(t, block)
	_, err = block.Links.Post[client.MeshBuildingBlockV2](t.Context(), "triggerRun", client.MeshBuildingBlockV2RunTrigger{})
	require.NoError(t, err)
	d.AwaitBuildingBlock(t, client.BuildingBlockActionApproveRun)
	applyUuid = d.LatestRunUuid(t)
	apply := d.ReadRun(t, applyUuid)
	detect, err := apply.Links.Get[client.MeshBuildingBlockRun](t.Context(), "predecessor")
	require.NoError(t, err)
	assert.Equal(t, "DETECT", detect.Spec.Behavior)
	assert.Equal(t, succeeded, detect.Status)
	return applyUuid, detect.Metadata.Uuid, func() {
		_, err := apply.Links.Post[struct{}](t.Context(), "approve", client.MeshBuildingBlockRunApproval{PredecessorRunUuid: detect.Metadata.Uuid})
		require.NoError(t, err)
	}
}
