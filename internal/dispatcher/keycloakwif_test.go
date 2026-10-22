package dispatcher

import (
	"encoding/base64"
	"encoding/json/v2"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
	"uuid"

	"github.com/meshcloud/meshstack-cli/client"
	"github.com/meshcloud/meshstack-cli/client/types/xurl"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/config/xtime"
)

const (
	testIssuer  = "https://id.example.com/realms/wif"
	testSubject = "bbd:my-workspace:0d43a2b8-aaaa-bbbb-cccc-1234567890ab"
)

func Test_KeycloakRedirect_IsNotFollowedWithTheSecret(t *testing.T) {
	elsewhere := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		t.Errorf("the redirect was followed with %q", body)
	}))
	t.Cleanup(elsewhere.Close)
	keycloak := httptest.NewServer(http.RedirectHandler(elsewhere.URL, http.StatusPermanentRedirect))
	t.Cleanup(keycloak.Close)
	wif := NewKeycloakWif(config.KeycloakWif{
		Url: xurl.MustParsef("%s", keycloak.URL), Realm: "wif", ClientId: "runner", ClientSecret: "admin-secret", TokenLifetime: xtime.Minutes(90),
	})

	_, err := wif.Issue(t.Context(), Run{Metadata: client.MeshRunnerRunMetadata{Uuid: uuid.New()}})

	require.ErrorContains(t, err, "HTTP 308")
}

func Test_Claims_RejectsATokenThatIsNotExactlyTheRunsToken(t *testing.T) {
	now := time.Now()
	for name, testCase := range map[string]struct {
		change func(claims map[string]any)
		error  string
	}{
		"issuer over http":     {func(c map[string]any) { c["iss"] = "http://id.example.com/realms/wif" }, "token iss"},
		"service account sub":  {func(c map[string]any) { c["sub"] = "f0e1d2c3" }, "token sub"},
		"azp is the client id": {func(c map[string]any) { c["azp"] = "bb-run-1" }, "token azp"},
		"aud missing":          {func(c map[string]any) { delete(c, "aud") }, "token aud"},
		"second audience":      {func(c map[string]any) { c["aud"] = []string{keycloakWifAudience, "account"} }, "token aud"},
		"client_id claim":      {func(c map[string]any) { c["client_id"] = "bb-run-1" }, `unexpected claim "client_id"`},
		"client address claim": {func(c map[string]any) { c["clientAddress"] = "10.0.0.1" }, `unexpected claim "clientAddress"`},
		"realm roles":          {func(c map[string]any) { c["realm_access"] = map[string]any{} }, `unexpected claim "realm_access"`},
		"exp missing":          {func(c map[string]any) { delete(c, "exp") }, "no exp"},
		"iat missing":          {func(c map[string]any) { delete(c, "iat") }, "no iat"},
		"lifetime cut":         {func(c map[string]any) { c["exp"] = now.Add(10 * time.Minute).Unix() }, "token lifetime is 10m0s"},
		"realm lifespan":       {func(c map[string]any) { c["exp"] = now.Add(24 * time.Hour).Unix() }, "token lifetime is 24h0m0s"},
		"no JWT":               {nil, "not a JWT"},
	} {
		t.Run(name, func(t *testing.T) {
			claims := map[string]any{
				"iss": testIssuer, "sub": testSubject, "aud": keycloakWifAudience, "azp": keycloakWifAudience,
				"exp": now.Add(90 * time.Minute).Unix(), "iat": now.Unix(), "jti": "id", "typ": "Bearer",
			}
			token := "opaque-token"
			if testCase.change != nil {
				testCase.change(claims)
				payload, err := json.Marshal(claims)
				require.NoError(t, err)
				token = "header." + base64.RawURLEncoding.EncodeToString(payload) + ".signature"
			}

			assert.ErrorContains(t, checkClaims(token, testIssuer, testSubject, 90*time.Minute), testCase.error)
		})
	}
}
