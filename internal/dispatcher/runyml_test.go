package dispatcher

import (
	"encoding/json/v2"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_DecodeRunYaml_ReadsJsonAndYaml(t *testing.T) {
	t.Run("JSON round trip keeps every digit and ignores an unknown member", func(t *testing.T) {
		data := []byte(`{"spec":{"buildingBlock":{"spec":{"inputs":[{"key":"count","value":123456789012345678901234567890}]}}},"fromANewerMeshStack":true}`)

		decoded, err := DecodeRunYaml(data)
		require.NoError(t, err)
		encoded, err := json.Marshal(decoded)
		require.NoError(t, err)
		decoded, err = DecodeRunYaml(encoded)

		require.NoError(t, err)
		assert.Equal(t, "123456789012345678901234567890", decoded.Spec.BuildingBlock.Spec.Inputs[0].Value.String())
	})

	t.Run("YAML", func(t *testing.T) {
		decoded, err := DecodeRunYaml([]byte("metadata:\n  uuid: 0f6d8f2e-6c5b-4d2a-9a43-2f1b6c7d8e90\nspec:\n  runToken: a-run-token\n"))

		require.NoError(t, err)
		assert.Equal(t, "0f6d8f2e-6c5b-4d2a-9a43-2f1b6c7d8e90", decoded.Metadata.Uuid.String())
		assert.Equal(t, "a-run-token", decoded.Spec.RunToken)
	})
}
