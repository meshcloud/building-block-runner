package testacc

import (
	"bytes"
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/base64"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"text/template"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	envKeycloakAdminUrl      = "KEYCLOAK_ADMIN_URL"
	envKeycloakAdminUsername = "KEYCLOAK_ADMIN_USERNAME"
	envKeycloakAdminPassword = "KEYCLOAK_ADMIN_PASSWORD"

	// keycloakRealm is the one realm that meshfed-api's Keycloak admin may manage; it cannot create a realm.
	keycloakRealm = "meshfed"
)

//go:embed testdata/keycloak/client.json.tmpl
var clientJson string

var clientTemplate = template.Must(template.New("client").Parse(clientJson))

type idOnly struct {
	Id string `json:"id"`
}

type keycloakClient struct {
	url      string
	username string
	password string
	http     *http.Client

	id           string
	clientId     string
	clientSecret string
}

func setUpKeycloakClient(ctx context.Context) (*keycloakClient, error) {
	k := &keycloakClient{
		url:          strings.TrimSuffix(os.Getenv(envKeycloakAdminUrl), "/"),
		username:     os.Getenv(envKeycloakAdminUsername),
		password:     os.Getenv(envKeycloakAdminPassword),
		http:         &http.Client{Timeout: 30 * time.Second},
		clientId:     uniqueName("runner-testacc"),
		clientSecret: rand.Text(),
	}
	if k.url == "" || k.username == "" || k.password == "" {
		return nil, fmt.Errorf("%s, %s and %s name the Keycloak of the local dev stack. `./gradlew :building-block-runner:satelliteEnv` in ../meshfed-release writes them to ../.env-testacc-building-block-runner",
			envKeycloakAdminUrl, envKeycloakAdminUsername, envKeycloakAdminPassword)
	}
	if err := k.create(ctx); err != nil {
		return nil, fmt.Errorf("create the Keycloak client %s: %w", k.clientId, err)
	}
	return k, nil
}

func (k *keycloakClient) create(ctx context.Context) error {
	var representation bytes.Buffer
	if err := clientTemplate.Execute(&representation, struct{ ClientId, ClientSecret string }{k.clientId, k.clientSecret}); err != nil {
		return err
	}
	location, err := k.adminRequest(ctx, http.MethodPost, "/clients", jsontext.Value(representation.Bytes()), nil)
	if err != nil {
		return err
	}
	_, k.id, _ = strings.CutLast(location, "/")
	var serviceAccount idOnly
	if _, err = k.adminRequest(ctx, http.MethodGet, "/clients/"+k.id+"/service-account-user", nil, &serviceAccount); err != nil {
		return err
	}
	var realmManagement []idOnly
	if _, err = k.adminRequest(ctx, http.MethodGet, "/clients?clientId=realm-management", nil, &realmManagement); err != nil {
		return err
	}
	if len(realmManagement) != 1 {
		return fmt.Errorf("the realm has %d realm-management clients", len(realmManagement))
	}
	var manageClients map[string]any
	if _, err = k.adminRequest(ctx, http.MethodGet, "/clients/"+realmManagement[0].Id+"/roles/manage-clients", nil, &manageClients); err != nil {
		return err
	}
	_, err = k.adminRequest(ctx, http.MethodPost, "/users/"+serviceAccount.Id+"/role-mappings/clients/"+realmManagement[0].Id, []any{manageClients}, nil)
	return err
}

func (k *keycloakClient) delete(ctx context.Context) error {
	_, err := k.adminRequest(ctx, http.MethodDelete, "/clients/"+k.id, nil, nil)
	return err
}

func (k *keycloakClient) issuer() string {
	return k.url + "/realms/" + keycloakRealm
}

func (k *keycloakClient) runnerConfig(clientSecret string) map[string]any {
	return map[string]any{"url": k.url, "realm": keycloakRealm, "clientId": k.clientId, "clientSecret": clientSecret}
}

func (k *keycloakClient) runClientIds(t *testing.T) []string {
	t.Helper()
	var clients []struct {
		ClientId string `json:"clientId"`
	}
	query := url.Values{"clientId": {"bb-run-"}, "search": {"true"}}
	_, err := k.adminRequest(t.Context(), http.MethodGet, "/clients?"+query.Encode(), nil, &clients)
	require.NoError(t, err)
	clientIds := make([]string, 0, len(clients))
	for _, client := range clients {
		clientIds = append(clientIds, client.ClientId)
	}
	return clientIds
}

// adminRequest logs in for each request, as an admin token of the master realm lives for a minute only.
func (k *keycloakClient) adminRequest(ctx context.Context, method, path string, body, result any) (location string, err error) {
	adminToken, err := k.adminToken(ctx)
	if err != nil {
		return "", err
	}
	var reader io.Reader
	if body != nil {
		encoded, marshalErr := json.Marshal(body)
		if marshalErr != nil {
			return "", marshalErr
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, k.url+"/admin/realms/"+keycloakRealm+path, reader)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+adminToken)
	req.Header.Set("Content-Type", "application/json")
	return k.do(req, result)
}

func (k *keycloakClient) adminToken(ctx context.Context) (string, error) {
	form := url.Values{"grant_type": {"password"}, "client_id": {"admin-cli"}, "username": {k.username}, "password": {k.password}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, k.url+"/realms/master/protocol/openid-connect/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var token struct {
		AccessToken string `json:"access_token"`
	}
	if _, err := k.do(req, &token); err != nil {
		return "", fmt.Errorf("admin login to Keycloak as %s: %w", k.username, err)
	}
	return token.AccessToken, nil
}

func (k *keycloakClient) do(req *http.Request, result any) (location string, err error) {
	resp, err := k.http.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("%s %s: %s", req.Method, req.URL.Path, resp.Status)
	}
	if result != nil {
		err = json.UnmarshalRead(resp.Body, result)
	}
	return resp.Header.Get("Location"), err
}

func decodeClaims(t *testing.T, token string) map[string]any {
	t.Helper()
	parts := strings.Split(token, ".")
	require.Len(t, parts, 3, "the token is no JWT")
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	require.NoError(t, err)
	var claims map[string]any
	require.NoError(t, json.Unmarshal(payload, &claims))
	return claims
}
