package httpclient

import (
	"context"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNoRedirectClient_DoesNotFollowRedirects(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer srv.Close()

	c := NoRedirectClient(0)
	resp, err := c.Get(srv.URL)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusFound, resp.StatusCode, "redirect must be surfaced, not followed")
	assert.Equal(t, target.URL, resp.Header.Get("Location"))
}

func TestNoRedirectClient_ZeroTimeoutLeavesClientUnbounded(t *testing.T) {
	c := NoRedirectClient(0)
	assert.Equal(t, time.Duration(0), c.Timeout)
}

func TestNoRedirectClient_AppliesTimeout(t *testing.T) {
	c := NoRedirectClient(5 * time.Second)
	assert.Equal(t, 5*time.Second, c.Timeout)
}

func TestSentinelCheckRedirect_SentinelSet_StopsAtLastResponse(t *testing.T) {
	req, err := http.NewRequestWithContext(WithoutRedirects(context.Background()), http.MethodGet, "http://example.invalid", nil)
	require.NoError(t, err)

	err = SentinelCheckRedirect(req, nil)

	assert.ErrorIs(t, err, http.ErrUseLastResponse)
}

func TestNoRedirectClient_WithRootCAs_TrustsProvidedPool(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())

	trusting := NoRedirectClient(0, WithRootCAs(pool))
	resp, err := trusting.Get(srv.URL)
	require.NoError(t, err, "client configured with the server's cert must trust it")
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	untrusting := NoRedirectClient(0)
	_, err = untrusting.Get(srv.URL)
	assert.Error(t, err, "client without the pool must fail TLS verification against the test server")
}

func TestNoRedirectClient_WithRootCAs_NilPoolIsNoop(t *testing.T) {
	c := NoRedirectClient(0, WithRootCAs(nil))
	assert.Nil(t, c.Transport, "nil pool must leave Transport unset")
}

func TestSentinelCheckRedirect_SentinelUnset_Follows(t *testing.T) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.invalid", nil)
	require.NoError(t, err)

	err = SentinelCheckRedirect(req, nil)

	assert.NoError(t, err, "no sentinel on the request's context must follow the redirect")
}
