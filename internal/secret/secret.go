// Package secret is the single home for the sensitive-value decryption seam and the
// sensitive-input TYPE policy shared by every runner type (tf, gitlab, azdevops, github,
// manual, run-controller).
//
// It owns two things that used to be duplicated (and drift) across packages:
//   - the Decryptor seam (NoopDecryptor for single-run mode, CertDecryptor for polling
//     mode), with the Kotlin decrypt("") == "" empty-ciphertext guard living once, here;
//   - the policy that only STRING, CODE and FILE inputs may be encrypted -- a sensitive
//     input of any other declared type is a misconfigured building block and must fail the
//     run rather than leak ciphertext downstream (see DecryptValue /
//     UnsupportedSensitiveTypeError).
//
// It deliberately depends only on internal/crypto (the cert crypto) and internal/valuestring
// (the transport-string renderer) plus stdlib, so it never creates an import cycle with the
// meshapi DTO package whose JSON-traversal decrypt helpers call into it.
package secret

import (
	"fmt"

	meshcrypto "github.com/meshcloud/building-block-runner/internal/crypto"
	"github.com/meshcloud/building-block-runner/internal/valuestring"
)

// Decryptor decrypts a single sensitive-value ciphertext. It is the shared wiring seam every
// runner injects: CertDecryptor in polling mode (the runner holds the private key),
// NoopDecryptor in single-run mode (the controller-supplied run JSON is already decrypted).
type Decryptor interface {
	Decrypt(ciphertext string) (string, error)
}

// NoopDecryptor is the identity decryptor for single-run mode: the run JSON handed over by
// the controller already contains decrypted values, so ciphertext is returned unchanged.
type NoopDecryptor struct{}

func (NoopDecryptor) Decrypt(ciphertext string) (string, error) { return ciphertext, nil }

// CertDecryptor wraps the RSA+AES cert-based crypto used in polling mode.
//
// Kotlin's decrypt("") returns "" (MeshCertDecryptionService.kt); the underlying Go crypto
// errors on empty input ("encrypted value empty or too short", meshcertbasedcrypto.go)
// because it was never asked to decrypt a legitimately empty value (an empty
// pipeline-trigger-token is a real case -- the request is still sent with an empty token
// field). The guard lives here, once, so every consumer of this seam gets the Kotlin
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

// NewCertDecryptorFromCrypto adapts an already-constructed cert crypto instance (e.g. the
// run-controller's validated key pair) into a Decryptor, so a caller that owns the crypto
// instance can hand it to a decrypt helper without re-loading the key.
func NewCertDecryptorFromCrypto(c *meshcrypto.MeshCertBasedCrypto) Decryptor {
	return CertDecryptor{crypto: c}
}

// The only input types whose values may be encrypted in meshStack. A sensitive input of any
// other declared type indicates a misconfigured building block.
const (
	TypeString = "STRING"
	TypeCode   = "CODE"
	TypeFile   = "FILE"
)

// Decryptable reports whether an input of the given declared type may carry an encrypted
// value: true iff ioType is STRING, CODE or FILE.
func Decryptable(ioType string) bool {
	switch ioType {
	case TypeString, TypeCode, TypeFile:
		return true
	default:
		return false
	}
}

// UnsupportedSensitiveTypeError is returned when a sensitive input's declared type is not one
// of STRING/CODE/FILE. It fails the run (rather than silently passing ciphertext downstream
// into tfvars/pipeline vars/workflow inputs, a data-integrity and secret-leak hazard).
type UnsupportedSensitiveTypeError struct {
	Key  string
	Type string
}

func (e UnsupportedSensitiveTypeError) Error() string {
	return fmt.Sprintf("sensitive input %q has type %q; only STRING, CODE and FILE inputs may be encrypted", e.Key, e.Type)
}

// DecryptValue decrypts one sensitive input's value under the shared type policy. Callers
// invoke it only for inputs already known to be sensitive: if the declared type is not
// STRING/CODE/FILE it returns an UnsupportedSensitiveTypeError (failing the run); otherwise
// it renders value to its transport string (valuestring.Render) and decrypts it.
func DecryptValue(dec Decryptor, key, ioType string, value any) (string, error) {
	if !Decryptable(ioType) {
		return "", UnsupportedSensitiveTypeError{Key: key, Type: ioType}
	}
	return dec.Decrypt(valuestring.Render(value))
}
