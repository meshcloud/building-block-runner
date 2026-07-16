package crypto

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test_NormalizePEM_SingleLine pins the config-compat fix: a single-line PEM (no newline
// after BEGIN/before END, the shape Kotlin's runner-config.yml ships) is re-wrapped into a
// standard multi-line PEM.
func Test_NormalizePEM_SingleLine(t *testing.T) {
	singleLine := "-----BEGIN PRIVATE KEY-----MIIBVgIBADANBgkq-----END PRIVATE KEY-----"
	out, ok := normalizePEM([]byte(singleLine))
	require.True(t, ok)
	assert.Contains(t, string(out), "-----BEGIN PRIVATE KEY-----\n")
	assert.Contains(t, string(out), "\n-----END PRIVATE KEY-----\n")
}

func Test_NormalizePEM_NoMarkers(t *testing.T) {
	_, ok := normalizePEM([]byte("not a pem at all"))
	require.False(t, ok)
}

func Test_NormalizePEM_MismatchedTypes(t *testing.T) {
	_, ok := normalizePEM([]byte("-----BEGIN PRIVATE KEY-----abc-----END CERTIFICATE-----"))
	require.False(t, ok)
}

// Test_ReadRSAPrivateKey_SingleLineFallback proves the end-to-end fix: the exact single-line
// PEM shape shipped in the azure-devops-block-runner container's baked dev key parses
// successfully via readRSAPrivateKey (through NewCertBasedDecryptor), where a bare
// pem.Decode would fail.
func Test_ReadRSAPrivateKey_SingleLineFallback(t *testing.T) {
	multiLine, err := os.ReadFile("../resources/test.key")
	require.NoError(t, err)

	singleLine := collapseToSingleLine(t, string(multiLine))
	_, err = NewCertBasedDecryptor(singleLine)
	require.NoError(t, err, "a single-line PEM (Kotlin runner-config.yml shape) must still parse")
}

// collapseToSingleLine mirrors what a Kotlin-era runner-config.yml literally stores: markers
// glued directly onto the base64 body, no internal newlines.
func collapseToSingleLine(t *testing.T, pem string) string {
	t.Helper()
	out := ""
	for _, line := range splitLinesForTest(pem) {
		out += line
	}
	return out
}

func splitLinesForTest(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}
