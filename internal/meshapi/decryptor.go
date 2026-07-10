package meshapi

import (
	meshcrypto "github.com/meshcloud/building-block-runner/internal/crypto"
)

// Decryptor decrypts a single sensitive-value ciphertext. It is the shared seam every
// phase-6 Kotlin-port handler injects (umbrella §5.3, plan 06B §4.1): CertDecryptor in
// polling mode, NoopDecryptor in single-run mode (the controller/file source already
// decrypted). tf keeps its own package-local twin unchanged (umbrella §2 out-of-scope) --
// this is the first shared home for the concept, introduced by the first port that needs
// it outside tf (06B).
type Decryptor interface {
	Decrypt(ciphertext string) (string, error)
}

// CertDecryptor wraps the RSA+AES cert-based crypto used in polling mode.
//
// Kotlin's decrypt("") returns "" (MeshCertDecryptionService.kt:34-37); the underlying Go
// crypto errors on empty input ("encrypted value empty or too short",
// meshcertbasedcrypto.go:117-132) because it was never asked to decrypt a legitimately
// empty value before this port (T8: an empty pipeline-trigger-token is a real case, G-P11
// -- the request is still sent with an empty token field). The guard lives here, once, so
// every consumer of this seam (trigger-token decrypt, DecryptInputs) gets the Kotlin
// behavior without re-deriving it.
type CertDecryptor struct {
	crypto *meshcrypto.MeshCertBasedCrypto
}

func (d CertDecryptor) Decrypt(ciphertext string) (string, error) {
	if ciphertext == "" {
		return "", nil
	}
	return d.crypto.DecryptMeshCertBased(ciphertext)
}

// NewCertDecryptor builds the polling-mode Decryptor from the runner's private key PEM.
func NewCertDecryptor(privateKeyPEM string) (Decryptor, error) {
	c, err := meshcrypto.NewCertBasedDecryptor(privateKeyPEM)
	if err != nil {
		return nil, err
	}
	return CertDecryptor{crypto: c}, nil
}

// NoopDecryptor is the identity decryptor for single-run mode: the run JSON handed over
// by the controller already contains decrypted values (and, for gitlab specifically, an
// already-decrypted pipelineTriggerToken -- §2.6 k8s caveat), so ciphertext is returned
// unchanged.
type NoopDecryptor struct{}

func (NoopDecryptor) Decrypt(ciphertext string) (string, error) { return ciphertext, nil }
