// Package testmeshstack points the meshstack-cli client at a test server.
package testmeshstack

import (
	"testing"

	"github.com/meshcloud/meshstack-cli/client"
	"github.com/meshcloud/meshstack-cli/pkg/auth"
	"github.com/meshcloud/meshstack-cli/pkg/setting"
	"github.com/stretchr/testify/require"
)

// Token is a JWT without signature, as the client parses the token it authenticates with.
const Token = "e30.e30.test"

// Isolate keeps the profiles and the MESHSTACK_* environment of the developer out of the client.
func Isolate(t *testing.T) {
	t.Helper()
	t.Setenv("MESHSTACK_CONFIG_DIR", t.TempDir())
	t.Setenv(setting.SkipVersionCheck.EnvKey(), "true")
	for _, s := range []setting.Setting{
		setting.Endpoint, setting.Profile, setting.Workspace, setting.ApiToken,
		setting.ApiKeyClientId, setting.ApiKeyClientSecret, setting.ApiUsername, setting.ApiPassword,
	} {
		t.Setenv(s.EnvKey(), "")
	}
}

func Client(t *testing.T, endpoint string) client.Client {
	t.Helper()
	Isolate(t)
	t.Setenv(setting.Endpoint.EnvKey(), endpoint)
	t.Setenv(setting.ApiToken.EnvKey(), Token)
	c, err := auth.ResolveClient(t.Context(), auth.ResolveClientOptions{
		Version:    "test",
		GitHubRepo: "meshcloud/building-block-runner",
	})
	require.NoError(t, err)
	return c
}
