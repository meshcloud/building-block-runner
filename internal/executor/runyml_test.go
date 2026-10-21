package executor

import (
	"encoding/json/v2"
	"math/big"
	"os"
	"testing"

	"github.com/meshcloud/meshstack-cli/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_DecodeRunYaml_ReadsJsonAndYaml(t *testing.T) {
	data, readErr := os.ReadFile("testdata/run.json")
	require.NoError(t, readErr)
	run, decodeErr := DecodeRunYaml(data)
	require.NoError(t, decodeErr)

	t.Run("decodes the inputs by type and round-trips unchanged", func(t *testing.T) {
		inputs := run.Spec.BuildingBlock.Spec.Inputs
		count, isBig := inputs[0].Value.(*big.Int)
		require.True(t, isBig)
		assert.Equal(t, "12345678901234567890", count.String())
		assert.InDelta(t, 0.5, inputs[1].Value, 0)
		assert.Equal(t, map[string]any{"nested": []any{1.0, 2.0}}, inputs[2].Value)
		assert.Equal(t, "ZW5jcnlwdGVk", inputs[3].Value)
		assert.Equal(t, "eu", inputs[4].Value)

		implementation := run.Spec.Definition.Spec.Implementation
		assert.Equal(t, client.MeshRunnerImplementationTypeTerraform, implementation.Type)
		require.NotNil(t, implementation.Terraform)
		assert.Equal(t, "1.9.0", implementation.Terraform.TerraformVersion)
		assert.True(t, implementation.Terraform.Async)

		encoded, err := json.Marshal(run)
		require.NoError(t, err)
		assert.JSONEq(t, string(data), string(encoded))
	})

	t.Run("round-trips unchanged once decrypted", func(t *testing.T) {
		run.Spec.BuildingBlock.Spec.Inputs[3].Value = "plaintext"
		run.Spec.Definition.Spec.Implementation.Terraform.SshPrivateKey = new("-----BEGIN OPENSSH PRIVATE KEY-----")

		encoded, err := json.Marshal(run)
		require.NoError(t, err)
		decoded, err := DecodeRunYaml(encoded)
		require.NoError(t, err)
		reencoded, err := json.Marshal(decoded)
		require.NoError(t, err)
		assert.JSONEq(t, string(encoded), string(reencoded))
		assert.Equal(t, "plaintext", decoded.Spec.BuildingBlock.Spec.Inputs[3].Value)
	})

	t.Run("an implementation keeps its type", func(t *testing.T) {
		for _, implementation := range []string{
			`{"type":"MANUAL"}`,
			`{"type":"GITLAB_CICD","gitlabBaseUrl":"https://gitlab.com","projectId":"1","refName":"main","pipelineTriggerToken":"token"}`,
			`{"type":"A_NEW_TYPE"}`,
		} {
			var decoded client.MeshRunnerRunImplementation
			require.NoError(t, json.Unmarshal([]byte(implementation), &decoded))
			encoded, err := json.Marshal(decoded)
			require.NoError(t, err)
			assert.JSONEq(t, implementation, string(encoded))
		}
	})

	t.Run("ignores a member of a newer meshStack", func(t *testing.T) {
		_, err := DecodeRunYaml([]byte(`{"spec":{"buildingBlockDefinition":{"spec":{"implementation":{"type":"MANUAL"}}}},"fromANewerMeshStack":true}`))
		require.NoError(t, err)
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
