package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	meshapi "github.com/meshcloud/building-block-runner/go-meshapi-client/meshapi"
)

func TestApi_NewAuthProvider_ApiKeyWinsWhenComplete(t *testing.T) {
	a := Api{Url: "https://api.example", ClientId: "id", ClientSecret: "secret", Username: "u", Password: "p"}
	provider := a.NewAuthProvider("")
	require.NotNil(t, provider)
	_, ok := provider.(*meshapi.ApiKeyAuth)
	assert.True(t, ok, "expected ApiKeyAuth when clientId+clientSecret are both set, got %T", provider)
}

func TestApi_NewAuthProvider_BasicWhenApiKeyIncomplete(t *testing.T) {
	a := Api{Url: "https://api.example", ClientId: "id-only", Username: "u", Password: "p"}
	provider := a.NewAuthProvider("")
	require.NotNil(t, provider)
	assert.Equal(t, meshapi.BasicAuth{Username: "u", Password: "p"}, provider)
}

func TestApi_NewAuthProvider_UserAliasFeedsBasicAuth(t *testing.T) {
	a := Api{User: "legacy-user", Password: "p"}
	provider := a.NewAuthProvider("")
	assert.Equal(t, meshapi.BasicAuth{Username: "legacy-user", Password: "p"}, provider)
}

func TestApi_NewAuthProvider_NilWhenNothingConfigured(t *testing.T) {
	assert.Nil(t, Api{}.NewAuthProvider(""))
}

func TestApi_NewAuthProvider_FallbackURLUsedWhenUrlEmpty(t *testing.T) {
	a := Api{ClientId: "id", ClientSecret: "secret"}
	provider := a.NewAuthProvider("https://fallback.example")
	apiKeyAuth, ok := provider.(*meshapi.ApiKeyAuth)
	require.True(t, ok)
	// AuthHeader would dial fallback URL's /api/login; we only assert construction here
	// since ApiKeyAuth keeps baseURL unexported -- behavior is pinned by meshapi's own tests.
	assert.NotNil(t, apiKeyAuth)
}

func TestApi_Validate_CompleteBasicAuth_OK(t *testing.T) {
	a := Api{Username: "u", Password: "p"}
	assert.NoError(t, a.Validate("api", true))
}

func TestApi_Validate_CompleteApiKeyAuth_OK(t *testing.T) {
	a := Api{ClientId: "id", ClientSecret: "secret"}
	assert.NoError(t, a.Validate("api", true))
}

func TestApi_Validate_NotRequired_IncompleteIsOK(t *testing.T) {
	assert.NoError(t, Api{}.Validate("api", false))
}

func TestApi_Validate_RequiredAndEmpty_GenericMessage(t *testing.T) {
	err := Api{}.Validate("api", true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no authentication configured")
}

func TestApi_Validate_PartialApiKey_MissingSecret(t *testing.T) {
	err := Api{ClientId: "id"}.Validate("api", true)
	require.Error(t, err)
	assert.Equal(t, "api.clientSecret is required when using API key auth", err.Error())
}

func TestApi_Validate_PartialApiKey_MissingClientId(t *testing.T) {
	err := Api{ClientSecret: "secret"}.Validate("api", true)
	require.Error(t, err)
	assert.Equal(t, "api.clientId is required when using API key auth", err.Error())
}

func TestApi_Validate_PartialBasic_MissingPassword(t *testing.T) {
	err := Api{Username: "u"}.Validate("api", true)
	require.Error(t, err)
	assert.Equal(t, "api.password is required when using Basic auth", err.Error())
}

func TestApi_Validate_PartialBasic_MissingUsername(t *testing.T) {
	err := Api{Password: "p"}.Validate("api", true)
	require.Error(t, err)
	assert.Equal(t, "api.username is required when using Basic auth", err.Error())
}

func TestApi_Validate_UserAliasCountsAsUsername(t *testing.T) {
	assert.NoError(t, Api{User: "u", Password: "p"}.Validate("api", true))
}
