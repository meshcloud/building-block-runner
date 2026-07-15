package meshapi

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// AuthProvider supplies the Authorization header value for outgoing API requests.
type AuthProvider interface {
	AuthHeader() string
}

// Authorization is the DoAuthorizedRequest seam: it resolves the Authorization header
// value, fetching/refreshing it as needed. Modeled on the terraform-provider-meshstack
// client's Authorization interface for the future shared-SDK merge.
type Authorization interface {
	Header(ctx context.Context) (string, error)
}

// legacyAuthProvider adapts an AuthProvider that does not itself implement Authorization
// (e.g. internal/tf's runApiAuth, which only implements AuthHeader) to the Authorization
// seam, without changing the AuthProvider interface or any constructor signature.
type legacyAuthProvider struct{ AuthProvider }

func (l legacyAuthProvider) Header(context.Context) (string, error) {
	return l.AuthHeader(), nil
}

// authorizationOf returns ap as an Authorization, wrapping it in legacyAuthProvider if it
// does not already implement the interface.
func authorizationOf(ap AuthProvider) Authorization {
	if a, ok := ap.(Authorization); ok {
		return a
	}
	return legacyAuthProvider{ap}
}

// BasicAuth implements AuthProvider using HTTP Basic authentication.
type BasicAuth struct {
	Username string
	Password string
}

func (a BasicAuth) AuthHeader() string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(a.Username+":"+a.Password))
}

// BearerTokenAuth implements AuthProvider using a Bearer token.
type BearerTokenAuth struct {
	Token string
}

func (a BearerTokenAuth) AuthHeader() string {
	return "Bearer " + a.Token
}

// apiKeyLoginRequest is the JSON body for POST /api/login.
type apiKeyLoginRequest struct {
	ClientId     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
}

// apiKeyLoginResponse is the JSON response from POST /api/login.
type apiKeyLoginResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
}

// tokenExpiryBuffer is subtracted from expires_in to avoid using tokens right at their limit.
const tokenExpiryBuffer = 30 * time.Second

// ApiKeyAuth implements AuthProvider using the meshStack API key login flow.
// It fetches a short-lived Bearer token on demand and caches it until it nears expiry.
// Safe for concurrent use.
type ApiKeyAuth struct {
	baseURL      string
	clientId     string
	clientSecret string
	httpClient   *http.Client

	mu        sync.Mutex
	token     string
	expiresAt time.Time
}

// NewApiKeyAuth creates an ApiKeyAuth over the process-wide sharedHTTPClient (retry, incl.
// the login-POST whitelist, is a transport-level concern of that singleton).
func NewApiKeyAuth(baseURL, clientId, clientSecret string) *ApiKeyAuth {
	return NewApiKeyAuthWithClient(baseURL, clientId, clientSecret, sharedHTTPClient)
}

// NewApiKeyAuthWithClient creates an ApiKeyAuth with a custom http.Client (useful for testing).
func NewApiKeyAuthWithClient(baseURL, clientId, clientSecret string, httpClient *http.Client) *ApiKeyAuth {
	return &ApiKeyAuth{
		baseURL:      baseURL,
		clientId:     clientId,
		clientSecret: clientSecret,
		httpClient:   httpClient,
	}
}

// Header implements Authorization. It reuses AuthHeader's fetch/refresh/cache/swallow-to-
// empty logic verbatim, so behavior stays byte-identical; the error return exists for the
// T2b github re-mint and is always nil here.
func (a *ApiKeyAuth) Header(context.Context) (string, error) {
	return a.AuthHeader(), nil
}

// AuthHeader returns the Authorization header value.
// It fetches a fresh token if the cached one is absent or nearing expiry.
// Returns an empty string if the token cannot be fetched (the error is logged).
func (a *ApiKeyAuth) AuthHeader() string {
	a.mu.Lock()
	defer a.mu.Unlock()

	if time.Now().Before(a.expiresAt) && a.token != "" {
		return "Bearer " + a.token
	}

	if err := a.fetchToken(); err != nil {
		// Deep transport-level path with no injected logger (mirrors runapi.go's slog.Default
		// use): the failure is surfaced as an empty header the caller treats as unauthorized.
		slog.Error("failed to fetch access token", "component", "ApiKeyAuth", "error", err)
		return ""
	}
	return "Bearer " + a.token
}

// fetchToken performs POST /api/login and updates the cached token.
// Must be called with a.mu held.
func (a *ApiKeyAuth) fetchToken() error {
	ctx := context.Background()
	loginURL := fmt.Sprintf("%s/api/login", a.baseURL)

	loginResp, err := DoRequest[apiKeyLoginResponse](ctx, a.httpClient, noopLogger{}, http.MethodPost, loginURL,
		WithJSONPayload(apiKeyLoginRequest{ClientId: a.clientId, ClientSecret: a.clientSecret}),
		WithHeader("Accept", "application/json"))
	if err != nil {
		return err
	}
	if loginResp.AccessToken == "" {
		return fmt.Errorf("login response contained empty access_token")
	}

	expiry := time.Duration(loginResp.ExpiresIn)*time.Second - tokenExpiryBuffer
	if expiry <= 0 {
		expiry = time.Second
	}
	a.token = loginResp.AccessToken
	a.expiresAt = time.Now().Add(expiry)
	return nil
}
