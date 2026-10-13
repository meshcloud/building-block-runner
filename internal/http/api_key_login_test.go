package http_test

import (
	"encoding/json/v2"
	"fmt"
	gohttp "net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/config/xurl"
	"github.com/meshcloud/building-block-runner/internal/http"
)

func Test_ApiKeyLogin_TokenReusedUntilRejected(t *testing.T) {
	logins := 0
	var authorizations []string
	mux := gohttp.NewServeMux()
	mux.HandleFunc("POST /api/login", func(w gohttp.ResponseWriter, r *gohttp.Request) {
		var login map[string]string
		assert.NoError(t, json.UnmarshalRead(r.Body, &login))
		assert.Equal(t, map[string]string{"clientId": "an-id", "clientSecret": "a-secret"}, login)
		logins++
		_, _ = fmt.Fprintf(w, `{"access_token":"token-%d","expires_in":300}`, logins)
	})
	mux.HandleFunc("GET /api/runs", func(w gohttp.ResponseWriter, r *gohttp.Request) {
		authorizations = append(authorizations, r.Header.Get("Authorization"))
		if len(authorizations) == 2 {
			w.WriteHeader(gohttp.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`"ok"`))
	})
	client, serverUrl := newTestClient(t, mux.ServeHTTP)
	authorized := client.WithAuthorization(http.NewApiKeyLogin(client, serverUrl, "an-id", "a-secret"))

	for range 3 {
		_, err := authorized.DoRequest[string](t.Context(), http.MethodGet, serverUrl.JoinPath("api/runs"))
		require.NoError(t, err)
	}
	assert.Equal(t, []string{"Bearer token-1", "Bearer token-1", "Bearer token-2", "Bearer token-2"}, authorizations)
	assert.Equal(t, 2, logins)
}

func Test_NewAuthorization_PrefersTokenOverKeyOverBasic(t *testing.T) {
	client := http.NewClient(http.UserAgent{GitHubRepo: "meshcloud/building-block-runner", Version: "test"})
	api := config.Api{Url: xurl.MustParse("https://meshstack.example.com")}
	api.Auth.Basic.Username, api.Auth.Basic.Password = "user", "pass"

	auth, err := http.NewAuthorization(client, api)
	require.NoError(t, err)
	assert.Equal(t, http.BasicAuth{Username: "user", Password: "pass"}, auth)

	api.Auth.Key.ClientId, api.Auth.Key.ClientSecret = "an-id", "a-secret"
	auth, err = http.NewAuthorization(client, api)
	require.NoError(t, err)
	assert.IsType(t, &http.ApiKeyLogin{}, auth)

	api.Auth.Token = "a-run-token"
	auth, err = http.NewAuthorization(client, api)
	require.NoError(t, err)
	assert.Equal(t, http.BearerToken("a-run-token"), auth)
}

func Test_NewAuthorization_ProfileUnsupported(t *testing.T) {
	client := http.NewClient(http.UserAgent{GitHubRepo: "meshcloud/building-block-runner", Version: "test"})
	api := config.Api{}
	api.Auth.Profile = "dev"

	_, err := http.NewAuthorization(client, api)
	assert.EqualError(t, err, "api.auth.profile is not supported yet")
}
