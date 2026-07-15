package httpclient

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Logger is the minimal logging seam RetryTransport uses to record a retry attempt. Its
// shape (ctx-first, three levels) matches every per-package Logger seam in this repo (e.g.
// meshapi.Logger), so a caller's own logger value can be passed here directly — Go's
// structural interface satisfaction does the rest, no adapter needed.
type Logger interface {
	Debug(ctx context.Context, msg string, args ...any)
	Info(ctx context.Context, msg string, args ...any)
	Warn(ctx context.Context, msg string, args ...any)
}

// noopLogger discards everything; the default when no logger is supplied.
type noopLogger struct{}

func (noopLogger) Debug(context.Context, string, ...any) {}
func (noopLogger) Info(context.Context, string, ...any)  {}
func (noopLogger) Warn(context.Context, string, ...any)  {}

// Backoff computes the wait before a retry attempt (1-indexed).
type Backoff interface {
	Wait(attempt int) time.Duration
}

// ExponentialBackoff waits MinWait*2^(attempt-1), capped at MaxWait.
type ExponentialBackoff struct {
	MinWait time.Duration
	MaxWait time.Duration
}

func (b ExponentialBackoff) Wait(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	d := b.MinWait
	for i := 1; i < attempt; i++ {
		d *= 2
		if d >= b.MaxWait {
			return b.MaxWait
		}
	}
	if d > b.MaxWait {
		return b.MaxWait
	}
	return d
}

// RetryOptions configures a RetryTransport. WhitelistedPosts lists path suffixes for the
// POSTs that are safe to replay — a POST is otherwise never retried, since replaying an
// ambiguously-failed non-idempotent call could duplicate its effect (e.g. double-claim a
// resource); callers whitelist only the POSTs whose replay is provably safe (e.g. a 409-on-
// replay that is itself treated as success).
type RetryOptions struct {
	MaxRetries       int
	Backoff          Backoff
	WhitelistedPosts []string
}

// maxRetryAfter caps how long a Retry-After header can push a retry out, so a hostile or
// misconfigured server cannot park a caller indefinitely.
const maxRetryAfter = 5 * time.Minute

// RetryTransport is an http.RoundTripper wrapper: idempotent-by-method + POST-whitelist,
// retryable on transport error / 429 / 502 / 503 / 504 (Retry-After honored, capped),
// exponential backoff, request-body replay via GetBody, and a drained-and-closed body on
// discarded attempts for connection reuse. A plain 500 is NOT retried.
type RetryTransport struct {
	base http.RoundTripper
	opts RetryOptions
	log  Logger
}

// NewRetryTransport wraps base (http.DefaultTransport if nil) with retry/backoff per opts. A
// nil log discards retry-attempt log lines.
func NewRetryTransport(base http.RoundTripper, opts RetryOptions, log Logger) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	if log == nil {
		log = noopLogger{}
	}
	return &RetryTransport{base: base, opts: opts, log: log}
}

func (rt *RetryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx := req.Context()
	var (
		resp *http.Response
		err  error
	)
	for attempt := 0; ; attempt++ {
		if attempt > 0 && req.GetBody != nil {
			body, gerr := req.GetBody()
			if gerr != nil {
				return nil, gerr
			}
			req.Body = body
		}

		resp, err = rt.base.RoundTrip(req)

		if attempt >= rt.opts.MaxRetries || !rt.shouldRetry(req, resp, err) {
			return resp, err
		}

		wait := rt.retryDelay(resp, attempt+1)
		drainAndClose(resp)

		rt.log.Debug(ctx, "httpclient retrying request",
			"method", req.Method, "url", req.URL.String(),
			"attempt", attempt+1, "wait", wait.String())

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
	}
}

func (rt *RetryTransport) shouldRetry(req *http.Request, resp *http.Response, err error) bool {
	if !rt.methodRetryable(req) {
		return false
	}
	if err != nil {
		return true // transport error
	}
	switch resp.StatusCode {
	case http.StatusTooManyRequests, // 429
		http.StatusBadGateway,         // 502
		http.StatusServiceUnavailable, // 503
		http.StatusGatewayTimeout:     // 504
		return true
	}
	return false
}

func (rt *RetryTransport) methodRetryable(req *http.Request) bool {
	switch req.Method {
	case http.MethodGet, http.MethodHead, http.MethodPut, http.MethodDelete:
		return true
	case http.MethodPost:
		for _, suffix := range rt.opts.WhitelistedPosts {
			if strings.HasSuffix(req.URL.Path, suffix) {
				return true
			}
		}
		return false
	default:
		// PATCH and everything else is never retried.
		return false
	}
}

func (rt *RetryTransport) retryDelay(resp *http.Response, attempt int) time.Duration {
	if resp != nil && (resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable) {
		if d := parseRetryAfter(resp.Header.Get("Retry-After")); d > 0 {
			if d > maxRetryAfter {
				d = maxRetryAfter
			}
			return d
		}
	}
	return rt.opts.Backoff.Wait(attempt)
}

// parseRetryAfter parses a Retry-After header as either delta-seconds or an HTTP-date.
func parseRetryAfter(v string) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs < 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

// drainAndClose discards up to a bounded amount of a to-be-retried response body so the
// underlying connection can be reused, then closes it.
func drainAndClose(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	_ = resp.Body.Close()
}
