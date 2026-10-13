//go:build k8s

package dispatcher

import (
	"io"
	"log"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/meshcloud/building-block-runner/internal/config"
)

func Test_Serve_RefusesARunFileWithTheK8sTag(t *testing.T) {
	t.Setenv(claimRunYmlEnv, "run.yml")

	err := Serve(t.Context(), config.Config{}, log.New(io.Discard, "", 0))

	assert.EqualError(t, err, "RUNNER_CLAIM_RUN_YML needs a binary built without -tags k8s")
}
