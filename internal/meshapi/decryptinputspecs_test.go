package meshapi

import (
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	meshcrypto "github.com/meshcloud/building-block-runner/internal/crypto"
)

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func encryptForTest(t *testing.T, crypto *meshcrypto.MeshCertBasedCrypto, plaintext string) string {
	t.Helper()
	ciphertext, err := crypto.EncryptMeshCertBased(plaintext)
	require.NoError(t, err)
	return ciphertext
}

// Test_DecryptInputSpecs pins the F-P1 branch rules (Kotlin decryptBlockRunInputs
// asymmetry, umbrella §4 row 8/§7.6): sensitive STRING/CODE/FILE decrypted, other sensitive
// types left as ciphertext, non-sensitive inputs untouched, and the original slice never
// mutated (value-copy, P4).
func Test_DecryptInputSpecs(t *testing.T) {
	dec, crypto := testCryptoPair(t)

	stringCipher := encryptForTest(t, crypto, "secret-string")
	codeCipher := encryptForTest(t, crypto, "secret-code")
	fileCipher := encryptForTest(t, crypto, "secret-file")
	listCipher := encryptForTest(t, crypto, "secret-list")

	inputs := []BuildingBlockInputSpecDTO{
		{Key: "s", Value: stringCipher, Type: ioTypeString, IsSensitive: true},
		{Key: "c", Value: codeCipher, Type: ioTypeCode, IsSensitive: true},
		{Key: "f", Value: fileCipher, Type: ioTypeFile, IsSensitive: true},
		{Key: "l", Value: listCipher, Type: "LIST", IsSensitive: true}, // sensitive LIST: left as-is (quirk)
		{Key: "plain", Value: "not-sensitive", Type: ioTypeString, IsSensitive: false},
	}

	out, err := DecryptInputSpecs(dec, discardLog(), inputs)
	require.NoError(t, err)

	require.Equal(t, "secret-string", out[0].Value)
	require.Equal(t, "secret-code", out[1].Value)
	require.Equal(t, "secret-file", out[2].Value)
	require.Equal(t, listCipher, out[3].Value, "sensitive LIST is left encrypted (not STRING/CODE/FILE)")
	require.Equal(t, "not-sensitive", out[4].Value)

	// The original slice is untouched (value-copy semantics, P4).
	assert.Equal(t, stringCipher, inputs[0].Value)
}

func Test_DecryptInputSpecs_Empty(t *testing.T) {
	out, err := DecryptInputSpecs(NoopDecryptor{}, discardLog(), nil)
	require.NoError(t, err)
	assert.Nil(t, out)
}

func Test_DecryptInputSpecs_DecryptError(t *testing.T) {
	failing := failingDecryptor{}
	_, err := DecryptInputSpecs(failing, discardLog(), []BuildingBlockInputSpecDTO{
		{Key: "s", Value: "ciphertext", Type: ioTypeString, IsSensitive: true},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "s")
}

// Test_DecryptInputSpecs_NilLogFallsBackToDefault exercises the nil-log defaulting branch
// (P8: a constructed/called value is always usable).
func Test_DecryptInputSpecs_NilLogFallsBackToDefault(t *testing.T) {
	out, err := DecryptInputSpecs(NoopDecryptor{}, nil, []BuildingBlockInputSpecDTO{
		{Key: "k", Value: "v", Type: "BOOLEAN", IsSensitive: true},
	})
	require.NoError(t, err)
	assert.Equal(t, "v", out[0].Value)
}

type failingDecryptor struct{}

func (failingDecryptor) Decrypt(string) (string, error) {
	return "", assert.AnError
}
