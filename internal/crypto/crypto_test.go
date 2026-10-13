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
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDecrypt(t *testing.T) {
	newKey, oldKey, unknownKey := generateKey(t), generateKey(t), generateKey(t)
	t.Chdir(t.TempDir())
	writePKCS8(t, "old.pem", oldKey)
	var oldKeyFile PrivateKeyFile
	require.NoError(t, oldKeyFile.UnmarshalText([]byte("old.pem")))
	c := Crypto{PrivateKeys: PrivateKeys{{newKey}}, PrivateKeyFiles: PrivateKeyFiles{oldKeyFile}}

	for name, key := range map[string]*rsa.PrivateKey{"first key": newKey, "key file": oldKey} {
		t.Run("decrypts for the "+name, func(t *testing.T) {
			decrypted, err := c.Decrypt(encryptLikeMeshStack(t, key, "secret"))
			require.NoError(t, err)
			assert.Equal(t, "secret", decrypted)
		})
	}

	t.Run("fails for an unknown key", func(t *testing.T) {
		_, err := c.Decrypt(encryptLikeMeshStack(t, unknownKey, "secret"))
		assert.ErrorContains(t, err, "no private key decrypts the value")
	})

	t.Run("fails without a key", func(t *testing.T) {
		_, err := Crypto{}.Decrypt(encryptLikeMeshStack(t, newKey, "secret"))
		assert.ErrorIs(t, err, errNoPrivateKey)
	})
}

func Test_Decrypt_ReadsAValueThatTheFormerGoClientEncrypted(t *testing.T) {
	keyFile, err := LoadPrivateKeyFile("testdata/meshstack.key")
	require.NoError(t, err)
	encrypted, err := os.ReadFile("testdata/meshstack.encrypted")
	require.NoError(t, err)

	decrypted, err := Crypto{PrivateKeyFiles: PrivateKeyFiles{keyFile}}.Decrypt(string(encrypted))

	require.NoError(t, err)
	assert.Equal(t, "secret", decrypted)
}

func TestFirstPublicKey(t *testing.T) {
	first := generateKey(t)
	c := Crypto{PrivateKeys: PrivateKeys{{first}, {generateKey(t)}}}

	publicKeyPEM, err := c.FirstPublicKey()
	require.NoError(t, err)
	block, _ := pem.Decode([]byte(publicKeyPEM))
	require.NotNil(t, block)
	assert.Equal(t, "PUBLIC KEY", block.Type)
	publicKey, err := x509.ParsePKIXPublicKey(block.Bytes)
	require.NoError(t, err)
	assert.Equal(t, &first.PublicKey, publicKey)

	_, err = Crypto{}.FirstPublicKey()
	assert.ErrorIs(t, err, errNoPrivateKey)
}

func TestPrivateKeyUnmarshalText(t *testing.T) {
	key := generateKey(t)
	pkcs8 := pkcs8PEM(t, key)
	pkcs1 := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})

	for name, text := range map[string]string{
		"PKCS8":    string(pkcs8),
		"PKCS1":    string(pkcs1),
		"one line": strings.ReplaceAll(string(pkcs8), "\n", ""),
	} {
		t.Run(name, func(t *testing.T) {
			var parsed PrivateKey
			require.NoError(t, parsed.UnmarshalText([]byte(text)))
			assert.True(t, key.Equal(parsed.PrivateKey))
		})
	}

	t.Run("empty", func(t *testing.T) {
		var parsed PrivateKey
		require.NoError(t, parsed.UnmarshalText([]byte(" \n")))
		assert.Nil(t, parsed.PrivateKey)
	})

	t.Run("no key", func(t *testing.T) {
		var parsed PrivateKey
		assert.Error(t, parsed.UnmarshalText([]byte("-----BEGIN PRIVATE KEY-----bm8ga2V5-----END PRIVATE KEY-----")))
	})
}

func TestPrivateKeyFileUnmarshalText(t *testing.T) {
	key := generateKey(t)
	t.Chdir(t.TempDir())
	writePKCS8(t, "key.pem", key)
	require.NoError(t, os.WriteFile("broken.pem", []byte("no key"), 0o600))

	t.Run("loads a path relative to the working directory", func(t *testing.T) {
		var file PrivateKeyFile
		require.NoError(t, file.UnmarshalText([]byte("key.pem")))
		assert.Equal(t, "key.pem", file.Path)
		assert.True(t, key.Equal(file.PrivateKey.PrivateKey))
	})

	t.Run("fails on a missing file", func(t *testing.T) {
		var file PrivateKeyFile
		assert.ErrorIs(t, file.UnmarshalText([]byte(filepath.Join("missing", "key.pem"))), fs.ErrNotExist)
	})

	t.Run("fails on a file without a key", func(t *testing.T) {
		var file PrivateKeyFile
		assert.ErrorContains(t, file.UnmarshalText([]byte("broken.pem")), "broken.pem")
	})
}

func generateKey(t *testing.T) *rsa.PrivateKey {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	return key
}

func pkcs8PEM(t *testing.T, key *rsa.PrivateKey) []byte {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
}

func writePKCS8(t *testing.T, path string, key *rsa.PrivateKey) {
	require.NoError(t, os.WriteFile(path, pkcs8PEM(t, key), 0o600))
}

func encryptLikeMeshStack(t *testing.T, key *rsa.PrivateKey, plainText string) string {
	aesKey := make([]byte, 32)
	rand.Read(aesKey)
	block, err := aes.NewCipher(aesKey)
	require.NoError(t, err)
	gcm, err := cipher.NewGCM(block)
	require.NoError(t, err)
	iv := make([]byte, gcm.NonceSize())
	rand.Read(iv)
	encryptedKey, err := rsa.EncryptOAEP(sha1.New(), rand.Reader, &key.PublicKey, aesKey, nil)
	require.NoError(t, err)

	cipherText := binary.BigEndian.AppendUint32(encryptedKey, uint32(len(iv)))
	cipherText = append(cipherText, iv...)
	cipherText = gcm.Seal(cipherText, iv, []byte(plainText), nil)
	return base64.StdEncoding.EncodeToString(cipherText)
}

func TestAddImplicitPrivateKeyFile(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	t.Run("adds nothing without the file", func(t *testing.T) {
		var c Crypto
		added, err := c.AddImplicitPrivateKeyFile()
		require.NoError(t, err)
		assert.False(t, added)
		assert.Empty(t, c.PrivateKeyFiles)
	})

	require.NoError(t, os.WriteFile(filepath.Join(dir, ImplicitPrivateKeyFile), pkcs8PEM(t, generateKey(t)), 0o600))

	t.Run("adds the file", func(t *testing.T) {
		var c Crypto
		added, err := c.AddImplicitPrivateKeyFile()
		require.NoError(t, err)
		assert.True(t, added)
		assert.Equal(t, ImplicitPrivateKeyFile, c.PrivateKeyFiles[0].Path)
	})

	t.Run("adds nothing when a key file has the path", func(t *testing.T) {
		c := Crypto{PrivateKeyFiles: PrivateKeyFiles{{Path: "./" + ImplicitPrivateKeyFile}}}
		added, err := c.AddImplicitPrivateKeyFile()
		require.NoError(t, err)
		assert.False(t, added)
		assert.Len(t, c.PrivateKeyFiles, 1)
	})

	t.Run("fails on a broken file", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(dir, ImplicitPrivateKeyFile), []byte("broken"), 0o600))
		var c Crypto
		_, err := c.AddImplicitPrivateKeyFile()
		assert.ErrorContains(t, err, "private key file runner-private.pem")
	})
}
