package config

import (
	"testing"

	"github.com/meshcloud/meshstack-cli/pkg/setting"
	"github.com/stretchr/testify/assert"
)

func lookup(t *testing.T, api Api, s setting.Setting) string {
	t.Helper()
	value, err := api.Lookup(t.Context(), s.EnvKey())
	assert.NoError(t, err)
	return value
}

func Test_ApiLookup_MapsTheMeshstackCliSettings(t *testing.T) {
	api := newApi("https://meshstack.example.com", "", "")
	api.Auth.Profile, api.Auth.Workspace = "local-dev", "admin-customer"

	assert.Equal(t, "https://meshstack.example.com", lookup(t, api, setting.Endpoint))
	assert.Equal(t, "local-dev", lookup(t, api, setting.Profile))
	assert.Equal(t, "admin-customer", lookup(t, api, setting.Workspace))
	assert.Equal(t, "bb-api", lookup(t, api, setting.ApiUsername))
	assert.Equal(t, "guest", lookup(t, api, setting.ApiPassword))
	assert.Empty(t, lookup(t, api, setting.ApiToken))
	assert.Equal(t, "runner config api.auth.basic.username", api.Describe(setting.ApiUsername.EnvKey()))
	assert.Empty(t, api.Describe(setting.ApiToken.EnvKey()))
}

func Test_ApiLookupWithKeyAndBasicAuth_ReturnsOnlyTheKey(t *testing.T) {
	api := newApi("https://meshstack.example.com", "an-id", "a-secret")

	assert.Equal(t, "an-id", lookup(t, api, setting.ApiKeyClientId))
	assert.Equal(t, "a-secret", lookup(t, api, setting.ApiKeyClientSecret))
	assert.Empty(t, lookup(t, api, setting.ApiUsername))
	assert.Empty(t, lookup(t, api, setting.ApiPassword))
}
