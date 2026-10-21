package build

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_ImageTag_IsTheReleaseVersionOrTheRevision(t *testing.T) {
	version, revision := Version, Revision
	t.Cleanup(func() { Version, Revision = version, revision })
	const commit = "966dc16a0e2d4f4b8c3b1a7d5e9f0c2b4a6d8e1f"
	for name, tc := range map[string]struct{ version, revision, want string }{
		"release":        {"v2.0.0", commit, "v2.0.0"},
		"pseudo-version": {"v2.0.1-0.20261027120000-966dc16a0e2d", commit, commit},
		"dirty":          {"v2.0.0+dirty", commit, commit},
		"neither":        {"dev", "", ""},
	} {
		t.Run(name, func(t *testing.T) {
			Version, Revision = tc.version, tc.revision
			assert.Equal(t, tc.want, ImageTag())
		})
	}
}
