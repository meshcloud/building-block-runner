// Package keycloakwif issues workload identity tokens from Keycloak. It creates one Keycloak client per token, because
// Keycloak can set a fixed sub claim only per client.
package keycloakwif

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Audience is the value of the aud claim and of the azp claim. Keycloak always sets azp, normally to the client ID, and
// AWS checks azp instead of aud when a token has azp. So createClient sets azp to the audience as well.
const Audience = "meshstack-wif"

const (
	clientIdPrefix         = "bb-run-"
	adminTokenExpiryMargin = 30 * time.Second
	deleteClientTimeout    = 10 * time.Second
)

type Config struct {
	Url           string
	Realm         string
	Issuer        string
	ClientId      string
	ClientSecret  string
	TokenLifetime time.Duration
}

// Issuer mints workload identity tokens from short-lived per-run Keycloak clients. It is not safe for concurrent use,
// because it caches the admin token without a lock.
type Issuer struct {
	config Config
	http   *http.Client
	logger *log.Logger
	now    func() time.Time

	adminToken       string
	adminTokenExpiry time.Time
}

func NewIssuer(config Config, httpClient *http.Client, logger *log.Logger) *Issuer {
	client := *httpClient
	// Do not follow redirects. On a 307 or 308 redirect, http.Client sends the request body again, so it would send
	// the client secret to whatever host or plain http URL the redirect names.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Issuer{config: config, http: &client, logger: logger, now: time.Now}
}

func (i *Issuer) Issue(ctx context.Context, runUuid, subject string) (string, error) {
	adminToken, err := i.adminAccessToken(ctx)
	if err != nil {
		return "", err
	}

	// A client of an earlier dispatch of the same run can still exist, because the run-controller does not retry a
	// failed delete, and someone must delete that client by hand. The random suffix keeps the new client ID unique.
	suffix, err := randomString(4)
	if err != nil {
		return "", err
	}
	clientId := clientIdPrefix + runUuid + "-" + suffix
	clientSecret, err := randomString(32)
	if err != nil {
		return "", err
	}
	id, err := i.createClient(ctx, adminToken, clientId, clientSecret, subject)
	if err != nil {
		return "", err
	}
	defer i.deleteClientIgnoringCancel(ctx, adminToken, id, clientId)

	// Remove the realm's default scopes. They add claims such as client_id and the run-controller's IP address,
	// and checkClaims rejects every claim that it does not expect.
	if err := i.removeClientScopes(ctx, adminToken, id); err != nil {
		return "", err
	}

	token, err := i.clientCredentialsToken(ctx, clientId, clientSecret)
	if err != nil {
		return "", err
	}
	if err := checkClaims(token, i.config.Issuer, subject, i.config.TokenLifetime); err != nil {
		return "", err
	}
	return token, nil
}

func (i *Issuer) createClient(ctx context.Context, adminToken, clientId, clientSecret, subject string) (string, error) {
	representation := map[string]any{
		"clientId":                  clientId,
		"enabled":                   true,
		"protocol":                  "openid-connect",
		"publicClient":              false,
		"clientAuthenticatorType":   "client-secret",
		"secret":                    clientSecret,
		"standardFlowEnabled":       false,
		"implicitFlowEnabled":       false,
		"directAccessGrantsEnabled": false,
		"serviceAccountsEnabled":    true,
		"fullScopeAllowed":          false,
		"defaultClientScopes":       []string{},
		"optionalClientScopes":      []string{},
		"attributes": map[string]string{
			"access.token.lifespan":                       strconv.Itoa(int(i.config.TokenLifetime.Seconds())),
			"client.use.lightweight.access.token.enabled": "false",
		},
		"protocolMappers": []map[string]any{
			hardcodedClaimMapper("sub", subject),
			hardcodedClaimMapper("azp", Audience),
			{
				"name":           "aud",
				"protocol":       "openid-connect",
				"protocolMapper": "oidc-audience-mapper",
				"config": map[string]string{
					"included.custom.audience":  Audience,
					"access.token.claim":        "true",
					"id.token.claim":            "false",
					"introspection.token.claim": "false",
				},
			},
		},
	}

	resp, err := i.send(ctx, adminToken, http.MethodPost, i.adminUrl("/clients"), representation)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return "", keycloakError(resp, "create client "+clientId)
	}
	location := resp.Header.Get("Location")
	id := location[strings.LastIndex(location, "/")+1:]
	if id == "" {
		return "", fmt.Errorf("create client %s: Keycloak returned no Location header", clientId)
	}
	return id, nil
}

