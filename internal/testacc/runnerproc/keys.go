package runnerproc

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"testing"

	"github.com/stretchr/testify/require"
)

type KeyPair struct {
	PrivateKeyPem string
	PublicKeyPem  string
}

func NewKeyPair(t *testing.T) KeyPair {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	privateDer, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	publicDer, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	require.NoError(t, err)
	return KeyPair{
		PrivateKeyPem: string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDer})),
		PublicKeyPem:  string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDer})),
	}
}
