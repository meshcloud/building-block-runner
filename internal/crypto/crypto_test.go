package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/pem"
	"io/fs"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_Decrypt_TriesEveryKey(t *testing.T) {
	formerClientKey, err := LoadPrivateKeyFile("testdata/meshstack.key")
	require.NoError(t, err)
	formerClientValue, err := os.ReadFile("testdata/meshstack.encrypted")
	require.NoError(t, err)
	newKey := generateKey(t)
	c := Crypto{PrivateKeys: PrivateKeys{{newKey}}, PrivateKeyFiles: PrivateKeyFiles{formerClientKey}}

	t.Run("tries every key", func(t *testing.T) {
		decrypted, err := c.Decrypt(encryptLikeMeshStack(t, newKey, "secret"))
		require.NoError(t, err)
		assert.Equal(t, "secret", decrypted)

		_, err = c.Decrypt(encryptLikeMeshStack(t, generateKey(t), "secret"))
		require.ErrorContains(t, err, "no private key decrypts the value")
		_, err = new(Crypto).Decrypt(encryptLikeMeshStack(t, newKey, "secret"))
		assert.ErrorIs(t, err, errNoPrivateKey)
	})

	t.Run("reads a value that the former Go client encrypted", func(t *testing.T) {
		decrypted, err := c.Decrypt(string(formerClientValue))
		require.NoError(t, err)
		assert.Equal(t, "secret", decrypted)
	})

	t.Run("publishes the public key of the first key", func(t *testing.T) {
		publicKeyPEM, err := c.FirstPublicKey()
		require.NoError(t, err)
		block, _ := pem.Decode([]byte(publicKeyPEM))
		require.NotNil(t, block)
		assert.Equal(t, "PUBLIC KEY", block.Type)
		publicKey, err := x509.ParsePKIXPublicKey(block.Bytes)
		require.NoError(t, err)
		assert.Equal(t, &newKey.PublicKey, publicKey)

		_, err = new(Crypto).FirstPublicKey()
		assert.ErrorIs(t, err, errNoPrivateKey)
	})
}

func Test_PrivateKey_ParsesPemAndLoadsKeyFiles(t *testing.T) {
	key := generateKey(t)
	pkcs8 := string(pkcs8PEM(t, key))
	t.Chdir(t.TempDir())

	t.Run("parses PKCS8, PKCS1 and one line", func(t *testing.T) {
		pkcs1 := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
		for _, text := range []string{pkcs8, pkcs1, strings.ReplaceAll(pkcs8, "\n", "")} {
			var parsed PrivateKey
			require.NoError(t, parsed.UnmarshalText([]byte(text)))
			assert.True(t, key.Equal(parsed.PrivateKey))
		}
	})

	t.Run("takes blank for no key and fails on a broken one", func(t *testing.T) {
		var parsed PrivateKey
		require.NoError(t, parsed.UnmarshalText([]byte(" \n")))
		assert.Nil(t, parsed.PrivateKey)
		assert.Error(t, parsed.UnmarshalText([]byte("-----BEGIN PRIVATE KEY-----bm8ga2V5-----END PRIVATE KEY-----")))
	})

	t.Run("the implicit file is optional", func(t *testing.T) {
		var c Crypto
		added, err := c.AddImplicitPrivateKeyFile()
		require.NoError(t, err)
		assert.False(t, added)
		assert.Empty(t, c.PrivateKeyFiles)
	})

	require.NoError(t, os.WriteFile(ImplicitPrivateKeyFile, []byte(pkcs8), 0o600))

	t.Run("a key file has a path relative to the working directory", func(t *testing.T) {
		var file PrivateKeyFile
		require.NoError(t, file.UnmarshalText([]byte(ImplicitPrivateKeyFile)))
		assert.Equal(t, ImplicitPrivateKeyFile, file.Path)
		assert.True(t, key.Equal(file.PrivateKey.PrivateKey))
		assert.ErrorIs(t, file.UnmarshalText([]byte("missing.pem")), fs.ErrNotExist)
	})

	t.Run("adds the implicit file once", func(t *testing.T) {
		var c Crypto
		added, err := c.AddImplicitPrivateKeyFile()
		require.NoError(t, err)
		assert.True(t, added)
		c.PrivateKeyFiles[0].Path = "./" + ImplicitPrivateKeyFile
		added, err = c.AddImplicitPrivateKeyFile()
		require.NoError(t, err)
		assert.False(t, added)
		assert.Len(t, c.PrivateKeyFiles, 1)
	})

	t.Run("a broken key file fails naming it", func(t *testing.T) {
		require.NoError(t, os.WriteFile(ImplicitPrivateKeyFile, []byte("broken"), 0o600))
		var file PrivateKeyFile
		require.ErrorContains(t, file.UnmarshalText([]byte(ImplicitPrivateKeyFile)), ImplicitPrivateKeyFile)
		_, err := (&Crypto{}).AddImplicitPrivateKeyFile()
		assert.ErrorContains(t, err, "private key file runner-private.pem")
	})
}

func Test_IvLengthOtherThanTheNonceSize_FailsToDecrypt(t *testing.T) {
	key := generateKey(t)
	cipherText, err := base64.StdEncoding.DecodeString(encryptLikeMeshStack(t, key, "secret"))
	require.NoError(t, err)
	binary.BigEndian.PutUint32(cipherText[key.Size():], 8)

	_, err = decrypt(key, cipherText)

	assert.EqualError(t, err, "encrypted value has no IV of 12 bytes")
}

func generateKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	return key
}

func pkcs8PEM(t *testing.T, key *rsa.PrivateKey) []byte {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
}

func encryptLikeMeshStack(t *testing.T, key *rsa.PrivateKey, plainText string) string {
	t.Helper()
	aesKey := make([]byte, 32)
	_, err := rand.Read(aesKey)
	require.NoError(t, err)
	block, err := aes.NewCipher(aesKey)
	require.NoError(t, err)
	gcm, err := cipher.NewGCM(block)
	require.NoError(t, err)
	const ivLength = 12
	iv := make([]byte, ivLength)
	_, err = rand.Read(iv)
	require.NoError(t, err)
	encryptedKey, err := rsa.EncryptOAEP(sha1.New(), rand.Reader, &key.PublicKey, aesKey, nil)
	require.NoError(t, err)

	cipherText := binary.BigEndian.AppendUint32(encryptedKey, ivLength)
	cipherText = append(cipherText, iv...)
	cipherText = gcm.Seal(cipherText, iv, []byte(plainText), nil)
	return base64.StdEncoding.EncodeToString(cipherText)
}
