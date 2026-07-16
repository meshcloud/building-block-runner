package tf

// workspace_scenario_test.go pins the tf workspace select/create naming logic
// (matrix row "workspace select/create/delete
// naming logic") for its *current, correct* behavior. The bug pins for this same code
// (tfcmd.go:222-269) live in bug_inventory_test.go, kept separate so this file stays about the
// naming contract a caller can rely on, not the D13 inventory.
//
// These drive GenericTfCmd.useWorkspaceIfNeeded/deleteWorkspaceIfNeeded directly against the
// shared MockedTfFacade's workspace hooks (mockedtffacade.go) — the same "one level below
// Worker" boundary bug_inventory_test.go and the tfcmd_test.go unit suite already
// use for this file's non-I/O logic; workspace naming has no meaningful HTTP-transport-observable
// surface of its own; the Worker matrix rows (worker_scenario_test.go) already prove tfcmd
// wiring end to end.

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/hashicorp/terraform-exec/tfexec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	meshapi "github.com/meshcloud/building-block-runner/internal/meshapi"
)

// newWorkspaceTestCmd builds a *GenericTfCmd wired with the given buildingBlockId/suggestedWorkspace
// and a MockedTfFacade whose workspace hooks are the caller's to configure. The workspace timeout
// must be positive: useWorkspaceIfNeeded/selectWorkspace/deleteWorkspaceIfNeeded each derive a
// context.WithTimeout(ctx, params.wsTimeout), and a zero timeout would hand them an
// already-expired context (threaded via params, formerly AppConfig.WsTimeoutMins).
func newWorkspaceTestCmd(t *testing.T, buildingBlockId, suggestedWorkspace string, useWorkspaces bool) (*GenericTfCmd, *MockedTfFacade) {
	t.Helper()

	mock := &MockedTfFacade{}
	mock.initMockFuncs()

	lw, err := NewLogWrap(slog.New(slog.NewTextHandler(io.Discard, nil)), "/dev/null")
	require.NoError(t, err)

	cmd := &GenericTfCmd{
		ctx: context.Background(),
		params: &TfCmdParams{
			buildingBlockId:    buildingBlockId,
			suggestedWorkspace: suggestedWorkspace,
			useWorkspaces:      useWorkspaces,
			wsTimeout:          1 * time.Minute,
		},
		runContextInfo: &RunContextInfo{
			logwrap: lw,
		},
	}
	return cmd, mock
}

