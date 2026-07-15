package httpclient

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// scriptedTransport answers each RoundTrip from a script of (status, err) steps, counting
// attempts. When the script is exhausted it repeats its last step, so a "never retried"
// path that makes a single call still gets a deterministic answer.
type scriptedTransport struct {
	steps  []scriptStep
	calls  atomic.Int32
	bodies []string // captured request bodies per attempt
}

type scriptStep struct {
	status int
	err    error
	header http.Header
}

func (t *scriptedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	i := int(t.calls.Add(1)) - 1
	if req.Body != nil {
		b, _ := io.ReadAll(req.Body)
		t.bodies = append(t.bodies, string(b))
	} else {
		t.bodies = append(t.bodies, "")
	}
	step := t.steps[len(t.steps)-1]
	if i < len(t.steps) {
		step = t.steps[i]
	}
	if step.err != nil {
		return nil, step.err
	}
	h := step.header
	if h == nil {
		h = make(http.Header)
	}
	return &http.Response{
		StatusCode: step.status,
		Body:       io.NopCloser(strings.NewReader("body")),
		Header:     h,
	}, nil
}

// runRetryOptions mirrors the shape of meshapi's run-endpoint policy (MaxRetries 4, 1-8s
// backoff, register-source POST whitelisted) so the generic mechanics are exercised with a
// realistic-looking policy without meshapi's package living here.
func runRetryOptions() RetryOptions {
	return RetryOptions{
		MaxRetries:       4,
		Backoff:          ExponentialBackoff{MinWait: time.Millisecond, MaxWait: 2 * time.Millisecond},
		WhitelistedPosts: []string{"/status/source"},
	}
}

func fastRetry(opts RetryOptions) *RetryTransport {
	opts.Backoff = ExponentialBackoff{MinWait: time.Millisecond, MaxWait: 2 * time.Millisecond}
	return &RetryTransport{base: nil, opts: opts, log: noopLogger{}}
}

func doVia(t *testing.T, rt *RetryTransport, base http.RoundTripper, method, url string, body string) (*http.Response, error) {
	t.Helper()
	rt.base = base
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, r)
	require.NoError(t, err)
	return rt.RoundTrip(req)
}

func TestRetry_GetRetriesUntilSuccess(t *testing.T) {
	base := &scriptedTransport{steps: []scriptStep{
		{status: 503}, {status: 503}, {status: 200},
	}}
	rt := fastRetry(runRetryOptions())

	resp, err := doVia(t, rt, base, http.MethodGet, "http://x/artifact", "")

	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, int32(3), base.calls.Load(), "GET should retry 503 twice then succeed")
}

func TestRetry_RespectsMaxRetries(t *testing.T) {
	base := &scriptedTransport{steps: []scriptStep{{status: 503}}}
	rt := fastRetry(RetryOptions{MaxRetries: 2})

	resp, err := doVia(t, rt, base, http.MethodGet, "http://x/artifact", "")

	require.NoError(t, err)
	assert.Equal(t, 503, resp.StatusCode)
	assert.Equal(t, int32(3), base.calls.Load(), "1 initial + MaxRetries(2) = 3 attempts, then give up")
}

func TestRetry_Plain500NotRetried(t *testing.T) {
	base := &scriptedTransport{steps: []scriptStep{{status: 500}}}
	rt := fastRetry(runRetryOptions())

	resp, err := doVia(t, rt, base, http.MethodGet, "http://x/artifact", "")

	require.NoError(t, err)
	assert.Equal(t, 500, resp.StatusCode)
	assert.Equal(t, int32(1), base.calls.Load(), "plain 500 must not be retried")
}

func TestRetry_UnwhitelistedPostNeverRetried(t *testing.T) {
	base := &scriptedTransport{steps: []scriptStep{{status: 503}}}
	rt := fastRetry(runRetryOptions())

	// Not in the whitelist (only "/status/source" is), so it must not be retried — e.g. a
	// claim-style POST that would double-claim a resource if replayed.
	_, err := doVia(t, rt, base, http.MethodPost, "http://x/api/meshobjects/meshbuildingblockruns/create?forRunnerUuid=u", "")

	require.NoError(t, err)
	assert.Equal(t, int32(1), base.calls.Load(), "unwhitelisted POST must never be retried")
}

func TestRetry_PatchNeverRetried(t *testing.T) {
	base := &scriptedTransport{steps: []scriptStep{{status: 503}}}
	rt := fastRetry(runRetryOptions())

	_, err := doVia(t, rt, base, http.MethodPatch, "http://x/api/meshobjects/meshbuildingblockruns/r/status/source/s", "{}")

	require.NoError(t, err)
	assert.Equal(t, int32(1), base.calls.Load(), "PATCH must never be retried")
}

