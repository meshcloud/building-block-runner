package tfrun

import (
	"encoding/json/v2"
	"io"
	gohttp "net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/internal/http"
	"github.com/meshcloud/building-block-runner/internal/http/meshapi"
)

var (
	testRunUuid    = uuid.MustParse("0f6d8f2e-6c5b-4d2a-9a43-2f1b6c7d8e90")
	testRunnerUuid = uuid.MustParse("5b1c2d3e-4f50-4a6b-8c7d-9e0f1a2b3c4d")
)

const testRunToken = "test-mock-run-token-12345"

type caughtRequest struct {
	method, path, authorization string
	body                        map[string]any
}

func meshStackAnswering(t *testing.T, responseBody string) (RunApi, *[]caughtRequest) {
	t.Helper()
	var requests []caughtRequest
	server := httptest.NewServer(gohttp.HandlerFunc(func(w gohttp.ResponseWriter, r *gohttp.Request) {
		data, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		var body map[string]any
		require.NoError(t, json.Unmarshal(data, &body))
		delete(body, "createdOn")
		requests = append(requests, caughtRequest{r.Method, r.URL.Path, r.Header.Get("Authorization"), body})
		_, _ = w.Write([]byte(responseBody))
	}))
	t.Cleanup(server.Close)
	return newTestRunApi(http.NewClient(testUserAgent), testRun(server.URL)), &requests
}

var testUserAgent = http.UserAgent{GitHubRepo: "meshcloud/building-block-runner", Version: "test"}

func newTestRunApi(client http.Client, run meshapi.MeshRunnerRun) RunApi {
	runner := meshapi.NewMeshRunnerClient(client.WithAuthorization(http.BasicAuth{Username: "user", Password: "password"}), new(url.URL))
	return NewRunApi(runner.ForRun(run), run.Metadata.Uuid, testRunnerUuid)
}

func testRun(meshStackUrl string) meshapi.MeshRunnerRun {
	var run meshapi.MeshRunnerRun
	run.Metadata.Uuid = testRunUuid
	run.Spec.RunToken = testRunToken
	sourcePath := meshStackUrl + "/api/meshobjects/meshbuildingblockruns/" + testRunUuid.String() + "/status/source"
	run.Links.RegisterSource.Href = sourcePath
	run.Links.UpdateSource.Href = sourcePath + "/{sourceId}"
	return run
}

func Test_Register_RegistersTheRunnerWithTheStepsAndTheRunToken(t *testing.T) {
	api, requests := meshStackAnswering(t, "")

	err := api.Register(t.Context(), &RunStatus{
		Status: IN_PROGRESS,
		Steps: []*StepStatus{
			{Name: "step1", DisplayName: "display1", Status: IN_PROGRESS},
			{Name: "step2", DisplayName: "display2"},
		},
	})

	require.NoError(t, err)
	require.Len(t, *requests, 1)
	request := (*requests)[0]
	assert.Equal(t, gohttp.MethodPost, request.method)
	assert.Equal(t, "/api/meshobjects/meshbuildingblockruns/"+testRunUuid.String()+"/status/source", request.path)
	assert.Equal(t, "Bearer "+testRunToken, request.authorization)
	assert.Equal(t, map[string]any{
		"source": map[string]any{"id": testRunnerUuid.String()},
		"steps": []any{
			map[string]any{"id": "step1", "displayName": "display1", "status": nil},
			map[string]any{"id": "step2", "displayName": "display2", "status": nil},
		},
	}, request.body)
}

func Test_UpdateState_SendsTheStatusWithItsStepsAndOutputs(t *testing.T) {
	api, requests := meshStackAnswering(t, `{"runAborted": true}`)

	abort, err := api.UpdateState(t.Context(), &RunStatus{
		Status:  IN_PROGRESS,
		Summary: new("run summary"),
		Steps: []*StepStatus{
			{
				Name:          "step1",
				DisplayName:   "display1",
				Status:        SUCCEEDED,
				UserMessage:   new("user message for step 1"),
				SystemMessage: new("logs for step 1"),
				Outputs: map[string]*TfOutput{
					"test1": {Type: DATA_TYPE_BOOLEAN, Value: true},
					"test2": {Type: DATA_TYPE_CODE, Value: `["foo", "bar"]`, Sensitive: true},
				},
			},
			{Name: "step2", DisplayName: "display2"},
		},
	})

	require.NoError(t, err)
	assert.True(t, abort)
	require.Len(t, *requests, 1)
	request := (*requests)[0]
	assert.Equal(t, gohttp.MethodPatch, request.method)
	assert.Equal(t, "/api/meshobjects/meshbuildingblockruns/"+testRunUuid.String()+"/status/source/"+testRunnerUuid.String(), request.path)
	assert.Equal(t, "Bearer "+testRunToken, request.authorization)
	assert.Equal(t, map[string]any{
		"blockRunId": testRunUuid.String(),
		"source":     testRunnerUuid.String(),
		"type":       "TERRAFORM",
		"status":     "IN_PROGRESS",
		"summary":    "run summary",
		"steps": []any{
			map[string]any{
				"id":            "step1",
				"displayName":   "display1",
				"status":        "SUCCEEDED",
				"userMessage":   "user message for step 1",
				"systemMessage": "logs for step 1",
				"outputs": map[string]any{
					"test1": map[string]any{"value": true, "type": "BOOLEAN", "isSensitive": false},
					"test2": map[string]any{"value": `["foo", "bar"]`, "type": "CODE", "isSensitive": true},
				},
			},
			map[string]any{"id": "step2", "displayName": "display2", "status": "PENDING"},
		},
	}, request.body)
}

func Test_UpdateStateWithoutSteps_SendsStepsAsNull(t *testing.T) {
	api, requests := meshStackAnswering(t, "{}")

	_, err := api.UpdateState(t.Context(), &RunStatus{Status: FAILED})

	require.NoError(t, err)
	require.Len(t, *requests, 1)
	assert.Contains(t, (*requests)[0].body, "steps")
	assert.Nil(t, (*requests)[0].body["steps"])
}
