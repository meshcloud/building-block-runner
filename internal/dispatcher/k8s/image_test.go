//go:build k8s

package k8s

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_WithDefaultTag_TagsOnlyAnImageWithoutTagAndDigest(t *testing.T) {
	const digest = "sha256:4f53cda18c2baa0c0354bb5f9a3ecbe5ed12ab4d8e11ba873c2f11161202b945"
	for image, want := range map[string]string{
		"ghcr.io/meshcloud/building-block-runner":    "ghcr.io/meshcloud/building-block-runner:v1.2.3",
		"ghcr.io/meshcloud/building-block-runner:v1": "ghcr.io/meshcloud/building-block-runner:v1",
		"ghcr.io/meshcloud/runner@" + digest:         "ghcr.io/meshcloud/runner@" + digest,
		"ghcr.io/meshcloud/runner:v1@" + digest:      "ghcr.io/meshcloud/runner:v1@" + digest,
		"localhost:5000/runner":                      "localhost:5000/runner:v1.2.3",
		"localhost:5000/runner:dev":                  "localhost:5000/runner:dev",
		"localhost:5000/runner@" + digest:            "localhost:5000/runner@" + digest,
		"runner":                                     "runner:v1.2.3",
	} {
		t.Run(image, func(t *testing.T) {
			assert.Equal(t, want, withDefaultTag(image, "v1.2.3"))
		})
	}
	assert.Equal(t, "runner", withDefaultTag("runner", ""))
}
