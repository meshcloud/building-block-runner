// Package httpclient holds the cross-cutting HTTP transport concerns shared by every
// JSON-API client in this repo (meshapi, azdevops, gitlab, github): a no-follow-redirect
// *http.Client builder and a generic retry/backoff http.RoundTripper. Each client keeps its
// own per-type request/response DTOs and error classification here — those encode a frozen,
// byte-exact wire contract per package (azdevops HTTP-Basic; github Bearer + custom headers;
// gitlab token-in-multipart-body + a 4-way classifier) and deliberately do not move here.
package httpclient

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"time"
)

// noRedirectKey is the ctx key for the per-request redirect opt-out sentinel (see
// WithoutRedirects/SentinelCheckRedirect). Unexported so only this package can stamp it.
type noRedirectKey struct{}

// WithoutRedirects marks ctx so a shared client's CheckRedirect (see SentinelCheckRedirect)
// surfaces the next redirect response instead of following it, without switching that
// request off the process-wide client. Go's http.Client copies the originating request's
// context onto every redirect request it builds, so the sentinel rides along.
func WithoutRedirects(ctx context.Context) context.Context {
	return context.WithValue(ctx, noRedirectKey{}, true)
}

// SentinelCheckRedirect is an http.Client.CheckRedirect that honors a per-request opt-out
// (WithoutRedirects) on an otherwise redirect-following shared client: it returns
// http.ErrUseLastResponse when req's context carries the sentinel, and nil (follow) otherwise.
func SentinelCheckRedirect(req *http.Request, _ []*http.Request) error {
	if v, ok := req.Context().Value(noRedirectKey{}).(bool); ok && v {
		return http.ErrUseLastResponse
	}
	return nil
}

// ClientOption customizes an *http.Client built by NoRedirectClient.
type ClientOption func(*http.Client)

// WithRootCAs sets the client's TLS trust anchors to pool instead of the system default,
// cloning http.DefaultTransport so other transport settings (proxy, idle-conn limits) are
// left untouched and the shared DefaultTransport singleton is never mutated. A nil pool
// is a no-op, leaving Transport unset (system default trust).
func WithRootCAs(pool *x509.CertPool) ClientOption {
	return func(c *http.Client) {
		if pool == nil {
			return
		}
		transport := http.DefaultTransport.(*http.Transport).Clone() //nolint:forcetypeassert // http.DefaultTransport is always *http.Transport
		transport.TLSClientConfig = &tls.Config{RootCAs: pool}
		c.Transport = transport
	}
}

// NoRedirectClient returns an *http.Client that never follows redirects: CheckRedirect
// always returns http.ErrUseLastResponse, so a 3xx response comes back to the caller as-is
// instead of being followed — a redirect followed automatically could otherwise silently
// retarget which server receives request credentials/tokens.
//
// timeout is the client-wide request timeout (connect+read+write+redirect-check combined);
// pass 0 to leave it unset (the http.Client zero-value default of no timeout), for callers
// that bound requests via context instead.
func NoRedirectClient(timeout time.Duration, opts ...ClientOption) *http.Client {
	c := &http.Client{
		Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}
