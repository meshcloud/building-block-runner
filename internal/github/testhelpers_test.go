package github

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/meshcloud/building-block-runner/internal/report"
)

// testKey generates a fresh RSA key and returns it with a PKCS#1 PEM (the GitHub App format).
func testKey(t *testing.T) (*rsa.PrivateKey, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating key: %v", err)
	}
	der := x509.MarshalPKCS1PrivateKey(key)
	pemStr := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: der}))
	return key, pemStr
}

// fakeClock is a deterministic Clock. Now returns the current fake time; Wait advances it by
// d, then (optionally) runs onWait, then returns false if ctx is cancelled or cancelNow is
// set — so tests drive both the poll-timeout and ctx-cancellation paths without real sleeps.
type fakeClock struct {
	now       time.Time
	waitCalls int
	// onWait runs after now advances, receiving the 1-based call number; a test can jump now
	// forward (poll-timeout) or trigger cancellation.
	onWait func(call int)
	// cancelOnWait, when >0, makes Wait return false on that call number (ctx-cancel path).
	cancelOnWait int
}

func newFakeClock(start time.Time) *fakeClock { return &fakeClock{now: start} }

func (c *fakeClock) Now() time.Time { return c.now }

func (c *fakeClock) Wait(ctx context.Context, d time.Duration) bool {
	c.waitCalls++
	c.now = c.now.Add(d)
	if c.onWait != nil {
		c.onWait(c.waitCalls)
	}
	if ctx.Err() != nil {
		return false
	}
	if c.cancelOnWait > 0 && c.waitCalls == c.cancelOnWait {
		return false
	}
	return true
}

// fakeReporter captures every Register/Report call for assertions. It is safe for concurrent
// use (the poller reports from the handler goroutine only, but tests read after).
type fakeReporter struct {
	mu         sync.Mutex
	registered []report.RunStatus
	reports    []report.RunStatus
	// failReport, when set, is returned from Report to exercise transport-failure paths.
	failReport error
	// failRegister, when set, is returned from Register.
	failRegister error
	// abortOnReport reports the abort flag returned to callers (always discarded by github).
	abortOnReport bool
}

func (r *fakeReporter) Register(s report.RunStatus) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.registered = append(r.registered, s.Clone())
	return r.failRegister
}

func (r *fakeReporter) Report(s report.RunStatus) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reports = append(r.reports, s.Clone())
	return r.abortOnReport, r.failReport
}

func (r *fakeReporter) lastReport() report.RunStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.reports) == 0 {
		return report.RunStatus{}
	}
	return r.reports[len(r.reports)-1]
}

// recordedRequest is one captured HTTP call to the fake GitHub.
type recordedRequest struct {
	Method string
	Path   string
	Query  string
	Header http.Header
	Body   string
}

// githubStub is a configurable fake GitHub API. Each endpoint has a handler the test can
// override; defaults produce a happy async trigger path.
type githubStub struct {
	server *httptest.Server
	mu     sync.Mutex
	reqs   []recordedRequest

	installation http.HandlerFunc
	token        http.HandlerFunc
	dispatch     http.HandlerFunc
	listRuns     http.HandlerFunc
	getRun       http.HandlerFunc
	listJobs     http.HandlerFunc
}

func newGithubStub(t *testing.T) *githubStub {
	t.Helper()
	s := &githubStub{}
	s.installation = jsonHandler(200, `{"id": 42}`)
	s.token = jsonHandler(200, `{"token":"ghs_installation","permissions":{"actions":"write"}}`)
	s.dispatch = func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }
	s.listRuns = jsonHandler(200, `{"workflow_runs":[]}`)
	s.getRun = jsonHandler(200, `{"id":100,"status":"completed","conclusion":"success","created_at":"2026-07-10T10:00:00Z","html_url":"https://gh/run/100"}`)
	s.listJobs = jsonHandler(200, `{"jobs":[]}`)

	s.server = httptest.NewServer(http.HandlerFunc(s.route))
	t.Cleanup(s.server.Close)
	return s
}

func (s *githubStub) url() string { return s.server.URL }

func (s *githubStub) route(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	s.mu.Lock()
	s.reqs = append(s.reqs, recordedRequest{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, Header: r.Header.Clone(), Body: string(body)})
	s.mu.Unlock()
	// restore body for the handler if needed (none of ours read it, but keep it clean)
	r.Body = io.NopCloser(strings.NewReader(string(body)))

	p := r.URL.Path
	switch {
	case strings.HasSuffix(p, "/installation"):
		s.installation(w, r)
	case strings.HasSuffix(p, "/access_tokens"):
		s.token(w, r)
	case strings.HasSuffix(p, "/dispatches"):
		s.dispatch(w, r)
	case strings.HasSuffix(p, "/runs") && r.Method == http.MethodGet:
		s.listRuns(w, r)
	case strings.HasSuffix(p, "/jobs"):
		s.listJobs(w, r)
	case strings.Contains(p, "/actions/runs/") && r.Method == http.MethodGet:
		s.getRun(w, r)
	default:
		http.Error(w, "unexpected path: "+p, http.StatusNotImplemented)
	}
}

func (s *githubStub) requests() []recordedRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]recordedRequest, len(s.reqs))
	copy(out, s.reqs)
	return out
}

// sequence returns a handler that serves the given bodies in order (last repeats).
func sequence(status int, bodies ...string) http.HandlerFunc {
	var i int
	var mu sync.Mutex
	return func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		b := bodies[i]
		if i < len(bodies)-1 {
			i++
		}
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, b)
	}
}

func jsonHandler(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}
}

// asMap is a checked map type assertion for parsed-JSON navigation in tests.
func asMap(t *testing.T, v any) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("expected a JSON object, got %T", v)
	}
	return m
}

// asSlice is a checked slice type assertion.
func asSlice(t *testing.T, v any) []any {
	t.Helper()
	s, ok := v.([]any)
	if !ok {
		t.Fatalf("expected a JSON array, got %T", v)
	}
	return s
}

// asFloat is a checked float64 (JSON number) type assertion.
func asFloat(t *testing.T, v any) float64 {
	t.Helper()
	f, ok := v.(float64)
	if !ok {
		t.Fatalf("expected a JSON number, got %T", v)
	}
	return f
}

// encodeRawJSON base64-encodes a raw run JSON string, matching ClaimedRun.RawJson.
func encodeRawJSON(raw string) string {
	return base64.StdEncoding.EncodeToString([]byte(raw))
}

// b64decode decodes a base64 dispatch input for parsed-JSON assertions.
func b64decode(t *testing.T, s string) []byte {
	t.Helper()
	data, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		t.Fatalf("decoding base64 payload: %v", err)
	}
	return data
}
