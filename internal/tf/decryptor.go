package tf

import (
	meshcrypto "github.com/meshcloud/building-block-runner/internal/crypto"
	meshapi "github.com/meshcloud/building-block-runner/internal/meshapi"
)

// Decryptor is the shared decrypt seam. PLAN_DETAIL_03 step 8 retargets tf's phase-2 port to
// the shared meshapi.Decryptor (same shape -- Decrypt(ciphertext) (string, error)) so the
// concept has one home; this alias keeps every existing tf call site and assertion unchanged
// while removing the duplicate interface declaration.
type Decryptor = meshapi.Decryptor

// NoopDecryptor is the identity decryptor for single-run mode (the run JSON handed over by
// the controller already contains decrypted values). It is the shared meshapi.NoopDecryptor
// -- byte-identical behavior, deduplicated.
type NoopDecryptor = meshapi.NoopDecryptor

// certDecryptor wraps the RSA+AES cert-based crypto used in polling mode. tf keeps its own
// concrete decryptor (rather than meshapi.CertDecryptor) deliberately: meshapi.CertDecryptor
// returns "" for an empty ciphertext (Kotlin decrypt("") parity for the port personas),
// whereas tf's historic behavior is to surface the underlying crypto error on an empty/too-
// short value. Preserving that keeps tf's polling decrypt path behavior-identical (additive-
// only constraint); only the shared interface is deduplicated above.
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
