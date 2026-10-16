package keycloakwif

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testSubject = "bbd:my-workspace:0d43a2b8-aaaa-bbbb-cccc-1234567890ab"

var testNow = time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)

func newTestIssuer(t *testing.T) (*Issuer, *fakeKeycloak, *bytes.Buffer) {
	now := func() time.Time { return testNow }
	keycloak := newFakeKeycloak(t, now)
	logs := &bytes.Buffer{}
	issuer := NewIssuer(Config{
		Url:           keycloak.server.URL + "/",
		Realm:         fakeRealm,
		Issuer:        keycloak.issuer(),
		ClientId:      fakeAdminId,
		ClientSecret:  fakeAdminSecret,
		TokenLifetime: 90 * time.Minute,
	}, http.DefaultClient, log.New(logs, "", 0))
	issuer.now = now
	return issuer, keycloak, logs
}

func decodeClaims(t *testing.T, token string) map[string]any {
	t.Helper()
	payload, err := base64.RawURLEncoding.DecodeString(strings.Split(token, ".")[1])
	require.NoError(t, err)
	var claims map[string]any
	require.NoError(t, json.Unmarshal(payload, &claims))
	return claims
}

func Test_TokenIssued_ClaimsNameOnlyIssuerSubjectAndAudience(t *testing.T) {
	issuer, keycloak, _ := newTestIssuer(t)

	token, err := issuer.Issue(context.Background(), "run-1", testSubject)

	require.NoError(t, err)
	claims := decodeClaims(t, token)
	assert.Equal(t, keycloak.issuer(), claims["iss"])
	assert.Equal(t, testSubject, claims["sub"])
	assert.Equal(t, Audience, claims["aud"])
	assert.Equal(t, Audience, claims["azp"])
	assert.NotContains(t, claims, "client_id")
	assert.NotContains(t, claims, "clientHost")
	assert.NotContains(t, claims, "clientAddress")
	assert.Equal(t, float64(testNow.Add(90*time.Minute).Unix()), claims["exp"])
}

func Test_TokenIssued_ClientDeleted(t *testing.T) {
	issuer, keycloak, _ := newTestIssuer(t)

	_, err := issuer.Issue(context.Background(), "run-1", testSubject)

	require.NoError(t, err)
	assert.Empty(t, keycloak.clientIds())
}

func Test_SecondToken_ReusesAdminLogin(t *testing.T) {
	issuer, keycloak, _ := newTestIssuer(t)

	_, err := issuer.Issue(context.Background(), "run-1", testSubject)
	require.NoError(t, err)
	_, err = issuer.Issue(context.Background(), "run-2", testSubject)
	require.NoError(t, err)

	assert.Equal(t, 1, keycloak.adminLogins)
}

func Test_TokenRequestFails_ClientDeletedAndNoSecretInErrorOrLog(t *testing.T) {
	issuer, keycloak, logs := newTestIssuer(t)
	keycloak.failRunTokens = 1

	_, err := issuer.Issue(context.Background(), "run-1", testSubject)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unauthorized_client")
	assert.NotContains(t, err.Error(), "client_secret")
	assert.NotContains(t, logs.String(), "client_secret")
	assert.Empty(t, keycloak.clientIds())
}

func Test_TokenHasUnknownClaim_Rejected(t *testing.T) {
	issuer, keycloak, _ := newTestIssuer(t)
	keycloak.extraClaims = map[string]string{"clientHost": "10.0.0.1"}

	_, err := issuer.Issue(context.Background(), "run-1", testSubject)

	require.ErrorContains(t, err, `unexpected claim "clientHost"`)
	assert.NotContains(t, err.Error(), "10.0.0.1")
	assert.Empty(t, keycloak.clientIds())
}

func Test_RealmCutsLifetime_TokenRejected(t *testing.T) {
	issuer, keycloak, _ := newTestIssuer(t)
	keycloak.maxLifetime = 30 * time.Minute

	_, err := issuer.Issue(context.Background(), "run-1", testSubject)

	require.ErrorContains(t, err, "token expires in 30m0s, want 1h30m0s")
}

func Test_WrongAdminSecret_NoClientCreated(t *testing.T) {
	issuer, keycloak, _ := newTestIssuer(t)
	issuer.config.ClientSecret = "wrong"

	_, err := issuer.Issue(context.Background(), "run-1", testSubject)

	require.ErrorContains(t, err, "admin login")
	assert.Empty(t, keycloak.clientIds())
}

func Test_StaleClients_OnlyOldAndUndatedRunClientsDeleted(t *testing.T) {
	issuer, keycloak, _ := newTestIssuer(t)
	createdAt := func(age time.Duration) map[string]string {
		return map[string]string{createdAtAttribute: strconv.FormatInt(testNow.Add(-age).Unix(), 10)}
	}
	keycloak.addClient("bb-run-old", createdAt(11*time.Minute))
	keycloak.addClient("bb-run-fresh", createdAt(time.Minute))
	keycloak.addClient("bb-run-undated", nil)
	keycloak.addClient("run-controller", nil)
	keycloak.addClient("realm-management", nil)

	deleted, err := issuer.DeleteStaleClients(context.Background(), 10*time.Minute)

	require.NoError(t, err)
	assert.Equal(t, 2, deleted)
	assert.ElementsMatch(t, []string{"bb-run-fresh", "run-controller", "realm-management"}, keycloak.clientIds())
}

func Test_MoreStaleClientsThanOnePage_AllDeleted(t *testing.T) {
	issuer, keycloak, _ := newTestIssuer(t)
	for i := range listPageSize + 5 {
		keycloak.addClient("bb-run-"+strconv.Itoa(i), nil)
	}

	deleted, err := issuer.DeleteStaleClients(context.Background(), 10*time.Minute)

	require.NoError(t, err)
	assert.Equal(t, listPageSize+5, deleted)
	assert.Empty(t, keycloak.clientIds())
}

func Test_AdminTokenRejected_IssueLogsInAgain(t *testing.T) {
	issuer, keycloak, _ := newTestIssuer(t)
	_, err := issuer.Issue(context.Background(), "run-1", testSubject)
	require.NoError(t, err)
	keycloak.revokeAdminToken()

	_, err = issuer.Issue(context.Background(), "run-2", testSubject)

	require.NoError(t, err)
	assert.Equal(t, 2, keycloak.adminLogins)
	assert.Empty(t, keycloak.clientIds())
}

func Test_AdminTokenRejected_CleanupLogsInAgainNextTime(t *testing.T) {
	issuer, keycloak, _ := newTestIssuer(t)
	_, err := issuer.DeleteStaleClients(context.Background(), 0)
	require.NoError(t, err)
	keycloak.revokeAdminToken()

	_, err = issuer.DeleteStaleClients(context.Background(), 0)
	require.ErrorIs(t, err, errAdminTokenRejected)
	_, err = issuer.DeleteStaleClients(context.Background(), 0)

	require.NoError(t, err)
	assert.Equal(t, 2, keycloak.adminLogins)
}
