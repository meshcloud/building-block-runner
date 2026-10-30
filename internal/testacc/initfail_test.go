package testacc

import (
	"testing"

	"github.com/meshcloud/meshstack-cli/client"
	"github.com/stretchr/testify/assert"

	"github.com/meshcloud/building-block-runner/v2/internal/testacc/harness"
	"github.com/meshcloud/building-block-runner/v2/internal/testacc/runnerproc"
)

func TestAccInitFail(t *testing.T) {
	t.Parallel()
	s := requireSuite(t)

	d := harness.Deploy(t, s, harness.Case{
		Files:              caseFiles(t),
		Bootstrap:          tfblockBootstrap(t),
		ImplementationType: "TERRAFORM",
		Runner:             runnerproc.Options{ForType: "tf"},
		// The destroy run would fail its init too.
		Vars: map[string]any{"purge_on_delete": true},
	})

	runLog := d.RequireStatus(t, client.BuildingBlockStatusFailed)
	assert.Contains(t, runLog, "meshcloud/does-not-exist")
	assert.Contains(t, runLog, "Check provider and / or backend config")
}
