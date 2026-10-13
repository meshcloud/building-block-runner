package http

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	gohttp "net/http"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStallGuardEndsABodyThatStopsArriving(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client := newPipeClient(t, time.Minute, 100, func(w io.Writer) {
			_, _ = io.WriteString(w, "partial")
		})

		resp, err := client.Get("http://meshstack.invalid") //nolint:noctx // the stall guard is what ends it
		require.NoError(t, err)
		defer func() {
			_ = resp.Body.Close()
		}()
		start := time.Now()
		body, err := io.ReadAll(resp.Body)

		require.ErrorContains(t, err, "response body stalled: nothing received for 1m0s")
		assert.Equal(t, "partial", string(body))
		assert.Equal(t, time.Minute, time.Since(start), "the body was ended as soon as it had been silent for the timeout")
	})
}

func TestStallGuardReadsABodyThatKeepsArriving(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client := newPipeClient(t, time.Minute, 5, func(w io.Writer) {
			for range 5 {
				synctest.Sleep(40 * time.Second)
				_, _ = io.WriteString(w, ".")
			}
		})

		resp, err := client.Get("http://meshstack.invalid") //nolint:noctx // the server answers in full
		require.NoError(t, err)
		defer func() {
			_ = resp.Body.Close()
		}()
		start := time.Now()
		body, err := io.ReadAll(resp.Body)

		require.NoError(t, err)
		assert.Equal(t, ".....", string(body))
		assert.Greater(t, time.Since(start), time.Minute, "the body took longer than the timeout, but was never silent for as long")
	})
}

// newPipeClient stays in memory, because a socket keeps the synctest clock from advancing.
func newPipeClient(t *testing.T, timeout time.Duration, contentLength int, writeBody func(io.Writer)) *gohttp.Client {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	go func() {
		defer func() {
			_ = serverConn.Close()
		}()
		if _, err := gohttp.ReadRequest(bufio.NewReader(serverConn)); !assert.NoError(t, err) {
			return
		}
		_, _ = fmt.Fprintf(serverConn, "HTTP/1.1 200 OK\r\nContent-Length: %d\r\nConnection: close\r\n\r\n", contentLength)
		writeBody(serverConn)
		_, _ = io.Copy(io.Discard, serverConn)
	}()
	transport := &gohttp.Transport{DialContext: func(context.Context, string, string) (net.Conn, error) {
		return clientConn, nil
	}}
	return &gohttp.Client{Transport: stallGuard{Next: transport, Timeout: timeout}}
}
