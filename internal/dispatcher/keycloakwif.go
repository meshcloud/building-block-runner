package dispatcher

import (
	"bytes"
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"io"
	"log/slog"
	gohttp "net/http"
	"net/url"
	"strings"
	"text/template"

	"github.com/meshcloud/meshstack-cli/client"
	"github.com/meshcloud/meshstack-cli/client/http"

	"github.com/meshcloud/building-block-runner/v2/internal/config"
	"github.com/meshcloud/building-block-runner/v2/internal/executor"
)

const (
	EnvKeycloakWifToken        = "RUNNER_KEYCLOAK_WIF_TOKEN_FILE"
	KeycloakWifTokenModulePath = ".meshstack/wif-token" //nolint:gosec // a path, no credential

	// AWS checks azp when a token has one, so azp carries the audience too.
	keycloakWifAudience = "external-platform"

	keycloakWifClientIdPrefix = "bb-run-"
)

//go:embed keycloak-client.json.tmpl
var keycloakClientJson string

var keycloakClientTemplate = template.Must(template.New("keycloak-client").Parse(keycloakClientJson))

// keycloakOptions follow no redirect, which would send a body with a client secret to wherever it points.
var keycloakOptions = []http.RequestOption{http.WithoutRedirects(), http.WithRedactedJsonPointers("/secret", "/client_secret", "/access_token")}

// KeycloakWif creates a Keycloak client per run, as Keycloak sets a fixed sub claim only per client.
type KeycloakWif struct {
	cfg    config.KeycloakWif
	issuer *url.URL
	http   http.Client
}

func NewKeycloakWif(cfg config.KeycloakWif) *KeycloakWif {
	return &KeycloakWif{cfg: cfg, issuer: cfg.Url.JoinPath("realms", cfg.Realm), http: executor.NewHttpClient()}
}

func (k *KeycloakWif) Registration(tokenPath string) *client.MeshRunnerWorkloadIdentityFed {
	provider := func() *client.MeshRunnerWifProviderConfig {
		return &client.MeshRunnerWifProviderConfig{Audience: keycloakWifAudience, TokenPath: tokenPath}
	}
	return &client.MeshRunnerWorkloadIdentityFed{
		Issuer:          new(k.issuer.String()),
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
	adminToken, err := k.requestToken(ctx, k.cfg.ClientId, k.cfg.ClientSecret)
	if err != nil {
		return "", fmt.Errorf("admin login to Keycloak as %s: %w", k.cfg.ClientId, err)
	}
	definition := run.Spec.Definition
	subject := keycloakWifSubject(definition.Spec.WorkspaceIdentifier, definition.Uuid)
	// A client that an earlier dispatch of the run failed to delete may still exist.
	clientId := keycloakWifClientIdPrefix + run.Metadata.Uuid + "-" + rand.Text()[:6]
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
	return token, nil
}

func (k *KeycloakWif) createClient(ctx context.Context, adminToken, clientId, clientSecret, subject string) (string, error) {
	var representation bytes.Buffer
	err := keycloakClientTemplate.Execute(&representation, map[string]any{
		"ClientId": clientId, "ClientSecret": clientSecret, "Subject": subject, "Audience": keycloakWifAudience,
		"LifespanSeconds": int(k.cfg.TokenLifetime.Seconds()),
	})
	if err != nil {
		return "", err
	}
	var header gohttp.Header
	err = http.IgnoreResponse(k.adminRequest(ctx, adminToken, http.MethodPost, k.adminUrl("clients"),
		http.WithJsonRequest(jsontext.Value(representation.Bytes()), ""), http.WithCapturedResponseHeader(&header)))
	if err != nil {
		return "", err
	}
	_, id, _ := strings.CutLast(header.Get("Location"), "/")
	if id == "" {
		return "", fmt.Errorf("create client %s: Keycloak returned no Location header", clientId)
	}
	return id, nil
}

func (k *KeycloakWif) removeClientScopes(ctx context.Context, adminToken, id string) error {
	for _, kind := range []string{"default-client-scopes", "optional-client-scopes"} {
		scopes, err := http.UnmarshalJsonBody[[]struct {
			Id string `json:"id"`
		}](k.adminRequest(ctx, adminToken, http.MethodGet, k.adminUrl("clients", id, kind)))
		if err != nil {
			return err
		}
		for _, scope := range scopes {
			if err := http.IgnoreResponse(k.adminRequest(ctx, adminToken, http.MethodDelete, k.adminUrl("clients", id, kind, scope.Id))); err != nil {
				return err
			}
		}
	}
	return nil
}

func (k *KeycloakWif) deleteClient(ctx context.Context, adminToken, id, clientId string) {
	if err := http.IgnoreResponse(k.adminRequest(ctx, adminToken, http.MethodDelete, k.adminUrl("clients", id))); err != nil {
		slog.ErrorContext(ctx, "deleting the Keycloak client of the run failed, delete it by hand", "client", clientId, "error", err)
	}
}

func (k *KeycloakWif) requestToken(ctx context.Context, clientId, clientSecret string) (string, error) {
	token, err := http.UnmarshalJsonBody[struct {
		AccessToken string `json:"access_token"`
	}](k.http.DoRequest(ctx, http.MethodPost, k.issuer.JoinPath("protocol", "openid-connect", "token"), append(keycloakOptions,
		http.WithFormRequest(map[string]string{"grant_type": "client_credentials", "client_id": clientId, "client_secret": clientSecret}))...))
	if err != nil {
		return "", err
	}
	if token.AccessToken == "" {
		return "", errors.New("token response has no access_token")
	}
	return token.AccessToken, nil
}

func (k *KeycloakWif) adminUrl(segments ...string) *url.URL {
	return k.cfg.Url.JoinPath(append([]string{"admin", "realms", k.cfg.Realm}, segments...)...)
}

func (k *KeycloakWif) adminRequest(ctx context.Context, adminToken, method string, target *url.URL, options ...http.RequestOption) (io.ReadCloser, error) {
	return k.http.DoRequest(ctx, method, target, append(keycloakOptions, append(options, http.WithHeader("Authorization", "Bearer "+adminToken))...)...)
}
