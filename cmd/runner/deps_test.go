package main

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const module = "github.com/meshcloud/building-block-runner/v2/"

func Test_EachImageBuild_LinksOnlyWhatItRuns(t *testing.T) {
	t.Parallel()
	const executor = module + "internal/executor/"
	forbiddenByTags := map[string][]string{
		"":            {"k8s.io/"},
		"tf":          {"k8s.io/", executor + "manual", executor + "gitlab", executor + "azuredevops", executor + "github"},
		"manual":      {"k8s.io/", executor + "tf", executor + "gitlab", executor + "azuredevops", executor + "github"},
		"gitlab":      {"k8s.io/", executor + "tf", executor + "manual", executor + "azuredevops", executor + "github"},
		"azuredevops": {"k8s.io/", executor + "tf", executor + "manual", executor + "gitlab", executor + "github"},
		"github":      {"k8s.io/", executor + "tf", executor + "manual", executor + "gitlab", executor + "azuredevops"},
		"k8s":         {module + "internal/dispatcher/process", executor + "manual", executor + "tf", executor + "gitlab", executor + "azuredevops", executor + "github"},
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
