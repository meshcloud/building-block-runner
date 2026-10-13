package http

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sync"
	"time"
)

const tokenExpiryMargin = 30 * time.Second

// ApiKeyLogin logs in at /api/login again shortly before the token expires.
type ApiKeyLogin struct {
	client       Client
	loginUrl     *url.URL
	clientId     string
	clientSecret string

	mu        sync.Mutex
	token     BearerToken
	expiresAt time.Time
}

func NewApiKeyLogin(client Client, meshStackUrl *url.URL, clientId, clientSecret string) *ApiKeyLogin {
	return &ApiKeyLogin{
		client:       client,
		loginUrl:     meshStackUrl.JoinPath("api/login"),
		clientId:     clientId,
		clientSecret: clientSecret,
	}
}

func (l *ApiKeyLogin) GetCredential(ctx context.Context) (Credential, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.token != "" && time.Now().Before(l.expiresAt) {
		return l.token, nil
	}
	return l.login(ctx)
}

func (l *ApiKeyLogin) RefreshCredential(ctx context.Context, rejected Credential) (Credential, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.token != rejected && time.Now().Before(l.expiresAt) {
		return l.token, nil
	}
	return l.login(ctx)
}

type apiKeyLoginRequest struct {
	ClientId     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
}

type apiKeyLoginResponse struct {
	AccessToken BearerToken `json:"access_token"`
	ExpiresIn   int         `json:"expires_in"`
}

func (l *ApiKeyLogin) login(ctx context.Context) (Credential, error) {
	// A login invalidates no earlier token, so replaying it after a gateway error is harmless.
	response, err := l.client.DoRequest[apiKeyLoginResponse](ctx, MethodPost, l.loginUrl,
		WithJsonPayload(apiKeyLoginRequest{l.clientId, l.clientSecret}, "application/json"),
		Retryable(),
	)
	if err != nil {
		return nil, fmt.Errorf("cannot log in with API key %s: %w", l.clientId, err)
	}
	if response.AccessToken == "" {
		return nil, errors.New("the API key login answered without an access_token")
	}
	l.token = response.AccessToken
	l.expiresAt = time.Now().Add(max(time.Duration(response.ExpiresIn)*time.Second-tokenExpiryMargin, time.Second))
	return l.token, nil
}
