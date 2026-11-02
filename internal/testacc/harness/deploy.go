package harness

import (
	"cmp"
	"encoding/json/v2"
	"maps"
	"slices"
	"strings"
	"testing"
	"uuid"

	"github.com/meshcloud/meshstack-cli/client"
	"github.com/meshcloud/meshstack-cli/client/types/enum"
	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/v2/internal/testacc/runnerproc"
)

type Deployment struct {
	Suite

	Runner            runnerproc.Process
	Bootstrap         Bootstrap
	Name              string
	BuildingBlockUuid string
}

// Deploy has the runner claim while the building block is destroyed, and deletes the runner after it stopped.
func Deploy(t *testing.T, s Suite, c Case) Deployment {
	t.Helper()
	keys := runnerproc.NewKeyPair(t)
	d := Deployment{Suite: s, Name: UniqueName(t, "bbr")}
	vars := maps.Clone(c.Vars)
	if vars == nil {
		vars = map[string]any{}
	}
	vars["name"] = d.Name
	vars["public_key"] = keys.PublicKeyPem
	vars["runner_implementation_type"] = c.ImplementationType
	caseBootstrap := c.bootstrap(t)
	if c.ImplementationType == "TERRAFORM" && caseBootstrap != nil {
		vars["terraform_version"] = cmp.Or(c.TerraformVersion, s.TofuVersion)
	}
	if module := c.module(t); module != nil {
		if _, given := vars["repository_url"]; !given {
			vars["repository_url"] = s.CommitModule(t, module)
		}
	}

	d.Bootstrap = newBootstrap(t, s, caseBootstrap, vars)
	t.Cleanup(func() { d.destroyUnlessKept(t) })
	d.Bootstrap.Apply(t, runnerResource)
	d.Runner = runnerproc.Start(t, runnerproc.Config{
		Options:    c.Runner,
		Binary:     s.RunnerBinary,
		Dir:        s.WorkDir(t, "runner"),
		Uuid:       d.Bootstrap.Output[string](t, "runner_uuid"),
		Endpoint:   s.Endpoint,
		Workspace:  d.Name,
		Key:        s.RunnerKey,
		KeyPair:    keys,
		CoverDir:   s.CoverDir,
		TfCacheDir: s.TfCacheDir,
	})
	if c.WithoutBuildingBlock {
		return d
	}
	return d.ApplyBuildingBlock(t)
}

func (d Deployment) ApplyBuildingBlock(t *testing.T) Deployment {
	t.Helper()
	t.Cleanup(func() { d.destroyUnlessKept(t, buildingBlockResource) })
	d.Bootstrap.Apply(t)
	d.BuildingBlockUuid = d.Bootstrap.Output[string](t, "building_block_uuid")
	return d
}

func (d Deployment) DestroyBuildingBlock(t *testing.T) {
	t.Helper()
	d.Bootstrap.Destroy(t, buildingBlockResource)
}

func (d Deployment) ReadBuildingBlock(t *testing.T) *client.MeshBuildingBlockV2 {
	t.Helper()
	bb, err := d.Api.BuildingBlockV2.Read(t.Context(), d.BuildingBlockUuid)
	require.NoError(t, err)
	require.NotNil(t, bb.Status)
	return bb
}

func (d Deployment) RequireStatus(t *testing.T, status enum.Entry[client.BuildingBlockStatus]) string {
	t.Helper()
	runLog := d.RunLog(t, d.LatestRunUuid(t))
	require.Equalf(t, status, d.ReadBuildingBlock(t).Status.Status, "run log:\n%s", runLog)
	return runLog
}

func (d Deployment) AwaitBuildingBlock(t *testing.T, status enum.Entry[client.BuildingBlockStatus]) {
	t.Helper()
	require.Eventuallyf(t, func() bool {
		return d.ReadBuildingBlock(t).Status.Status == status
	}, runnerproc.RunTimeout, runnerproc.PollInterval, "building block %s did not reach %s", d.BuildingBlockUuid, status)
}

func (d Deployment) Output[T any](t *testing.T, name string) T {
	t.Helper()
	outputs := d.ReadBuildingBlock(t).Status.Outputs
	require.Contains(t, outputs, name)
	encoded, err := json.Marshal(outputs[name].Value)
	require.NoError(t, err)
	var value T
	require.NoErrorf(t, json.Unmarshal(encoded, &value), "output %s", name)
	return value
}

func (d Deployment) LatestRunUuid(t *testing.T) string {
	t.Helper()
	bb := d.ReadBuildingBlock(t)
	require.NotNil(t, bb.Status.LatestRunUuid, "the building block has no run")
	return *bb.Status.LatestRunUuid
}

func (d Deployment) ReadRun(t *testing.T, runUuid string) client.MeshBuildingBlockRun {
	t.Helper()
	raw, err := d.Api.Raw.Get[client.MeshBuildingBlockRun](t.Context(), uuid.MustParse(runUuid))
	require.NoError(t, err)
	var run client.MeshBuildingBlockRun
	require.NoError(t, json.Unmarshal(raw, &run))
	return run
}

func (d Deployment) AwaitRun(t *testing.T, runUuid string, statuses ...string) {
	t.Helper()
	require.Eventuallyf(t, func() bool {
		return slices.Contains(statuses, d.ReadRun(t, runUuid).Status)
	}, runnerproc.RunTimeout, runnerproc.PollInterval, "run %s did not reach %v", runUuid, statuses)
}

func (d Deployment) AbortRun(t *testing.T, runUuid string) {
	t.Helper()
	run := d.ReadRun(t, runUuid)
	require.NotNil(t, run.Links.Abort, "meshStack offers no abort link for the run")
	require.NoError(t, d.Api.BuildingBlockRunAction.AbortRun(t.Context(), *run.Links.Abort))
}

func (d Deployment) RunLog(t *testing.T, runUuid string) string {
	t.Helper()
	logs, err := d.Api.BuildingBlockRun.GetLogs(t.Context(), runUuid)
	require.NoError(t, err)
	var log strings.Builder
	for _, step := range logs.Steps {
		log.WriteString("## " + step.DisplayName + " " + step.Status + "\n")
		for _, message := range []*string{step.UserMessage, step.SystemMessage} {
			if message != nil {
				log.WriteString(*message + "\n")
			}
		}
	}
	return log.String()
}

func (d Deployment) destroyUnlessKept(t *testing.T, targets ...string) {
	t.Helper()
	if d.Keep {
		t.Logf("keeping the objects of %s", d.Name)
		return
	}
	d.Bootstrap.Destroy(t, targets...)
}
