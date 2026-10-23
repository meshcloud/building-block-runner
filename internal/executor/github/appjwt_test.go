package github

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json/v2"
	"encoding/pem"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_Pkcs1AndPkcs8Keys_SignAnAppJwt(t *testing.T) {
	t.Parallel()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	pkcs8, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	now := time.Unix(1_800_000_000, 0)

	for name, block := range map[string]*pem.Block{
		"PKCS#1": {Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)},
		"PKCS#8": {Type: "PRIVATE KEY", Bytes: pkcs8},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			token, err := appJwt("4242", string(pem.EncodeToMemory(block)), now)
			require.NoError(t, err)

			parts := strings.Split(token, ".")
			require.Len(t, parts, 3)
			signature, err := base64.RawURLEncoding.DecodeString(parts[2])
			require.NoError(t, err)
			digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
			require.NoError(t, rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, digest[:], signature))
			payload, err := base64.RawURLEncoding.DecodeString(parts[1])
			require.NoError(t, err)
			var claims map[string]any
			require.NoError(t, json.Unmarshal(payload, &claims))
			assert.Equal(t, map[string]any{"iss": "4242", "iat": float64(now.Unix() - 60), "exp": float64(now.Unix() + 540)}, claims)
		})
	}
}

func Test_KeyThatIsNoRsaKey_IsRefused(t *testing.T) {
	t.Parallel()
	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	pkcs8, err := x509.MarshalPKCS8PrivateKey(ecKey)
	require.NoError(t, err)

	for name, appKey := range map[string]string{
		"no PEM": "not a key",
		"EC key": string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8})),
		"no key": string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("garbage")})),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := appJwt("4242", appKey, time.Now())
			assert.ErrorContains(t, err, "GitHub App key")
		})
	}
}
