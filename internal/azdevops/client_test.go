package azdevops

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/internal/httpclient"
	"github.com/meshcloud/building-block-runner/internal/meshapi"
)

// captured transport request evidence for assertions.
type capturedReq struct {
	Method string
	URL    string
	Header http.Header
	Body   []byte
}

// fakeADO is a fake Azure DevOps HTTP transport: a real httptest.Server (dialed for real, so
// headers/URLs/redirects are exercised end to end, the real-transport-over-hand-rolled
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
	return newADOClient(baseUrl, "myorg", "myproj", "42", "the-pat", NewHTTPClient(0, nil), meshapi.SlogLogger(nil))
}

// Test_TriggerPipeline_URLAndPayload covers the trigger POST path + no-resources default,
// resources.repositories.self.refName when set, Basic auth header (never the PAT in the
// body -- the leak pin), Accept/Content-Type headers.
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
	require.NotContains(t, string(gotBody), "the-pat", "the PAT must never appear in the trigger body (leak pin)")

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

// Test_TriggerPipeline_RedirectNotFollowed covers the redirect half: a 302 surfaces as a
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
// pipelines/runs) and unwraps TimelineResponse.records.
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

// Test_BaseURL_NotSanitized pins that a trailing slash on the base URL is preserved
// verbatim, producing a double-slash request URL (azdevops never sanitizes, unlike
// gitlab/github).
func Test_BaseURL_NotSanitized(t *testing.T) {
	srv := newFakeADO(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		_, _ = w.Write([]byte(`{"id":1,"state":"completed","result":"succeeded","createdDate":"now"}`))
	})
	c := newADOClient(srv.URL+"/", "myorg", "myproj", "42", "pat", NewHTTPClient(0, nil), meshapi.SlogLogger(nil))
	_, err := c.GetPipelineRun(context.Background(), 1)
	require.NoError(t, err)
	require.Equal(t, "//myorg/myproj/_apis/pipelines/42/runs/1?api-version=7.1", srv.requests[0].URL)
}

// Test_GetPipelineRun_203HTMLSignInPage_IsAuthError pins the ADO PAT quirk: an expired/
// invalid PAT answers with "203 Non-Authoritative" plus an HTML sign-in page instead of a
// clean 401. WithStrictJSONSuccess must surface this as an ExternalCallError (an auth/
// external error), never as a JSON-unmarshal error.
func Test_GetPipelineRun_203HTMLSignInPage_IsAuthError(t *testing.T) {
	srv := newFakeADO(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusNonAuthoritativeInfo)
		_, _ = w.Write([]byte("<html><body>Sign in</body></html>"))
	})
	c := testClient(srv.URL)
	_, err := c.GetPipelineRun(context.Background(), 1)
	require.Error(t, err)

	var extErr ExternalCallError
	require.ErrorAs(t, err, &extErr, "the 203/HTML sign-in quirk must surface as an ExternalCallError, not a JSON parse error")
	require.Equal(t, http.StatusNonAuthoritativeInfo, extErr.StatusCode)

	require.Len(t, srv.requests, 1)
	require.Equal(t, "Suppress", srv.requests[0].Header.Get("X-TFS-FedAuthRedirect"))
}

// Test_TriggerPipeline_307Redirect_BodyNeverReachesTarget pins the WithNoRedirect half for
// the trigger POST: a 307 (which, unlike 301/302/303, replays the POST body onto the
// redirect target) must never be followed, since the trigger body carries decrypted
// template parameters.
func Test_TriggerPipeline_307Redirect_BodyNeverReachesTarget(t *testing.T) {
	var secondHit atomic.Bool
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secondHit.Store(true)
		w.WriteHeader(http.StatusOK)
	}))
	defer second.Close()

	srv := newFakeADO(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		http.Redirect(w, r, second.URL, http.StatusTemporaryRedirect)
	})

	// A client wired with the shared singleton's sentinel CheckRedirect (follows by
	// default), so TriggerPipeline's per-request meshapi.WithNoRedirect is what stops the
	// redirect here -- not a client-level ban (unlike testClient's NoRedirectClient).
	sentinelClient := &http.Client{CheckRedirect: httpclient.SentinelCheckRedirect}
	c := newADOClient(srv.URL, "myorg", "myproj", "42", "the-pat", sentinelClient, meshapi.SlogLogger(nil))
	_, err := c.TriggerPipeline(context.Background(), map[string]string{"foo": "bar"}, nil)
	require.Error(t, err)

	var extErr ExternalCallError
	require.ErrorAs(t, err, &extErr)
	require.Equal(t, http.StatusTemporaryRedirect, extErr.StatusCode)
	require.False(t, secondHit.Load(), "the redirect target must never receive the trigger body")
}

// Test_GetPipelineRun_RetriedTransparently pins that a transport-retryable 503 on the GET
// run-status endpoint is retried inside the shared retry transport and succeeds without the
// caller seeing the intermediate failure.
func Test_GetPipelineRun_RetriedTransparently(t *testing.T) {
	var calls atomic.Int32
	srv := newFakeADO(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":1,"state":"completed","result":"succeeded","createdDate":"now"}`))
	})

	retryClient := &http.Client{
		Transport: httpclient.NewRetryTransport(nil, httpclient.RetryOptions{
			MaxRetries: 2,
			Backoff:    httpclient.ExponentialBackoff{MinWait: time.Millisecond, MaxWait: 2 * time.Millisecond},
		}, nil),
	}
	c := newADOClient(srv.URL, "myorg", "myproj", "42", "the-pat", retryClient, meshapi.SlogLogger(nil))

	pr, err := c.GetPipelineRun(context.Background(), 1)
	require.NoError(t, err)
	require.Equal(t, int64(1), pr.Id)
	require.Equal(t, int32(2), calls.Load(), "503 then 200: retried once and succeeded")
}

// Test_TriggerPipeline_NeverRetried pins that the trigger POST is not in the shared client's
// retry whitelist: even a retry-capable transport that would otherwise retry a transport-
// retryable 503 leaves the trigger call as exactly one POST, failing hard instead of risking
// a double-triggered pipeline.
func Test_TriggerPipeline_NeverRetried(t *testing.T) {
	var calls atomic.Int32
	srv := newFakeADO(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	})

	retryClient := &http.Client{
		Transport: httpclient.NewRetryTransport(nil, httpclient.RetryOptions{
			MaxRetries: 2,
			Backoff:    httpclient.ExponentialBackoff{MinWait: time.Millisecond, MaxWait: 2 * time.Millisecond},
		}, nil),
	}
	c := newADOClient(srv.URL, "myorg", "myproj", "42", "the-pat", retryClient, meshapi.SlogLogger(nil))

	_, err := c.TriggerPipeline(context.Background(), map[string]string{}, nil)
	require.Error(t, err)
	require.Equal(t, int32(1), calls.Load(), "the trigger POST must never be retried")
}
