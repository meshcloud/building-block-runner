package tf

// CP10 (PLAN_DETAIL_01_tf_characterization_tests.md §9): hermetic seam tests on the auth
// implementations (SshAuth/NoAuth). These are seam tests on the `auth` interface (a real
// consumer-side seam, gitsource.go), acceptable per §13 because phase 2 moves this file intact into
// an adapter package (D11). All key material is generated in-process (crypto/ed25519 + x/crypto/ssh)
// and all known_hosts resolution runs against fixture files — no live SSH server, no network.

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"io"
	"log"
	"net"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	gogitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
)

func testLogwrap(t *testing.T) *logwrap {
	t.Helper()
	// A logwrap needs an update-log file; the run's working dir is a natural throwaway location.
	lw, err := NewLogWrap(log.New(io.Discard, "", 0), filepath.Join(t.TempDir(), "update.log"))
	require.NoError(t, err)
	return lw
}

// genHostKey returns a freshly generated SSH public key plus the (keyType, base64) split used to
// build a KnownHost / known_hosts line for it.
func genHostKey(t *testing.T) (ssh.PublicKey, string, string) {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	sshPub, err := ssh.NewPublicKey(pub)
	require.NoError(t, err)
	fields := strings.Fields(string(ssh.MarshalAuthorizedKey(sshPub)))
	require.Len(t, fields, 2)
	return sshPub, fields[0], fields[1]
}

// genPrivateKeyPEM returns a PEM-encoded ed25519 private key, the shape SshAuth.certStr carries and
// go-git's NewPublicKeysFromFile parses.
func genPrivateKeyPEM(t *testing.T) string {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	require.NoError(t, err)
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}

const (
	testSshHostname = "example.com:22"
	testSshHostPat  = "example.com"
)

func testRemoteAddr() net.Addr { return &net.TCPAddr{IP: net.IPv4(192, 0, 2, 1), Port: 22} }

func Test_SshAuth_UnwrapCert(t *testing.T) {
	t.Run("Crypto nil passthrough (single-run)", func(t *testing.T) {
		// A nil decryptor is the single-run passthrough path where the cert arrives already decrypted.
		sshAuth := &SshAuth{certStr: "already-decrypted"}
		got, err := sshAuth.unwrapCert()
		require.NoError(t, err)
		assert.Equal(t, "already-decrypted", got)
	})

	t.Run("genuine decrypt (polling)", func(t *testing.T) {
		crypto := testCrypto(t)
		ciphertext := encryptForTest(t, crypto, "the-real-key")
		sshAuth := &SshAuth{certStr: ciphertext, dec: certDecryptor{crypto: crypto}}
		got, err := sshAuth.unwrapCert()
		require.NoError(t, err)
		assert.Equal(t, "the-real-key", got)
	})

	t.Run("decrypt error", func(t *testing.T) {
		sshAuth := &SshAuth{certStr: "not-valid-ciphertext", dec: certDecryptor{crypto: testCrypto(t)}}
		_, err := sshAuth.unwrapCert()
		assert.Error(t, err)
	})
}

func Test_SshAuth_Name(t *testing.T) {
	assert.Equal(t, AuthType(AUTH_TYPE_SSH), (&SshAuth{}).name())
}

