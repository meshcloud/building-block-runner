package meshapi

// Pins the DoRequest/DoAuthorizedRequest facade branches directly (the migrated method
// tests in client_test.go/consolidation_test.go/runner_client_test.go already drive most of
// this through RunClient/RunnerClient/ApiKeyAuth, but a facade-level test survives a future
// call-site refactor).

import (
	"context"
	"crypto/x509"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type doRequestPayload struct {
	Ok bool `json:"ok"`
}

// (1) happy path: a 2xx JSON body unmarshals into the caller's type parameter.
func TestDoRequest_HappyPath_UnmarshalsIntoResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	result, err := DoRequest[doRequestPayload](context.Background(), srv.Client(), noopLogger{}, http.MethodGet, srv.URL)

	require.NoError(t, err)
	assert.True(t, result.Ok)
}

// (2) an empty 2xx body yields the zero value and no error — meshapi's deliberate
// divergence from tf-provider, which treats an empty success body as an error.
func TestDoRequest_Empty2xxBody_ReturnsZeroValueNoError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	result, err := DoRequest[doRequestPayload](context.Background(), srv.Client(), noopLogger{}, http.MethodPost, srv.URL)

	require.NoError(t, err)
	assert.Equal(t, doRequestPayload{}, result)
}

// (3) a non-2xx response yields an HttpError carrying the status and the (capped) body.
func TestDoRequest_Non2xx_ReturnsHttpErrorWithCappedBody(t *testing.T) {
	oversized := strings.Repeat("x", maxErrorBodyBytes+100)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(oversized))
	}))
	defer srv.Close()

	_, err := DoRequest[doRequestPayload](context.Background(), srv.Client(), noopLogger{}, http.MethodGet, srv.URL)

	require.Error(t, err)
	he, ok := AsHttpError(err)
	require.True(t, ok)
	assert.Equal(t, http.StatusInternalServerError, he.StatusCode)
	assert.Len(t, he.ResponseBody, maxErrorBodyBytes, "response body must be capped at maxErrorBodyBytes")
}

// (4) WithResponseSink streams a 2xx body into the writer, enforces the maxBytes cap, and
// logs only metadata (never the body) — see also artifact_cap_test.go for the production
// 128MiB cap end-to-end through DownloadArtifact.
func TestDoRequest_WithResponseSink_StreamsAndCapsAndLogsMetadataOnly(t *testing.T) {
	payload := "hello-sink"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(payload))
	}))
	defer srv.Close()

	var buf syncBuffer
	dbg := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	var sink strings.Builder
	_, err := DoRequest[doRequestPayload](context.Background(), srv.Client(), SlogLogger(dbg), http.MethodGet, srv.URL,
		WithResponseSink(&sink, int64(len(payload))+1))

	require.NoError(t, err)
	assert.Equal(t, payload, sink.String())

	out := buf.String()
	assert.Contains(t, out, "meshapi response")
	assert.Contains(t, out, "contentLength")
	assert.NotContains(t, out, payload, "sink body must never be routed through the body logger")

	// Cap enforcement: a body that reaches exactly maxBytes is rejected as oversized.
	var cappedSink strings.Builder
	_, err = DoRequest[doRequestPayload](context.Background(), srv.Client(), noopLogger{}, http.MethodGet, srv.URL,
		WithResponseSink(&cappedSink, int64(len(payload))))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds the maximum allowed size")
}

// fixedAuth is a minimal Authorization for pinning DoAuthorizedRequest's header wiring
// without needing a full ApiKeyAuth/login round-trip.
type fixedAuth string

func (f fixedAuth) Header(context.Context) (string, error) { return string(f), nil }

// (5) DoAuthorizedRequest resolves the Authorization header via the given Authorization and
// sends it on the request.
func TestDoAuthorizedRequest_AppliesAuthorizationHeader(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	_, err := DoAuthorizedRequest[doRequestPayload](context.Background(), srv.Client(), noopLogger{}, fixedAuth("Bearer wired-token"), http.MethodGet, srv.URL)

	require.NoError(t, err)
	assert.Equal(t, "Bearer wired-token", gotAuth)
}

