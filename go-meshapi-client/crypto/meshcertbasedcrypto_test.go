package crypto

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_CertBasedCrypto(t *testing.T) {

	pubKey, err := os.ReadFile("../resources/test.pem")
	if err != nil {
		t.Errorf("failed to read public key file.")
		return
	}

	certBasedCrypto, err1, err2 := NewCertBasedCrypto("../resources/test.key", pubKey)
	if err1 != nil || err2 != nil {
		t.Errorf("expected no error during CertBasedCrypto initialization")
		return
	}

	plaintext := "foobar"
	encrypted, err := certBasedCrypto.EncryptMeshCertBased(plaintext)
	if err != nil {
		t.Errorf("Test was not supposed to fail on encryption.")
		return
	}

	result, err := certBasedCrypto.DecryptMeshCertBased(encrypted)
	if err != nil {
		t.Errorf("Test was not supposed to fail on decryption.")
		return
	}

	assert.Equal(t, plaintext, result)
}

func Test_readRSAPrivateKey_PKCS1(t *testing.T) {
	keyContent, err := os.ReadFile("../resources/pkcs1_test.key")
	require.NoError(t, err, "failed to read PKCS#1 test key")

	key, err := readRSAPrivateKey(keyContent)
	require.NoError(t, err, "expected no error parsing PKCS#1 key")
	assert.NotNil(t, key, "expected non-nil RSA key")
}

func Test_readRSAPrivateKey_PKCS8RSA(t *testing.T) {
	keyContent, err := os.ReadFile("../resources/pkcs8_rsa_test.key")
	require.NoError(t, err, "failed to read PKCS#8 RSA test key")

	key, err := readRSAPrivateKey(keyContent)
	require.NoError(t, err, "expected no error parsing PKCS#8 RSA key")
	assert.NotNil(t, key, "expected non-nil RSA key")
}

func Test_readRSAPrivateKey_PKCS8NonRSA(t *testing.T) {
	keyContent, err := os.ReadFile("../resources/pkcs8_ec_test.key")
	require.NoError(t, err, "failed to read PKCS#8 ECDSA test key")

	key, err := readRSAPrivateKey(keyContent)
	assert.Nil(t, key, "expected nil key for non-RSA key")
	require.Error(t, err, "expected error parsing non-RSA PKCS#8 key")
	assert.Equal(t, "private key is not an RSA key", err.Error(), "expected specific error message for non-RSA key")
}

func Test_DecryptMeshCertBased_MissingPrivateKey(t *testing.T) {
	crypto := &MeshCertBasedCrypto{
		publicKey:  nil,
		privateKey: nil,
	}

	result, err := crypto.DecryptMeshCertBased("encrypted_data")
	require.Error(t, err, "expected error when decrypting with missing private key")
	assert.Empty(t, result, "expected empty result on error")
	assert.Equal(t, "cannot decrypt sensitive input as private key is missing", err.Error(), "expected specific error message")
}