func hardcodedClaimMapper(claim, value string) map[string]any {
	return map[string]any{
		"name":           claim,
		"protocol":       "openid-connect",
		"protocolMapper": "oidc-hardcoded-claim-mapper",
		"config": map[string]string{
			"claim.name":                claim,
			"claim.value":               value,
			"jsonType.label":            "String",
			"access.token.claim":        "true",
			"id.token.claim":            "false",
			"userinfo.token.claim":      "false",
			"introspection.token.claim": "false",
		},
	}
}

func (i *Issuer) removeClientScopes(ctx context.Context, adminToken, id string) error {
	for _, kind := range []string{"default-client-scopes", "optional-client-scopes"} {
		path := "/clients/" + url.PathEscape(id) + "/" + kind
		var scopes []struct {
			Id string `json:"id"`
		}
		if err := i.adminRequest(ctx, adminToken, http.MethodGet, path, nil, &scopes); err != nil {
			return err
		}
		for _, scope := range scopes {
			if err := i.adminRequest(ctx, adminToken, http.MethodDelete, path+"/"+url.PathEscape(scope.Id), nil, nil); err != nil {
				return err
			}
		}
	}
	return nil
}

func (i *Issuer) deleteClientIgnoringCancel(ctx context.Context, adminToken, id, clientId string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), deleteClientTimeout)
	defer cancel()
	if err := i.adminRequest(ctx, adminToken, http.MethodDelete, "/clients/"+url.PathEscape(id), nil, nil); err != nil {
		i.logger.Printf("Failed to delete Keycloak client %s: %v", clientId, err)
	}
}

func (i *Issuer) adminAccessToken(ctx context.Context) (string, error) {
	if i.adminToken != "" && i.now().Before(i.adminTokenExpiry) {
		return i.adminToken, nil
	}

	token, err := i.requestToken(ctx, i.config.ClientId, i.config.ClientSecret)
	if err != nil {
		return "", fmt.Errorf("admin login: %w", err)
	}
	i.adminToken = token.AccessToken
	i.adminTokenExpiry = i.now().Add(time.Duration(token.ExpiresIn)*time.Second - adminTokenExpiryMargin)
	return i.adminToken, nil
}

func (i *Issuer) clientCredentialsToken(ctx context.Context, clientId, clientSecret string) (string, error) {
	token, err := i.requestToken(ctx, clientId, clientSecret)
	if err != nil {
		return "", fmt.Errorf("token for client %s: %w", clientId, err)
	}
	return token.AccessToken, nil
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
}

func (i *Issuer) requestToken(ctx context.Context, clientId, clientSecret string) (tokenResponse, error) {
	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {clientId},
		"client_secret": {clientSecret},
	}
	tokenUrl := i.config.Url + "/realms/" + url.PathEscape(i.config.Realm) + "/protocol/openid-connect/token"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenUrl, strings.NewReader(form.Encode()))
	if err != nil {
		return tokenResponse{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := i.http.Do(req)
	if err != nil {
		return tokenResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return tokenResponse{}, keycloakError(resp, "token request")
	}
	var token tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&token); err != nil {
		return tokenResponse{}, fmt.Errorf("token response: %w", err)
	}
	if token.AccessToken == "" {
		return tokenResponse{}, errors.New("token response has no access_token")
	}
	return token, nil
}

func (i *Issuer) adminUrl(path string) string {
	return i.config.Url + "/admin/realms/" + url.PathEscape(i.config.Realm) + path
}

func (i *Issuer) adminRequest(ctx context.Context, adminToken, method, path string, body, result any) error {
	resp, err := i.send(ctx, adminToken, method, i.adminUrl(path), body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return keycloakError(resp, method+" "+strings.SplitN(path, "?", 2)[0])
	}
	if result == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(result)
}

func (i *Issuer) send(ctx context.Context, adminToken, method, target string, body any) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+adminToken)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return i.http.Do(req)
}

// keycloakError reads only the error fields of a Keycloak response body. The rest of the body can contain a copy of
// the request, which can contain a client secret, and the run-controller writes the error to its log.
func keycloakError(resp *http.Response, action string) error {
	var body struct {
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
		ErrorMessage     string `json:"errorMessage"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 64*1024)).Decode(&body)
	detail := strings.TrimSpace(strings.Join([]string{body.Error, body.ErrorDescription, body.ErrorMessage}, " "))
	if detail == "" {
		return fmt.Errorf("%s: Keycloak returned HTTP %d", action, resp.StatusCode)
	}
	return fmt.Errorf("%s: Keycloak returned HTTP %d: %s", action, resp.StatusCode, detail)
}

func randomString(bytes int) (string, error) {
	random := make([]byte, bytes)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(random), nil
}
