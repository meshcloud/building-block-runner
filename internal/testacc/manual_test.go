package testacc

import (
	"testing"

	"github.com/meshcloud/meshstack-cli/client"
	"github.com/stretchr/testify/assert"

	"github.com/meshcloud/building-block-runner/v2/internal/testacc/harness"
	"github.com/meshcloud/building-block-runner/v2/internal/testacc/runnerproc"
)

func TestAccManual(t *testing.T) {
	t.Parallel()
	s := requireSuite(t)

	d := harness.Deploy(t, s, harness.Case{Files: caseFiles(t), ImplementationType: "MANUAL", Runner: runnerproc.Options{ForType: "manual"}})

	d.RequireStatus(t, client.BuildingBlockStatusSucceeded)
	assert.Equal(t, "hello", d.Output[string](t, "text"))
	assert.Equal(t, 42, d.Output[int](t, "num"))
	assert.True(t, d.Output[bool](t, "flag"))
	assert.Len(t, d.ReadBuildingBlock(t).Status.Outputs, 3)
}
