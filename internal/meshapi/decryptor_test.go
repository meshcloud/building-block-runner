package meshapi

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	meshcrypto "github.com/meshcloud/building-block-runner/internal/crypto"
)

// testCryptoPair builds a real CertDecryptor plus a matching encryptor for round-trip
// tests, reusing the same fixture keypair internal/crypto's own tests use.
func testCryptoPair(t *testing.T) (Decryptor, *meshcrypto.MeshCertBasedCrypto) {
	t.Helper()
	pubKey, err := os.ReadFile("../resources/test.pem")
	require.NoError(t, err)
	privKeyPEM, err := os.ReadFile("../resources/test.key")
	require.NoError(t, err)

	full, err1, err2 := meshcrypto.NewCertBasedCrypto("../resources/test.key", pubKey)
	require.NoError(t, err1)
	require.NoError(t, err2)

	dec, err := NewCertDecryptor(string(privKeyPEM))
	require.NoError(t, err)
	return dec, full
}

// TestCertDecryptor_RoundTrip cross-checks against a real ciphertext produced by the same
// algorithm the Kotlin MeshCertDecryptionService implements: encrypt then
// decrypt through the shared seam and recover the plaintext.
func TestCertDecryptor_RoundTrip(t *testing.T) {
	dec, full := testCryptoPair(t)

	ciphertext, err := full.EncryptMeshCertBased("s3cr3t-token")
	require.NoError(t, err)

	plaintext, err := dec.Decrypt(ciphertext)
	require.NoError(t, err)
	require.Equal(t, "s3cr3t-token", plaintext)
}

// TestCertDecryptor_EmptyString pins that Kotlin's decrypt("") returns "" rather than
// erroring, so an empty pipeline-trigger-token still produces a (present, empty) token
// field instead of failing the run.
func TestCertDecryptor_EmptyString(t *testing.T) {
	dec, _ := testCryptoPair(t)
	got, err := dec.Decrypt("")
	require.NoError(t, err)
	require.Empty(t, got)
}

// TestCertDecryptor_InvalidCiphertext proves non-empty garbage still errors (the empty-
// string guard must not swallow real decrypt failures).
func TestCertDecryptor_InvalidCiphertext(t *testing.T) {
	dec, _ := testCryptoPair(t)
	_, err := dec.Decrypt("not-valid-base64!!!")
	require.Error(t, err)
}

// TestNewCertDecryptor_InvalidKey covers the constructor's error path.
func TestNewCertDecryptor_InvalidKey(t *testing.T) {
	_, err := NewCertDecryptor("not a pem")
	require.Error(t, err)
}

func TestNoopDecryptor_Identity(t *testing.T) {
	got, err := NoopDecryptor{}.Decrypt("anything, even empty")
	require.NoError(t, err)
	require.Equal(t, "anything, even empty", got)

	got, err = NoopDecryptor{}.Decrypt("")
	require.NoError(t, err)
	require.Empty(t, got)
}
