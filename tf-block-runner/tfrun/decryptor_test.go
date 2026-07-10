package tfrun

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test_Decryptor covers the D4 Decryptor seam that replaced the meshcrypto.Crypto global:
// NoopDecryptor passthrough, the cert-based decryptor's constructor (success + error), and a
// genuine encrypt→decrypt round trip with the checked-in test key pair.
func Test_Decryptor(t *testing.T) {
	t.Run("NoopDecryptor passes ciphertext through unchanged", func(t *testing.T) {
		got, err := NoopDecryptor{}.Decrypt("anything")
		require.NoError(t, err)
		assert.Equal(t, "anything", got)
	})

	t.Run("NewCertDecryptor round-trips a value encrypted with the matching public key", func(t *testing.T) {
		privateKeyPEM, err := os.ReadFile("../resources/test.key")
		require.NoError(t, err)

		dec, err := NewCertDecryptor(string(privateKeyPEM))
		require.NoError(t, err)

		ciphertext := encryptForTest(t, testCrypto(t), "top-secret")
		got, err := dec.Decrypt(ciphertext)
		require.NoError(t, err)
		assert.Equal(t, "top-secret", got)
	})

	t.Run("NewCertDecryptor fails on an unparsable private key", func(t *testing.T) {
		_, err := NewCertDecryptor("not a valid pem private key")
		assert.Error(t, err)
	})
}