func Test_SshAuth_Prepare(t *testing.T) {
	// nil decryptor: certStr already decrypted (single-run passthrough)
	_, keyType, keyB64 := genHostKey(t)

	dir := t.TempDir()
	sshAuth := &SshAuth{
		certStr:        "my-private-key", // no trailing newline: prepare must add one
		knownHostEntry: &KnownHost{host: testSshHostPat, key: keyType, value: keyB64},
	}
	require.NoError(t, sshAuth.prepare(dir, testLogwrap(t)))

	certPath := path.Join(dir, TMP_FILE_SSH_CERT)
	certData, err := os.ReadFile(certPath)
	require.NoError(t, err)
	assert.Equal(t, "my-private-key\n", string(certData), "prepare must ensure a trailing newline")
	info, err := os.Stat(certPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	khData, err := os.ReadFile(path.Join(dir, TMP_FILE_KNOWN_HOSTS))
	require.NoError(t, err)
	assert.Equal(t, sshAuth.knownHostEntry.toKnownHostsFileContent(), string(khData))

	assert.NoError(t, sshAuth.done())
}

func Test_SshAuth_Prepare_UnwrapCertError(t *testing.T) {
	// polling mode: prepare decrypts the cert first
	sshAuth := &SshAuth{certStr: "not-valid-ciphertext", dec: certDecryptor{crypto: testCrypto(t)}}
	err := sshAuth.prepare(t.TempDir(), testLogwrap(t))
	assert.Error(t, err, "an undecryptable cert must fail prepare before any file is written")
}

func Test_SshAuth_CheckKnownHostsEnv_BrokenFile(t *testing.T) {
	// SSH_KNOWN_HOSTS pointing at a nonexistent file makes knownhosts.New fail
	// (authSsh.go:241-247), so method 2 reports an error rather than a miss.
	key, _, _ := genHostKey(t)
	t.Setenv("SSH_KNOWN_HOSTS", filepath.Join(t.TempDir(), "no-such-known-hosts"))
	err := (&SshAuth{}).checkKnownHostsEnv(testSshHostname, testRemoteAddr(), key, testLogwrap(t))
	assert.Error(t, err)
}

func Test_SshAuth_Prepare_WriteFailure(t *testing.T) {
	// A prepDir that does not exist makes the cert WriteFile fail.
	sshAuth := &SshAuth{certStr: "k"}
	err := sshAuth.prepare(filepath.Join(t.TempDir(), "does", "not", "exist"), testLogwrap(t))
	assert.Error(t, err)
}

func Test_SshAuth_ToTransport(t *testing.T) {
	_, keyType, keyB64 := genHostKey(t)

	dir := t.TempDir()
	sshAuth := &SshAuth{
		certStr:        genPrivateKeyPEM(t),
		knownHostEntry: &KnownHost{host: testSshHostPat, key: keyType, value: keyB64},
	}
	require.NoError(t, sshAuth.prepare(dir, testLogwrap(t)))

	auth, err := sshAuth.toTransport("git@example.com:org/repo.git", testLogwrap(t))
	require.NoError(t, err)
	pk, ok := auth.(*gogitssh.PublicKeys)
	require.True(t, ok)
	assert.Equal(t, []string{keyType}, pk.HostKeyAlgorithms, "HostKeyAlgorithms must be pinned to the configured key type")
}

func Test_SshAuth_ToTransport_GarbageKey(t *testing.T) {
	dir := t.TempDir()
	sshAuth := &SshAuth{certStr: "not a pem key"}
	require.NoError(t, sshAuth.prepare(dir, testLogwrap(t)))

	_, err := sshAuth.toTransport("git@example.com:repo.git", testLogwrap(t))
	assert.Error(t, err, "a garbage private key must fail toTransport")
}

func Test_SshAuth_KnownHostsKeyCallback_Insecure(t *testing.T) {
	sshAuth := &SshAuth{}
	helper := sshAuth.knownHostsKeyCallback(t.TempDir(), true, testLogwrap(t))
	require.NotNil(t, helper.HostKeyCallback)
	// InsecureIgnoreHostKey accepts any key.
	key, _, _ := genHostKey(t)
	assert.NoError(t, helper.HostKeyCallback(testSshHostname, testRemoteAddr(), key))
}

func Test_SshAuth_CombinedCallback_Method1_ConfiguredKnownHost(t *testing.T) {
	key, keyType, keyB64 := genHostKey(t)
	dir := t.TempDir()
	sshAuth := &SshAuth{knownHostEntry: &KnownHost{host: testSshHostPat, key: keyType, value: keyB64}}
	require.NoError(t, os.WriteFile(path.Join(dir, TMP_FILE_KNOWN_HOSTS),
		[]byte(sshAuth.knownHostEntry.toKnownHostsFileContent()), 0o600))

	cb := sshAuth.combinedKnownHostsKeyCallback(dir, testLogwrap(t)).HostKeyCallback
	assert.NoError(t, cb(testSshHostname, testRemoteAddr(), key), "method 1 must accept the configured host key")
}

func Test_SshAuth_CombinedCallback_Method2_EnvKnownHosts(t *testing.T) {
	key, keyType, keyB64 := genHostKey(t)
	// No knownHostEntry => method 1 short-circuits; method 2 (SSH_KNOWN_HOSTS) resolves it.
	envFile := filepath.Join(t.TempDir(), "env_known_hosts")
	require.NoError(t, os.WriteFile(envFile, []byte((&KnownHost{host: testSshHostPat, key: keyType, value: keyB64}).toKnownHostsFileContent()), 0o600))
	t.Setenv("SSH_KNOWN_HOSTS", envFile)

	sshAuth := &SshAuth{}
	cb := sshAuth.combinedKnownHostsKeyCallback(t.TempDir(), testLogwrap(t)).HostKeyCallback
	assert.NoError(t, cb(testSshHostname, testRemoteAddr(), key))
}

func Test_SshAuth_CombinedCallback_Method3_HomeKnownHosts(t *testing.T) {
	key, keyType, keyB64 := genHostKey(t)
	home := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".ssh"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(home, ".ssh", "known_hosts"),
		[]byte((&KnownHost{host: testSshHostPat, key: keyType, value: keyB64}).toKnownHostsFileContent()), 0o600))
	t.Setenv("HOME", home)
	t.Setenv("SSH_KNOWN_HOSTS", "") // ensure method 2 misses

	sshAuth := &SshAuth{}
	cb := sshAuth.combinedKnownHostsKeyCallback(t.TempDir(), testLogwrap(t)).HostKeyCallback
	assert.NoError(t, cb(testSshHostname, testRemoteAddr(), key))
}

