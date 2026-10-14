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
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Crypto lists inline keys before key files, and meshStack encrypts for the first key, so a rotated-in key goes first.
type Crypto struct {
	PrivateKeys     PrivateKeys     `yaml:"privateKeys" env:"PRIVATE_KEYS" envFallback:"RUNNER_PRIVATE_KEY"`
	PrivateKeyFiles PrivateKeyFiles `yaml:"privateKeyFiles" env:"PRIVATE_KEY_FILES" envFallback:"RUNNER_PRIVATE_KEY_FILE"`
}

type PrivateKeys []PrivateKey

type PrivateKey struct {
	*rsa.PrivateKey
}

func (k *PrivateKey) UnmarshalText(text []byte) (err error) {
	if strings.TrimSpace(string(text)) == "" {
		k.PrivateKey = nil
		return nil
	}
	k.PrivateKey, err = parsePrivateKey(text)
	return
}

type PrivateKeyFiles []PrivateKeyFile

// PrivateKeyFile is read when it is unmarshaled, with a path relative to the working directory.
type PrivateKeyFile struct {
	Path string
	PrivateKey
}

func (f *PrivateKeyFile) UnmarshalText(text []byte) error {
	loaded, err := LoadPrivateKeyFile(string(text))
	*f = loaded
	return err
}

func LoadPrivateKeyFile(path string) (PrivateKeyFile, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return PrivateKeyFile{}, err
	}
	key, err := parsePrivateKey(content)
	if err != nil {
		return PrivateKeyFile{}, fmt.Errorf("private key file %s: %w", path, err)
	}
	return PrivateKeyFile{Path: path, PrivateKey: PrivateKey{key}}, nil
}

// FirstPublicKey returns the PEM encoded public key that meshStack encrypts for.
func (c Crypto) FirstPublicKey() (string, error) {
	keys := c.privateKeys()
	if len(keys) == 0 {
		return "", errNoPrivateKey
	}
	der, err := x509.MarshalPKIXPublicKey(&keys[0].PublicKey)
	if err != nil {
		return "", err
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})), nil
}

func (c Crypto) Decrypt(encrypted string) (string, error) {
	keys := c.privateKeys()
	if len(keys) == 0 {
		return "", errNoPrivateKey
	}
	cipherText, err := base64.StdEncoding.DecodeString(encrypted)
	if err != nil {
		return "", err
	}
	var errs []error
	for _, key := range keys {
		plainText, err := decrypt(key, cipherText)
		if err == nil {
			return plainText, nil
		}
		errs = append(errs, err)
	}
	return "", fmt.Errorf("no private key decrypts the value: %w", errors.Join(errs...))
}

var errNoPrivateKey = errors.New("no private key configured")

func (c Crypto) privateKeys() []*rsa.PrivateKey {
	var keys []*rsa.PrivateKey
	for _, key := range c.PrivateKeys {
		if key.PrivateKey != nil {
			keys = append(keys, key.PrivateKey)
		}
	}
	for _, file := range c.PrivateKeyFiles {
		keys = append(keys, file.PrivateKey.PrivateKey)
	}
	return keys
}

var pemBoundary = regexp.MustCompile(`-----(BEGIN|END) [A-Z ]+-----|\s`)

// parsePrivateKey also accepts a PEM key on a single line, as the Kotlin runners did.
func parsePrivateKey(text []byte) (*rsa.PrivateKey, error) {
	der, err := base64.StdEncoding.DecodeString(pemBoundary.ReplaceAllString(string(text), ""))
	if err != nil {
		return nil, fmt.Errorf("private key is no base64 encoded PEM: %w", err)
	}
	key, err := parseDer(der)
	if err != nil {
		return nil, err
	}
	return key, key.Validate()
}

func parseDer(der []byte) (*rsa.PrivateKey, error) {
	if key, err := x509.ParsePKCS1PrivateKey(der); err == nil {
		return key, nil
	}
	key, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, err
	}
	rsaKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("private key is %T, expected an RSA key", key)
	}
	return rsaKey, nil
}

// decrypt reads the format of meshStack's MeshCertBasedCrypto:
// RSA-OAEP encrypted AES key | IV length (4 bytes, big endian) | IV | AES-GCM cipher text.
func decrypt(key *rsa.PrivateKey, cipherText []byte) (string, error) {
	if len(cipherText) < key.Size()+4 {
		return "", errors.New("encrypted value is too short")
	}
	aesKey, err := rsa.DecryptOAEP(sha1.New(), rand.Reader, key, cipherText[:key.Size()], nil)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(aesKey)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	symmetric := cipherText[key.Size():]
	ivEnd := 4 + gcm.NonceSize()
	if ivEnd > len(symmetric) || binary.BigEndian.Uint32(symmetric[:4]) != uint32(gcm.NonceSize()) {
		return "", fmt.Errorf("encrypted value has no IV of %d bytes", gcm.NonceSize())
	}
	plainText, err := gcm.Open(nil, symmetric[4:ivEnd], symmetric[ivEnd:], nil)
	if err != nil {
		return "", err
	}
	return string(plainText), nil
}

// ImplicitPrivateKeyFile is read from the working directory when it exists, as the tf runner image mounts its key there.
const ImplicitPrivateKeyFile = "runner-private.pem"

func (c *Crypto) AddImplicitPrivateKeyFile() (bool, error) {
	for _, file := range c.PrivateKeyFiles {
		if filepath.Clean(file.Path) == ImplicitPrivateKeyFile {
			return false, nil
		}
	}
	file, err := LoadPrivateKeyFile(ImplicitPrivateKeyFile)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	c.PrivateKeyFiles = append(c.PrivateKeyFiles, file)
	return true, nil
}

func (c Crypto) Validate() error {
	if len(c.privateKeys()) == 0 {
		return errNoPrivateKey
	}
	return nil
}
