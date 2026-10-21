package main

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const module = "github.com/meshcloud/building-block-runner/"

func Test_EachImageBuild_LinksOnlyWhatItRuns(t *testing.T) {
	t.Parallel()
	forbiddenByTags := map[string][]string{
		"":       {"k8s.io/"},
		"tf":     {"k8s.io/", module + "internal/executor/manual"},
		"manual": {"k8s.io/", module + "internal/executor/tf"},
		"k8s":    {module + "internal/dispatcher/process", module + "internal/executor/manual", module + "internal/executor/tf"},
	}
	for tags, forbidden := range forbiddenByTags {
		t.Run("tags="+tags, func(t *testing.T) {
			t.Parallel()
			out, err := exec.CommandContext(t.Context(), "go", "list", "-deps", "-tags", tags, ".").Output()
			require.NoError(t, err)

			for pkg := range strings.Lines(string(out)) {
				for _, prefix := range forbidden {
					assert.False(t, strings.HasPrefix(pkg, prefix), "links %s", strings.TrimSpace(pkg))
				}
			}
		})
	}
}
