package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
)

const (
	IV_LENGTH           = 12
	INT_LENGTH_IN_BYTES = 4
)

// This uses a symmetric key and encrypted data will look like this:
// |------cipherText--------------------------|
// |--len(IV)--|-----IV-----|-----plaintext---|
// The first 4 bytes indicate the length of the IV (nonce) => len(IV)
// the next len(IV) bytes represent the IV (nonce)
// the remainder is the encrypted plaintext
//
// The IV will be randomly chosen at encryption time.

type MeshSymmetricCrypto struct {
	aesgcm cipher.AEAD
}

func NewSymmetricCrypto(secret []byte) (*MeshSymmetricCrypto, error) {
	aesBlock, err := aes.NewCipher(secret)
	if err != nil {
		return nil, err
	}
	aesgcm, err := cipher.NewGCM(aesBlock)
	if err != nil {
		return nil, err
	}
	return &MeshSymmetricCrypto{
		aesgcm: aesgcm,
	}, nil
}

func (c *MeshSymmetricCrypto) EncryptSymmetric(plainText string) ([]byte, error) {

	// create random IV
	iv := make([]byte, IV_LENGTH)
	n, err := rand.Reader.Read(iv)
	if n != IV_LENGTH || err != nil {
		return nil, err
	}

	data := c.aesgcm.Seal(nil, iv, []byte(plainText), nil)

	// Wire layout (unchanged): 4-byte big-endian IV length, then the IV, then the ciphertext.
	// Built from a zero-length, pre-sized slice so the appends don't trip makezero.
	cipher := make([]byte, 0, 4+len(iv)+len(data))
	cipher = binary.BigEndian.AppendUint32(cipher, uint32(IV_LENGTH))
	cipher = append(cipher, iv...)
	cipher = append(cipher, data...)

	return cipher, nil
}

func (c *MeshSymmetricCrypto) DecryptSymmetric(cipherText []byte) (string, error) {

	// extract IV (which is called nonce in go)
	lenBytes := cipherText[0:INT_LENGTH_IN_BYTES]
	u := binary.BigEndian.Uint32(lenBytes)
	ivLen := int(u)
	ivBytes := cipherText[INT_LENGTH_IN_BYTES : INT_LENGTH_IN_BYTES+ivLen]

	plaintext, err := c.aesgcm.Open(nil, ivBytes, cipherText[INT_LENGTH_IN_BYTES+ivLen:], nil)

	if err != nil {
		return "", err
	}

	return string(plaintext), nil
}
