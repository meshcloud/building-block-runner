package secret

import (
	"errors"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	meshcrypto "github.com/meshcloud/building-block-runner/internal/crypto"
)

// fakeDecryptor appends "-dec" to whatever it is asked to decrypt, so tests can tell a
// decrypted value apart from its ciphertext without a real keypair.
type fakeDecryptor struct{ err error }

func (f fakeDecryptor) Decrypt(ciphertext string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return ciphertext + "-dec", nil
}

func TestNoopDecryptor_Identity(t *testing.T) {
	got, err := NoopDecryptor{}.Decrypt("anything, even empty")
	require.NoError(t, err)
	assert.Equal(t, "anything, even empty", got)

	got, err = NoopDecryptor{}.Decrypt("")
	require.NoError(t, err)
	assert.Empty(t, got)
}

// certPair builds a real CertDecryptor plus a matching encryptor over the checked-in fixture
// keypair (the same fixtures internal/crypto's own tests use).
func certPair(t *testing.T) (Decryptor, *meshcrypto.MeshCertBasedCrypto) {
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

func TestCertDecryptor_RoundTripAndEmptyGuard(t *testing.T) {
	dec, full := certPair(t)

	ciphertext, err := full.EncryptMeshCertBased("s3cr3t-token")
	require.NoError(t, err)
	plaintext, err := dec.Decrypt(ciphertext)
	require.NoError(t, err)
	assert.Equal(t, "s3cr3t-token", plaintext)

	// empty ciphertext -> "" (Kotlin decrypt("") parity), not an error.
	got, err := dec.Decrypt("")
	require.NoError(t, err)
	assert.Empty(t, got)

	// non-empty garbage still errors: the empty guard must not swallow real failures.
	_, err = dec.Decrypt("not-valid-base64!!!")
	require.Error(t, err)
}

func TestNewCertDecryptorFromCrypto(t *testing.T) {
	_, full := certPair(t)
	dec := NewCertDecryptorFromCrypto(full)
	ciphertext, err := full.EncryptMeshCertBased("via-crypto")
	require.NoError(t, err)
	got, err := dec.Decrypt(ciphertext)
	require.NoError(t, err)
	assert.Equal(t, "via-crypto", got)
}

func TestNewCertDecryptor_InvalidKey(t *testing.T) {
	_, err := NewCertDecryptor("not a pem")
	require.Error(t, err)
}

func TestDecryptable(t *testing.T) {
	tests := []struct {
		ioType string
		want   bool
	}{
		{"STRING", true},
		{"CODE", true},
		{"FILE", true},
		{"LIST", false},
		{"BOOLEAN", false},
		{"INTEGER", false},
		{"SINGLE_SELECT", false},
		{"MULTI_SELECT", false},
		{"", false},
	}
	for _, tc := range tests {
		assert.Equalf(t, tc.want, Decryptable(tc.ioType), "Decryptable(%q)", tc.ioType)
	}
}

func TestDecryptValue_HappyPath(t *testing.T) {
	for _, ioType := range []string{TypeString, TypeCode, TypeFile} {
		got, err := DecryptValue(fakeDecryptor{}, "k", ioType, "cipher")
		require.NoError(t, err, ioType)
		assert.Equal(t, "cipher-dec", got, ioType)
	}
}

func TestDecryptValue_RendersNonStringValue(t *testing.T) {
	// A STRING-typed value that arrived as a json.Number-style scalar is rendered via
	// valuestring.Render before decryption.
	got, err := DecryptValue(fakeDecryptor{}, "k", TypeString, true)
	require.NoError(t, err)
	assert.Equal(t, "true-dec", got)
}

func TestDecryptValue_UnsupportedType(t *testing.T) {
	for _, ioType := range []string{"LIST", "INTEGER", "BOOLEAN", "SINGLE_SELECT", "MULTI_SELECT"} {
		_, err := DecryptValue(fakeDecryptor{}, "bad", ioType, "cipher")
		require.Error(t, err, ioType)
		var uerr UnsupportedSensitiveTypeError
		require.ErrorAs(t, err, &uerr)
		assert.Equal(t, "bad", uerr.Key)
		assert.Equal(t, ioType, uerr.Type)
		assert.Contains(t, err.Error(), "only STRING, CODE and FILE inputs may be encrypted")
	}
}

func TestDecryptValue_PropagatesDecryptError(t *testing.T) {
	sentinel := errors.New("boom")
	_, err := DecryptValue(fakeDecryptor{err: sentinel}, "k", TypeString, "cipher")
	require.ErrorIs(t, err, sentinel)
}
