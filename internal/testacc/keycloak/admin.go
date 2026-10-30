package keycloak

import (
	"context"
	"encoding/json/v2"
	"fmt"
	gohttp "net/http"

	"github.com/meshcloud/meshstack-cli/client/http"
	"github.com/meshcloud/meshstack-cli/client/types/xurl"
)

type admin struct {
	url      string
	username string
	password string
	http     http.Client
}

func (a admin) grantManageClients(ctx context.Context, clientUuid string) error {
	var serviceAccount idOnly
	if _, err := a.request(ctx, http.MethodGet, "/clients/"+clientUuid+"/service-account-user", nil, &serviceAccount); err != nil {
		return err
	}
	var realmManagement []idOnly
	if _, err := a.request(ctx, http.MethodGet, "/clients?clientId=realm-management", nil, &realmManagement); err != nil {
		return err
	}
	if len(realmManagement) != 1 {
		return fmt.Errorf("the realm has %d realm-management clients", len(realmManagement))
	}
	var manageClients map[string]any
	if _, err := a.request(ctx, http.MethodGet, "/clients/"+realmManagement[0].Id+"/roles/manage-clients", nil, &manageClients); err != nil {
		return err
	}
	_, err := a.request(ctx, http.MethodPost, "/users/"+serviceAccount.Id+"/role-mappings/clients/"+realmManagement[0].Id, []any{manageClients}, nil)
	return err
}

// request logs in for each request, as an admin token of the master realm lives for a minute only.
func (a admin) request(ctx context.Context, method, path string, body, result any) (location string, err error) {
	adminToken, err := a.token(ctx)
	if err != nil {
		return "", err
	}
	var header gohttp.Header
	data, err := http.ReadAllBody(a.http.DoRequest(ctx, method, xurl.MustParsef("%s/admin/realms/%s%s", a.url, realm, path).URL,
		http.WithHeader("Authorization", "Bearer "+adminToken), http.WithJsonRequest(body, ""), http.WithCapturedResponseHeader(&header)))
	if err != nil {
		return "", err
	}
	if result != nil {
		err = json.Unmarshal(data, result)
	}
	return header.Get("Location"), err
}

func (a admin) token(ctx context.Context) (string, error) {
	token, err := http.UnmarshalJsonBody[struct {
		AccessToken string `json:"access_token"`
	}](a.http.DoRequest(ctx, http.MethodPost, xurl.MustParsef("%s/realms/master/protocol/openid-connect/token", a.url).URL,
		http.WithFormRequest(map[string]string{"grant_type": "password", "client_id": "admin-cli", "username": a.username, "password": a.password})))
	if err != nil {
		return "", fmt.Errorf("admin login to Keycloak as %s: %w", a.username, err)
	}
	return token.AccessToken, nil
}
