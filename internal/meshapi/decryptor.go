package meshapi

import (
	meshcrypto "github.com/meshcloud/building-block-runner/internal/crypto"
	"github.com/meshcloud/building-block-runner/internal/secret"
)

// Decryptor, NoopDecryptor and the cert-based constructors now live in the shared
// internal/secret package (the single home for the decrypt seam and the sensitive-input type
// policy). These aliases and thin wrappers keep the historic meshapi.* call sites
// (meshapi.Decryptor, meshapi.NoopDecryptor, meshapi.NewCertDecryptor,
// meshapi.NewCertDecryptorFromCrypto) working unchanged.

// Decryptor decrypts a single sensitive-value ciphertext (secret.Decryptor).
type Decryptor = secret.Decryptor

// NoopDecryptor is the identity decryptor for single-run mode (secret.NoopDecryptor).
type NoopDecryptor = secret.NoopDecryptor

// NewCertDecryptor builds the polling-mode Decryptor from the runner's private key PEM.
func NewCertDecryptor(privateKeyPEM string) (Decryptor, error) {
	return secret.NewCertDecryptor(privateKeyPEM)
}

// NewCertDecryptorFromCrypto adapts an already-constructed cert crypto instance (e.g. the
// run-controller's validated key pair from meshcrypto.NewCertBasedDecryptorWithValidation)
// into a Decryptor, so a caller that owns the crypto instance can hand it to
// DecryptRunDetails without meshapi re-loading the key. The empty-string guard is
// CertDecryptor's (Kotlin decrypt("") == ""); DecryptRunDetails never passes an empty value.
func NewCertDecryptorFromCrypto(c *meshcrypto.MeshCertBasedCrypto) Decryptor {
	return secret.NewCertDecryptorFromCrypto(c)
}
