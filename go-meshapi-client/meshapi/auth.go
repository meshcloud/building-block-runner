package meshapi

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"
)

// AuthProvider supplies the Authorization header value for outgoing API requests.
type AuthProvider interface {
	AuthHeader() string
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

// defaultLoginTimeout is the HTTP client timeout used when no custom client is provided.
const defaultLoginTimeout = 30 * time.Second

// NewApiKeyAuth creates an ApiKeyAuth using the default http.Client.
func NewApiKeyAuth(baseURL, clientId, clientSecret string) *ApiKeyAuth {
	return NewApiKeyAuthWithClient(baseURL, clientId, clientSecret, &http.Client{Timeout: defaultLoginTimeout})
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
		log.Printf("[ApiKeyAuth] failed to fetch access token: %v", err)
		return ""
	}
	return "Bearer " + a.token
}

// fetchToken performs POST /api/login and updates the cached token.
// Must be called with a.mu held.
func (a *ApiKeyAuth) fetchToken() error {
	loginURL := fmt.Sprintf("%s/api/login", a.baseURL)

	body, err := json.Marshal(apiKeyLoginRequest{
		ClientId:     a.clientId,
		ClientSecret: a.clientSecret,
	})
	if err != nil {
		return fmt.Errorf("marshal login request: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, loginURL, bytes.NewBuffer(body))
	if err != nil {
		return fmt.Errorf("create login request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("execute login request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("login returned HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var loginResp apiKeyLoginResponse
	if err := json.NewDecoder(resp.Body).Decode(&loginResp); err != nil {
		return fmt.Errorf("decode login response: %w", err)
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
