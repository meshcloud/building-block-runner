package azdevops

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// captured transport request evidence for assertions.
type capturedReq struct {
	Method string
	URL    string
	Header http.Header
	Body   []byte
}

// fakeADO is a fake Azure DevOps HTTP transport: a real httptest.Server (dialed for real, so
// headers/URLs/redirects are exercised end to end, D6's real-transport-over-hand-rolled
// principle applied to this port's external API too) with seedable responses per path.
type fakeADO struct {
	*httptest.Server
	requests []capturedReq
	handler  func(w http.ResponseWriter, r *http.Request, body []byte)
}

func newFakeADO(t *testing.T, handler func(w http.ResponseWriter, r *http.Request, body []byte)) *fakeADO {
	t.Helper()
	f := &fakeADO{handler: handler}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body []byte
		if r.Body != nil {
			body, _ = io.ReadAll(r.Body)
		}
		f.requests = append(f.requests, capturedReq{Method: r.Method, URL: r.URL.String(), Header: r.Header.Clone(), Body: body})
		f.handler(w, r, body)
	}))
	t.Cleanup(f.Close)
	return f
}

func testClient(baseUrl string) adoClient {
	return newADOClient(baseUrl, "myorg", "myproj", "42", "the-pat", NewHTTPClient(0))
}

// Test_TriggerPipeline_URLAndPayload is A-P4/A-P5: trigger POST path + no-resources default,
// resources.repositories.self.refName when set, Basic auth header (never the PAT in the
// body -- the §7.6 leak pin), Accept/Content-Type headers.
func Test_TriggerPipeline_URLAndPayload(t *testing.T) {
	var gotBody []byte
	srv := newFakeADO(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		gotBody = body
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":7,"state":"inProgress","result":null,"createdDate":"now"}`))
	})

	c := testClient(srv.URL)
	pr, err := c.TriggerPipeline(context.Background(), map[string]string{"foo": "bar"}, nil)
	require.NoError(t, err)
	require.Equal(t, int64(7), pr.Id)

	require.Len(t, srv.requests, 1)
	req := srv.requests[0]
	require.Equal(t, http.MethodPost, req.Method)
	require.Equal(t, "/myorg/myproj/_apis/pipelines/42/runs?api-version=7.1", req.URL)
	require.Equal(t, "application/json", req.Header.Get("Accept"))

	wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte(":the-pat"))
	require.Equal(t, wantAuth, req.Header.Get("Authorization"))
	require.NotContains(t, string(gotBody), "the-pat", "the PAT must never appear in the trigger body (leak pin, §7.6)")

	var payload map[string]any
	require.NoError(t, json.Unmarshal(gotBody, &payload))
	require.Equal(t, "bar", asMap(t, payload["templateParameters"])["foo"])
	_, hasResources := payload["resources"]
	require.False(t, hasResources, "resources must be omitted when refName is nil")
}

// asMap safely asserts v is a map[string]any, failing the test (rather than panicking) if
// not -- used to inspect JSON payloads decoded into map[string]any without bare, unchecked
// type assertions.
func asMap(t *testing.T, v any) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	require.True(t, ok, "expected a JSON object, got %T", v)
	return m
}

// Test_TriggerPipeline_WithRefName pins the resources.repositories.self.refName shape.
func Test_TriggerPipeline_WithRefName(t *testing.T) {
	var gotBody []byte
	srv := newFakeADO(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		gotBody = body
		_, _ = w.Write([]byte(`{"id":1,"state":"completed","result":"succeeded","createdDate":"now"}`))
	})
	c := testClient(srv.URL)
	ref := "refs/heads/main"
	_, err := c.TriggerPipeline(context.Background(), map[string]string{}, &ref)
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(gotBody, &payload))
	resources := asMap(t, payload["resources"])
	repos := asMap(t, resources["repositories"])
	self := asMap(t, repos["self"])
	require.Equal(t, ref, self["refName"])
}

