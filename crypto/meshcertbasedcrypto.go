package crypto

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"os"
)

const (
	RSK_LENGTH = 16
)

var Crypto *MeshCertBasedCrypto = nil

//
// This is designed to decrypt MeshCertBasedEncryption-encrypted
// stuff. The plaintext has been encrypted with help of our public key
// and a asymmetric+symmetric encrpytion combination using a RSA-4096
// based certificate and AES-128
//

type MeshCertBasedCrypto struct {
	publicKey  *rsa.PublicKey
	privateKey *rsa.PrivateKey
}

func NewCertBasedCrypto(privateKeyFile string, publicKey []byte) (*MeshCertBasedCrypto, error, error) {
	pubKey, pubKeyError := readRSAPublicKeyFromString(publicKey)
	privKey, privateKeyError := readRSAPrivateKeyFromPemFile(privateKeyFile)

	crypto := &MeshCertBasedCrypto{
		publicKey:  pubKey,
		privateKey: privKey,
	}

	// Validate that public and private keys match if both were loaded successfully
	if pubKeyError == nil && privateKeyError == nil {
		if err := validateKeyPair(pubKey, privKey); err != nil {
			return crypto, err, nil
		}
	}

	return crypto, pubKeyError, privateKeyError
}

func NewCertBasedDecryptor(privateKeyContent string) (*MeshCertBasedCrypto, error) {
	privKey, privateKeyError := readRSAPrivateKey([]byte(privateKeyContent))

	return &MeshCertBasedCrypto{
		publicKey:  nil,
		privateKey: privKey,
	}, privateKeyError
}

// NewCertBasedDecryptorWithValidation creates a decryptor and validates that the provided
// public key matches the private key
func NewCertBasedDecryptorWithValidation(privateKeyContent string, publicKeyContent []byte) (*MeshCertBasedCrypto, error) {
	privKey, privateKeyError := readRSAPrivateKey([]byte(privateKeyContent))
	if privateKeyError != nil {
		return nil, privateKeyError
	}

	pubKey, pubKeyError := readRSAPublicKeyFromString(publicKeyContent)
	if pubKeyError != nil {
		return nil, pubKeyError
	}

	// Validate that public and private keys match
	if err := validateKeyPair(pubKey, privKey); err != nil {
		return nil, err
	}

	return &MeshCertBasedCrypto{
		publicKey:  pubKey,
		privateKey: privKey,
	}, nil
}

func (c *MeshCertBasedCrypto) EncryptMeshCertBased(plaintext string) (string, error) {

	// create random symmetric key RSK
	rsk := make([]byte, RSK_LENGTH)
	n, err := rand.Reader.Read(rsk)
	if n != RSK_LENGTH || err != nil {
		return "", err
	}

	// obtain symmetric crypto
	symmetricCrypto, err := NewSymmetricCrypto(rsk)
	if err != nil {
		return "", err
	}

	data, err := symmetricCrypto.EncryptSymmetric(plaintext)
	if err != nil {
		return "", err
	}

	encryptedRSK, err := rsa.EncryptOAEP(sha1.New(), rand.Reader, c.publicKey, rsk, nil)
	if err != nil {
		return "", err
	}

	return base64.StdEncoding.EncodeToString(append(encryptedRSK, data...)), nil
}

func (c *MeshCertBasedCrypto) DecryptMeshCertBased(encrypted string) (string, error) {

	// apply base64 decode first
	data, err := base64.StdEncoding.DecodeString(encrypted)
	if err != nil {
		return "", err
	}

	cipherBytes := []byte(data)
	encryptedRSKLen := c.privateKey.Size() // !byte length
	if len(cipherBytes) < encryptedRSKLen {
		return "", errors.New("encrypted value empty or too short")
	}

	// extract random symmetric key RSK
	encryptedRSK := cipherBytes[0:encryptedRSKLen]

	// the result of the symmetric encryption is the rest
	symEncrResult := cipherBytes[encryptedRSKLen:]

	// decrypt encrypted-RK with asym decryption using the private
	RSK, err := rsa.DecryptOAEP(sha1.New(), rand.Reader, c.privateKey, encryptedRSK, nil)
	if err != nil {
		return "", err
	}

	symmetricCrypto, err := NewSymmetricCrypto(RSK)
	if err != nil {
		return "", err
	}

	return symmetricCrypto.DecryptSymmetric(symEncrResult)
}

func readRSAPublicKeyFromString(keyStr []byte) (*rsa.PublicKey, error) {

	var blocks [][]byte
	for {
		var certDERBlock *pem.Block
		certDERBlock, keyStr = pem.Decode(keyStr)
		if certDERBlock == nil {
			break
		}

		if certDERBlock.Type == "CERTIFICATE" {
			blocks = append(blocks, certDERBlock.Bytes)
		}
	}

	for _, block := range blocks {
		cert, err := x509.ParseCertificate(block)
		if err != nil {
			continue
		} else {
			return cert.PublicKey.(*rsa.PublicKey), nil
		}
	}
	return nil, errors.New("no public key block found")
}

func readRSAPrivateKeyFromPemFile(pemFile string) (*rsa.PrivateKey, error) {
	pemContent, err := os.ReadFile(pemFile)
	if err != nil {
		return nil, err
	}

	return readRSAPrivateKey(pemContent)
}

func readRSAPrivateKey(pemContent []byte) (*rsa.PrivateKey, error) {

	block, _ := pem.Decode(pemContent)
	if block == nil {
		return nil, errors.New("no valid PEM block found")
	}

	pk, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}

	return pk.(*rsa.PrivateKey), nil
}

// validateKeyPair validates that a public and private key pair match
// by comparing the public key from the certificate with the public key embedded in the private key
func validateKeyPair(publicKey *rsa.PublicKey, privateKey *rsa.PrivateKey) error {
	// First validate the private key structure itself
	if err := privateKey.Validate(); err != nil {
		return errors.New("invalid private key: " + err.Error())
	}

	// Compare the public key from the certificate with the public key in the private key
	if publicKey.E != privateKey.PublicKey.E {
		return errors.New("public key and private key do not match: public exponent mismatch")
	}

	if publicKey.N.Cmp(privateKey.PublicKey.N) != 0 {
		return errors.New("public key and private key do not match: modulus mismatch")
	}

	return nil
}
