package tf

import (
	"github.com/meshcloud/building-block-runner/internal/secret"
)

// The decrypt seam now lives in the shared internal/secret package (the single home for the
// decryptor and the sensitive-input type policy). These aliases and the thin constructor keep
// every existing tf call site and assertion unchanged while removing the duplicate
// definitions.

// Decryptor is the shared decrypt seam (secret.Decryptor).
type Decryptor = secret.Decryptor

// NoopDecryptor is the identity decryptor for single-run mode (secret.NoopDecryptor): the run
// JSON handed over by the controller already contains decrypted values.
type NoopDecryptor = secret.NoopDecryptor

// NewCertDecryptor builds the polling-mode Decryptor from the runner's private key PEM.
//
// tf's former package-local certDecryptor deliberately had NO empty-ciphertext guard; using
// secret.CertDecryptor now gives tf the same Kotlin decrypt("") == "" guard every other
// runner type has (a sensitive input's empty ciphertext decrypts to "" instead of surfacing
// the underlying "encrypted value empty or too short" crypto error).
func NewCertDecryptor(privateKeyPEM string) (Decryptor, error) {
	return secret.NewCertDecryptor(privateKeyPEM)
}
