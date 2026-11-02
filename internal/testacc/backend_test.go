package testacc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/meshcloud/meshstack-cli/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/v2/internal/testacc/harness"
	"github.com/meshcloud/building-block-runner/v2/internal/testacc/runnerproc"
)

func TestAccBackend(t *testing.T) {
	t.Parallel()
	s := requireSuite(t)
	stateDir := s.WorkDir(t, "state")
	workspaceDir := filepath.Join(stateDir, "ws")

	d := harness.Deploy(t, s, harness.Case{
		Files:              caseFiles(t),
		Bootstrap:          tfblockBootstrap(t),
		ImplementationType: "TERRAFORM",
		Runner:             runnerproc.Options{ForType: "tf"},
		Vars:               map[string]any{"static_inputs": map[string]string{"state_dir": stateDir}, "pre_run_script": echoBehaviorScript},
	})

	d.RequireStatus(t, client.BuildingBlockStatusSucceeded)
	workspaces, err := os.ReadDir(workspaceDir)
	require.NoError(t, err)
	require.Len(t, workspaces, 1)
	assert.True(t, strings.HasSuffix(workspaces[0].Name(), d.BuildingBlockUuid), "workspace %s does not end with the building block uuid", workspaces[0].Name())

	d.DestroyBuildingBlock(t)
	assert.NoDirExists(t, filepath.Join(workspaceDir, workspaces[0].Name()))
	d.Runner.Records.Await(t, "of the pre-run script of the DESTROY run", func(r runnerproc.Record) bool {
		return r.Attr("step") == stepPreRunScript && r.Attr("msg") == "behavior DESTROY"
	})
}
