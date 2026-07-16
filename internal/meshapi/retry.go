package meshapi

import (
	"net/http"
	"time"

	"github.com/meshcloud/building-block-runner/internal/httpclient"
)

// Backoff, ExponentialBackoff and RetryOptions are aliased from the shared
// internal/httpclient package: the transport-level retry/backoff mechanism (the
// http.RoundTripper wrapper, its Retry-After handling, body-replay, ...) moved there as a
// cross-cutting concern shared with future retry-enabled clients (PLAN.md "Generic JSON
// HTTP client"). Aliasing — rather than redefining — keeps every existing call site, and the
// tests that reference these names, unchanged.
type (
	Backoff            = httpclient.Backoff
	ExponentialBackoff = httpclient.ExponentialBackoff
	RetryOptions       = httpclient.RetryOptions
	retryTransport     = httpclient.RetryTransport
)

// newRetryTransport delegates to the shared httpclient package; kept as a thin wrapper so
// every meshapi call site (and its tests) keeps referring to a package-local name.
func newRetryTransport(base http.RoundTripper, opts RetryOptions, log Logger) http.RoundTripper {
	return httpclient.NewRetryTransport(base, opts, log)
}

// globalRetryOptions is the single retry policy for the process-wide sharedHTTPClient
// (dorequest.go): MaxRetries 12, 1–30s exponential (~a few minutes total, riding out a
// longer backend restart than the old per-client policies). Idempotent methods
// (GET/PUT/DELETE) always retry; of the POSTs, register-source ("/status/source"), login
// ("/api/login") and github's installation-token mint ("/access_tokens") are whitelisted for
// replay — register-source because a 409-on-replay is already treated as success, login and
// the token mint because they idempotently (re-)mint a token with no other side effect. The
// claim POST (".../create") and the status PATCH are deliberately NOT whitelisted so an
// ambiguous failure never double-claims a run or delays abort detection; nor are the CI
// trigger POSTs (gitlab pipeline trigger, github workflow_dispatch, azdevops run), which must
// fail hard rather than risk double-triggering a build.
func globalRetryOptions() RetryOptions {
	return RetryOptions{
		MaxRetries:       12,
		Backoff:          ExponentialBackoff{MinWait: 1 * time.Second, MaxWait: 30 * time.Second},
		WhitelistedPosts: []string{"/status/source", "/api/login", "/access_tokens"},
	}
}
