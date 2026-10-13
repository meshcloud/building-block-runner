package http_test

import (
	"encoding/json/v2"
	"fmt"
	gohttp "net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
