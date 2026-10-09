package manual

import (
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/meshcloud/building-block-runner/go-meshapi-client/meshapi"
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

func Test_RunJsonNotJSON_FailsBeforeContactingMeshStack(t *testing.T) {
	err := ExecuteRunJson(t.Context(), defaultConfig(), []byte("not json"))
	assert.ErrorContains(t, err, "parse run")
}

func Test_Run_ReportsThroughItsSourceLinks(t *testing.T) {
	var requests []string
	meshStack := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.EscapedPath())
		w.WriteHeader(http.StatusOK)
	}))
	defer meshStack.Close()

	cfg := defaultConfig()
	cfg.Uuid = "runner/1"
	run := testRun(t)
	run.Links.RegisterSource.Href = meshStack.URL + "/register"
	run.Links.UpdateSource.Href = meshStack.URL + "/update/{sourceId}"

	require.NoError(t, ExecuteRun(t.Context(), cfg, run))

	assert.Equal(t, []string{"POST /register", "PATCH /update/runner%2F1"}, requests)
}

func Test_RunWithoutSourceLink_FailsNamingTheLink(t *testing.T) {
	cfg := defaultConfig()

	t.Run("registerSource", func(t *testing.T) {
		run := testRun(t)
		run.Links.RegisterSource = meshapi.LinkDTO{}
		assert.ErrorContains(t, ExecuteRun(t.Context(), cfg, run), "no registerSource link")
	})

	t.Run("updateSource", func(t *testing.T) {
		run := testRun(t)
		run.Links.UpdateSource = meshapi.LinkDTO{}
		assert.ErrorContains(t, ExecuteRun(t.Context(), cfg, run), "no updateSource link")
	})
}

func testRun(t *testing.T) *meshapi.RunDetailsDTO {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "run.json"))
	require.NoError(t, err)
	run, err := parseRun(data)
	require.NoError(t, err)
	return run
}
