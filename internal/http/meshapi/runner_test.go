package meshapi_test

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"io"
	gohttp "net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/internal/http"
	"github.com/meshcloud/building-block-runner/internal/http/meshapi"
)

func readRun(t *testing.T) ([]byte, meshapi.MeshRunnerRun) {
	t.Helper()
	data, err := os.ReadFile("testdata/run.json")
	require.NoError(t, err)
	var run meshapi.MeshRunnerRun
	require.NoError(t, json.Unmarshal(data, &run))
	return data, run
}

func Test_Run_RoundTripsUnchanged(t *testing.T) {
	data, run := readRun(t)

	assert.Equal(t, "12345678901234567890", run.Spec.BuildingBlock.Spec.Inputs[0].Value.String())
	implementationType, err := run.Spec.Definition.Spec.ImplementationType()
	require.NoError(t, err)
	assert.Equal(t, meshapi.MeshRunnerImplementationTypeTerraform, implementationType)

	encoded, err := json.Marshal(run)
	require.NoError(t, err)
	assert.JSONEq(t, string(data), string(encoded))
}

func Test_DecryptedRun_RoundTripsUnchanged(t *testing.T) {
	_, run := readRun(t)
	var err error
	run.Spec.BuildingBlock.Spec.Inputs[3].Value, err = json.Marshal("plaintext")
	require.NoError(t, err)
	var implementation meshapi.MeshRunnerTerraformImplementation
	require.NoError(t, json.Unmarshal(run.Spec.Definition.Spec.Implementation, &implementation))
	implementation.SshPrivateKey = new("-----BEGIN OPENSSH PRIVATE KEY-----")
	run.Spec.Definition.Spec.Implementation, err = json.Marshal(implementation)
	require.NoError(t, err)

	encoded, err := json.Marshal(run)
	require.NoError(t, err)
	var decoded meshapi.MeshRunnerRun
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	reencoded, err := json.Marshal(decoded)
	require.NoError(t, err)
	assert.JSONEq(t, string(encoded), string(reencoded))
	assert.JSONEq(t, `"plaintext"`, decoded.Spec.BuildingBlock.Spec.Inputs[3].Value.String())
}

func Test_PatchStatus_SendsOnlyTheFieldsSet(t *testing.T) {
	sourceId := uuid.MustParse("98520496-627d-43e6-82da-ce499179ff3f")
	runClient := newRunClient(t, func(w gohttp.ResponseWriter, r *gohttp.Request) {
		assert.Equal(t, gohttp.MethodPatch, r.Method)
		assert.Equal(t, "/runs/c662eb0d/status/source/"+sourceId.String(), r.URL.Path)
		assert.Equal(t, "Bearer a-run-token", r.Header.Get("Authorization"))
		assert.Equal(t, "application/vnd.meshcloud.api.meshbuildingblockrun.v1.hal+json", r.Header.Get("Content-Type"))
		assert.Equal(t, "test", r.Header.Get("X-Meshcloud-Runner-Version"))
		body, err := io.ReadAll(r.Body)
		assert.NoError(t, err)
		assert.JSONEq(t, `{
			"status": "SUCCEEDED",
			"summary": null,
			"steps": [{"id": "manual", "displayName": "Manual Block Run", "status": "SUCCEEDED", "userMessage": "",
				"outputs": {"count": {"value": 12345678901234567890, "type": "INTEGER", "isSensitive": false}}}]
		}`, string(body))
		_, _ = w.Write([]byte(`{"runAborted": true}`))
	})

	result, err := runClient.PatchStatus(t.Context(), sourceId, meshapi.MeshRunnerRunStatusUpdate{
		Status: new("SUCCEEDED"),
		Steps: []meshapi.MeshRunnerRunStepStatusUpdate{{
			Id: "manual", DisplayName: "Manual Block Run", Status: new("SUCCEEDED"), UserMessage: new(""),
			Outputs: map[string]meshapi.MeshRunnerRunOutput{"count": {Value: jsontext.Value("12345678901234567890"), Type: "INTEGER"}},
		}},
	})
	require.NoError(t, err)
	assert.True(t, result.Abort)
}

func Test_DownloadPlanArtifact_StreamsUpToTheCap(t *testing.T) {
	var size atomic.Int64
	size.Store(1 << 20)
	runClient := newRunClient(t, func(w gohttp.ResponseWriter, r *gohttp.Request) {
		assert.Equal(t, "/plan", r.URL.Path)
		assert.Equal(t, "application/octet-stream", r.Header.Get("Accept"))
		_, _ = io.CopyN(w, zeros{}, size.Load())
	})

	var plan bytes.Buffer
	require.NoError(t, runClient.DownloadPlanArtifact(t.Context(), &plan))
	assert.Equal(t, size.Load(), int64(plan.Len()))

	size.Store(128<<20 + 1)
	assert.ErrorContains(t, runClient.DownloadPlanArtifact(t.Context(), io.Discard), "the artifact exceeds 134217728 bytes")
}

func Test_UploadArtifact_SendsTheFile(t *testing.T) {
	planFile := filepath.Join(t.TempDir(), "plan.tfplan")
	require.NoError(t, os.WriteFile(planFile, []byte("a plan"), 0o600))
	runClient := newRunClient(t, func(w gohttp.ResponseWriter, r *gohttp.Request) {
		assert.Equal(t, gohttp.MethodPut, r.Method)
		assert.Equal(t, "/upload", r.URL.Path)
		assert.Equal(t, "application/octet-stream", r.Header.Get("Content-Type"))
		assert.Equal(t, int64(6), r.ContentLength)
		body, err := io.ReadAll(r.Body)
		assert.NoError(t, err)
		assert.Equal(t, "a plan", string(body))
		w.WriteHeader(gohttp.StatusNoContent)
	})

	require.NoError(t, runClient.UploadArtifact(t.Context(), planFile))
}

type zeros struct{}

func (zeros) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

func newRunClient(t *testing.T, handler gohttp.HandlerFunc) meshapi.MeshRunnerRunClient {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	serverUrl, err := url.Parse(server.URL)
	require.NoError(t, err)
	httpClient := http.Client{Client: server.Client(), UserAgent: http.UserAgent{GitHubRepo: "meshcloud/building-block-runner", Version: "test"}}
	runnerClient := meshapi.NewMeshRunnerClient(httpClient.WithAuthorization(http.BasicAuth{Username: "runner", Password: "secret"}), serverUrl)
	return runnerClient.ForRun(meshapi.MeshRunnerRun{
		Spec: meshapi.MeshRunnerRunSpec{RunToken: "a-run-token"},
		Links: meshapi.MeshRunnerRunLinks{
			UpdateSource:   meshapi.MeshRunnerRunLink{Href: server.URL + "/runs/c662eb0d/status/source/{sourceId}", Templated: true},
			PlanArtifact:   meshapi.MeshRunnerRunLink{Href: server.URL + "/plan"},
			ArtifactUpload: meshapi.MeshRunnerRunLink{Href: server.URL + "/upload"},
		},
	})
}
