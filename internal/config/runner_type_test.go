package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_RunnerType_ParsesAnyNameAndExpandsAll(t *testing.T) {
	t.Run("parses config and API names in any case", func(t *testing.T) {
		for name, expected := range map[string]RunnerType{
			"tf": RunnerTypeTf, "TERRAFORM": RunnerTypeTf, "terraform": RunnerTypeTf,
			"azuredevops": RunnerTypeAzureDevOps, "AZURE_DEVOPS_PIPELINE": RunnerTypeAzureDevOps,
			"*": RunnerTypeAll, "ALL": RunnerTypeAll,
		} {
			runnerType, err := ParseRunnerType(name)
			require.NoError(t, err, name)
			assert.Equal(t, expected, runnerType, name)
		}
		_, err := ParseRunnerType("unknown")
		assert.EqualError(t, err, "runner type unknown is unknown")
	})

	t.Run("ALL expands to every type", func(t *testing.T) {
		assert.Equal(t, RunnerTypes{RunnerTypeTf, RunnerTypeManual, RunnerTypeGitHub, RunnerTypeGitLab, RunnerTypeAzureDevOps}, RunnerTypes{RunnerTypeManual, RunnerTypeAll}.Expand())
		assert.True(t, RunnerTypes{RunnerTypeAll}.Contains(RunnerTypeGitLab))
		assert.False(t, RunnerTypes{RunnerTypeTf}.Contains(RunnerTypeGitLab))
	})
}
