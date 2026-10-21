package executor

import (
	"encoding/json/v2"
	"testing"

	"github.com/meshcloud/meshstack-cli/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/internal/config"
)

func Test_EncodeRunJson_KeepsOnlyTheImplementationTypeForAPipeline(t *testing.T) {
	var run client.MeshBuildingBlockRun
	require.NoError(t, json.Unmarshal([]byte(`{"spec":{"buildingBlockDefinition":{"spec":{"implementation":{"type":"GITLAB_CICD","pipelineTriggerToken":"a-trigger-token"}}}}}`), &run))
	require.NotNil(t, run.Spec.Definition.Spec.Implementation.GitlabCiCd)

	encoded, err := encodeRunJson(run, config.RunnerTypeGitLab)

	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "a-trigger-token")
	var decoded client.MeshBuildingBlockRun
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	assert.Equal(t, run.Spec.Definition.Spec.Implementation.Type, decoded.Spec.Definition.Spec.Implementation.Type)
	assert.NotNil(t, run.Spec.Definition.Spec.Implementation.GitlabCiCd, "the run keeps its implementation")
}
