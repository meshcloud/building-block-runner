package dispatcher

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/meshcloud/meshstack-cli/client"

	"github.com/meshcloud/building-block-runner/internal/config"
)

const (
	EnvKeycloakWifToken = "RUNNER_KEYCLOAK_WIF_TOKEN_FILE"
	// KeycloakWifTokenModulePath is relative to the module dir, where the tf executor links it to the token file.
	KeycloakWifTokenModulePath = ".meshstack/wif-token" //nolint:gosec // a path, no credential

	// keycloakWifAudience is the aud and the azp claim, as AWS checks azp in place of aud when a token has azp.
	keycloakWifAudience = "meshstack-wif"

	keycloakWifClientIdPrefix = "bb-run-"
	keycloakTimeout           = 15 * time.Second
	adminTokenExpiryMargin    = 30 * time.Second
	keycloakErrorBodyLimit    = 64 << 10
	tokenLifetimeTolerance    = 2 * time.Minute
)

var allowedClaims = []string{"iss", "sub", "aud", "azp", "exp", "iat", "nbf", "jti", "typ", "scope"}

// KeycloakWif issues each run a workload identity token from a Keycloak client of its own, as Keycloak sets a fixed
// sub claim only per client.
type KeycloakWif struct {
	cfg    config.KeycloakWif
	issuer string
	http   *http.Client //nolint:forbidigo // Keycloak is no meshStack API.

	adminToken       string
	adminTokenExpiry time.Time
}

