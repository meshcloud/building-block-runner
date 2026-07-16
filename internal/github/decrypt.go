package github

import "github.com/meshcloud/building-block-runner/internal/secret"

// The decrypt seam now lives in the shared internal/secret package (the single home for the
// decryptor and the sensitive-input type policy). These aliases keep the github.* names
// (github.Decryptor, github.NoOpDecryptor, github.NewCertDecryptor) working unchanged.
//
// github's former package-local certDecryptor had NO empty-ciphertext guard; it now inherits
// secret.CertDecryptor's Kotlin decrypt("") == "" guard. That is inert in practice here
// (github only decrypts a present appPem and present sensitive inputs, never an empty value),
// but the behavior is now uniform across all runner types.

// Decryptor decrypts a single ciphertext (secret.Decryptor).
type Decryptor = secret.Decryptor

// NoOpDecryptor is the identity decryptor for single-run mode (secret.NoopDecryptor): the
// controller-supplied run JSON is already plaintext, so ciphertext is returned unchanged.
type NoOpDecryptor = secret.NoopDecryptor

// NewCertDecryptor builds the polling-mode Decryptor from the runner's private key PEM.
func NewCertDecryptor(privateKeyPEM string) (Decryptor, error) {
	return secret.NewCertDecryptor(privateKeyPEM)
}
