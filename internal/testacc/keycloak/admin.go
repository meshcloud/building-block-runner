package keycloak

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

type admin struct {
	url      string
	username string
	password string
	http     *http.Client
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
	var reader io.Reader
	if body != nil {
		encoded, marshalErr := json.Marshal(body)
		if marshalErr != nil {
			return "", marshalErr
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.url+"/admin/realms/"+realm+path, reader)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+adminToken)
	req.Header.Set("Content-Type", "application/json")
	return a.do(req, result)
}

func (a admin) token(ctx context.Context) (string, error) {
	form := url.Values{"grant_type": {"password"}, "client_id": {"admin-cli"}, "username": {a.username}, "password": {a.password}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.url+"/realms/master/protocol/openid-connect/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var token struct {
		AccessToken string `json:"access_token"`
	}
	if _, err := a.do(req, &token); err != nil {
		return "", fmt.Errorf("admin login to Keycloak as %s: %w", a.username, err)
	}
	return token.AccessToken, nil
}

func (a admin) do(req *http.Request, result any) (location string, err error) {
	resp, err := a.http.Do(req)
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
