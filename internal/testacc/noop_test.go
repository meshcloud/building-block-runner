package testacc

import (
	"testing"

	"github.com/meshcloud/meshstack-cli/client"
	"github.com/stretchr/testify/assert"

	"github.com/meshcloud/building-block-runner/v2/internal/testacc/harness"
	"github.com/meshcloud/building-block-runner/v2/internal/testacc/runnerproc"
)

func TestAccNoop(t *testing.T) {
	t.Parallel()
	s := requireSuite(t)

	d := harness.Deploy(t, s, harness.Case{Files: caseFiles(t), ImplementationType: "TERRAFORM", Runner: runnerproc.Options{ForType: "tf"}})

	d.RequireStatus(t, client.BuildingBlockStatusSucceeded)
	assert.Regexp(t, `^hello aws-cli/2\.`, d.Output[string](t, "text"))
}
