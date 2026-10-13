package http

import (
	"context"
	"errors"
	"fmt"
	"net"
	gohttp "net/http"
	"net/http/httptest"
	"sync/atomic"
	"syscall"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExponentialBackoff_Calculate(t *testing.T) {
	tests := []struct {
		attempt int
		want    time.Duration
	}{
		{1, 1 * time.Second},
		{2, 2 * time.Second},
		{3, 4 * time.Second},
		{4, 5 * time.Second},
		{5, 5 * time.Second},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("attempt %d", tt.attempt), func(t *testing.T) {
			b := ExponentialBackoff{
				MinWait: 1 * time.Second,
				MaxWait: 5 * time.Second,
			}
			assert.Equalf(t, tt.want, b.Calculate(tt.attempt), "Calculate(%v)", tt.attempt)
		})
	}
}

func TestRetryAfterBackoff(t *testing.T) {
	bubbleStart := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	fallback := ExponentialBackoff{MinWait: 1 * time.Second, MaxWait: 10 * time.Second}

	tests := []struct {
		name   string
		header string
		want   time.Duration
	}{
		{"delay-seconds", "30", 30 * time.Second},
		{"zero seconds", "0", 0},
		{"capped at 5 minutes", "600", 5 * time.Minute},
		{"empty header", "", 1 * time.Second},
		{"unparseable header", "not-a-number-or-date", 1 * time.Second},
		{"HTTP-date in the past", bubbleStart.Add(-10 * time.Second).Format(gohttp.TimeFormat), 1 * time.Second},
		{"HTTP-date in the future", bubbleStart.Add(45 * time.Second).Format(gohttp.TimeFormat), 45 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				b := retryAfterBackoff{
					Response: &gohttp.Response{Header: gohttp.Header{"Retry-After": {tt.header}}},
					Fallback: fallback,
				}
				assert.Equal(t, tt.want, b.Calculate(1))
			})
		})
	}
}

type roundTripperFunc func(*gohttp.Request) (*gohttp.Response, error)

func (f roundTripperFunc) RoundTrip(req *gohttp.Request) (*gohttp.Response, error) {
	return f(req)
}

func TestRetryStopsOnceTheContextIsDone(t *testing.T) {
	interrupted := errors.New("interrupt signal received")
	ctx, cancel := context.WithCancelCause(t.Context())
	cancel(interrupted)
	req, err := gohttp.NewRequestWithContext(ctx, MethodGet, "http://meshstack.invalid", nil)
	require.NoError(t, err)

	calls := 0
	retrying := &retryRoundTripper{
		Next: roundTripperFunc(func(req *gohttp.Request) (*gohttp.Response, error) {
			calls++
			return nil, context.Cause(req.Context())
		}),
		MaxRetries:          3,
		ShouldRetryRequest:  func(*gohttp.Request) bool { return true },
		ShouldRetryResponse: func(*gohttp.Response, error) RetryBackoff { return ExponentialBackoff{} },
	}

	_, err = retrying.RoundTrip(req) //nolint:bodyclose // no response comes back
	require.ErrorIs(t, err, interrupted)
	assert.Equal(t, 1, calls)
}

func TestRetryGivesUpOnAServerThatDoesNotAnswer(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(gohttp.HandlerFunc(func(_ gohttp.ResponseWriter, r *gohttp.Request) {
		calls.Add(1)
		<-r.Context().Done()
	}))
	defer server.Close()
	client := &gohttp.Client{Transport: newTransport(10 * time.Millisecond)}
	RetryOptions{MaxRetries: 3, Backoff: ExponentialBackoff{}}.ApplyTo(client)

	_, err := client.Get(server.URL) //nolint:bodyclose,noctx // no response comes back

	require.ErrorContains(t, err, "timeout awaiting response headers")
	assert.Equal(t, int32(1), calls.Load())
}

func TestRetryGivesUpOnARefusedConnection(t *testing.T) {
	server := httptest.NewServer(gohttp.NotFoundHandler())
	server.Close()
	var calls atomic.Int32
	client := &gohttp.Client{Transport: roundTripperFunc(func(req *gohttp.Request) (*gohttp.Response, error) {
		calls.Add(1)
		return gohttp.DefaultTransport.RoundTrip(req)
	})}
	RetryOptions{MaxRetries: 3, Backoff: ExponentialBackoff{}}.ApplyTo(client)

	_, err := client.Get(server.URL) //nolint:bodyclose,noctx // no response comes back

	require.ErrorIs(t, err, syscall.ECONNREFUSED)
	assert.Equal(t, int32(1), calls.Load())
}

func TestRetryRecoversFromABrokenConnection(t *testing.T) {
	for name, breakConnection := range map[string]func(*net.TCPConn){
		"closed": func(*net.TCPConn) {},
		"reset":  func(conn *net.TCPConn) { _ = conn.SetLinger(0) },
	} {
		t.Run(name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(gohttp.HandlerFunc(func(w gohttp.ResponseWriter, _ *gohttp.Request) {
				if calls.Add(1) > 1 {
					return
				}
				conn, _, err := gohttp.NewResponseController(w).Hijack()
				if !assert.NoError(t, err) {
					return
				}
				breakConnection(conn.(*net.TCPConn)) //nolint:forcetypeassert // httptest serves plain TCP
				_ = conn.Close()
			}))
			defer server.Close()
			client := &gohttp.Client{Transport: newTransport(time.Minute)}
			RetryOptions{MaxRetries: 3, Backoff: ExponentialBackoff{}}.ApplyTo(client)

			resp, err := client.Get(server.URL) //nolint:noctx // the test server answers at once

			require.NoError(t, err)
			_ = resp.Body.Close()
			assert.Equal(t, gohttp.StatusOK, resp.StatusCode)
			assert.Equal(t, int32(2), calls.Load())
		})
	}
}
