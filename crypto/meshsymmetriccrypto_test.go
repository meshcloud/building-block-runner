package crypto

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_SymmetricCrypto(t *testing.T) {

	symCrypto, err := NewSymmetricCrypto([]byte("16byteLongSecret"))
	if err != nil {
		t.Errorf("failed to create symmetric crypto instance.")
		return
	}

	plaintext := "foobar"
	encrypted, err := symCrypto.EncryptSymmetric(plaintext)
	if err != nil {
		t.Errorf("Test was not supposed to fail on encryption.")
		return
	}

	result, err := symCrypto.DecryptSymmetric(encrypted)
	if err != nil {
		t.Errorf("Test was not supposed to fail on decryption.")
		return
	}

	assert.Equal(t, plaintext, result)
}
