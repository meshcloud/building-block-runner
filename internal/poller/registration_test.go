package poller

import (
	"crypto/rand"
	"crypto/rsa"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/meshcloud/meshstack-cli/client"
	"github.com/meshcloud/meshstack-cli/client/types/xurl"
	"github.com/meshcloud/meshstack-cli/pkg/setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/crypto"
	"github.com/meshcloud/building-block-runner/internal/testmeshstack"
)

func Test_Register_NeedsAWorkspaceAndAddsTheWorkloadIdentity(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	cfg := testConfig(1)
	cfg.Crypto = crypto.Crypto{PrivateKeys: crypto.PrivateKeys{{PrivateKey: key}}}
	cfg.Dispatcher.ForTypes = config.RunnerTypes{config.RunnerTypeAll}
	cfg.Dispatcher.Register = &config.Register{DisplayName: "hosted runner"}

	t.Run("fails without a workspace naming where to set it", func(t *testing.T) {
		testmeshstack.Isolate(t)
		t.Setenv(setting.ApiToken.EnvKey(), testmeshstack.Token)
		server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}))
		t.Cleanup(server.Close)
		cfg := cfg
		cfg.Api.Url = xurl.MustParsef("%s", server.URL)

		err := Register(t.Context(), cfg)

		assert.EqualError(t, err, "dispatcher.register needs a workspace: set api.auth.workspace, MESHSTACK_WORKSPACE or the default workspace of the profile")
	})

	t.Run("adds the workload identity of the dispatcher", func(t *testing.T) {
		wif := &client.MeshRunnerWorkloadIdentityFed{Issuer: new("https://issuer.example.com")}

		plain, err := newRegistration(cfg, "admin-customer", nil)
		require.NoError(t, err)
		withWif, err := newRegistration(cfg, "admin-customer", wif)
		require.NoError(t, err)

		assert.Nil(t, plain.Spec.WorkloadIdentityFederation)
		assert.Equal(t, wif, withWif.Spec.WorkloadIdentityFederation)
	})
}
