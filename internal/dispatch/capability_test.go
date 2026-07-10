package dispatch

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	meshapi "github.com/meshcloud/building-block-runner/internal/meshapi"
)

func TestParseCapability_AllBackendValuesAccepted(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want meshapi.RunnerImplementationType
	}{
		{"manual", "MANUAL", meshapi.RunnerTypeManual},
		{"terraform", "TERRAFORM", meshapi.RunnerTypeTerraform},
		{"github workflow", "GITHUB_WORKFLOW", meshapi.RunnerTypeGitHubWorkflow},
		{"gitlab pipeline", "GITLAB_PIPELINE", meshapi.RunnerTypeGitLabPipeline},
		{"azure devops pipeline", "AZURE_DEVOPS_PIPELINE", meshapi.RunnerTypeAzureDevOpsPipeline},
		{"all", "ALL", meshapi.RunnerTypeAll},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ParseCapability(c.in)
			require.NoError(t, err)
			assert.Equal(t, Capability(c.want), got)
			assert.Equal(t, c.in, got.String())
		})
	}
}

func TestParseCapability_RejectsUnknownOrSubsetValues(t *testing.T) {
	cases := []string{
		"",
		"terraform",        // case-sensitive: lowercase is not a backend value
		"BOGUS",            // not a backend enum member at all
		"TERRAFORM,MANUAL", // subsets are unrepresentable by design (D5)
		"ALL ",             // no trimming -- config layer's job, not the parser's
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			_, err := ParseCapability(in)
			require.Error(t, err)
			assert.Contains(t, err.Error(), in)
			assert.Contains(t, err.Error(), "ALL")
		})
	}
}
