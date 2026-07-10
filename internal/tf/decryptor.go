package tf

import (
	meshcrypto "github.com/meshcloud/building-block-runner/internal/crypto"
)

// Decryptor decrypts a single sensitive input ciphertext. It replaces the former
// package-level meshcrypto.Crypto global (D4): the concrete implementation is chosen by
// the process wiring — certDecryptor in polling mode, NoopDecryptor in single-run mode
// (where the controller has already decrypted every sensitive field).
type Decryptor interface {
	Decrypt(ciphertext string) (string, error)
}

// certDecryptor wraps the RSA+AES cert-based crypto used in polling mode.
type certDecryptor struct {
	crypto *meshcrypto.MeshCertBasedCrypto
}

func (d certDecryptor) Decrypt(ciphertext string) (string, error) {
	return d.crypto.DecryptMeshCertBased(ciphertext)
}

// NewCertDecryptor builds the polling-mode Decryptor from the runner's private key PEM.
func NewCertDecryptor(privateKeyPEM string) (Decryptor, error) {
	crypto, err := meshcrypto.NewCertBasedDecryptor(privateKeyPEM)
	if err != nil {
		return nil, err
	}
	return certDecryptor{crypto: crypto}, nil
}

// NoopDecryptor is the identity decryptor for single-run mode: the run JSON handed over by
// the controller already contains decrypted values, so ciphertext is returned unchanged.
// It also stands in as the "not configured" default wherever a decryptor is genuinely
// absent (matching the previous meshcrypto.Crypto == nil passthrough behavior).
type NoopDecryptor struct{}

func (NoopDecryptor) Decrypt(ciphertext string) (string, error) {
	return ciphertext, nil
}