func NewKeycloakWif(cfg config.KeycloakWif) *KeycloakWif {
	return &KeycloakWif{
		cfg:    cfg,
		issuer: strings.TrimSuffix(cfg.Url.String(), "/") + "/realms/" + cfg.Realm,
		//nolint:forbidigo // Keycloak is no meshStack API.
		http: &http.Client{
			Timeout: keycloakTimeout,
			// On a 307 or 308, http.Client sends the body again, which holds a client secret, to wherever the redirect points.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

func (k *KeycloakWif) Registration(tokenPath string) *client.MeshRunnerWorkloadIdentityFed {
	provider := func() *client.MeshRunnerWifProviderConfig {
		return &client.MeshRunnerWifProviderConfig{Audience: keycloakWifAudience, TokenPath: tokenPath}
	}
	return &client.MeshRunnerWorkloadIdentityFed{
		Issuer:          new(k.issuer),
		SubjectTemplate: new(keycloakWifSubject("{{ workspaceIdentifier }}", "{{ buildingBlockDefinitionUuid }}")),
		Gcp:             provider(),
		Aws:             provider(),
		Azure:           provider(),
	}
}

func keycloakWifSubject(workspace, definitionUuid string) string {
	return "bbd:" + workspace + ":" + definitionUuid
}

func (k *KeycloakWif) Issue(ctx context.Context, run Run) (string, error) {
	adminToken, err := k.adminAccessToken(ctx)
	if err != nil {
		return "", err
	}
	definition := run.Spec.Definition
	subject := keycloakWifSubject(definition.Spec.WorkspaceIdentifier, definition.Uuid)
	// A client that an earlier dispatch of the run failed to delete may still exist.
	clientId := keycloakWifClientIdPrefix + run.Metadata.Uuid.String() + "-" + rand.Text()[:6]
	clientSecret := rand.Text()
	id, err := k.createClient(ctx, adminToken, clientId, clientSecret, subject)
	if err != nil {
		return "", err
	}
	defer k.deleteClient(ctx, adminToken, id, clientId)

	// The default scopes add claims such as client_id and the runner's IP address.
	if err = k.removeClientScopes(ctx, adminToken, id); err != nil {
		return "", err
	}
	token, err := k.requestToken(ctx, clientId, clientSecret)
	if err != nil {
		return "", fmt.Errorf("token for client %s: %w", clientId, err)
	}
	if err = checkClaims(token.AccessToken, k.issuer, subject, k.cfg.TokenLifetime.Duration); err != nil {
		return "", err
	}
	return token.AccessToken, nil
}

func (k *KeycloakWif) createClient(ctx context.Context, adminToken, clientId, clientSecret, subject string) (string, error) {
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
			"access.token.lifespan":                       strconv.Itoa(int(k.cfg.TokenLifetime.Seconds())),
			"client.use.lightweight.access.token.enabled": "false",
		},
		"protocolMappers": []map[string]any{
			hardcodedClaimMapper("sub", subject),
			hardcodedClaimMapper("azp", keycloakWifAudience),
			{
				"name":           "aud",
				"protocol":       "openid-connect",
				"protocolMapper": "oidc-audience-mapper",
				"config": map[string]string{
					"included.custom.audience":  keycloakWifAudience,
					"access.token.claim":        "true",
					"id.token.claim":            "false",
					"introspection.token.claim": "false",
				},
			},
		},
	}
	resp, err := k.send(ctx, adminToken, http.MethodPost, k.adminUrl("/clients"), representation)
	if err != nil {
		return "", err
	}
	defer closeBody(resp)
	if resp.StatusCode != http.StatusCreated {
		return "", keycloakError(resp, "create client "+clientId)
	}
	_, id, _ := strings.CutLast(resp.Header.Get("Location"), "/")
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

func (k *KeycloakWif) removeClientScopes(ctx context.Context, adminToken, id string) error {
	for _, kind := range []string{"default-client-scopes", "optional-client-scopes"} {
		path := "/clients/" + url.PathEscape(id) + "/" + kind
		var scopes []struct {
			Id string `json:"id"`
		}
		if err := k.adminRequest(ctx, adminToken, http.MethodGet, path, &scopes); err != nil {
			return err
		}
		for _, scope := range scopes {
			if err := k.adminRequest(ctx, adminToken, http.MethodDelete, path+"/"+url.PathEscape(scope.Id), nil); err != nil {
				return err
			}
		}
	}
	return nil
}

func (k *KeycloakWif) deleteClient(ctx context.Context, adminToken, id, clientId string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := k.adminRequest(ctx, adminToken, http.MethodDelete, "/clients/"+url.PathEscape(id), nil); err != nil {
		slog.ErrorContext(ctx, "deleting the Keycloak client of the run failed, delete it by hand", "client", clientId, "error", err)
	}
}

func (k *KeycloakWif) adminAccessToken(ctx context.Context) (string, error) {
	if k.adminToken != "" && time.Now().Before(k.adminTokenExpiry) {
		return k.adminToken, nil
	}
	token, err := k.requestToken(ctx, k.cfg.ClientId, k.cfg.ClientSecret)
	if err != nil {
		return "", fmt.Errorf("admin login to Keycloak as %s: %w", k.cfg.ClientId, err)
	}
	k.adminToken = token.AccessToken
	k.adminTokenExpiry = time.Now().Add(time.Duration(token.ExpiresIn)*time.Second - adminTokenExpiryMargin)
	return k.adminToken, nil
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
}

func (k *KeycloakWif) requestToken(ctx context.Context, clientId, clientSecret string) (tokenResponse, error) {
	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {clientId},
		"client_secret": {clientSecret},
	}
	tokenUrl := k.issuer + "/protocol/openid-connect/token"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenUrl, strings.NewReader(form.Encode()))
	if err != nil {
		return tokenResponse{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := k.http.Do(req)
	if err != nil {
		return tokenResponse{}, err
	}
	defer closeBody(resp)
	if resp.StatusCode != http.StatusOK {
		return tokenResponse{}, keycloakError(resp, "token request")
	}
	var token tokenResponse
	if err := json.UnmarshalRead(resp.Body, &token); err != nil {
		return tokenResponse{}, fmt.Errorf("token response: %w", err)
	}
	if token.AccessToken == "" {
		return tokenResponse{}, errors.New("token response has no access_token")
	}
	return token, nil
}

func (k *KeycloakWif) adminUrl(path string) string {
	return strings.TrimSuffix(k.cfg.Url.String(), "/") + "/admin/realms/" + url.PathEscape(k.cfg.Realm) + path
}

func (k *KeycloakWif) adminRequest(ctx context.Context, adminToken, method, path string, result any) error {
	resp, err := k.send(ctx, adminToken, method, k.adminUrl(path), nil)
	if err != nil {
		return err
	}
	defer closeBody(resp)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return keycloakError(resp, method+" "+path)
	}
	if result == nil {
		return nil
	}
	return json.UnmarshalRead(resp.Body, result)
}

func (k *KeycloakWif) send(ctx context.Context, adminToken, method, target string, body any) (*http.Response, error) {
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
	return k.http.Do(req)
}

func closeBody(resp *http.Response) {
	_ = resp.Body.Close()
}

// keycloakError reads only the error fields, as the rest of a body can echo a request with a client secret.
func keycloakError(resp *http.Response, action string) error {
	var body struct {
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
		ErrorMessage     string `json:"errorMessage"`
	}
	_ = json.UnmarshalRead(io.LimitReader(resp.Body, keycloakErrorBodyLimit), &body)
	detail := strings.TrimSpace(strings.Join([]string{body.Error, body.ErrorDescription, body.ErrorMessage}, " "))
	if detail == "" {
		return fmt.Errorf("%s: Keycloak returned HTTP %d", action, resp.StatusCode)
	}
	return fmt.Errorf("%s: Keycloak returned HTTP %d: %s", action, resp.StatusCode, detail)
}

func checkClaims(token, issuer, subject string, lifetime time.Duration) error {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return errors.New("token is not a JWT")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return errors.New("token payload is not base64url")
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return errors.New("token payload is not a JSON object")
	}
	for claim := range claims {
		if !slices.Contains(allowedClaims, claim) {
			return fmt.Errorf("token has the unexpected claim %q", claim)
		}
	}
	for claim, want := range map[string]string{"iss": issuer, "sub": subject, "azp": keycloakWifAudience} {
		if claims[claim] != want {
			return fmt.Errorf("token %s is %v, want %s", claim, claims[claim], want)
		}
	}
	if !isOnlyAudience(claims["aud"]) {
		return fmt.Errorf("token aud is %v, want %s", claims["aud"], keycloakWifAudience)
	}
	exp, ok := claims["exp"].(float64)
	if !ok {
		return errors.New("token has no exp claim")
	}
	iat, ok := claims["iat"].(float64)
	if !ok {
		return errors.New("token has no iat claim")
	}
	// Keycloak sets both from its own clock, so the runner's clock does not matter.
	if issued := time.Duration(exp-iat) * time.Second; issued < lifetime-tokenLifetimeTolerance || issued > lifetime+tokenLifetimeTolerance {
		return fmt.Errorf("token lifetime is %s, want %s", issued, lifetime)
	}
	return nil
}

func isOnlyAudience(aud any) bool {
	switch aud := aud.(type) {
	case string:
		return aud == keycloakWifAudience
	case []any:
		return len(aud) == 1 && aud[0] == keycloakWifAudience
	default:
		return false
	}
}
