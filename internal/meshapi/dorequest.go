package meshapi

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/meshcloud/building-block-runner/internal/httpclient"
)

// requestOptions accumulates the effect of RequestOption values applied to a single
// DoRequest/DoAuthorizedRequest call.
type requestOptions struct {
	header       http.Header
	body         io.Reader
	contentType  string
	sink         io.Writer
	sinkMaxBytes int64
	noRedirect   bool
	strictJSON   bool
}

// RequestOption configures a single DoRequest/DoAuthorizedRequest call.
type RequestOption func(*requestOptions)

// WithHeader sets a single request header (e.g. Accept, or a run-identity header like
// X-Block-Runner-Node-Id).
func WithHeader(key, value string) RequestOption {
	return func(o *requestOptions) {
		if o.header == nil {
			o.header = http.Header{}
		}
		o.header.Set(key, value)
	}
}

// WithJSONPayload json-encodes v into a buffered request body and sets Content-Type:
// application/json. The buffer backs GetBody so a whitelisted/retried POST (e.g. the
// ApiKeyAuth login) can replay its body, and the same bytes can be logged non-destructively.
func WithJSONPayload(v any) RequestOption {
	return func(o *requestOptions) {
		buf := &bytes.Buffer{}
		if err := json.NewEncoder(buf).Encode(v); err != nil {
			// Encoding a well-formed DTO cannot fail in practice; surface a body that makes
			// json.Marshal's error visible in the request log rather than panicking.
			o.body = bytes.NewBufferString(fmt.Sprintf(`{"error":"failed to encode JSON payload: %s"}`, err))
			o.contentType = "application/json"
			return
		}
		o.body = buf
		o.contentType = "application/json"
	}
}

// WithBody sets a caller-supplied request body and its Content-Type (e.g. a HAL+JSON
// payload). Pass a *bytes.Reader so http.NewRequestWithContext derives GetBody automatically,
// letting a whitelisted POST (e.g. RegisterSource) replay on retry.
func WithBody(r io.Reader, contentType string) RequestOption {
	return func(o *requestOptions) {
		o.body = r
		o.contentType = contentType
	}
}

// WithResponseSink marks the call as a streaming download: the 2xx response body is copied
// into w (capped at maxBytes) instead of being buffered and JSON-decoded.
func WithResponseSink(w io.Writer, maxBytes int64) RequestOption {
	return func(o *requestOptions) {
		o.sink = w
		o.sinkMaxBytes = maxBytes
	}
}

// WithNoRedirect opts this request out of the singleton's default redirect-following (see
// SharedHTTPClient's CheckRedirect). Use it where the request body carries a secret a 307/308
// would resend to the redirect target (e.g. a CI trigger POST) — a 3xx then comes back as a
// non-2xx HttpError instead of being followed.
func WithNoRedirect() RequestOption {
	return func(o *requestOptions) {
		o.noRedirect = true
	}
}

// WithStrictJSONSuccess guards against a 2xx response that isn't actually the expected JSON
// DTO: a 203 or an explicit text/html Content-Type is treated as an HttpError instead of being
// unmarshaled. This is the Azure DevOps quirk where an expired/invalid PAT answers with
// "203 Non-Authoritative" plus an HTML sign-in page rather than a clean 401. Deliberately
// narrow (203 or text/html only) so a JSON body served without an explicit application/json
// header — which Go's server sniffs to text/plain — still parses.
func WithStrictJSONSuccess() RequestOption {
	return func(o *requestOptions) {
		o.strictJSON = true
	}
}

// sharedHTTPClient is the process-wide http.Client used by every meshapi call site that
// does not inject its own (via a ...WithHTTP constructor, kept for tests), and reused as-is by
// the CI clients (gitlab/github/azdevops) via SharedHTTPClient so the whole binary funnels
// through one bound+retrying client. http.Client and RetryTransport are both safe for
// concurrent use, so one client is shared across every runner-type goroutine calling out.
// CheckRedirect follows by default and only stops at the sentinel WithNoRedirect stamps onto
// a single request's context (SentinelCheckRedirect), rather than switching that request off
// this shared client.
var sharedHTTPClient = &http.Client{
	Timeout:       5 * time.Minute,
	Transport:     newRetryTransport(nil, globalRetryOptions(), noopLogger{}),
	CheckRedirect: httpclient.SentinelCheckRedirect,
}

// SharedHTTPClient returns the process-wide *http.Client backing DoRequest/DoAuthorizedRequest
// for meshapi's own call sites, so CI clients (gitlab/github/azdevops) can pass the same
// instance into DoRequest/DoAuthorizedRequest instead of constructing their own — one bound,
// retrying, redirect-policy client for the whole binary.
func SharedHTTPClient() *http.Client {
	return sharedHTTPClient
}

