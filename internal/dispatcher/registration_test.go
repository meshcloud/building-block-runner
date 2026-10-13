package dispatcher

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json/v2"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/meshcloud/meshstack-cli/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/config/xurl"
	"github.com/meshcloud/building-block-runner/internal/crypto"
	"github.com/meshcloud/building-block-runner/internal/testmeshstack"
)

type fakeWifDispatcher struct {
	*fakeDispatcher
	wif *client.MeshRunnerWorkloadIdentityFed
}

func (d fakeWifDispatcher) WorkloadIdentity() *client.MeshRunnerWorkloadIdentityFed {
	return d.wif
}

func registrationConfig(t *testing.T) config.Config {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	cfg := testConfig(1)
	cfg.Crypto = crypto.Crypto{PrivateKeys: crypto.PrivateKeys{{PrivateKey: key}}}
	cfg.Api.Auth.Workspace = "admin-customer"
	cfg.Api.Auth.Basic = config.BasicAuth{Username: "user", Password: "password"}
	cfg.Dispatcher.ForTypes = config.RunnerTypes{config.RunnerTypeAll}
	cfg.Dispatcher.Register = &config.Register{DisplayName: "hosted runner"}
	return cfg
}

type caughtRegistration struct {
	method, path, authorization string
	runner                      client.MeshBuildingBlockRunner
}

func meshStackCatchingRegistration(t *testing.T) (*httptest.Server, *caughtRegistration) {
	t.Helper()
	var caught caughtRegistration
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		assert.NoError(t, err)
		caught = caughtRegistration{method: r.Method, path: r.URL.Path, authorization: r.Header.Get("Authorization")}
		assert.NoError(t, json.Unmarshal(body, &caught.runner))
		_, _ = w.Write(body)
	}))
	t.Cleanup(server.Close)
	return server, &caught
}

func Test_Register_UpdatesTheRunnerOwnedByTheWorkspaceOfTheConfig(t *testing.T) {
	testmeshstack.Isolate(t)
	server, caught := meshStackCatchingRegistration(t)
	cfg := registrationConfig(t)
	cfg.Api.Url = xurl.MustParse(server.URL)

	require.NoError(t, Register(t.Context(), cfg, log.New(io.Discard, "", 0)))

	assert.Equal(t, http.MethodPut, caught.method)
	assert.Equal(t, "/api/meshobjects/meshbuildingblockrunners/"+runnerUuid.String(), caught.path)
	assert.Equal(t, "Basic dXNlcjpwYXNzd29yZA==", caught.authorization)
	assert.Equal(t, "admin-customer", caught.runner.Metadata.OwnedByWorkspace)
	assert.Equal(t, "hosted runner", caught.runner.Spec.DisplayName)
	assert.Equal(t, "ALL", caught.runner.Spec.ImplementationType)
	assert.Contains(t, caught.runner.Spec.PublicKey, "-----BEGIN PUBLIC KEY-----")
}

func Test_RegisterWithoutWorkspace_FailsNamingWhereToSetIt(t *testing.T) {
	testmeshstack.Isolate(t)
	server, _ := meshStackCatchingRegistration(t)
	cfg := registrationConfig(t)
	cfg.Api.Url = xurl.MustParse(server.URL)
	cfg.Api.Auth.Workspace = ""

	err := Register(t.Context(), cfg, log.New(io.Discard, "", 0))

	assert.EqualError(t, err, "dispatcher.register needs a workspace: set api.auth.workspace, MESHSTACK_WORKSPACE or the default workspace of the profile")
}

func Test_NewRegistration_HasWorkloadIdentityOnlyFromAProvider(t *testing.T) {
	cfg := registrationConfig(t)
	wif := &client.MeshRunnerWorkloadIdentityFed{Issuer: new("https://issuer.example.com")}

	plain, err := newRegistration(cfg, "admin-customer", newFakeDispatcher())
	require.NoError(t, err)
	withWif, err := newRegistration(cfg, "admin-customer", fakeWifDispatcher{newFakeDispatcher(), wif})
	require.NoError(t, err)

	assert.Nil(t, plain.Spec.WorkloadIdentityFederation)
	assert.Equal(t, wif, withWif.Spec.WorkloadIdentityFederation)
}
