// Package testmeshstack isolates the meshstack-cli client in tests and fakes the status endpoints of a run.
package testmeshstack

import (
	"testing"

	"github.com/meshcloud/meshstack-cli/pkg/setting"
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