// ConfigureRootCAs rebuilds sharedHTTPClient's transport to trust pool instead of the Go
// runtime default, keeping the same retry policy/logger as the package-var literal. A nil
// pool is a no-op, preserving today's http.DefaultTransport-backed trust store.
//
// Call this once, at process startup, before any goroutine issues a request through
// SharedHTTPClient() — it is not safe to call concurrently with in-flight requests.
func ConfigureRootCAs(pool *x509.CertPool) {
	if pool == nil {
		return
	}
	sharedHTTPClient.Transport = newRetryTransport(
		&http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}},
		globalRetryOptions(),
		noopLogger{},
	)
}

// DoRequest is the sole http.Client.Do site in meshapi. It builds the request from method,
// url and opts, executes it on httpClient, logs the wire request/response at Debug via log,
// and decodes a 2xx JSON body into R. A non-2xx response yields a zero R and an HttpError.
//
// If opts includes WithResponseSink, the response is instead streamed into the sink and R is
// always the zero value (callers passing DownloadArtifact-shaped R must ignore it).
func DoRequest[R any](ctx context.Context, httpClient *http.Client, log Logger, method, url string, opts ...RequestOption) (R, error) {
	var result R

	o := &requestOptions{}
	for _, opt := range opts {
		opt(o)
	}
	if o.noRedirect {
		ctx = httpclient.WithoutRedirects(ctx)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, o.body)
	if err != nil {
		return result, fmt.Errorf("failed to create request: %w", err)
	}
	if o.contentType != "" {
		req.Header.Set("Content-Type", o.contentType)
	}
	for k, vs := range o.header {
		for _, v := range vs {
			req.Header.Set(k, v)
		}
	}

	log.Debug(ctx, "meshapi request",
		"method", req.Method, "url", req.URL.String(),
		"headers", loggedHeaders(req.Header), "body", loggedBody(bufferedBodyBytes(o.body)))

	resp, err := httpClient.Do(req)
	if err != nil {
		return result, fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if o.sink != nil {
		return result, doStream(ctx, log, resp, o.sink, o.sinkMaxBytes)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return result, fmt.Errorf("failed to read response body: %w", err)
	}
	log.Debug(ctx, "meshapi response", "status", resp.StatusCode, "body", loggedBody(body))

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return result, HttpError{StatusCode: resp.StatusCode, ResponseBody: capBody(body, maxErrorBodyBytes)}
	}

	if o.strictJSON && (resp.StatusCode == http.StatusNonAuthoritativeInfo || strings.Contains(resp.Header.Get("Content-Type"), "text/html")) {
		return result, HttpError{StatusCode: resp.StatusCode, ResponseBody: capBody(body, maxErrorBodyBytes)}
	}

	// RegisterSource/Update legitimately answer 2xx with an empty body; return the zero
	// value rather than failing (unlike tf-provider, which treats this as an error).
	if len(body) == 0 {
		return result, nil
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return result, fmt.Errorf("failed to parse response JSON: %w", err)
	}
	return result, nil
}

// doStream handles the WithResponseSink path: metadata-only logging (never the body), and a
// capped copy into the sink on success.
func doStream(ctx context.Context, log Logger, resp *http.Response, sink io.Writer, maxBytes int64) error {
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
		log.Debug(ctx, "meshapi response", "status", resp.StatusCode, "body", loggedBody(respBody))
		return HttpError{StatusCode: resp.StatusCode, ResponseBody: respBody}
	}

	log.Debug(ctx, "meshapi response", "status", resp.StatusCode, "contentLength", resp.ContentLength)

	// Read one byte past the cap so an oversized artifact is rejected rather than silently
	// truncated.
	n, err := io.Copy(sink, io.LimitReader(resp.Body, maxBytes))
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}
	if n == maxBytes {
		return fmt.Errorf("exceeds the maximum allowed size of %d bytes", maxBytes-1)
	}
	return nil
}

// bufferedBodyBytes returns the bytes of a request body built by WithJSONPayload/WithBody
// for logging, without consuming a reader that hasn't been sent yet: only *bytes.Buffer and
// *bytes.Reader are peeked (both are what WithJSONPayload/WithBody callers pass), so a
// streaming body reader is never buffered into memory for a log line.
func bufferedBodyBytes(body io.Reader) []byte {
	switch b := body.(type) {
	case *bytes.Buffer:
		return b.Bytes()
	case *bytes.Reader:
		data := make([]byte, b.Len())
		_, _ = b.ReadAt(data, 0)
		return data
	default:
		return nil
	}
}

// capBody truncates b to at most maxLen bytes, so a huge error response cannot exhaust the
// runner's RAM.
func capBody(b []byte, maxLen int) []byte {
	if len(b) > maxLen {
		return b[:maxLen]
	}
	return b
}

// DoAuthorizedRequest resolves the Authorization header via auth, adds it as a request
// header, and delegates to DoRequest.
func DoAuthorizedRequest[R any](ctx context.Context, httpClient *http.Client, log Logger, auth Authorization, method, url string, opts ...RequestOption) (R, error) {
	var result R

	header, err := auth.Header(ctx)
	if err != nil {
		return result, fmt.Errorf("failed to resolve authorization: %w", err)
	}

	return DoRequest[R](ctx, httpClient, log, method, url, append(opts, WithHeader("Authorization", header))...)
}
