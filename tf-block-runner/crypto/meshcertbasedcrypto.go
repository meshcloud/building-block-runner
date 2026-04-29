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

	return &MeshCertBasedCrypto{
		publicKey:  pubKey,
		privateKey: privKey,
	}, pubKeyError, privateKeyError
}

func NewCertBasedDecryptor(privateKeyContent string) (*MeshCertBasedCrypto, error) {
	privKey, privateKeyError := readRSAPrivateKey([]byte(privateKeyContent))

	return &MeshCertBasedCrypto{
		publicKey:  nil,
		privateKey: privKey,
	}, privateKeyError
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