func Test_WorkspaceNaming_UseCaseMatrix(t *testing.T) {
	t.Run("current workspace already suffix-matches building block id: no select, no new", func(t *testing.T) {
		cmd, mock := newWorkspaceTestCmd(t, "bb-current", "org.proj.plat:bb-current", true)

		mock.workspaceListFunc = func(ctx context.Context, opts ...tfexec.WorkspaceListOption) ([]string, string, error) {
			return []string{"some-other-workspace"}, "org.proj.plat:bb-current", nil
		}
		var selectCalls, newCalls []string
		mock.workspaceSelectFunc = func(ctx context.Context, workspace string, opts ...tfexec.WorkspaceSelectOption) error {
			selectCalls = append(selectCalls, workspace)
			return nil
		}
		mock.workspaceNewFunc = func(ctx context.Context, workspace string, opts ...tfexec.WorkspaceNewCmdOption) error {
			newCalls = append(newCalls, workspace)
			return nil
		}

		require.NoError(t, cmd.useWorkspaceIfNeeded(mock))
		assert.Empty(t, selectCalls, "already on the expected workspace: no select needed")
		assert.Empty(t, newCalls, "already on the expected workspace: no new workspace needed")
	})

	t.Run("listed workspace matches: WorkspaceSelect is called, no WorkspaceNew", func(t *testing.T) {
		cmd, mock := newWorkspaceTestCmd(t, "bb-listed", "org.proj.plat:bb-listed", true)

		const matchingWorkspace = "org.proj.plat:bb-listed"
		mock.workspaceListFunc = func(ctx context.Context, opts ...tfexec.WorkspaceListOption) ([]string, string, error) {
			return []string{"unrelated-ws", matchingWorkspace}, "default", nil
		}
		var selectCalls, newCalls []string
		mock.workspaceSelectFunc = func(ctx context.Context, workspace string, opts ...tfexec.WorkspaceSelectOption) error {
			selectCalls = append(selectCalls, workspace)
			return nil
		}
		mock.workspaceNewFunc = func(ctx context.Context, workspace string, opts ...tfexec.WorkspaceNewCmdOption) error {
			newCalls = append(newCalls, workspace)
			return nil
		}

		require.NoError(t, cmd.useWorkspaceIfNeeded(mock))
		assert.Equal(t, []string{matchingWorkspace}, selectCalls)
		assert.Empty(t, newCalls, "an existing match is selected, never (re-)created")
	})

	t.Run("no match: WorkspaceNew is called with the full toWorkspaceStr() name", func(t *testing.T) {
		buildingBlockId := "bb-new"
		run := &meshapi.Run{Spec: meshapi.RunSpecDTO{BuildingBlock: meshapi.BuildingBlockSpecDTO{
			Uuid: buildingBlockId,
			Spec: meshapi.BuildingBlockDetailsSpecDTO{
				WorkspaceIdentifier:    "ws1",
				FullPlatformIdentifier: "plat1",
				// ProjectIdentifier deliberately left empty: an optional identifier meshfed omitted.
			},
		}}}
		fullName := toWorkspaceStr(run)
		require.Equal(t, "ws1..plat1:bb-new", fullName, "sanity-check the fixture's own expectation")

		cmd, mock := newWorkspaceTestCmd(t, buildingBlockId, fullName, true)
		mock.workspaceListFunc = func(ctx context.Context, opts ...tfexec.WorkspaceListOption) ([]string, string, error) {
			return []string{}, "default", nil
		}
		var selectCalls, newCalls []string
		mock.workspaceSelectFunc = func(ctx context.Context, workspace string, opts ...tfexec.WorkspaceSelectOption) error {
			selectCalls = append(selectCalls, workspace)
			return nil
		}
		mock.workspaceNewFunc = func(ctx context.Context, workspace string, opts ...tfexec.WorkspaceNewCmdOption) error {
			newCalls = append(newCalls, workspace)
			return nil
		}

		require.NoError(t, cmd.useWorkspaceIfNeeded(mock))
		assert.Empty(t, selectCalls, "nothing to select when no workspace matches")
		assert.Equal(t, []string{fullName}, newCalls)
	})

	t.Run("useWorkspaces=false: no workspace calls at all (meshStack http backend fallback path)", func(t *testing.T) {
		cmd, mock := newWorkspaceTestCmd(t, "bb-no-ws", "org.proj.plat:bb-no-ws", false)

		var anyCall bool
		mock.workspaceListFunc = func(ctx context.Context, opts ...tfexec.WorkspaceListOption) ([]string, string, error) {
			anyCall = true
			return []string{}, "", nil
		}
		mock.workspaceSelectFunc = func(ctx context.Context, workspace string, opts ...tfexec.WorkspaceSelectOption) error {
			anyCall = true
			return nil
		}
		mock.workspaceNewFunc = func(ctx context.Context, workspace string, opts ...tfexec.WorkspaceNewCmdOption) error {
			anyCall = true
			return nil
		}
		mock.workspaceDeleteFunc = func(ctx context.Context, workspace string, opts ...tfexec.WorkspaceDeleteCmdOption) error {
			anyCall = true
			return nil
		}

		require.NoError(t, cmd.useWorkspaceIfNeeded(mock))
		cmd.deleteWorkspaceIfNeeded(mock)

		assert.False(t, anyCall, "useWorkspaces=false must short-circuit both use and delete with zero tf workspace calls")
	})
}
