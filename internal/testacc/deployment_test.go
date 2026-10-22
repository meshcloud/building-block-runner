package testacc

import (
	"cmp"
	"encoding/json/v2"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/meshcloud/meshstack-cli/client"
	"github.com/meshcloud/meshstack-cli/client/types/enum"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const pollInterval = 2 * time.Second

// testCase names the dirs below testdata that hold its bootstrap and its building block code.
type testCase struct {
	name               string
	implementationType string
	runner             runnerOptions
	vars               map[string]any
	// withoutBuildingBlock registers the runner only.
	withoutBuildingBlock bool
	module               string
	terraformVersion     string
}

type deployment struct {
	*accSuite

	runner            *runnerProcess
	bootstrap         *bootstrap
	name              string
	buildingBlockUuid string
}

// deploy has the runner claim while the building block is destroyed, and deletes the runner after it stopped.
func (s *accSuite) deploy(t *testing.T, c testCase) deployment {
	t.Helper()
	keys := newRunnerKeyPair(t)
	d := deployment{accSuite: s, name: uniqueName("bbr")}
	vars := maps.Clone(c.vars)
	if vars == nil {
		vars = map[string]any{}
	}
	vars["name"] = d.name
	vars["public_key"] = keys.publicKeyPem
	vars["runner_implementation_type"] = c.implementationType
	if c.implementationType == "TERRAFORM" && exists(filepath.Join("testdata", c.name, "bootstrap")) {
		vars["terraform_version"] = cmp.Or(c.terraformVersion, s.tofuVersion)
	}
	module := cmp.Or(c.module, c.name)
	if _, given := vars["repository_url"]; !given && exists(filepath.Join("testdata", module, "buildingblock")) {
		vars["repository_url"] = s.commitModule(t, module)
	}

	d.bootstrap = s.newBootstrap(t, c.name, vars)
	t.Cleanup(func() { d.destroyUnlessKept(t) })
	d.bootstrap.apply(t, runnerResource)
	d.runner = s.startRunner(t, d.bootstrap.output(t, "runner_uuid"), d.name, keys, c.runner)
	if c.withoutBuildingBlock {
		return d
	}
	t.Cleanup(func() { d.destroyUnlessKept(t, buildingBlockResource) })
	d.bootstrap.apply(t)
	d.buildingBlockUuid = d.bootstrap.output(t, "building_block_uuid")
	return d
}

func (d deployment) destroyUnlessKept(t *testing.T, targets ...string) {
	t.Helper()
	if d.keep {
		t.Logf("keeping the objects of %s", d.name)
		return
	}
	d.bootstrap.destroy(t, targets...)
}

func (d deployment) readBuildingBlock(t *testing.T) *client.MeshBuildingBlockV2 {
	t.Helper()
	bb, err := d.api.BuildingBlockV2.Read(t.Context(), d.buildingBlockUuid)
	require.NoError(t, err)
	require.NotNil(t, bb.Status)
	return bb
}

func (d deployment) awaitBuildingBlock(t *testing.T, status enum.Entry[client.BuildingBlockStatus]) {
	t.Helper()
	require.Eventuallyf(t, func() bool {
		return d.readBuildingBlock(t).Status.Status == status
	}, runTimeout, pollInterval, "building block %s did not reach %s", d.buildingBlockUuid, status)
}

func (d deployment) latestRunUuid(t *testing.T) string {
	t.Helper()
	bb := d.readBuildingBlock(t)
	require.NotNil(t, bb.Status.LatestRunUuid, "the building block has no run")
	return *bb.Status.LatestRunUuid
}

func (d deployment) readRun(t *testing.T, runUuid string) client.MeshBuildingBlockRun {
	t.Helper()
	raw, err := d.api.Raw.Get[client.MeshBuildingBlockRun](t.Context(), uuid.MustParse(runUuid))
	require.NoError(t, err)
	var run client.MeshBuildingBlockRun
	require.NoError(t, json.Unmarshal(raw, &run))
	return run
}

func (d deployment) awaitRun(t *testing.T, runUuid string, timeout time.Duration, statuses ...string) {
	t.Helper()
	require.Eventuallyf(t, func() bool {
		return slices.Contains(statuses, d.readRun(t, runUuid).Status)
	}, timeout, pollInterval, "run %s did not reach %v", runUuid, statuses)
}

// runLog joins what meshStack shows of each step, the user and the system message.
func (d deployment) runLog(t *testing.T, runUuid string) string {
	t.Helper()
	logs, err := d.api.BuildingBlockRun.GetLogs(t.Context(), runUuid)
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

func (d deployment) awaitRunInStep(t *testing.T, step string, timeout time.Duration) string {
	t.Helper()
	var runUuid string
	require.Eventuallyf(t, func() bool {
		var found bool
		runUuid, found = d.runner.records.firstRunInStep(step)
		return found
	}, timeout, pollInterval, "no run logged in step %s", step)
	return runUuid
}

func assertRecord(t *testing.T, records []record, description string, matches func(record) bool) {
	t.Helper()
	assert.Truef(t, slices.ContainsFunc(records, matches), "no record %s among %d records of the run", description, len(records))
}

func outputValues(bb *client.MeshBuildingBlockV2) map[string]any {
	values := map[string]any{}
	for key, output := range bb.Status.Outputs {
		values[key] = output.Value
	}
	return values
}
