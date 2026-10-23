package executor

import (
	"encoding/json/v2"
	"math/big"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_DecodeRunYaml_ReadsJsonAndYaml(t *testing.T) {
	t.Run("JSON round trip keeps every digit and ignores an unknown member", func(t *testing.T) {
		data := []byte(`{"spec":{"buildingBlockDefinition":{"spec":{"implementation":{"type":"MANUAL"}}},"buildingBlock":{"spec":{"inputs":[{"key":"count","type":"INTEGER","value":123456789012345678901234567890}]}}},"fromANewerMeshStack":true}`)

		decoded, err := DecodeRunYaml(data)
		require.NoError(t, err)
		encoded, err := json.Marshal(decoded)
		require.NoError(t, err)
		decoded, err = DecodeRunYaml(encoded)

		require.NoError(t, err)
		count, isBig := decoded.Spec.BuildingBlock.Spec.Inputs[0].Value.(*big.Int)
		require.True(t, isBig)
		assert.Equal(t, "123456789012345678901234567890", count.String())
	})

	t.Run("YAML", func(t *testing.T) {
		decoded, err := DecodeRunYaml([]byte("metadata:\n  uuid: 0f6d8f2e-6c5b-4d2a-9a43-2f1b6c7d8e90\nspec:\n  runToken: a-run-token\n"))

		require.NoError(t, err)
		assert.Equal(t, "0f6d8f2e-6c5b-4d2a-9a43-2f1b6c7d8e90", decoded.Metadata.Uuid.String())
		assert.Equal(t, "a-run-token", decoded.Spec.RunToken)
	})
}

func Test_EncodeWithoutImplementation_KeepsOnlyTheImplementationType(t *testing.T) {
	run, err := DecodeRunYaml([]byte(`{"spec":{"buildingBlockDefinition":{"spec":{"implementation":{"type":"GITLAB_CICD","pipelineTriggerToken":"a-trigger-token"}}}}}`))
	require.NoError(t, err)
	require.NotNil(t, run.Spec.Definition.Spec.Implementation.GitlabCiCd)

	encoded, err := encodeWithoutImplementation(run)

	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "a-trigger-token")
	decoded, err := DecodeRunYaml(encoded)
	require.NoError(t, err)
	assert.Equal(t, run.Spec.Definition.Spec.Implementation.Type, decoded.Spec.Definition.Spec.Implementation.Type)
	assert.NotNil(t, run.Spec.Definition.Spec.Implementation.GitlabCiCd, "the run keeps its implementation")
}
