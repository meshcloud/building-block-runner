package config

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v2"

	"github.com/meshcloud/building-block-runner/internal/meshapi"
)

// TestSharedBaseConfig_PrivateKeyIsValidPEM guards the shared top-level base
// containers/runner-config.yml (plan 06B §6.3, umbrella §10.5): the well-known dev
// private key must parse as a standard PEM block. The Kotlin classpath yaml ships the
// identical key material as one unbroken base64 line, which Go's stdlib encoding/pem
// (unlike Kotlin's own hand-rolled loader) refuses to parse -- this repo's copy is
// re-wrapped into standard multi-line PEM (same DER bytes, different textual wrapping) so
// config.ResolvePrivateKey's inline-key fallback actually works. This test fails loudly if
// that file is ever hand-edited back into an unwrapped single line.
func TestSharedBaseConfig_PrivateKeyIsValidPEM(t *testing.T) {
	data, err := os.ReadFile("../../containers/runner-config.yml")
	require.NoError(t, err)

	var doc struct {
		PrivateKey string `yaml:"privateKey"`
	}
	require.NoError(t, yaml.Unmarshal(data, &doc))
	require.NotEmpty(t, doc.PrivateKey)

	pem, err := ResolvePrivateKey(discardLog(), "", doc.PrivateKey)
	require.NoError(t, err)
	require.Equal(t, doc.PrivateKey, pem)

	// The real test: the key must actually parse as a usable RSA private key (Go's stdlib
	// encoding/pem is stricter than Kotlin's hand-rolled loader about line wrapping).
	_, err = meshapi.NewCertDecryptor(pem)
	require.NoError(t, err)
}
