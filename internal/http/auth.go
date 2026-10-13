package http

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
)

// Credential is comparable, so that a refresh can tell a new credential from the rejected one.
type Credential interface {
	AuthorizationHeader() string
}

// Authorization renews its credential before it expires, so that DoRequest refreshes only after a 401.
type Authorization interface {
	GetCredential(ctx context.Context) (Credential, error)

	// RefreshCredential returns the rejected credential unchanged when there is nothing new to try.
	RefreshCredential(ctx context.Context, rejected Credential) (Credential, error)
}

func (c Client) WithAuthorization(auth Authorization) AuthorizedClient {
	return AuthorizedClient{c, auth}
}

type AuthorizedClient struct {
	Client

	Authorization Authorization
}

func (c AuthorizedClient) DoRequest[R any](ctx context.Context, method string, url *url.URL, options ...RequestOption) (R, error) {
	return withCredential(ctx, c.Authorization, method, url, func(credential RequestOption) (R, error) {
		return c.Client.DoRequest[R](ctx, method, url, append(options, credential)...)
	})
}

func (c AuthorizedClient) DoStreamingRequest(ctx context.Context, method string, url *url.URL, w io.Writer, options ...RequestOption) error {
	_, err := withCredential(ctx, c.Authorization, method, url, func(credential RequestOption) (any, error) {
		return nil, c.Client.DoStreamingRequest(ctx, method, url, w, append(options, credential)...)
	})
	return err
}

func withCredential[R any](ctx context.Context, auth Authorization, method string, url *url.URL, send func(credential RequestOption) (R, error)) (result R, err error) {
	credential, credentialErr := auth.GetCredential(ctx)
	if credentialErr != nil {
		return result, credentialErr
	}
	result, err = send(asRequestOption(credential))

	if httpErr, ok := errors.AsType[Error](err); ok && httpErr.IsUnauthorized() {
		// Clock skew can make a credential look valid here and expired on the server.
		refreshed, refreshErr := auth.RefreshCredential(ctx, credential)
		switch {
		case refreshErr != nil:
			return result, errors.Join(err, fmt.Errorf("cannot renew the rejected credential: %w", refreshErr))
		case refreshed == credential:
			return result, err
		}
		slog.DebugContext(ctx, "retrying after 401 with a renewed credential", "url", url.String(), "method", method)
		return send(asRequestOption(refreshed))
	}
	return result, err
}

func asRequestOption(credential Credential) RequestOption {
	return withHeader("Authorization", credential.AuthorizationHeader())
}

type BearerToken string

func (token BearerToken) AuthorizationHeader() string {
	return "Bearer " + string(token)
}

func (token BearerToken) GetCredential(context.Context) (Credential, error) {
	return token, nil
}

func (token BearerToken) RefreshCredential(_ context.Context, rejected Credential) (Credential, error) {
	return rejected, nil
}

type BasicAuth struct {
	Username string
	Password string
}

func (auth BasicAuth) AuthorizationHeader() string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(auth.Username+":"+auth.Password))
}

func (auth BasicAuth) GetCredential(context.Context) (Credential, error) {
	return auth, nil
}

func (auth BasicAuth) RefreshCredential(_ context.Context, rejected Credential) (Credential, error) {
	return rejected, nil
}