// Test_TriggerPipeline_NonSuccessBecomesExternalCallError is A-P6.
func Test_TriggerPipeline_NonSuccessBecomesExternalCallError(t *testing.T) {
	srv := newFakeADO(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"not found"}`))
	})
	c := testClient(srv.URL)
	_, err := c.TriggerPipeline(context.Background(), map[string]string{}, nil)
	require.Error(t, err)

	var extErr ExternalCallError
	require.ErrorAs(t, err, &extErr)
	require.Equal(t, http.StatusNotFound, extErr.StatusCode)
	require.Contains(t, extErr.ResponseBody, "not found")
	require.Equal(t, "Failed to trigger Azure DevOps pipeline", extErr.UserMessage)
}

// Test_TriggerPipeline_RedirectNotFollowed is A-P6's redirect half: a 302 surfaces as a
// non-2xx ExternalCallError, never silently followed (OkHttp followRedirects(false) twin).
func Test_TriggerPipeline_RedirectNotFollowed(t *testing.T) {
	srv := newFakeADO(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		w.Header().Set("Location", "https://example.invalid/elsewhere")
		w.WriteHeader(http.StatusFound)
	})
	c := testClient(srv.URL)
	_, err := c.TriggerPipeline(context.Background(), map[string]string{}, nil)
	require.Error(t, err)
	var extErr ExternalCallError
	require.ErrorAs(t, err, &extErr)
	require.Equal(t, http.StatusFound, extErr.StatusCode)
}

// Test_GetPipelineRun pins the GET run URL/path shape.
func Test_GetPipelineRun(t *testing.T) {
	srv := newFakeADO(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		_, _ = w.Write([]byte(`{"id":99,"state":"completed","result":"succeeded","createdDate":"now","url":"https://ado/run/99"}`))
	})
	c := testClient(srv.URL)
	pr, err := c.GetPipelineRun(context.Background(), 99)
	require.NoError(t, err)
	require.Equal(t, int64(99), pr.Id)
	require.Equal(t, stateCompleted, pr.State)

	require.Len(t, srv.requests, 1)
	require.Equal(t, http.MethodGet, srv.requests[0].Method)
	require.Equal(t, "/myorg/myproj/_apis/pipelines/42/runs/99?api-version=7.1", srv.requests[0].URL)
}

// Test_GetTimeline pins the *build* timeline URL/path shape (a different API family than
// pipelines/runs, §2.5) and unwraps TimelineResponse.records.
func Test_GetTimeline(t *testing.T) {
	srv := newFakeADO(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		_, _ = w.Write([]byte(`{"records":[{"id":"s1","type":"Stage","order":0}]}`))
	})
	c := testClient(srv.URL)
	records, err := c.GetTimeline(context.Background(), 99)
	require.NoError(t, err)
	require.Len(t, records, 1)
	require.Equal(t, "s1", records[0].Id)

	require.Len(t, srv.requests, 1)
	require.Equal(t, "/myorg/myproj/_apis/build/builds/99/timeline?api-version=7.1", srv.requests[0].URL)
}

// Test_BaseURL_NotSanitized pins §16.7: a trailing slash on the base URL is preserved
// verbatim, producing a double-slash request URL (azdevops never sanitizes, unlike
// gitlab/github).
func Test_BaseURL_NotSanitized(t *testing.T) {
	srv := newFakeADO(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		_, _ = w.Write([]byte(`{"id":1,"state":"completed","result":"succeeded","createdDate":"now"}`))
	})
	c := newADOClient(srv.URL+"/", "myorg", "myproj", "42", "pat", NewHTTPClient(0))
	_, err := c.GetPipelineRun(context.Background(), 1)
	require.NoError(t, err)
	require.Equal(t, "//myorg/myproj/_apis/pipelines/42/runs/1?api-version=7.1", srv.requests[0].URL)
}

func Test_RenderValue(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want string
	}{
		{"string verbatim", "hello", "hello"},
		{"json.Number literal", json.Number("123456789012345678901234"), "123456789012345678901234"},
		{"bool true", true, "true"},
		{"bool false", false, "false"},
		{"nil", nil, "null"},
		{"array -> compact JSON (flagged delta, §16.6)", []any{"a", "b"}, `["a","b"]`},
		{"object -> compact JSON (flagged delta, §16.6)", map[string]any{"k": "v"}, `{"k":"v"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, c.want, renderValue(c.in))
		})
	}
}
