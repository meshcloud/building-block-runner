package manual

import (
	"encoding/json/v2"
	gohttp "net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/internal/http"
	"github.com/meshcloud/building-block-runner/internal/http/meshapi"
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

func Test_Run_ReportsThroughItsSourceLinksWithTheRunToken(t *testing.T) {
	var requests []string
	meshStack := httptest.NewServer(gohttp.HandlerFunc(func(w gohttp.ResponseWriter, r *gohttp.Request) {
		requests = append(requests, r.Method+" "+r.URL.EscapedPath()+" "+r.Header.Get("Authorization"))
		_, _ = w.Write([]byte("{}"))
	}))
	defer meshStack.Close()

	sourceId := uuid.MustParse("6f0c5c2c-5b7e-4c1e-9d6f-3c7f2b1a0e11")
	run := testRun(t)
	run.Links.RegisterSource.Href = meshStack.URL + "/register"
	run.Links.UpdateSource.Href = meshStack.URL + "/update/{sourceId}"

	require.NoError(t, Execute(t.Context(), run, runClient(t, meshStack.URL, run), sourceId))

	assert.Equal(t, []string{
		"POST /register Bearer aaaa-bbbb-cccc-dddd",
		"PATCH /update/" + sourceId.String() + " Bearer aaaa-bbbb-cccc-dddd",
	}, requests)
}

func Test_RunWithoutRegisterSourceLink_FailsNamingTheLink(t *testing.T) {
	run := testRun(t)
	run.Links.RegisterSource = meshapi.MeshRunnerRunLink{}

	err := Execute(t.Context(), run, runClient(t, "http://localhost", run), uuid.New())

	assert.ErrorContains(t, err, "no registerSource link")
}

func runClient(t *testing.T, meshStackUrl string, run meshapi.MeshRunnerRun) meshapi.MeshRunnerRunClient {
	t.Helper()
	return runnerClient(t, meshStackUrl).ForRun(run)
}

func runnerClient(t *testing.T, meshStackUrl string) meshapi.MeshRunnerClient {
	t.Helper()
	parsed, err := url.Parse(meshStackUrl)
	require.NoError(t, err)
	client := http.NewClient(http.UserAgent{GitHubRepo: "meshcloud/building-block-runner", Version: "test"})
	return meshapi.NewMeshRunnerClient(client.WithAuthorization(http.BasicAuth{Username: "user", Password: "password"}), parsed)
}

func testRun(t *testing.T) meshapi.MeshRunnerRun {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "run.json"))
	require.NoError(t, err)
	var run meshapi.MeshRunnerRun
	require.NoError(t, json.Unmarshal(data, &run))
	return run
}
