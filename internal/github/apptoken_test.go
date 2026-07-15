package github

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// Test_AppToken pins that the minted token has header {alg:RS256,typ:JWT}, claims exactly
// {iat: now-10, exp: now+300, iss: appId}, and a signature that verifies RS256 against the
// public key.
func Test_AppToken(t *testing.T) {
	key, _ := testKey(t)
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	clock := newFakeClock(now)

	tok, err := appToken(clock, "123456", key)
	if err != nil {
		t.Fatalf("appToken: %v", err)
	}

	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("expected 3 JWS segments, got %d", len(parts))
	}

	header := decodeSegment(t, parts[0])
	if header["alg"] != "RS256" || header["typ"] != "JWT" {
		t.Errorf("header = %v; want alg=RS256 typ=JWT", header)
	}

	claims := decodeSegment(t, parts[1])
	if got := int64(asFloat(t, claims["iat"])); got != now.Unix()-10 {
		t.Errorf("iat = %d; want %d", got, now.Unix()-10)
	}
	if got := int64(asFloat(t, claims["exp"])); got != now.Unix()+300 {
		t.Errorf("exp = %d; want %d", got, now.Unix()+300)
	}
	if claims["iss"] != "123456" {
		t.Errorf("iss = %v; want 123456", claims["iss"])
	}

	// Signature verifies against the public key (RS256).
	signingInput := parts[0] + "." + parts[1]
	digest := sha256.Sum256([]byte(signingInput))
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatalf("decoding signature: %v", err)
	}
	if err := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, digest[:], sig); err != nil {
		t.Errorf("signature does not verify: %v", err)
	}
}

func decodeSegment(t *testing.T, seg string) map[string]any {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(seg)
	if err != nil {
		t.Fatalf("decoding segment: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshaling segment: %v", err)
	}
	return m
}

// Test_ParseAppPem pins the PKCS#1 tolerance table — multi-line, single-line and
// stray-space PEMs all parse; a PKCS#8 "BEGIN PRIVATE KEY" and garbage base64 fail.
func Test_ParseAppPem(t *testing.T) {
	key, multiline := testKey(t)

	// Build the single-line variant: strip armor + all whitespace, keep markers on one line.
	body := multiline
	body = strings.ReplaceAll(body, "-----BEGIN RSA PRIVATE KEY-----", "")
	body = strings.ReplaceAll(body, "-----END RSA PRIVATE KEY-----", "")
	body = removeAllWhitespace(body)
	singleLine := "-----BEGIN RSA PRIVATE KEY-----" + body + "-----END RSA PRIVATE KEY-----"
	// A variant with stray spaces sprinkled into the base64 body.
	spaced := "-----BEGIN RSA PRIVATE KEY-----\n" + body[:20] + "   " + body[20:] + "\n-----END RSA PRIVATE KEY-----"

	ok := []struct {
		name string
		pem  string
	}{
		{"multiline", multiline},
		{"single-line", singleLine},
		{"stray-spaces", spaced},
	}
	for _, tc := range ok {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseAppPem(tc.pem)
			if err != nil {
				t.Fatalf("parseAppPem(%s): %v", tc.name, err)
			}
			if got.N.Cmp(key.N) != 0 {
				t.Errorf("parsed key modulus mismatch for %s", tc.name)
			}
		})
	}

	bad := []struct {
		name string
		pem  string
	}{
		{"pkcs8", "-----BEGIN PRIVATE KEY-----\nMIIBVAIBADANBgkqhkiG9w0BAQEFAAS=\n-----END PRIVATE KEY-----"},
		{"garbage", "-----BEGIN RSA PRIVATE KEY-----\n!!!!notbase64!!!!\n-----END RSA PRIVATE KEY-----"},
		{"empty", ""},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseAppPem(tc.pem); err == nil {
				t.Errorf("parseAppPem(%s) succeeded; want error (⇒ FAILED-generic UX)", tc.name)
			}
		})
	}
}
