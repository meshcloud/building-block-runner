//go:build keycloak

package keycloakwif

import (
	"cmp"
	"context"
	"log"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newKeycloakIssuer(t *testing.T) *Issuer {
	url := os.Getenv("KEYCLOAK_URL")
	require.NotEmpty(t, url, "run these tests with task test:keycloak")
	realm := cmp.Or(os.Getenv("KEYCLOAK_REALM"), "wif-test")
	return NewIssuer(Config{
		Url:           url,
		Realm:         realm,
		Issuer:        url + "/realms/" + realm,
		ClientId:      "run-controller",
		ClientSecret:  cmp.Or(os.Getenv("KEYCLOAK_CLIENT_SECRET"), "run-controller-secret"),
		TokenLifetime: 90 * time.Minute,
	}, http.DefaultClient, log.New(os.Stdout, "", 0))
}

func Test_Keycloak_TokenNamesOnlyIssuerSubjectAndAudience(t *testing.T) {
	issuer := newKeycloakIssuer(t)

	token, err := issuer.Issue(context.Background(), "integration-run", testSubject)

	require.NoError(t, err)
	claims := decodeClaims(t, token)
	assert.Equal(t, testSubject, claims["sub"])
	assert.Equal(t, Audience, claims["aud"])
	assert.Equal(t, Audience, claims["azp"])
	t.Logf("claims: %v", claims)
}

func Test_Keycloak_ClientDeletedAfterIssue(t *testing.T) {
	issuer := newKeycloakIssuer(t)

	_, err := issuer.Issue(context.Background(), "integration-run", testSubject)
	require.NoError(t, err)

	deleted, err := issuer.DeleteStaleClients(context.Background(), 0)
	require.NoError(t, err)
	assert.Zero(t, deleted)
}

func Test_Keycloak_LeftoverClientDeleted(t *testing.T) {
	issuer := newKeycloakIssuer(t)
	adminToken, err := issuer.adminAccessToken(context.Background())
	require.NoError(t, err)
	_, err = issuer.createClient(context.Background(), adminToken, clientIdPrefix+"leftover", "secret", testSubject)
	require.NoError(t, err)

	deleted, err := issuer.DeleteStaleClients(context.Background(), 0)

	require.NoError(t, err)
	assert.Equal(t, 1, deleted)
}
