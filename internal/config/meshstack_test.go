package config

import (
	"testing"

	"github.com/meshcloud/meshstack-cli/pkg/setting"
	"github.com/stretchr/testify/assert"
)

func Test_MeshstackLookup_MapsTheMeshstackCliSettings(t *testing.T) {
	lookup := func(meshstack Meshstack, s setting.Setting) string {
		value, err := meshstack.Lookup(t.Context(), s.EnvKey())
		assert.NoError(t, err)
		return value
	}
	meshstack := newMeshstack("https://meshstack.example.com", "an-id", "a-secret")
	meshstack.Profile, meshstack.Workspace = "local-dev", "admin-customer"

	t.Run("maps the meshstack-cli settings", func(t *testing.T) {
		assert.Equal(t, "https://meshstack.example.com", lookup(meshstack, setting.Endpoint))
		assert.Equal(t, "local-dev", lookup(meshstack, setting.Profile))
		assert.Equal(t, "admin-customer", lookup(meshstack, setting.Workspace))
		assert.Equal(t, "an-id", lookup(meshstack, setting.ApiKeyClientId))
		assert.Equal(t, "a-secret", lookup(meshstack, setting.ApiKeyClientSecret))
		assert.Empty(t, lookup(meshstack, setting.ApiToken))
	})
}
