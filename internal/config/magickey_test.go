package config

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v2"

	"github.com/meshcloud/building-block-runner/internal/meshapi"
)

// TestRunControllerConfig_PrivateKeyIsValidPEM guards the well-known local-dev magic key. It
// used to live in the shared base containers/runner-config.yml (now deleted); post-consolidation
// it lives ONLY in the run-controller's own config (cmd/bbrunner/runner-config.yml), where the
// superset/controller boundary decryptor reads it. The Kotlin classpath yaml ships the identical
// key material as one unbroken base64 line, which Go's stdlib encoding/pem (unlike Kotlin's own
// hand-rolled loader) refuses to parse -- this repo's copy is re-wrapped into standard multi-line
// PEM (same DER bytes, different textual wrapping) so config.ResolvePrivateKey's inline-key
// fallback actually works. This test fails loudly if that key is ever hand-edited back into an
// unwrapped single line.
func TestRunControllerConfig_PrivateKeyIsValidPEM(t *testing.T) {
	data, err := os.ReadFile("../../cmd/bbrunner/runner-config.yml")
	require.NoError(t, err)

	var doc struct {
		Crypto struct {
			PrivateKey string `yaml:"privateKey"`
		} `yaml:"crypto"`
	}
	require.NoError(t, yaml.Unmarshal(data, &doc))
	require.NotEmpty(t, doc.Crypto.PrivateKey)

	pem, err := ResolvePrivateKey(discardLog(), "", doc.Crypto.PrivateKey)
	require.NoError(t, err)
	require.Equal(t, doc.Crypto.PrivateKey, pem)

	// The real test: the key must actually parse as a usable RSA private key (Go's stdlib
	// encoding/pem is stricter than Kotlin's hand-rolled loader about line wrapping).
	_, err = meshapi.NewCertDecryptor(pem)
	require.NoError(t, err)
}
