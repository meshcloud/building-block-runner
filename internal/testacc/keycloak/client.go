package keycloak

import (
	"bytes"
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/base64"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"text/template"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	envAdminUrl      = "KEYCLOAK_ADMIN_URL"
	envAdminUsername = "KEYCLOAK_ADMIN_USERNAME"
	envAdminPassword = "KEYCLOAK_ADMIN_PASSWORD"

	// realm is the one realm that meshfed-api's Keycloak admin may manage; it cannot create a realm.
	realm = "meshfed"
)

//go:embed client.json.tmpl
var clientJson string

var clientTemplate = template.Must(template.New("client").Parse(clientJson))

type idOnly struct {
	Id string `json:"id"`
}

type Client struct {
	ClientSecret string
	Issuer       string

	admin    admin
	id       string
	clientId string
}

func New(t *testing.T, keep bool) Client {
	t.Helper()
	a := admin{
		url:      strings.TrimSuffix(os.Getenv(envAdminUrl), "/"),
		username: os.Getenv(envAdminUsername),
		password: os.Getenv(envAdminPassword),
		http:     &http.Client{Timeout: 30 * time.Second},
	}
	require.Truef(t, a.url != "" && a.username != "" && a.password != "",
		"%s, %s and %s name the Keycloak of the local dev stack. `./gradlew :building-block-runner:satelliteEnv` in ../meshfed-release writes them to ../.env-testacc-building-block-runner",
		envAdminUrl, envAdminUsername, envAdminPassword)
	c := Client{
		ClientSecret: rand.Text(),
		Issuer:       a.url + "/realms/" + realm,
		admin:        a,
		clientId:     "runner-testacc-" + strings.ToLower(rand.Text()[:10]),
	}
	var err error
	c.id, err = c.create(t.Context())
	require.NoErrorf(t, err, "create the Keycloak client %s", c.clientId)
	t.Cleanup(func() {
		if keep {
			t.Logf("keeping the Keycloak client %s", c.clientId)
			return
		}
		_, err := c.admin.request(context.WithoutCancel(t.Context()), http.MethodDelete, "/clients/"+c.id, nil, nil)
		assert.NoError(t, err)
	})
	return c
}

func (c Client) RunnerConfig(t *testing.T, clientSecret string) map[string]any {
	t.Helper()
	return map[string]any{"url": c.admin.url, "realm": realm, "clientId": c.clientId, "clientSecret": clientSecret}
}

func (c Client) RunClientIds(t *testing.T) []string {
	t.Helper()
	var clients []struct {
		ClientId string `json:"clientId"`
	}
	query := url.Values{"clientId": {"bb-run-"}, "search": {"true"}}
	_, err := c.admin.request(t.Context(), http.MethodGet, "/clients?"+query.Encode(), nil, &clients)
	require.NoError(t, err)
	clientIds := make([]string, 0, len(clients))
	for _, client := range clients {
		clientIds = append(clientIds, client.ClientId)
	}
	return clientIds
}

func (c Client) create(ctx context.Context) (id string, err error) {
	var representation bytes.Buffer
	if err = clientTemplate.Execute(&representation, struct{ ClientId, ClientSecret string }{c.clientId, c.ClientSecret}); err != nil {
		return "", err
	}
	location, err := c.admin.request(ctx, http.MethodPost, "/clients", jsontext.Value(representation.Bytes()), nil)
	if err != nil {
		return "", err
	}
	_, id, _ = strings.CutLast(location, "/")
	return id, c.admin.grantManageClients(ctx, id)
}

func DecodeClaims(t *testing.T, token string) map[string]any {
	t.Helper()
	parts := strings.Split(token, ".")
	require.Len(t, parts, 3, "the token is no JWT")
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	require.NoError(t, err)
	var claims map[string]any
	require.NoError(t, json.Unmarshal(payload, &claims))
	return claims
}