func Test_SshAuth_CombinedCallback_AllMiss(t *testing.T) {
	key, _, _ := genHostKey(t)
	// Point HOME/SSH_KNOWN_HOSTS at nonexistent files so all three methods fail.
	t.Setenv("HOME", filepath.Join(t.TempDir(), "no-home"))
	t.Setenv("SSH_KNOWN_HOSTS", "")

	t.Run("no configured known_host => ErrResolveNoExtraKnownHost", func(t *testing.T) {
		sshAuth := &SshAuth{}
		cb := sshAuth.combinedKnownHostsKeyCallback(t.TempDir(), testLogwrap(t)).HostKeyCallback
		assert.ErrorIs(t, cb(testSshHostname, testRemoteAddr(), key), ErrResolveNoExtraKnownHost)
	})

	t.Run("configured known_host that does not match => ErrResolveGeneric", func(t *testing.T) {
		_, otherType, otherB64 := genHostKey(t) // a different key than the presented one
		dir := t.TempDir()
		sshAuth := &SshAuth{knownHostEntry: &KnownHost{host: testSshHostPat, key: otherType, value: otherB64}}
		require.NoError(t, os.WriteFile(path.Join(dir, TMP_FILE_KNOWN_HOSTS),
			[]byte(sshAuth.knownHostEntry.toKnownHostsFileContent()), 0o600))
		cb := sshAuth.combinedKnownHostsKeyCallback(dir, testLogwrap(t)).HostKeyCallback
		assert.ErrorIs(t, cb(testSshHostname, testRemoteAddr(), key), ErrResolveGeneric)
	})
}

func Test_SshAuth_CheckKnownHostsHolder_BrokenFile(t *testing.T) {
	key, _, _ := genHostKey(t)
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(path.Join(dir, TMP_FILE_KNOWN_HOSTS), []byte("this is not a valid known_hosts line !!!"), 0o600))
	sshAuth := &SshAuth{knownHostEntry: &KnownHost{host: testSshHostPat, key: "ssh-ed25519", value: "AAAA"}}
	assert.Error(t, sshAuth.checkKnownHostsHolder(dir, testSshHostname, testRemoteAddr(), key, testLogwrap(t)))
}

func Test_KnownHost_ToKnownHostsFileContent(t *testing.T) {
	kh := &KnownHost{host: "h", key: "ssh-ed25519", value: "AAAA"}
	assert.Equal(t, "h ssh-ed25519 AAAA\n", kh.toKnownHostsFileContent())
}

func Test_NoAuth(t *testing.T) {
	noAuth := &NoAuth{}
	assert.Equal(t, AuthType(AUTH_TYPE_NONE), noAuth.name())
	assert.NoError(t, noAuth.prepare(t.TempDir(), testLogwrap(t)))
	assert.NoError(t, noAuth.done())

	t.Run("git@ url without auth is rejected", func(t *testing.T) {
		_, err := noAuth.toTransport("git@github.com:org/repo.git", testLogwrap(t))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot clone via ssh without auth")
	})

	t.Run("https url needs no transport auth", func(t *testing.T) {
		auth, err := noAuth.toTransport("https://github.com/org/repo.git", testLogwrap(t))
		require.NoError(t, err)
		assert.Nil(t, auth)
	})
}
