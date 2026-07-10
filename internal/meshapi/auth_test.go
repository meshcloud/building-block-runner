package meshapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// loginHandler returns a handler that responds with an API key login response.
func loginHandler(t *testing.T, token string, expiresIn int) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/login", r.URL.Path)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		var req apiKeyLoginRequest
		// assert, not require: this handler runs on the httptest server's own goroutine,
		// where require's t.FailNow would only end that goroutine, not the test.
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		assert.Equal(t, "test-client-id", req.ClientId)
		assert.Equal(t, "test-client-secret", req.ClientSecret)

		w.Header().Set("Content-Type", "application/json")
		assert.NoError(t, json.NewEncoder(w).Encode(apiKeyLoginResponse{
			AccessToken: token,
			ExpiresIn:   expiresIn,
		}))
	}
}

func TestApiKeyAuth_FetchesTokenOnFirstCall(t *testing.T) {
	srv := httptest.NewServer(loginHandler(t, "my-access-token", 3600))
	defer srv.Close()

	auth := NewApiKeyAuthWithClient(srv.URL, "test-client-id", "test-client-secret", srv.Client())

	header := auth.AuthHeader()

	assert.Equal(t, "Bearer my-access-token", header)
}

func TestApiKeyAuth_CachesToken(t *testing.T) {
	var callCount atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		assert.NoError(t, json.NewEncoder(w).Encode(apiKeyLoginResponse{
			AccessToken: "cached-token",
			ExpiresIn:   3600,
		}))
	}))
	defer srv.Close()

	auth := NewApiKeyAuthWithClient(srv.URL, "test-client-id", "test-client-secret", srv.Client())

	first := auth.AuthHeader()
	second := auth.AuthHeader()
	third := auth.AuthHeader()

	assert.Equal(t, "Bearer cached-token", first)
	assert.Equal(t, "Bearer cached-token", second)
	assert.Equal(t, "Bearer cached-token", third)
	assert.Equal(t, int32(1), callCount.Load(), "login endpoint should only be called once")
}

func TestApiKeyAuth_RefreshesExpiredToken(t *testing.T) {
	var callCount atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := callCount.Add(1)
		token := "token-v1"
		if n > 1 {
			token = "token-v2"
		}
		w.Header().Set("Content-Type", "application/json")
		// expires_in=1 minus the 30s buffer would be negative, but our code clamps to 1s minimum.
		// Use 31s so expiry = 31 - 30 = 1s, which we can wait out in the test.
		expiresIn := 31
		assert.NoError(t, json.NewEncoder(w).Encode(apiKeyLoginResponse{
			AccessToken: token,
			ExpiresIn:   expiresIn,
		}))
	}))
	defer srv.Close()

	auth := NewApiKeyAuthWithClient(srv.URL, "test-client-id", "test-client-secret", srv.Client())

	first := auth.AuthHeader()
	assert.Equal(t, "Bearer token-v1", first)
	assert.Equal(t, int32(1), callCount.Load())

	// Force expiry by reaching past expiresAt directly (1s window).
	time.Sleep(1100 * time.Millisecond)

	second := auth.AuthHeader()
	assert.Equal(t, "Bearer token-v2", second)
	assert.Equal(t, int32(2), callCount.Load(), "login endpoint should be called again after expiry")
}

func TestApiKeyAuth_ReturnsEmptyOnLoginFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	auth := NewApiKeyAuthWithClient(srv.URL, "test-client-id", "test-client-secret", srv.Client())

	header := auth.AuthHeader()

	assert.Empty(t, header, "should return empty string when login fails")
}

func TestApiKeyAuth_ReturnsEmptyOnNonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer srv.Close()

	auth := NewApiKeyAuthWithClient(srv.URL, "bad-id", "bad-secret", srv.Client())

	header := auth.AuthHeader()

	assert.Empty(t, header, "should return empty string on 401")
}

func TestBasicAuth_Header(t *testing.T) {
	auth := BasicAuth{Username: "user", Password: "pass"}
	// "user:pass" base64 = "dXNlcjpwYXNz"
	assert.Equal(t, "Basic dXNlcjpwYXNz", auth.AuthHeader())
}

func TestBearerTokenAuth_Header(t *testing.T) {
	auth := BearerTokenAuth{Token: "my-token"}
	assert.Equal(t, "Bearer my-token", auth.AuthHeader())
}
