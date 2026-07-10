package meshapi

import (
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Backoff computes the wait before a retry attempt (1-indexed).
type Backoff interface {
	Wait(attempt int) time.Duration
}

// ExponentialBackoff waits MinWait*2^(attempt-1), capped at MaxWait (provider design).
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

// RetryOptions configures the retry transport. Shaped like the provider's RetryOptions;
// WhitelistedPosts lists path suffixes for the POSTs that are safe to replay (a POST is
// otherwise never retried — see the claim-POST exclusion, §5.2.3).
type RetryOptions struct {
	MaxRetries       int
	Backoff          Backoff
	WhitelistedPosts []string
}

// maxRetryAfter caps how long a Retry-After header can push a retry out, so a hostile or
// misconfigured server cannot park a runner indefinitely (provider cap).
const maxRetryAfter = 5 * time.Minute

func defaultBackoff() Backoff {
	return ExponentialBackoff{MinWait: 1 * time.Second, MaxWait: 8 * time.Second}
}

// defaultRunRetryOptions is the run-endpoint policy (§5.2.3): MaxRetries 4, 1–8s
// exponential (~15s total). Idempotent methods (GET/PUT/DELETE) always retry; of the
// POSTs only register-source (path suffix "/status/source") is whitelisted — replaying it
// is safe because a 409-on-replay is already treated as success. The claim POST
// (".../create") and the status PATCH are deliberately NOT retryable (PATCH by method,
// claim by omission from the whitelist) so an ambiguous failure never double-claims a run
// or delays abort detection.
func defaultRunRetryOptions() RetryOptions {
	return RetryOptions{
		MaxRetries:       4,
		Backoff:          defaultBackoff(),
		WhitelistedPosts: []string{"/status/source"},
	}
}

// defaultLoginRetryOptions is the ApiKeyAuth login-endpoint policy: the login POST
// (path "/api/login") is idempotent (it mints a token) and is whitelisted so a token
// refresh can ride out a 503 during a backend restart.
func defaultLoginRetryOptions() RetryOptions {
	return RetryOptions{
		MaxRetries:       4,
		Backoff:          defaultBackoff(),
		WhitelistedPosts: []string{"/api/login"},
	}
}

// retryTransport is an http.RoundTripper wrapper implementing the provider's retry design:
// idempotent-by-method + POST-whitelist, retryable on transport error / 429 / 502 / 503 /
// 504 (Retry-After honored, capped), exponential backoff, request-body replay via GetBody,
// and a drained-and-closed body on discarded attempts for connection reuse. Plain 500 is
// NOT retried (matches the provider), keeping every phase-1 non-5xx-retry pin's transcript
// byte-identical (STOP-D).
type retryTransport struct {
	base http.RoundTripper
	opts RetryOptions
	log  Logger
}

func newRetryTransport(base http.RoundTripper, opts RetryOptions, log Logger) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	if log == nil {
		log = noopLogger{}
	}
	return &retryTransport{base: base, opts: opts, log: log}
}

func (rt *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
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

		rt.log.Debug(ctx, "meshapi retrying request",
			"method", req.Method, "url", req.URL.String(),
			"attempt", attempt+1, "wait", wait.String())

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
	}
}

func (rt *retryTransport) shouldRetry(req *http.Request, resp *http.Response, err error) bool {
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

func (rt *retryTransport) methodRetryable(req *http.Request) bool {
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
		// PATCH (the status update) and everything else is never retried.
		return false
	}
}

func (rt *retryTransport) retryDelay(resp *http.Response, attempt int) time.Duration {
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
