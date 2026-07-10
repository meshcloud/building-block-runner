package meshapi

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- HttpError ----

func TestHttpError_MessageAndClassification(t *testing.T) {
	assert.Equal(t, "unexpected HTTP status: 404", HttpError{StatusCode: 404}.Error())
	assert.Equal(t, "unexpected HTTP status 409: taken", HttpError{StatusCode: 409, ResponseBody: []byte("taken")}.Error())

	assert.True(t, HttpError{StatusCode: 404}.IsNotFound())
	assert.False(t, HttpError{StatusCode: 409}.IsNotFound())
	assert.True(t, HttpError{StatusCode: 409}.IsConflict())
	assert.True(t, HttpError{StatusCode: 403}.IsForbidden())
}

func TestAsHttpError_BareAndWrapped(t *testing.T) {
	bare := error(HttpError{StatusCode: 404})
	he, ok := AsHttpError(bare)
	require.True(t, ok)
	assert.Equal(t, 404, he.StatusCode)

	wrapped := fmt.Errorf("download artifact x: %w", HttpError{StatusCode: 500, ResponseBody: []byte("boom")})
	he, ok = AsHttpError(wrapped)
	require.True(t, ok)
	assert.Equal(t, 500, he.StatusCode)

	_, ok = AsHttpError(fmt.Errorf("plain error"))
	assert.False(t, ok)
}

// FetchRun's non-2xx paths must surface an HttpError whose Is* classify the frozen D9
// no-run / conflict signals.
func TestRunClient_FetchRun_HttpErrorClassification(t *testing.T) {
	for _, tc := range []struct {
		status         int
		wantNotFound   bool
		wantConflict   bool
		wantClassified bool
	}{
		{http.StatusNotFound, true, false, true},
		{http.StatusConflict, false, true, true},
		{http.StatusInternalServerError, false, false, true},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "no", tc.status)
		}))
		client := NewRunClient(srv.URL, "node", BasicAuth{})
		_, _, err := client.FetchRun("uuid")
		srv.Close()

		require.Error(t, err)
		he, ok := AsHttpError(err)
		require.Equal(t, tc.wantClassified, ok)
		assert.Equal(t, tc.wantNotFound, he.IsNotFound())
		assert.Equal(t, tc.wantConflict, he.IsConflict())
	}
}

// ---- Identity ----

func TestIdentity_UserAgentAndDefaults(t *testing.T) {
	assert.Equal(t, "meshcloud-unknown-runner/dev", Identity{}.UserAgent())
	assert.Equal(t, "meshcloud-run-controller/1.2.3", Identity{Name: "run-controller", Version: "1.2.3"}.UserAgent())
	assert.Equal(t, "unknown-runner", Identity{}.name())
	assert.Equal(t, "dev", Identity{}.version())
}

// WithIdentity stamps the User-Agent + X-Meshcloud-Runner-* headers; the zero Identity
// reproduces the former global defaults byte-identically.
func TestRunClient_WithIdentity_StampsHeaders(t *testing.T) {
	var gotUA, gotName, gotVer string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		gotName = r.Header.Get("X-Meshcloud-Runner-Name")
		gotVer = r.Header.Get("X-Meshcloud-Runner-Version")
		http.Error(w, "no", http.StatusNotFound)
	}))
	defer srv.Close()

	client := NewRunClient(srv.URL, "node", BasicAuth{}, WithIdentity(Identity{Name: "tf-block-runner", Version: "9.9"}))
	_, _, _ = client.FetchRun("uuid")

	assert.Equal(t, "meshcloud-tf-block-runner/9.9", gotUA)
	assert.Equal(t, "tf-block-runner", gotName)
	assert.Equal(t, "9.9", gotVer)
}

// ---- Logging seam ----

func TestLoggedHeaders_RedactsAuthorizationAndSorts(t *testing.T) {
	h := http.Header{
		"Authorization": {"Bearer super-secret"},
		"Accept":        {"application/json"},
		"X-Node":        {"n1"},
	}
	got := loggedHeaders(h).String()
	assert.Contains(t, got, "Authorization: [REDACTED]")
	assert.NotContains(t, got, "super-secret")
	// sorted: Accept before Authorization before X-Node
	assert.Less(t, strings.Index(got, "Accept"), strings.Index(got, "Authorization"))
	assert.Less(t, strings.Index(got, "Authorization"), strings.Index(got, "X-Node"))
}

