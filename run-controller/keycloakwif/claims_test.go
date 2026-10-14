package keycloakwif

import (
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

const testIssuer = "https://id-dev.meshstack.io/realms/meshcloud-dev-wif"

const testSubject = "bbd:my-workspace:0d43a2b8-aaaa-bbbb-cccc-1234567890ab"

var testNow = time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)

func tokenWith(claims map[string]any) string {
	payload, _ := json.Marshal(claims)
	return "header." + base64.RawURLEncoding.EncodeToString(payload) + ".signature"
}

func validClaims() map[string]any {
	return map[string]any{
		"iss": testIssuer,
		"sub": testSubject,
		"aud": Audience,
		"azp": Audience,
		"exp": testNow.Add(90 * time.Minute).Unix(),
		"iat": testNow.Unix(),
		"jti": "id",
		"typ": "Bearer",
	}
}

func Test_Claims_Accepted(t *testing.T) {
	for name, change := range map[string]func(map[string]any){
		"as issued":            func(map[string]any) {},
		"aud as array":         func(c map[string]any) { c["aud"] = []string{Audience} },
		"empty scope":          func(c map[string]any) { c["scope"] = "" },
		"lifetime within skew": func(c map[string]any) { c["exp"] = testNow.Add(89 * time.Minute).Unix() },
		"issued an hour ago": func(c map[string]any) {
			c["iat"], c["exp"] = testNow.Add(-time.Hour).Unix(), testNow.Add(30*time.Minute).Unix()
		},
	} {
		t.Run(name, func(t *testing.T) {
			claims := validClaims()
			change(claims)
			assert.NoError(t, checkClaims(tokenWith(claims), testIssuer, testSubject, 90*time.Minute))
		})
	}
}

func Test_Claims_Rejected(t *testing.T) {
	for name, testCase := range map[string]struct {
		change func(map[string]any)
		error  string
	}{
		"issuer over http":     {func(c map[string]any) { c["iss"] = "http://id-dev.meshstack.io/realms/meshcloud-dev-wif" }, "token iss"},
		"service account sub":  {func(c map[string]any) { c["sub"] = "f0e1d2c3" }, "token sub"},
		"azp is the client id": {func(c map[string]any) { c["azp"] = "bb-run-1" }, "token azp"},
		"aud missing":          {func(c map[string]any) { delete(c, "aud") }, "token aud"},
		"second audience":      {func(c map[string]any) { c["aud"] = []string{Audience, "account"} }, "token aud"},
		"client_id claim":      {func(c map[string]any) { c["client_id"] = "bb-run-1" }, `unexpected claim "client_id"`},
		"pod ip claim":         {func(c map[string]any) { c["clientAddress"] = "10.0.0.1" }, `unexpected claim "clientAddress"`},
		"realm roles":          {func(c map[string]any) { c["realm_access"] = map[string]any{} }, `unexpected claim "realm_access"`},
		"exp missing":          {func(c map[string]any) { delete(c, "exp") }, "no exp"},
		"iat missing":          {func(c map[string]any) { delete(c, "iat") }, "no iat"},
		"lifetime cut":         {func(c map[string]any) { c["exp"] = testNow.Add(10 * time.Minute).Unix() }, "token lifetime is 10m0s"},
		"realm lifespan":       {func(c map[string]any) { c["exp"] = testNow.Add(24 * time.Hour).Unix() }, "token lifetime is 24h0m0s"},
	} {
		t.Run(name, func(t *testing.T) {
			claims := validClaims()
			testCase.change(claims)
			assert.ErrorContains(t, checkClaims(tokenWith(claims), testIssuer, testSubject, 90*time.Minute), testCase.error)
		})
	}
}

func Test_NotAJwt_Rejected(t *testing.T) {
	assert.ErrorContains(t, checkClaims("opaque-token", testIssuer, testSubject, time.Hour), "not a JWT")
}