// authorizationOf returns an Authorization implementation as-is, and wraps a bare
// AuthProvider (that does not itself implement Authorization) in legacyAuthProvider.
func TestAuthorizationOf(t *testing.T) {
	apiKey := &ApiKeyAuth{}
	got := authorizationOf(apiKey)
	assert.Same(t, apiKey, got, "an Authorization implementation is returned as-is")

	basic := BasicAuth{Username: "u", Password: "p"}
	got2 := authorizationOf(basic)
	_, ok := got2.(legacyAuthProvider)
	assert.True(t, ok, "a bare AuthProvider must be wrapped in legacyAuthProvider")
}

// (6) the process-wide singleton follows redirects by default, matching meshapi's
// pre-facade &http.Client{} (which also followed redirects using Go's default policy).
func TestSharedHTTPClient_FollowsRedirectsByDefault(t *testing.T) {
	var finalHit bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "/final", http.StatusFound)
			return
		}
		finalHit = true
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	result, err := DoRequest[doRequestPayload](context.Background(), sharedHTTPClient, noopLogger{}, http.MethodGet, srv.URL+"/redirect")

	require.NoError(t, err)
	assert.True(t, finalHit, "the redirect target must have been hit")
	assert.True(t, result.Ok)
}

// (7) WithNoRedirect opts a single request on the shared singleton out of redirect-following:
// the 3xx surfaces as an HttpError and the redirect target is never hit — the CI trigger
// POSTs need this because their bodies carry secrets a 307/308 would resend cross-target.
func TestSharedHTTPClient_WithNoRedirect_SurfacesRedirectAsErrorAndSkipsTarget(t *testing.T) {
	var targetHit bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "/final", http.StatusFound)
			return
		}
		targetHit = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	_, err := DoRequest[doRequestPayload](context.Background(), SharedHTTPClient(), noopLogger{}, http.MethodGet, srv.URL+"/redirect",
		WithNoRedirect())

	require.Error(t, err)
	he, ok := AsHttpError(err)
	require.True(t, ok)
	assert.Equal(t, http.StatusFound, he.StatusCode)
	assert.False(t, targetHit, "the redirect target must never be hit when WithNoRedirect is set")
}

// (8) WithStrictJSONSuccess rejects the Azure DevOps 203+HTML sign-in-page quirk as an
// HttpError instead of trying (and failing) to unmarshal HTML as the expected DTO.
func TestDoRequest_WithStrictJSONSuccess_203HTML_ReturnsHttpError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusNonAuthoritativeInfo)
		_, _ = w.Write([]byte("<html>sign in</html>"))
	}))
	defer srv.Close()

	_, err := DoRequest[doRequestPayload](context.Background(), srv.Client(), noopLogger{}, http.MethodGet, srv.URL,
		WithStrictJSONSuccess())

	require.Error(t, err)
	he, ok := AsHttpError(err)
	require.True(t, ok)
	assert.Equal(t, http.StatusNonAuthoritativeInfo, he.StatusCode)
}

// (9) WithStrictJSONSuccess stays lenient for a 200 whose body is valid JSON served without an
// explicit application/json Content-Type (Go's server sniffs this to text/plain) — several
// azdevops responses look like this and must still parse.
func TestDoRequest_WithStrictJSONSuccess_200PlainJSONWithoutHeader_StillParses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	result, err := DoRequest[doRequestPayload](context.Background(), srv.Client(), noopLogger{}, http.MethodGet, srv.URL,
		WithStrictJSONSuccess())

	require.NoError(t, err)
	assert.True(t, result.Ok)
}

// (10) ConfigureRootCAs rebuilds sharedHTTPClient's transport to trust a pool built from a
// test server's cert, so a request through SharedHTTPClient() (rather than srv.Client())
// succeeds against that TLS server.
func TestConfigureRootCAs_TrustsPoolAndClientSucceeds(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	originalTransport := sharedHTTPClient.Transport
	defer func() { sharedHTTPClient.Transport = originalTransport }()

	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	ConfigureRootCAs(pool)

	result, err := DoRequest[doRequestPayload](context.Background(), SharedHTTPClient(), noopLogger{}, http.MethodGet, srv.URL)

	require.NoError(t, err)
	assert.True(t, result.Ok)
}

// (11) ConfigureRootCAs is a no-op for a nil pool, preserving today's
// http.DefaultTransport-backed trust store.
func TestConfigureRootCAs_NilPool_NoOp(t *testing.T) {
	originalTransport := sharedHTTPClient.Transport
	defer func() { sharedHTTPClient.Transport = originalTransport }()

	ConfigureRootCAs(nil)

	assert.Same(t, originalTransport, sharedHTTPClient.Transport)
}
