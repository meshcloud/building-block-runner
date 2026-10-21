package tf

import (
	"context"
	"errors"
	"io"
	"testing"
	"testing/synctest"
	"time"

	"github.com/hashicorp/terraform-exec/tfexec"
	"github.com/meshcloud/meshstack-cli/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	tfconfig "github.com/meshcloud/building-block-runner/internal/config/tf"
)

type fakeWorkspaces struct {
	Facade

	available []string
	selectErr error
	selected  []string
	deleted   []string
	deadlines []time.Time
	blockInit bool
}

func (f *fakeWorkspaces) Init(ctx context.Context, _ ...tfexec.InitOption) error {
	f.recordDeadline(ctx)
	if f.blockInit {
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}

func (f *fakeWorkspaces) WorkspaceList(ctx context.Context, _ ...tfexec.WorkspaceListOption) ([]string, string, error) {
	f.recordDeadline(ctx)
	return f.available, "default", nil
}

func (f *fakeWorkspaces) WorkspaceNew(ctx context.Context, _ string, _ ...tfexec.WorkspaceNewCmdOption) error {
	f.recordDeadline(ctx)
	return nil
}

func (f *fakeWorkspaces) WorkspaceSelect(ctx context.Context, workspace string, _ ...tfexec.WorkspaceSelectOption) error {
	f.recordDeadline(ctx)
	f.selected = append(f.selected, workspace)
	return f.selectErr
}

func (f *fakeWorkspaces) WorkspaceDelete(ctx context.Context, workspace string, _ ...tfexec.WorkspaceDeleteCmdOption) error {
	f.recordDeadline(ctx)
	f.deleted = append(f.deleted, workspace)
	return nil
}

func (*fakeWorkspaces) SetStdout(io.Writer) {}

func (*fakeWorkspaces) SetStderr(io.Writer) {}

func (f *fakeWorkspaces) recordDeadline(ctx context.Context) {
	deadline, _ := ctx.Deadline()
	f.deadlines = append(f.deadlines, deadline)
}

func newWorkspaceCmd(t *testing.T) *GenericCmd {
	t.Helper()
	tfRun := run{}
	tfRun.Spec.BuildingBlock.Uuid = "bb-id"
	tfRun.Spec.Definition.Spec.Implementation.Terraform = &client.MeshRunnerTerraformImplementation{}
	_, steps, _ := newTestReporter(t)
	return &GenericCmd{ctx: t.Context(), run: tfRun, cfg: tfconfig.Default(), steps: steps, useWorkspaces: true}
}

func Test_InitWorkspace_SharesOneInitTimeoutDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		workspaces := &fakeWorkspaces{available: []string{"default"}}
		tfcmd := newWorkspaceCmd(t)
		start := time.Now()
		ctx, cancel := tfcmd.withInitTimeout(t.Context(), "the preparation")
		defer cancel()
		synctest.Sleep(time.Minute)

		require.NoError(t, tfcmd.initWorkspace(ctx, workspaces))

		deadline := start.Add(5 * time.Minute)
		assert.Equal(t, []time.Time{deadline, deadline, deadline}, workspaces.deadlines)
	})
}

func Test_InitWorkspace_NamesInitTimeoutAndTheStepItHit(t *testing.T) {
	tfcmd := newWorkspaceCmd(t)
	tfcmd.cfg.InitTimeout.Duration = time.Millisecond
	ctx, cancel := tfcmd.withInitTimeout(t.Context(), "the preparation (clone, tf install, init, workspace)")
	defer cancel()

	err := tfcmd.initWorkspace(ctx, &fakeWorkspaces{blockInit: true})

	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.EqualError(t, err, "the preparation (clone, tf install, init, workspace) exceeded tf.initTimeout of 1ms during tf init: context deadline exceeded")
}

func Test_SelectWorkspace_ReturnsTheNameOfTheWorkspace(t *testing.T) {
	workspaces := &fakeWorkspaces{available: []string{"default", "my-ws-bb-id"}}

	workspace, err := newWorkspaceCmd(t).selectWorkspace(t.Context(), workspaces)

	require.NoError(t, err)
	assert.Equal(t, "my-ws-bb-id", workspace)
	assert.Equal(t, []string{"my-ws-bb-id"}, workspaces.selected)
}

func Test_SelectWorkspace_FailsWhenTheSelectFails(t *testing.T) {
	workspaces := &fakeWorkspaces{available: []string{"my-ws-bb-id"}, selectErr: errors.New("state locked")}

	_, err := newWorkspaceCmd(t).selectWorkspace(t.Context(), workspaces)

	assert.EqualError(t, err, "select the workspace my-ws-bb-id: state locked")
}

func Test_DeleteWorkspaceIfNeeded_DeletesTheWorkspaceOfTheBuildingBlockOnly(t *testing.T) {
	t.Run("deletes the workspace", func(t *testing.T) {
		workspaces := &fakeWorkspaces{available: []string{"default", "my-ws-bb-id"}}

		newWorkspaceCmd(t).deleteWorkspaceIfNeeded(workspaces)

		assert.Equal(t, []string{"my-ws-bb-id"}, workspaces.deleted)
		assert.WithinDuration(t, time.Now().Add(5*time.Minute), workspaces.deadlines[len(workspaces.deadlines)-1], time.Second)
	})

	t.Run("deletes nothing without a workspace", func(t *testing.T) {
		workspaces := &fakeWorkspaces{available: []string{"default"}}

		newWorkspaceCmd(t).deleteWorkspaceIfNeeded(workspaces)

		assert.Empty(t, workspaces.deleted)
	})
}
