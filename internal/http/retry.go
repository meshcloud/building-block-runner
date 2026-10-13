package http

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	gohttp "net/http"
	"strconv"
	"syscall"
	"time"
)

type RetryOptions struct {
	MaxRetries int
	Backoff    RetryBackoff
}

// ApplyTo retries a GET, and any other method only when its request is [Retryable].
func (options RetryOptions) ApplyTo(c *gohttp.Client) {
	next := gohttp.DefaultTransport
	if c.Transport != nil {
		next = c.Transport
	}
	c.Transport = &retryRoundTripper{
		Next:       next,
		MaxRetries: options.MaxRetries,
		ShouldRetryRequest: func(req *gohttp.Request) bool {
			if options.Backoff == nil {
				return false
			}
			return req.Method == MethodGet || isRetryable(req.Context())
		},
		ShouldRetryResponse: func(resp *gohttp.Response, err error) RetryBackoff {
			if err != nil {
				// An unknown host, a refused connection or a TLS error repeats on every attempt.
				if isBrokenConnection(err) {
					return options.Backoff
				}
				return nil
			}
			switch resp.StatusCode {
			case gohttp.StatusTooManyRequests, gohttp.StatusServiceUnavailable:
				return retryAfterBackoff{Response: resp, Fallback: options.Backoff}
			case gohttp.StatusBadGateway, gohttp.StatusGatewayTimeout:
				return options.Backoff
			default:
				return nil
			}
		},
	}
}

func isBrokenConnection(err error) bool {
	return errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
}

type RetryBackoff interface {
	Calculate(attempt int) time.Duration
}

type ExponentialBackoff struct {
	MinWait, MaxWait time.Duration
}

func (b ExponentialBackoff) Calculate(attempt int) time.Duration {
	nextWait := time.Duration(math.Pow(2, float64(attempt-1))) * b.MinWait
	if b.MaxWait > 0 && nextWait > b.MaxWait {
		return b.MaxWait
	}
	return nextWait
}

var timeNow = time.Now

type retryAfterBackoff struct {
	Response *gohttp.Response
	Fallback RetryBackoff
}

func (b retryAfterBackoff) Calculate(attempt int) (waitTime time.Duration) {
	defer func() {
		const maxRetryAfterWaitTime = 5 * time.Minute
		if waitTime < 0 {
			waitTime = b.Fallback.Calculate(attempt)
		} else if waitTime > maxRetryAfterWaitTime {
			waitTime = maxRetryAfterWaitTime
		}
	}()

	header := b.Response.Header.Get("Retry-After")
	if header == "" {
		return -1
	}

	if seconds, err := strconv.ParseInt(header, 10, 64); err == nil {
		return time.Duration(seconds) * time.Second
	}

	if date, err := gohttp.ParseTime(header); err == nil {
		return date.Sub(timeNow())
	}
	return -1
}

type retryRoundTripper struct {
	Next                gohttp.RoundTripper
	MaxRetries          int
	ShouldRetryRequest  func(req *gohttp.Request) bool
	ShouldRetryResponse func(resp *gohttp.Response, err error) RetryBackoff
}

func (r *retryRoundTripper) RoundTrip(req *gohttp.Request) (*gohttp.Response, error) {
	if !r.ShouldRetryRequest(req) {
		return r.Next.RoundTrip(req)
	}
	req = makeRequestBodyRetryable(req)
	for attempt := 1; ; attempt++ {
		resp, err := r.Next.RoundTrip(req)
		// The transport returns the context's cause, which need not wrap context.Canceled.
		if errors.Is(err, errRetryableBodyClose) || err != nil && req.Context().Err() != nil {
			return resp, err
		}
		backoff := r.ShouldRetryResponse(resp, err)
		if backoff == nil || attempt > r.MaxRetries {
			return resp, err
		}
		drainAndCloseResponseBody(req.Context(), resp)
		if req.GetBody != nil {
			body, bodyErr := req.GetBody()
			if bodyErr != nil {
				return nil, errors.Join(err, bodyErr)
			}
			req.Body = body
		}
		waitTime := backoff.Calculate(attempt)
		slog.WarnContext(req.Context(), "retrying request", append(
			func() []any {
				if err != nil {
					return []any{"error", err.Error()}
				}
				return []any{"status", resp.StatusCode}
			}(),
			"method", req.Method,
			"path", req.URL.Path,
			"attempt", fmt.Sprintf("%d/%d", attempt, r.MaxRetries),
			"waitTime", waitTime,
		)...)
		timer := time.NewTimer(waitTime)
		select {
		case <-req.Context().Done():
			timer.Stop()
			return nil, req.Context().Err()
		case <-timer.C:
		}
	}
}

func makeRequestBodyRetryable(req *gohttp.Request) *gohttp.Request {
	if req.Body == nil {
		return req
	}
	if req.GetBody != nil {
		return req
	}
	body := retryableBody{Closer: req.Body}
	body.Reader = io.TeeReader(req.Body, &body.Buffer)
	result := req.Clone(req.Context())
	result.Body = &body
	result.GetBody = nil
	return result
}

type retryableBody struct {
	io.Reader
	io.Closer

	Buffer appendWriter
}

var errRetryableBodyClose = errors.New("retryableBody failed to close")

func (b *retryableBody) Close() error {
	// The transport can stop reading early, and the retry replays all of Buffer.
	if _, err := io.Copy(io.Discard, b.Reader); err != nil {
		return errors.Join(err, errRetryableBodyClose)
	}
	if b.Closer != nil {
		if err := b.Closer.Close(); err != nil {
			return errors.Join(err, errRetryableBodyClose)
		}
	}
	b.Closer = nil
	b.Reader = bytes.NewReader(b.Buffer)
	return nil
}

type appendWriter []byte

func (w *appendWriter) Write(p []byte) (int, error) {
	*w = append(*w, p...)
	return len(p), nil
}

// drainAndCloseResponseBody reads at most maxBytes, so a large body cannot hold up the retry.
func drainAndCloseResponseBody(ctx context.Context, resp *gohttp.Response) {
	const maxBytes = 16 * 1024
	if resp != nil && resp.Body != nil {
		drainedBytes, err := io.CopyN(io.Discard, resp.Body, maxBytes)
		if err != nil && !errors.Is(err, io.EOF) {
			slog.DebugContext(ctx, "failed to drain response body: "+err.Error())
		}
		if err := resp.Body.Close(); err != nil {
			slog.DebugContext(ctx, fmt.Sprintf("failed to close response body after draining %d bytes: %s", drainedBytes, err.Error()))
		}
	}
}
