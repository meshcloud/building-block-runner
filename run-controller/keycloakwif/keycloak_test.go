//go:build keycloak

package keycloakwif

import (
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

var keycloakUrl string

func TestMain(m *testing.M) {
	ctx := context.Background()
	var realms []testcontainers.ContainerFile
	for _, file := range []string{"wif-test-realm.json", "wif-test-short-session-realm.json"} {
		realms = append(realms, testcontainers.ContainerFile{
			HostFilePath:      "testdata/" + file,
			ContainerFilePath: "/opt/keycloak/data/import/" + file,
			FileMode:          0o644,
		})
	}
	keycloak, err := testcontainers.Run(ctx, "quay.io/keycloak/keycloak:"+cmp.Or(os.Getenv("KEYCLOAK_VERSION"), "26.8.1"),
		testcontainers.WithFiles(realms...),
		testcontainers.WithCmd("start-dev", "--import-realm"),
		testcontainers.WithExposedPorts("8080/tcp"),
		testcontainers.WithWaitStrategy(wait.ForHTTP("/realms/wif-test-short-session").WithPort("8080/tcp").WithStartupTimeout(2*time.Minute)),
	)
	if err != nil {
		log.Fatalf("start Keycloak: %v", err)
	}
	keycloakUrl, err = keycloak.PortEndpoint(ctx, "8080/tcp", "http")
	if err != nil {
		log.Fatalf("Keycloak address: %v", err)
	}
	code := m.Run()
	_ = testcontainers.TerminateContainer(keycloak)
	os.Exit(code)
}

func newKeycloakIssuer(realm, adminSecret string) *Issuer {
	return NewIssuer(Config{
		Url:           keycloakUrl,
		Realm:         realm,
		Issuer:        keycloakUrl + "/realms/" + realm,
		ClientId:      "run-controller",
		ClientSecret:  adminSecret,
		TokenLifetime: 90 * time.Minute,
	}, http.DefaultClient, log.New(os.Stdout, "", 0))
}

func runClientIds(t *testing.T) []string {
	t.Helper()
	issuer := newKeycloakIssuer("wif-test", "run-controller-secret")
	adminToken, err := issuer.adminAccessToken(context.Background())
	require.NoError(t, err)
	var clients []struct {
		ClientId string `json:"clientId"`
	}
	query := url.Values{"clientId": {clientIdPrefix}, "search": {"true"}}
	require.NoError(t, issuer.adminRequest(context.Background(), adminToken, http.MethodGet, "/clients?"+query.Encode(), nil, &clients))
	var ids []string
	for _, client := range clients {
		ids = append(ids, client.ClientId)
	}
	return ids
}

func decodeClaims(t *testing.T, token string) map[string]any {
	t.Helper()
	payload, err := base64.RawURLEncoding.DecodeString(strings.Split(token, ".")[1])
	require.NoError(t, err)
	var claims map[string]any
	require.NoError(t, json.Unmarshal(payload, &claims))
	return claims
}

func Test_Keycloak_TokenNamesOnlyIssuerSubjectAndAudience(t *testing.T) {
	token, err := newKeycloakIssuer("wif-test", "run-controller-secret").Issue(context.Background(), "integration-run", testSubject)

	require.NoError(t, err)
	claims := decodeClaims(t, token)
	assert.Equal(t, testSubject, claims["sub"])
	assert.Equal(t, Audience, claims["aud"])
	assert.Equal(t, Audience, claims["azp"])
}

func Test_Keycloak_ClientDeletedAfterIssue(t *testing.T) {
	_, err := newKeycloakIssuer("wif-test", "run-controller-secret").Issue(context.Background(), "integration-run", testSubject)

	require.NoError(t, err)
	assert.Empty(t, runClientIds(t))
}

func Test_Keycloak_WrongAdminSecret_NoClientCreated(t *testing.T) {
	_, err := newKeycloakIssuer("wif-test", "wrong").Issue(context.Background(), "integration-run", testSubject)

	require.ErrorContains(t, err, "admin login")
	assert.Empty(t, runClientIds(t))
}

func Test_Keycloak_SessionMaxShorterThanLifetime_TokenRejected(t *testing.T) {
	_, err := newKeycloakIssuer("wif-test-short-session", "run-controller-secret").Issue(context.Background(), "integration-run", testSubject)

	require.ErrorContains(t, err, "token lifetime is 30m0s, want 1h30m0s")
}
