package testacc

import (
	"crypto/rand"
	"strings"
	"testing"

	"github.com/meshcloud/meshstack-cli/client"
	"github.com/meshcloud/meshstack-cli/client/types/enum"
	"github.com/stretchr/testify/assert"

	"github.com/meshcloud/building-block-runner/v2/internal/testacc/harness"
	"github.com/meshcloud/building-block-runner/v2/internal/testacc/runnerproc"
)

func TestAccInputs(t *testing.T) {
	t.Parallel()
	s := requireSuite(t)
	deploy := func(t *testing.T, secret, expectedSecret string) harness.Deployment {
		t.Helper()
		return harness.Deploy(t, s, harness.Case{
			Files:              caseFiles(t),
			ImplementationType: "TERRAFORM",
			Runner:             runnerproc.Options{ForType: "tf"},
			Vars:               map[string]any{"secret": secret, "expected_secret": expectedSecret, "env_secret": secret},
		})
	}

	t.Run("Inputs", func(t *testing.T) {
		t.Parallel()
		secret := rand.Text()

		d := deploy(t, secret, secret)

		runLog := d.RequireStatus(t, client.BuildingBlockStatusSucceeded)
		runUuid := d.LatestRunUuid(t)
		assert.Equal(t, "hello", d.Output[string](t, "text"))
		assert.Equal(t, 42, d.Output[int](t, "number"))
		assert.True(t, d.Output[bool](t, "bool"))
		assert.JSONEq(t, `["a","c"]`, d.Output[string](t, "list"))
		assert.JSONEq(t, `{"greeting":"hi"}`, d.Output[string](t, "object"))
		outputs := d.ReadBuildingBlock(t).Status.Outputs
		for key, valueType := range map[string]enum.Entry[client.MeshBuildingBlockIOType]{
			"number": client.MeshBuildingBlockIOTypeInteger,
			"bool":   client.MeshBuildingBlockIOTypeBoolean,
			"list":   client.MeshBuildingBlockIOTypeCode,
			"object": client.MeshBuildingBlockIOTypeCode,
		} {
			assert.Equalf(t, valueType, outputs[key].ValueType, "type of output %s", key)
		}
		assert.Equal(t, secret, d.Output[string](t, "sensitive_secret"))
		assert.Contains(t, runLog, `WARN: output "sensitive_secret" is sensitive, but meshStack shows building block outputs in clear text. Store the secret in a vault and output a reference to it.`)
		assert.NotContains(t, runLog, secret)
		assert.Equal(t, runUuid, d.Output[string](t, "run_uuid"))
		assert.Equal(t, d.BuildingBlockUuid, d.Output[string](t, "building_block_uuid"))
		assert.JSONEq(t, `{"type":"TERRAFORM"}`, d.Output[string](t, "run_implementation"), "meshstack_building_block_run_b64 carries no implementation secret")
		assert.Contains(t, runLog, "The pre-run script saw every environment input.")
		d.Runner.Records.Await(t, "of the runner that the run finished", runFinished(runUuid))
		records := d.Runner.Records.OfRun(t, runUuid)
		runnerproc.AssertRecord(t, records, "of the run that it sends SUCCEEDED", sendsFinalStatus(succeeded))
		runnerproc.AssertRecord(t, records, "of tofu apply", func(r runnerproc.Record) bool { return r.Attr("step") == stepExecuteTf })
		d.Runner.Records.AssertNotLoggedAtInfo(t, secret)
	})

	t.Run("SecretMismatch", func(t *testing.T) {
		t.Parallel()
		wrongSecret := rand.Text()
		const failedPostcondition = "sensitive input secret does not hold the expected secret."

		d := deploy(t, wrongSecret, rand.Text())

		runLog := d.RequireStatus(t, client.BuildingBlockStatusFailed)
		assert.Contains(t, runLog, failedPostcondition)
		assert.NotContains(t, runLog, wrongSecret)
		runnerproc.AssertRecord(t, d.Runner.Records.OfRun(t, d.LatestRunUuid(t)), "of tofu naming the postcondition", func(r runnerproc.Record) bool {
			return r.Attr("step") == stepExecuteTf && strings.Contains(r.Attr("msg"), failedPostcondition)
		})
		d.Runner.Records.AssertNotLoggedAtInfo(t, wrongSecret)
	})
}
