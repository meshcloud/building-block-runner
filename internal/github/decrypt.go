package github

import meshcrypto "github.com/meshcloud/building-block-runner/internal/crypto"

// Decryptor decrypts a single ciphertext. It is the process-wiring seam (plan 05 §16.2,
// D7): cert-based in polling mode (the runner holds the private key and decrypts appPem +
// sensitive inputs), NoOp in single-run mode (the controller already decrypted). Declared
// here rather than reusing tf's unexported one because that lives in another persona's
// package (kept disjoint per the slice boundary).
type Decryptor interface {
	Decrypt(ciphertext string) (string, error)
}

// NoOpDecryptor is the identity decryptor for single-run mode: the controller-supplied run
// JSON is already plaintext, so ciphertext is returned unchanged. It is also the safe
// default when a handler is minimally wired (P8).
type NoOpDecryptor struct{}

func (NoOpDecryptor) Decrypt(ciphertext string) (string, error) { return ciphertext, nil }

// certDecryptor wraps the RSA+AES cert-based crypto used in polling mode (MeshCertBasedCrypto,
// the twin of the controller's k8s-mode decryption — D7).
type certDecryptor struct {
	crypto *meshcrypto.MeshCertBasedCrypto
}

func (d certDecryptor) Decrypt(ciphertext string) (string, error) {
	return d.crypto.DecryptMeshCertBased(ciphertext)
}

// NewCertDecryptor builds the polling-mode Decryptor from the runner's private key PEM.
func NewCertDecryptor(privateKeyPEM string) (Decryptor, error) {
	c, err := meshcrypto.NewCertBasedDecryptor(privateKeyPEM)
	if err != nil {
		return nil, err
	}
	return certDecryptor{crypto: c}, nil
}
