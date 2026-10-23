package tf

import (
	"context"
	"errors"
	"testing"

	"github.com/hashicorp/terraform-exec/tfexec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	tfconfig "github.com/meshcloud/building-block-runner/internal/config/tf"
)

type fakeWorkspaces struct {
	TfFacade

	available []string
	selectErr error
	selected  []string
	deleted   []string
}

func (f *fakeWorkspaces) WorkspaceList(context.Context, ...tfexec.WorkspaceListOption) ([]string, string, error) {
	return f.available, "default", nil
}

func (f *fakeWorkspaces) WorkspaceSelect(_ context.Context, workspace string, _ ...tfexec.WorkspaceSelectOption) error {
	f.selected = append(f.selected, workspace)
	return f.selectErr
}

func (f *fakeWorkspaces) WorkspaceDelete(_ context.Context, workspace string, _ ...tfexec.WorkspaceDeleteCmdOption) error {
	f.deleted = append(f.deleted, workspace)
	return nil
}

func newWorkspaceTfCmd(t *testing.T) *GenericTfCmd {
	t.Helper()
	run := &run{}
	run.Spec.BuildingBlock.Uuid = "bb-id"
	return &GenericTfCmd{ctx: t.Context(), run: run, cfg: tfconfig.Default(), useWorkspaces: true}
}

func Test_SelectWorkspace_ReturnsTheNameOfTheWorkspace(t *testing.T) {
	workspaces := &fakeWorkspaces{available: []string{"default", "my-ws-bb-id"}}

	workspace, err := newWorkspaceTfCmd(t).selectWorkspace(workspaces)

	require.NoError(t, err)
	assert.Equal(t, "my-ws-bb-id", workspace)
	assert.Equal(t, []string{"my-ws-bb-id"}, workspaces.selected)
}

func Test_SelectWorkspace_FailsWhenTheSelectFails(t *testing.T) {
	workspaces := &fakeWorkspaces{available: []string{"my-ws-bb-id"}, selectErr: errors.New("state locked")}

	_, err := newWorkspaceTfCmd(t).selectWorkspace(workspaces)

	assert.EqualError(t, err, "select the workspace my-ws-bb-id: state locked")
}

func Test_DeleteWorkspaceIfNeeded_DeletesTheWorkspaceOfTheBuildingBlockOnly(t *testing.T) {
	t.Run("deletes the workspace", func(t *testing.T) {
		workspaces := &fakeWorkspaces{available: []string{"default", "my-ws-bb-id"}}

		newWorkspaceTfCmd(t).deleteWorkspaceIfNeeded(workspaces)

		assert.Equal(t, []string{"my-ws-bb-id"}, workspaces.deleted)
	})

	t.Run("deletes nothing without a workspace", func(t *testing.T) {
		workspaces := &fakeWorkspaces{available: []string{"default"}}

		newWorkspaceTfCmd(t).deleteWorkspaceIfNeeded(workspaces)

		assert.Empty(t, workspaces.deleted)
	})
}
