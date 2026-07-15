package github

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// appToken mints the GitHub App JWT: base64url(header) "." base64url(claims)
// "." base64url(signature), signed RS256 (SHA-256 + PKCS#1 v1.5). Header is the auth0
// library default {"alg":"RS256","typ":"JWT"}; claims are exactly {iat: now-10, exp:
// now+300, iss: appId} in epoch seconds — no jti, no audience. There is no JWT dependency:
// the JWS is three base64url segments plus one
// rsa.SignPKCS1v15 call, and the code only ever *signs* (never verifies untrusted tokens),
// so a claims/validation framework buys nothing. Verification in tests uses
// rsa.VerifyPKCS1v15.
//
// The 10s iat skew and 300s exp window are FROZEN toward GitHub's App-auth acceptance.
func appToken(clock Clock, appID string, key *rsa.PrivateKey) (string, error) {
	now := clock.Now().Unix()

	header := map[string]string{"alg": "RS256", "typ": "JWT"}
	claims := map[string]any{
		"iat": now - 10,
		"exp": now + 300,
		"iss": appID,
	}

	headerJSON, err := json.Marshal(header)
	if err != nil {
		return "", fmt.Errorf("marshaling JWT header: %w", err)
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("marshaling JWT claims: %w", err)
	}

	signingInput := b64url(headerJSON) + "." + b64url(claimsJSON)

	digest := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("signing JWT: %w", err)
	}

	return signingInput + "." + b64url(sig), nil
}

// b64url is the JWS base64url encoding: standard base64url alphabet, no padding.
func b64url(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}

// parseAppPem reproduces the Kotlin PKCS#1 tolerance: it is whitespace-
// tolerant string surgery, NOT strict PEM. It strips the literal
// "-----BEGIN/END RSA PRIVATE KEY-----" armor lines, removes ALL whitespace, base64-decodes
// and parses the ASN.1 RSAPrivateKey. Consequences preserved from Kotlin:
//   - a single-line PEM pasted without newlines parses fine (encoding/pem would reject it,
//     so we deliberately do not use pem.Decode);
//   - a PKCS#8 "-----BEGIN PRIVATE KEY-----" PEM fails (markers don't match ⇒ garbage
//     base64/ASN.1 ⇒ error ⇒ the FAILED-generic UX). GitHub Apps issue PKCS#1 PEMs.
//
// Divergence: Kotlin rebuilds the key from modulus+privateExponent
// only, while x509.ParsePKCS1PrivateKey validates the full CRT key — a corrupted-CRT PEM
// signs on the JVM and errors here (same FAILED UX, different trigger point; theoretical).
func parseAppPem(pem string) (*rsa.PrivateKey, error) {
	body := pem
	body = strings.ReplaceAll(body, "-----BEGIN RSA PRIVATE KEY-----", "")
	body = strings.ReplaceAll(body, "-----END RSA PRIVATE KEY-----", "")
	body = removeAllWhitespace(body)

	der, err := base64.StdEncoding.DecodeString(body)
	if err != nil {
		return nil, err2("decoding PKCS#1 base64", err)
	}
	key, err := x509.ParsePKCS1PrivateKey(der)
	if err != nil {
		return nil, err2("parsing PKCS#1 private key", err)
	}
	return key, nil
}

// removeAllWhitespace drops every unicode whitespace rune, matching the Kotlin
// replace("\\s".toRegex(), "") (newlines AND spaces AND tabs).
func removeAllWhitespace(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case ' ', '\t', '\n', '\r', '\v', '\f':
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func err2(context string, err error) error {
	return fmt.Errorf("%s: %w", context, err)
}
