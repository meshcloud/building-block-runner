package http_test

import (
	"context"
	"errors"
	gohttp "net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/internal/http"
)

func Test_Credentials_RenderAuthorizationHeader(t *testing.T) {
	assert.Equal(t, "Bearer a-token", http.BearerToken("a-token").AuthorizationHeader())
	assert.Equal(t, "Basic dXNlcjpwYXNz", http.BasicAuth{Username: "user", Password: "pass"}.AuthorizationHeader())
}

func Test_StaticCredentialRejected_NoRetry(t *testing.T) {
	for name, auth := range map[string]http.Authorization{
		"bearer": http.BearerToken("a-token"),
		"basic":  http.BasicAuth{Username: "user", Password: "pass"},
	} {
		t.Run(name, func(t *testing.T) {
			var received []string
			client, serverUrl := newTestClient(t, func(w gohttp.ResponseWriter, r *gohttp.Request) {
				received = append(received, r.Header.Get("Authorization"))
				w.WriteHeader(gohttp.StatusUnauthorized)
			})
			_, err := client.WithAuthorization(auth).DoRequest[any](t.Context(), http.MethodGet, serverUrl)
			httpErr, ok := errors.AsType[http.Error](err)
			require.True(t, ok, err)
			assert.True(t, httpErr.IsUnauthorized())
			assert.Len(t, received, 1)
		})
	}
}

func Test_RefreshedCredential_RetriedOnce(t *testing.T) {
	var received []string
	client, serverUrl := newTestClient(t, func(w gohttp.ResponseWriter, r *gohttp.Request) {
		received = append(received, r.Header.Get("Authorization"))
		if r.Header.Get("Authorization") != "Bearer fresh" {
			w.WriteHeader(gohttp.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`"ok"`))
	})
	auth := &refreshableAuthorization{credential: http.BearerToken("stale")}

	answer, err := client.WithAuthorization(auth).DoRequest[string](t.Context(), http.MethodGet, serverUrl)
	require.NoError(t, err)
	assert.Equal(t, "ok", answer)
	assert.Equal(t, []string{"Bearer stale", "Bearer fresh"}, received)
	assert.Equal(t, []http.Credential{http.BearerToken("stale")}, auth.rejected)
}

func Test_RefreshFails_BothErrorsReturned(t *testing.T) {
	client, serverUrl := newTestClient(t, func(w gohttp.ResponseWriter, _ *gohttp.Request) {
		w.WriteHeader(gohttp.StatusUnauthorized)
	})
	auth := &refreshableAuthorization{credential: http.BearerToken("stale"), refreshErr: errors.New("login refused")}

	_, err := client.WithAuthorization(auth).DoRequest[any](t.Context(), http.MethodGet, serverUrl)
	assert.ErrorContains(t, err, "http error 401")
	assert.ErrorContains(t, err, "login refused")
}

type refreshableAuthorization struct {
	credential http.Credential
	refreshErr error
	rejected   []http.Credential
}

func (a *refreshableAuthorization) GetCredential(context.Context) (http.Credential, error) {
	return a.credential, nil
}

func (a *refreshableAuthorization) RefreshCredential(_ context.Context, rejected http.Credential) (http.Credential, error) {
	a.rejected = append(a.rejected, rejected)
	if a.refreshErr != nil {
		return nil, a.refreshErr
	}
	a.credential = http.BearerToken("fresh")
	return a.credential, nil
}

func newTestClient(t *testing.T, handler gohttp.HandlerFunc) (http.Client, *url.URL) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	serverUrl, err := url.Parse(server.URL)
	require.NoError(t, err)
	userAgent := http.UserAgent{GitHubRepo: "meshcloud/building-block-runner", Version: "test"}
	return http.Client{Client: server.Client(), UserAgent: userAgent}, serverUrl
}
