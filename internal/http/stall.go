package http

import (
	"context"
	"fmt"
	"io"
	gohttp "net/http"
	"time"
)

type stallGuard struct {
	Next    gohttp.RoundTripper
	Timeout time.Duration
}

func (g stallGuard) RoundTrip(req *gohttp.Request) (*gohttp.Response, error) {
	ctx, cancel := context.WithCancelCause(req.Context())
	resp, err := g.Next.RoundTrip(req.WithContext(ctx))
	if err != nil {
		cancel(nil)
		return resp, err
	}
	stalled := fmt.Errorf("response body stalled: nothing received for %s", g.Timeout)
	body := &stallGuardedBody{ReadCloser: resp.Body, timeout: g.Timeout, cancel: cancel}
	body.timer = time.AfterFunc(g.Timeout, func() { cancel(stalled) })
	resp.Body = body
	return resp, nil
}

type stallGuardedBody struct {
	io.ReadCloser

	timeout time.Duration
	timer   *time.Timer
	cancel  context.CancelCauseFunc
}

func (b *stallGuardedBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.timer.Reset(b.timeout)
	return n, err
}

func (b *stallGuardedBody) Close() error {
	b.timer.Stop()
	// Closed before cancelling, so a fully read connection returns to the pool.
	err := b.ReadCloser.Close()
	b.cancel(nil)
	return err
}
