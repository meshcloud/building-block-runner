package meshapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"
)

func discardSlog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// stubDecryptor appends "-decrypted" to whatever it is asked to decrypt, mirroring the
// Kotlin test double convention (GitLabBlockRunnerServiceTest.kt) so assertions can tell
// decrypted values apart from ciphertext without a real keypair.
type stubDecryptor struct{ fail bool }

func (s stubDecryptor) Decrypt(ciphertext string) (string, error) {
	if s.fail {
		return "", errors.New("stub decrypt failure")
	}
	return ciphertext + "-decrypted", nil
}

func runJSONFixture(t *testing.T, inputsJSON string) []byte {
	t.Helper()
	doc := `{
		"apiVersion": "v1", "kind": "MeshBuildingBlockRun",
		"metadata": {"uuid": "run-1"},
		"spec": {
			"runToken": "the-run-token",
			"buildingBlockDefinition": {
				"spec": {"implementation": {"type": "GITLAB_CICD", "pipelineTriggerToken": "still-ciphertext"}}
			},
			"buildingBlock": {"spec": {"inputs": ` + inputsJSON + `}}
		},
		"_links": {"self": {"href": "http://x/self"}}
	}`
	return []byte(doc)
}

// TestDecryptInputs_TypeRules pins the Kotlin decryptBlockRunInputs branch rules
// (MeshCertDecryptionService.kt:58-97): sensitive STRING/CODE/FILE are decrypted; other
// sensitive types are left as-is (with a warning); non-sensitive inputs are untouched.
func TestDecryptInputs_TypeRules(t *testing.T) {
	inputs := `[
		{"key":"s","value":"ENC(s)","type":"STRING","isSensitive":true,"isEnvironment":true},
		{"key":"c","value":"ENC(c)","type":"CODE","isSensitive":true,"isEnvironment":false},
		{"key":"f","value":"ENC(f)","type":"FILE","isSensitive":true,"isEnvironment":false},
		{"key":"i","value":"ENC(i)","type":"INTEGER","isSensitive":true,"isEnvironment":true},
		{"key":"plain","value":"visible","type":"STRING","isSensitive":false,"isEnvironment":true}
	]`
	raw := runJSONFixture(t, inputs)

	out, err := DecryptInputs(raw, stubDecryptor{}, discardSlog())
	require.NoError(t, err)

	var doc map[string]any
	dec := json.NewDecoder(bytes.NewReader(out))
	dec.UseNumber()
	require.NoError(t, dec.Decode(&doc))
	got, err := navigateToInputs(doc)
	require.NoError(t, err)
	byKey := map[string]any{}
	for _, raw := range got {
		m, ok := raw.(map[string]any)
		require.True(t, ok)
		key, ok := m["key"].(string)
		require.True(t, ok)
		byKey[key] = m["value"]
	}

	require.Equal(t, "ENC(s)-decrypted", byKey["s"])
	require.Equal(t, "ENC(c)-decrypted", byKey["c"])
	require.Equal(t, "ENC(f)-decrypted", byKey["f"])
	require.Equal(t, "ENC(i)", byKey["i"], "sensitive non-STRING/CODE/FILE is left as-is")
	require.Equal(t, "visible", byKey["plain"], "non-sensitive input is untouched")
}

// TestDecryptInputs_ImplSecretUntouched is the §7.6 leak pin at the shared-helper level:
// DecryptInputs must never touch the implementation object, so a caller building an
// outbound payload from its output cannot leak a decrypted impl secret.
func TestDecryptInputs_ImplSecretUntouched(t *testing.T) {
	raw := runJSONFixture(t, `[]`)
	out, err := DecryptInputs(raw, stubDecryptor{}, discardSlog())
	require.NoError(t, err)
	require.Contains(t, string(out), "still-ciphertext")
	require.NotContains(t, string(out), "still-ciphertext-decrypted")
	require.Contains(t, string(out), "the-run-token", "runToken and other fields pass through untouched")
}

// TestDecryptInputs_NumberFidelity proves the generic-JSON transform preserves large
// integers byte-faithfully (UseNumber, not float64) -- the same requirement 06A recorded
// for gitlab/github (06A §4.2/§17).
func TestDecryptInputs_NumberFidelity(t *testing.T) {
	raw := runJSONFixture(t, `[{"key":"big","value":123456789012345678901234,"type":"INTEGER","isSensitive":false,"isEnvironment":true}]`)
	out, err := DecryptInputs(raw, stubDecryptor{}, discardSlog())
	require.NoError(t, err)
	require.Contains(t, string(out), "123456789012345678901234")
}

// TestDecryptInputs_NoopIdentity: with the NoOp decryptor (single-run mode) the transform
// is identity modulo re-marshaling -- values are unchanged.
func TestDecryptInputs_NoopIdentity(t *testing.T) {
	raw := runJSONFixture(t, `[{"key":"s","value":"already-plaintext","type":"STRING","isSensitive":true,"isEnvironment":true}]`)
	out, err := DecryptInputs(raw, NoopDecryptor{}, discardSlog())
	require.NoError(t, err)
	require.Contains(t, string(out), "already-plaintext")
}

// TestDecryptInputs_DecryptError propagates a real decrypt failure as an error.
func TestDecryptInputs_DecryptError(t *testing.T) {
	dec, _ := testCryptoPair(t)
	raw := runJSONFixture(t, `[{"key":"s","value":"not-valid-ciphertext","type":"STRING","isSensitive":true,"isEnvironment":true}]`)
	_, err := DecryptInputs(raw, dec, discardSlog())
	require.Error(t, err)
}

// TestDecryptInputs_MalformedJSON / missing-shape cover the navigation error paths.
func TestDecryptInputs_MalformedJSON(t *testing.T) {
	_, err := DecryptInputs([]byte("{not json"), NoopDecryptor{}, discardSlog())
	require.Error(t, err)
}

func TestDecryptInputs_MissingSpecShape(t *testing.T) {
	for _, doc := range []string{`{}`, `{"spec":{}}`, `{"spec":{"buildingBlock":{}}}`} {
		_, err := DecryptInputs([]byte(doc), NoopDecryptor{}, discardSlog())
		require.Error(t, err, doc)
	}
}

// TestDecryptInputs_NoInputsArray covers a building block with no inputs field at all --
// not an error, nothing to decrypt.
func TestDecryptInputs_NoInputsArray(t *testing.T) {
	out, err := DecryptInputs([]byte(`{"spec":{"buildingBlock":{"spec":{}}}}`), NoopDecryptor{}, discardSlog())
	require.NoError(t, err)
	require.Contains(t, string(out), "buildingBlock")
}

// TestDecryptInputs_NilLogger covers the nil-logger defaulting branch.
func TestDecryptInputs_NilLogger(t *testing.T) {
	raw := runJSONFixture(t, `[{"key":"i","value":"x","type":"BOOLEAN","isSensitive":true,"isEnvironment":true}]`)
	_, err := DecryptInputs(raw, stubDecryptor{}, nil)
	require.NoError(t, err)
}

// TestDecryptInputs_NonStringSensitiveValue defensively skips a STRING-typed sensitive
// input whose value isn't itself a JSON string (not a well-formed claim, but must not
// panic or corrupt the document).
func TestDecryptInputs_NonStringSensitiveValue(t *testing.T) {
	raw := runJSONFixture(t, `[{"key":"weird","value":42,"type":"STRING","isSensitive":true,"isEnvironment":true}]`)
	out, err := DecryptInputs(raw, stubDecryptor{}, discardSlog())
	require.NoError(t, err)
	require.Contains(t, string(out), `"value":42`)
}
