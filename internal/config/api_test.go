package config

import (
	"testing"

	"github.com/meshcloud/meshstack-cli/pkg/setting"
	"github.com/stretchr/testify/assert"
)

func Test_ApiLookup_MapsTheMeshstackCliSettings(t *testing.T) {
	lookup := func(api Api, s setting.Setting) string {
		value, err := api.Lookup(t.Context(), s.EnvKey())
		assert.NoError(t, err)
		return value
	}
	api := newApi("https://meshstack.example.com", "", "")
	api.Auth.Profile, api.Auth.Workspace = "local-dev", "admin-customer"

	t.Run("maps the meshstack-cli settings", func(t *testing.T) {
		assert.Equal(t, "https://meshstack.example.com", lookup(api, setting.Endpoint))
		assert.Equal(t, "local-dev", lookup(api, setting.Profile))
		assert.Equal(t, "admin-customer", lookup(api, setting.Workspace))
		assert.Equal(t, "bb-api", lookup(api, setting.ApiUsername))
		assert.Equal(t, "guest", lookup(api, setting.ApiPassword))
		assert.Empty(t, lookup(api, setting.ApiToken))
	})

	t.Run("an API key hides basic auth", func(t *testing.T) {
		api.Auth.Key = ApiKey{ClientId: "an-id", ClientSecret: "a-secret"}
		assert.Equal(t, "an-id", lookup(api, setting.ApiKeyClientId))
		assert.Equal(t, "a-secret", lookup(api, setting.ApiKeyClientSecret))
		assert.Empty(t, lookup(api, setting.ApiUsername))
		assert.Empty(t, lookup(api, setting.ApiPassword))
	})
}
