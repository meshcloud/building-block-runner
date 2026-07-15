package azdevops

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/internal/dispatch"
	"github.com/meshcloud/building-block-runner/internal/meshapi"
)

// Test_RealClock exercises the production Clock (never used by the fake-clock-driven
// scenario suite above), proving it delegates to real wall time.
func Test_RealClock(t *testing.T) {
	c := RealClock{}
	before := time.Now()
	require.False(t, c.Now().Before(before.Add(-time.Second)))
	select {
	case <-c.After(time.Millisecond):
	case <-time.After(2 * time.Second):
		t.Fatal("RealClock.After did not fire")
	}
}

// Test_EnumName_UnknownWireValue is the tolerant-parse fallback: an unrecognized wire
// string is representable and rendered uppercased-echo rather than failing to parse.
func Test_EnumName_UnknownWireValue(t *testing.T) {
	assert.Equal(t, "brandNew", pipelineRunState("brandNew").enumName())
	assert.Equal(t, "brandNew", pipelineRunResult("brandNew").enumName())
}

func Test_ResultEnumNameOrNull(t *testing.T) {
	assert.Equal(t, "null", resultEnumNameOrNull(nil))
	assert.Equal(t, "SUCCEEDED", resultEnumNameOrNull(resultPtr(resultSucceeded)))
}

// Test_WebURL covers every precedence rung: _links.web.href wins, then url, then "N/A".
func Test_WebURL(t *testing.T) {
	assert.Equal(t, "https://link", pipelineRun{
		Links: map[string]map[string]string{"web": {"href": "https://link"}},
		Url:   strPtr("https://url"),
	}.webURL())
	assert.Equal(t, "https://url", pipelineRun{Url: strPtr("https://url")}.webURL())
	assert.Equal(t, "N/A", pipelineRun{}.webURL())
	assert.Equal(t, "N/A", pipelineRun{Links: map[string]map[string]string{"other": {"href": "x"}}}.webURL())
}

func strPtr(s string) *string { return &s }

// Test_TriggerPipeline_MalformedResponse / Test_GetPipelineRun_MalformedResponse /
// Test_GetTimeline_MalformedResponse cover the unmarshal-error branches: an ADO response
// that returns 2xx but an unparsable body.
func Test_TriggerPipeline_MalformedResponse(t *testing.T) {
	srv := newFakeADO(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		_, _ = w.Write([]byte("not json"))
	})
	c := testClient(srv.URL)
	_, err := c.TriggerPipeline(context.Background(), map[string]string{}, nil)
	require.Error(t, err)
}

func Test_GetPipelineRun_MalformedResponse(t *testing.T) {
	srv := newFakeADO(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		_, _ = w.Write([]byte("not json"))
	})
	c := testClient(srv.URL)
	_, err := c.GetPipelineRun(context.Background(), 1)
	require.Error(t, err)
}

func Test_GetTimeline_MalformedResponse(t *testing.T) {
	srv := newFakeADO(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		_, _ = w.Write([]byte("not json"))
	})
	c := testClient(srv.URL)
	_, err := c.GetTimeline(context.Background(), 1)
	require.Error(t, err)
}

// Test_Client_TransportError covers the plain-wrapped-error branch of `do` (a connection
// failure, not a non-2xx response) -- dialing a closed server.
func Test_Client_TransportError(t *testing.T) {
	srv := newFakeADO(t, func(w http.ResponseWriter, r *http.Request, body []byte) {})
	url := srv.URL
	srv.Close()

	c := testClient(url)
	_, err := c.GetPipelineRun(context.Background(), 1)
	require.Error(t, err)
	var extErr ExternalCallError
	require.NotErrorAs(t, err, &extErr, "a transport failure is not an ExternalCallError")
}

// Test_ParseImplementation_NoDetails / Test_ParseImplementation_MalformedJSON cover the two
// parseImplementation error rungs beyond the wrong-type check (already covered by the
// handler scenario suite).
func Test_ParseImplementation_NoDetails(t *testing.T) {
	_, err := parseImplementation(dispatch.ClaimedRun{})
	require.Error(t, err)
}

func Test_ParseImplementation_MalformedJSON(t *testing.T) {
	dto := &meshapi.RunDetailsDTO{
		Spec: meshapi.RunSpecDTO{
			Definition: meshapi.DefinitionSpecDTO{
				Spec: meshapi.DefinitionDetailsSpecDTO{Implementation: json.RawMessage(`{"type":"AZURE_DEVOPS","pipelineId":123}`)},
			},
		},
	}
	_, err := parseImplementation(dispatch.ClaimedRun{Details: dto})
	require.Error(t, err, "pipelineId must be a string; a type-mismatched field fails to unmarshal")
}

// Test_ReadInputs_FallbackPaths covers readInputs's RawJson-invalid-base64 and
// RawJson-empty-with-nil-Details branches.
func Test_ReadInputs_FallbackPaths(t *testing.T) {
	log := testLog()

	t.Run("invalid base64 falls back to Details", func(t *testing.T) {
		dto := &meshapi.RunDetailsDTO{Spec: meshapi.RunSpecDTO{BuildingBlock: meshapi.BuildingBlockSpecDTO{
			Spec: meshapi.BuildingBlockDetailsSpecDTO{Inputs: []meshapi.BuildingBlockInputSpecDTO{{Key: "k"}}},
		}}}
		inputs, err := readInputs(dispatch.ClaimedRun{RawJson: "not-valid-base64!!!", Details: dto}, log)
		require.NoError(t, err)
		require.Len(t, inputs, 1)
	})

	t.Run("empty RawJson and nil Details returns nil", func(t *testing.T) {
		inputs, err := readInputs(dispatch.ClaimedRun{}, log)
		require.NoError(t, err)
		require.Nil(t, inputs)
	})

	t.Run("unparsable RawJson returns an error", func(t *testing.T) {
		_, err := readInputs(dispatch.ClaimedRun{RawJson: "bm90IGpzb24="}, log) // base64("not json")
		require.Error(t, err)
	})
}

// Test_LoadConfig_MalformedYAML covers the config-parse-error branch of LoadConfig.
func Test_LoadConfig_MalformedYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runner-config.yml")
	require.NoError(t, os.WriteFile(path, []byte("not: [valid: yaml"), 0o600))
	t.Setenv("RUNNER_CONFIG_FILE", path)
	_, err := LoadConfig(testLog(), "v", false)
	require.Error(t, err)
}
