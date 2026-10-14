// Package keycloakwif issues each workload identity token from a Keycloak client of its own, because Keycloak can
// hardcode the sub claim only per client.
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
	"sync"
	"time"
)

// Audience is also the azp claim, because AWS checks azp instead of aud when azp is present.
const Audience = "meshstack-wif"

const (
	clientIdPrefix         = "bb-run-"
	createdAtAttribute     = "meshcloud.io/created-at"
	adminTokenExpiryMargin = 30 * time.Second
	deleteClientTimeout    = 10 * time.Second
	listPageSize           = 100
)

var errAdminTokenRejected = errors.New("Keycloak rejected the admin token")

type Config struct {
	Url           string
	Realm         string
	Issuer        string
	ClientId      string
	ClientSecret  string
	TokenLifetime time.Duration
}

type Issuer struct {
	config Config
	http   *http.Client
	logger *log.Logger
	now    func() time.Time

	adminTokenMu     sync.Mutex
	adminToken       string
	adminTokenExpiry time.Time
}

func NewIssuer(config Config, httpClient *http.Client, logger *log.Logger) *Issuer {
	config.Url = strings.TrimSuffix(config.Url, "/")
	return &Issuer{config: config, http: httpClient, logger: logger, now: time.Now}
}

func (i *Issuer) Issue(ctx context.Context, runUuid string, subject string) (string, error) {
	adminToken, err := i.adminAccessToken(ctx)
	if err != nil {
		return "", err
	}

	clientId := clientIdPrefix + runUuid
	clientSecret, err := randomSecret()
	if err != nil {
		return "", err
	}
	id, err := i.createClient(ctx, adminToken, clientId, clientSecret, subject)
	// The rejected token is out of the cache by now, and nothing exists in Keycloak yet.
	if errors.Is(err, errAdminTokenRejected) {
		if adminToken, err = i.adminAccessToken(ctx); err != nil {
			return "", err
		}
		id, err = i.createClient(ctx, adminToken, clientId, clientSecret, subject)
	}
	if err != nil {
		return "", err
	}
	defer i.deleteClientIgnoringCancel(ctx, adminToken, id, clientId)

	// The realm's default scopes add claims such as client_id and the pod IPs.
	if err := i.removeClientScopes(ctx, adminToken, id); err != nil {
		return "", err
	}

	token, err := i.clientCredentialsToken(ctx, clientId, clientSecret)
	if err != nil {
		return "", err
	}
	if err := CheckClaims(token, i.config.Issuer, subject, i.config.TokenLifetime, i.now()); err != nil {
		return "", err
	}
	return token, nil
}

func (i *Issuer) DeleteStaleClients(ctx context.Context, minAge time.Duration) (int, error) {
	adminToken, err := i.adminAccessToken(ctx)
	if err != nil {
		return 0, err
	}

	var stale []keycloakClient
	for first := 0; ; first += listPageSize {
		query := url.Values{
			"clientId": {clientIdPrefix},
			"search":   {"true"},
			"first":    {strconv.Itoa(first)},
			"max":      {strconv.Itoa(listPageSize)},
		}
		var page []keycloakClient
		if err := i.adminRequest(ctx, adminToken, http.MethodGet, "/clients?"+query.Encode(), nil, &page); err != nil {
			return 0, err
		}
		for _, client := range page {
			if strings.HasPrefix(client.ClientId, clientIdPrefix) && i.olderThan(client, minAge) {
				stale = append(stale, client)
			}
		}
		if len(page) < listPageSize {
			break
		}
	}

	deleted := 0
	for _, client := range stale {
		if err := i.adminRequest(ctx, adminToken, http.MethodDelete, "/clients/"+url.PathEscape(client.Id), nil, nil); err != nil {
			return deleted, err
		}
		deleted++
	}
	return deleted, nil
}

type keycloakClient struct {
	Id         string            `json:"id"`
	ClientId   string            `json:"clientId"`
	Attributes map[string]string `json:"attributes"`
}

func (i *Issuer) olderThan(client keycloakClient, minAge time.Duration) bool {
	createdAt, err := strconv.ParseInt(client.Attributes[createdAtAttribute], 10, 64)
	if err != nil {
		return true
	}
	return i.now().Sub(time.Unix(createdAt, 0)) >= minAge
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
			createdAtAttribute:                            strconv.FormatInt(i.now().Unix(), 10),
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
		return "", i.adminError(resp, adminToken, "create client "+clientId)
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
		i.logger.Printf("Keycloak client %s stays until the stale client cleanup deletes it: %v", clientId, err)
	}
}

func (i *Issuer) adminAccessToken(ctx context.Context) (string, error) {
	i.adminTokenMu.Lock()
	defer i.adminTokenMu.Unlock()
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

// A rejected admin token stays rejected, for example after the realm's issuer changed, so it leaves the cache.
func (i *Issuer) adminError(resp *http.Response, adminToken, action string) error {
	err := keycloakError(resp, action)
	if resp.StatusCode != http.StatusUnauthorized {
		return err
	}
	i.adminTokenMu.Lock()
	if i.adminToken == adminToken {
		i.adminToken = ""
	}
	i.adminTokenMu.Unlock()
	return fmt.Errorf("%w: %w", errAdminTokenRejected, err)
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
		return i.adminError(resp, adminToken, method+" "+strings.SplitN(path, "?", 2)[0])
	}
	if result == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(result)
}

func (i *Issuer) send(ctx context.Context, adminToken, method, url string, body any) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+adminToken)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return i.http.Do(req)
}

// The rest of a response body can echo the request, which can carry a client secret.
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

func randomSecret() (string, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(secret), nil
}
