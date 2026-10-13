package dispatcher

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/config/xurl"
	"github.com/meshcloud/building-block-runner/internal/http"
)

var testUserAgent = http.UserAgent{GitHubRepo: "meshcloud/building-block-runner", Version: "test"}

func Test_NewAuthorization_PrefersKeyOverBasic(t *testing.T) {
	client := http.NewClient(testUserAgent)
	api := config.Api{Url: xurl.MustParse("https://meshstack.example.com")}
	api.Auth.Basic.Username, api.Auth.Basic.Password = "user", "pass"

	auth, err := newAuthorization(client, api)
	require.NoError(t, err)
	assert.Equal(t, http.BasicAuth{Username: "user", Password: "pass"}, auth)

	api.Auth.Key.ClientId, api.Auth.Key.ClientSecret = "an-id", "a-secret"
	auth, err = newAuthorization(client, api)
	require.NoError(t, err)
	assert.IsType(t, &http.ApiKeyLogin{}, auth)
}

func Test_NewAuthorization_ProfileUnsupported(t *testing.T) {
	client := http.NewClient(testUserAgent)
	api := config.Api{}
	api.Auth.Profile = "dev"

	_, err := newAuthorization(client, api)
	assert.EqualError(t, err, "api.auth.profile is not supported yet")
}
