package dispatcher

import (
	"encoding/json/v2"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func readTestRun(t *testing.T) ([]byte, Run) {
	t.Helper()
	data, err := os.ReadFile("testdata/run.json")
	require.NoError(t, err)
	var run Run
	require.NoError(t, json.Unmarshal(data, &run))
	return data, run
}

func Test_RunYaml_RoundTripsEveryJsonValue(t *testing.T) {
	data, run := readTestRun(t)

	encoded, err := EncodeRunYaml(run)
	require.NoError(t, err)
	decoded, err := DecodeRunYaml(encoded)
	require.NoError(t, err)

	reencoded, err := json.Marshal(decoded)
	require.NoError(t, err)
	assert.JSONEq(t, string(data), string(reencoded))
	assert.Equal(t, "12345678901234567890", decoded.Spec.BuildingBlock.Spec.Inputs[0].Value.String())
}

func Test_RunYaml_ShowsImplementationAsYaml(t *testing.T) {
	_, run := readTestRun(t)

	encoded, err := EncodeRunYaml(run)
	require.NoError(t, err)

	assert.Contains(t, string(encoded), "        preRunScript: |\n          #!/bin/bash\n          set -e\n")
	assert.Contains(t, string(encoded), "value: 12345678901234567890\n")
}

func Test_DecodeRunYaml_ReadsJson(t *testing.T) {
	data, _ := readTestRun(t)

	decoded, err := DecodeRunYaml(data)

	require.NoError(t, err)
	reencoded, err := json.Marshal(decoded)
	require.NoError(t, err)
	assert.JSONEq(t, string(data), string(reencoded))
	assert.Equal(t, "12345678901234567890", decoded.Spec.BuildingBlock.Spec.Inputs[0].Value.String())
}

func Test_DecodeRunYaml_IgnoresAnUnknownField(t *testing.T) {
	decoded, err := DecodeRunYaml([]byte("metadata:\n  uuid: 0f6d8f2e-6c5b-4d2a-9a43-2f1b6c7d8e90\nfromANewerMeshStack: true\n"))

	require.NoError(t, err)
	assert.Equal(t, "0f6d8f2e-6c5b-4d2a-9a43-2f1b6c7d8e90", decoded.Metadata.Uuid.String())
}
