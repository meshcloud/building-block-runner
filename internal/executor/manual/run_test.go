package manual

import (
	"encoding/json/v2"
	"os"
	"path/filepath"
	"testing"

	"github.com/meshcloud/meshstack-cli/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_Outputs_EchoInputsWithOutputTypes(t *testing.T) {
	run := testRun(t)

	outputs, err := json.Marshal(outputsFrom(run.Spec.BuildingBlock.Spec.Inputs))
	require.NoError(t, err)

	t.Run("each input becomes an output of a type meshStack accepts, keeping its sensitivity", func(t *testing.T) {
		assert.JSONEq(t, `{
			"test":     {"value": "sss", "type": "STRING", "isSensitive": false},
			"count":    {"value": 12345678901234567890, "type": "INTEGER", "isSensitive": false},
			"enabled":  {"value": true, "type": "BOOLEAN", "isSensitive": false},
			"config":   {"value": "{\"a\": 1}", "type": "CODE", "isSensitive": false},
			"file":     {"value": "data:text/plain;base64,aGk=", "type": "STRING", "isSensitive": false},
			"regions":  {"value": ["eu", "us"], "type": "CODE", "isSensitive": false},
			"size":     {"value": "large", "type": "STRING", "isSensitive": false},
			"zones":    {"value": ["a", "b"], "type": "CODE", "isSensitive": false},
			"password": {"value": "ZW5jcnlwdGVk", "type": "STRING", "isSensitive": true}
		}`, string(outputs))
	})

	t.Run("an integer beyond float64 precision comes back unchanged", func(t *testing.T) {
		assert.Contains(t, string(outputs), "12345678901234567890")
	})
}

func testRun(t *testing.T) client.MeshRunnerRun {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "run.json"))
	require.NoError(t, err)
	var run client.MeshRunnerRun
	require.NoError(t, json.Unmarshal(data, &run))
	return run
}