func TestRetry_WhitelistedPostRetriedWithBodyReplay(t *testing.T) {
	base := &scriptedTransport{steps: []scriptStep{{status: 503}, {status: 200}}}
	rt := fastRetry(runRetryOptions())

	_, err := doVia(t, rt, base, http.MethodPost, "http://x/api/meshobjects/meshbuildingblockruns/r/status/source", `{"reg":true}`)

	require.NoError(t, err)
	assert.Equal(t, int32(2), base.calls.Load(), "whitelisted POST path suffix retries")
	require.Len(t, base.bodies, 2)
	assert.Equal(t, `{"reg":true}`, base.bodies[0])
	assert.Equal(t, `{"reg":true}`, base.bodies[1], "request body must be replayed on retry via GetBody")
}

func TestRetry_TransportErrorRetriedOnIdempotent(t *testing.T) {
	boom := errors.New("connection reset by peer")
	base := &scriptedTransport{steps: []scriptStep{{err: boom}, {status: 200}}}
	rt := fastRetry(runRetryOptions())

	resp, err := doVia(t, rt, base, http.MethodGet, "http://x/artifact", "")

	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, int32(2), base.calls.Load(), "transport error on a GET is retried")
}

func TestRetry_TransportErrorSurfacesAfterBudget(t *testing.T) {
	boom := errors.New("connection reset by peer")
	base := &scriptedTransport{steps: []scriptStep{{err: boom}}}
	rt := fastRetry(RetryOptions{MaxRetries: 2})

	_, err := doVia(t, rt, base, http.MethodGet, "http://x/artifact", "")

	require.Error(t, err)
	require.ErrorIs(t, err, boom)
	assert.Equal(t, int32(3), base.calls.Load())
}

func TestRetry_HonorsRetryAfterCappedAt5Min(t *testing.T) {
	// A 503 with an absurd Retry-After must be capped at maxRetryAfter, not obeyed literally.
	rt := &RetryTransport{opts: runRetryOptions(), log: noopLogger{}}
	resp := &http.Response{StatusCode: 503, Header: http.Header{"Retry-After": {"99999"}}}
	assert.Equal(t, maxRetryAfter, rt.retryDelay(resp, 1))

	resp2 := &http.Response{StatusCode: 503, Header: http.Header{"Retry-After": {"2"}}}
	assert.Equal(t, 2*time.Second, rt.retryDelay(resp2, 1))
}

func TestParseRetryAfter(t *testing.T) {
	assert.Equal(t, 5*time.Second, parseRetryAfter("5"))
	assert.Equal(t, time.Duration(0), parseRetryAfter(""))
	assert.Equal(t, time.Duration(0), parseRetryAfter("-1"))
	assert.Equal(t, time.Duration(0), parseRetryAfter("garbage"))
	// HTTP-date in the past yields 0 (don't wait).
	assert.Equal(t, time.Duration(0), parseRetryAfter("Mon, 02 Jan 2006 15:04:05 GMT"))
	// HTTP-date in the future yields a positive wait.
	future := time.Now().Add(30 * time.Second).UTC().Format(http.TimeFormat)
	assert.Greater(t, parseRetryAfter(future), time.Duration(0))
}

func TestExponentialBackoff(t *testing.T) {
	b := ExponentialBackoff{MinWait: time.Second, MaxWait: 8 * time.Second}
	assert.Equal(t, 1*time.Second, b.Wait(1))
	assert.Equal(t, 2*time.Second, b.Wait(2))
	assert.Equal(t, 4*time.Second, b.Wait(3))
	assert.Equal(t, 8*time.Second, b.Wait(4))
	assert.Equal(t, 8*time.Second, b.Wait(5), "capped at MaxWait")
	assert.Equal(t, 1*time.Second, b.Wait(0), "attempt<1 clamps to 1")
}

func TestRetry_ContextCancellationStopsRetries(t *testing.T) {
	base := &scriptedTransport{steps: []scriptStep{{status: 503}}}
	rt := &RetryTransport{base: base, opts: RetryOptions{MaxRetries: 5, Backoff: ExponentialBackoff{MinWait: time.Hour, MaxWait: time.Hour}}, log: noopLogger{}}

	req, err := http.NewRequest(http.MethodGet, "http://x/artifact", nil)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled: the first backoff select must return ctx.Err()
	req = req.WithContext(ctx)

	_, err = rt.RoundTrip(req)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestNewRetryTransport_DefaultsBaseAndLogger(t *testing.T) {
	rt := NewRetryTransport(nil, RetryOptions{}, nil)
	concrete, ok := rt.(*RetryTransport)
	require.True(t, ok)
	assert.Equal(t, http.DefaultTransport, concrete.base)
	assert.NotNil(t, concrete.log)
}

func TestNoopLogger(t *testing.T) {
	// Must not panic; that's the whole contract.
	n := noopLogger{}
	n.Debug(context.Background(), "d")
	n.Info(context.Background(), "i")
	n.Warn(context.Background(), "w")
}
