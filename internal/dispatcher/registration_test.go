package dispatcher

import (
	"crypto/rand"
	"crypto/rsa"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/crypto"
	"github.com/meshcloud/building-block-runner/internal/http/meshapi"
)

type fakeWifDispatcher struct {
	*fakeDispatcher
	wif *meshapi.MeshRunnerRegistrationWif
}

func (d fakeWifDispatcher) WorkloadIdentity() *meshapi.MeshRunnerRegistrationWif {
	return d.wif
}

func registrationConfig(t *testing.T) config.Config {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	cfg := testConfig(1)
	cfg.Crypto = crypto.Crypto{PrivateKeys: crypto.PrivateKeys{{PrivateKey: key}}}
	cfg.Api.Auth.Workspace = "admin-customer"
	cfg.Dispatcher.ForTypes = config.RunnerTypes{config.RunnerTypeAll}
	cfg.Dispatcher.Register = &config.Register{DisplayName: "hosted runner"}
	return cfg
}

func Test_NewRegistration_HasWorkloadIdentityOnlyFromAProvider(t *testing.T) {
	cfg := registrationConfig(t)
	wif := &meshapi.MeshRunnerRegistrationWif{Issuer: "https://issuer.example.com"}

	plain, err := newRegistration(cfg, newFakeDispatcher())
	require.NoError(t, err)
	withWif, err := newRegistration(cfg, fakeWifDispatcher{newFakeDispatcher(), wif})
	require.NoError(t, err)

	assert.Nil(t, plain.Spec.WorkloadIdentityFederation)
	assert.Equal(t, wif, withWif.Spec.WorkloadIdentityFederation)
	assert.Equal(t, "ALL", plain.Spec.ImplementationType)
	assert.Equal(t, "admin-customer", plain.Metadata.OwnedByWorkspace)
	assert.Contains(t, plain.Spec.PublicKey, "-----BEGIN PUBLIC KEY-----")
}