func TestBytesToPrettyJson(t *testing.T) {
	assert.Empty(t, bytesToPrettyJson(nil))
	assert.Equal(t, "{\n  \"a\": 1\n}", bytesToPrettyJson([]byte(`{"a":1}`)))
	assert.Equal(t, "not json", bytesToPrettyJson([]byte("not json")))
}

func TestSlogLogger_NilYieldsNoop(t *testing.T) {
	assert.IsType(t, noopLogger{}, SlogLogger(nil))
}

// At DEBUG, the client logs the request/response with the Authorization header redacted
// and the JSON body in full; at INFO it logs nothing.
func TestRunClient_WithLogger_DebugLogsWireRedactingAuth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"runAborted":false}`))
	}))
	defer srv.Close()

	var buf syncBuffer
	dbg := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	client := NewRunClient(srv.URL, "node", BasicAuth{Username: "u", Password: "p"}, WithLogger(SlogLogger(dbg)))

	_, err := client.PatchStatus("run", "src", map[string]string{"status": "SUCCEEDED"})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "meshapi request")
	assert.Contains(t, out, "meshapi response")
	assert.Contains(t, out, "[REDACTED]", "Authorization must be masked in logs")
	assert.NotContains(t, out, "dTpw", "the base64 basic-auth secret must not appear")
	assert.Contains(t, out, "SUCCEEDED", "request body is logged in full at debug")

	var infoBuf syncBuffer
	info := slog.New(slog.NewTextHandler(&infoBuf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	client2 := NewRunClient(srv.URL, "node", BasicAuth{}, WithLogger(SlogLogger(info)))
	_, err = client2.PatchStatus("run", "src", map[string]string{"status": "SUCCEEDED"})
	require.NoError(t, err)
	assert.Empty(t, infoBuf.String(), "no wire logging below debug level")
}

// The 128MiB artifact stream is never routed through the body logger (§5.2.6): only
// metadata is logged for a successful download.
func TestRunClient_DownloadArtifact_BodyNotLogged(t *testing.T) {
	payload := "PRETEND-ARTIFACT-BYTES"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(payload))
	}))
	defer srv.Close()

	var buf syncBuffer
	dbg := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	client := NewRunClient(srv.URL, "node", BasicAuth{}, WithLogger(SlogLogger(dbg)))

	var sink strings.Builder
	require.NoError(t, client.DownloadArtifact(srv.URL+"/a", &sink))
	assert.Equal(t, payload, sink.String())

	out := buf.String()
	assert.Contains(t, out, "meshapi artifact response")
	assert.NotContains(t, out, payload, "artifact bytes must never be routed through the body logger")
}

// The retry transport is wired into the default constructor: a whitelisted register-source
// POST rides out a 503 and succeeds, end-to-end through NewRunClient.
func TestRunClient_RegisterSource_RetriesThrough503(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := newRunClientFastRetry(srv.URL)
	err := client.RegisterSource("run", RegistrationDTO{Source: SourceDTO{Id: "s"}})
	require.NoError(t, err)
	assert.Equal(t, int32(2), calls.Load(), "register-source rides out one 503")
}

// newRunClientFastRetry builds a RunClient whose retry backoff is sub-millisecond so the
// integration retry test does not sleep for the production 1-8s budget.
func newRunClientFastRetry(baseURL string) *RunClient {
	c := &RunClient{baseURL: baseURL, nodeID: "node", auth: BasicAuth{}, log: noopLogger{}}
	opts := defaultRunRetryOptions()
	opts.Backoff = ExponentialBackoff{MinWait: 100_000, MaxWait: 200_000} // 0.1-0.2ms
	c.http = &http.Client{Transport: newRetryTransport(nil, opts, noopLogger{})}
	return c
}

// syncBuffer is a tiny goroutine-safe buffer for capturing slog output written from the
// httptest server's handler goroutine and the test goroutine.
type syncBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
