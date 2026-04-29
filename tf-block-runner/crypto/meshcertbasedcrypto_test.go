package crypto

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
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
