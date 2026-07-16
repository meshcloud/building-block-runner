package catrust

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"log/slog"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestRootCAs_EnvUnset_ReturnsSystemPoolUnchanged(t *testing.T) {
	t.Setenv(customCaCertsPathEnv, "")

	pool, err := RootCAs(discardLogger())

	require.NoError(t, err)
	require.NotNil(t, pool)
}

func TestRootCAs_EnvSetToEmptyDir_ReturnsPoolUnchanged(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(customCaCertsPathEnv, dir)

	pool, err := RootCAs(discardLogger())

	require.NoError(t, err)
	require.NotNil(t, pool)
}

func TestRootCAs_GoodPemAppended(t *testing.T) {
	dir := t.TempDir()
	before := poolSize(t)
	writeSelfSignedCert(t, filepath.Join(dir, "good.pem"))
	t.Setenv(customCaCertsPathEnv, dir)

	pool, err := RootCAs(discardLogger())

	require.NoError(t, err)
	require.NotNil(t, pool)
	assert.Greater(t, len(pool.Subjects()), before, "the appended cert must grow the pool") //nolint:staticcheck // Subjects() is the simplest cross-platform way to assert an append happened
}

func TestRootCAs_BadPemSkippedAlongsideGood(t *testing.T) {
	dir := t.TempDir()
	before := poolSize(t)
	writeSelfSignedCert(t, filepath.Join(dir, "good.pem"))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bad.pem"), []byte("not a cert"), 0o600))
	t.Setenv(customCaCertsPathEnv, dir)

	pool, err := RootCAs(discardLogger())

	require.NoError(t, err, "a malformed cert file must be skipped, not fail the whole call")
	require.NotNil(t, pool)
	assert.Greater(t, len(pool.Subjects()), before, "the good cert alongside the bad one must still be appended") //nolint:staticcheck
}

func TestRootCAs_UnreadableFileSkippedAlongsideGood(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: chmod 0 does not block reads")
	}

	dir := t.TempDir()
	before := poolSize(t)
	writeSelfSignedCert(t, filepath.Join(dir, "good.pem"))
	unreadable := filepath.Join(dir, "unreadable.pem")
	require.NoError(t, os.WriteFile(unreadable, []byte("irrelevant"), 0o000))
	t.Setenv(customCaCertsPathEnv, dir)

	pool, err := RootCAs(discardLogger())

	require.NoError(t, err, "an unreadable file must be skipped, not fail the whole call")
	require.NotNil(t, pool)
	assert.Greater(t, len(pool.Subjects()), before) //nolint:staticcheck
}

func TestRootCAs_SubdirectoryIgnored(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, "subdir"), 0o755))
	t.Setenv(customCaCertsPathEnv, dir)

	pool, err := RootCAs(discardLogger())

	require.NoError(t, err)
	require.NotNil(t, pool)
}

func TestRootCAs_MissingDirNoOp(t *testing.T) {
	// The scratch runtime sets CUSTOM_CA_CERTS_PATH as a mount point; when nothing is mounted the
	// dir does not exist. That must be a no-op (matching entrypoint-go.sh's nullglob), not a boot
	// failure -- otherwise every scratch image crashes on startup.
	t.Setenv(customCaCertsPathEnv, filepath.Join(t.TempDir(), "does-not-exist"))

	pool, err := RootCAs(discardLogger())

	require.NoError(t, err, "an absent (nothing-mounted) dir must be a no-op, not an error")
	require.NotNil(t, pool)
}

func TestRootCAs_NonDirPathReturnsError(t *testing.T) {
	// A CUSTOM_CA_CERTS_PATH that exists but is not a directory is a genuine misconfiguration
	// (ENOTDIR, not ErrNotExist) and must surface as an error rather than be silently ignored.
	file := filepath.Join(t.TempDir(), "not-a-dir")
	require.NoError(t, os.WriteFile(file, []byte("x"), 0o600))
	t.Setenv(customCaCertsPathEnv, file)

	_, err := RootCAs(discardLogger())

	require.Error(t, err, "a non-directory mount path must surface as an error")
}

func TestRootCAs_NilLoggerDoesNotPanic(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bad.pem"), []byte("not a cert"), 0o600))
	t.Setenv(customCaCertsPathEnv, dir)

	assert.NotPanics(t, func() {
		_, err := RootCAs(nil)
		require.NoError(t, err)
	})
}

func TestSyncSystemStore_EnvUnset_NoOp(t *testing.T) {
	t.Setenv(customCaCertsPathEnv, "")

	err := SyncSystemStore(context.Background(), discardLogger())

	require.NoError(t, err)
}

func TestSyncSystemStore_EnvSetToEmptyDir_NoOp(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(customCaCertsPathEnv, dir)

	err := SyncSystemStore(context.Background(), discardLogger())

	require.NoError(t, err)
}

func TestSyncSystemStore_MissingDirNoOp(t *testing.T) {
	// Mirrors RootCAs: an absent CUSTOM_CA_CERTS_PATH dir (nothing mounted) is a no-op, not an
	// error -- otherwise the tf image's polling bootstrap fails before it can serve any run.
	t.Setenv(customCaCertsPathEnv, filepath.Join(t.TempDir(), "does-not-exist"))

	err := SyncSystemStore(context.Background(), discardLogger())

	require.NoError(t, err)
}

func TestSyncSystemStore_NonDirPathReturnsError(t *testing.T) {
	file := filepath.Join(t.TempDir(), "not-a-dir")
	require.NoError(t, os.WriteFile(file, []byte("x"), 0o600))
	t.Setenv(customCaCertsPathEnv, file)

	err := SyncSystemStore(context.Background(), discardLogger())

	require.Error(t, err, "a non-directory mount path must surface as an error")
}

// poolSize returns the current system pool's subject count as a baseline, so growth
// assertions hold regardless of the test image's own trust store contents.
func poolSize(t *testing.T) int {
	t.Helper()
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		return 0
	}
	return len(pool.Subjects()) //nolint:staticcheck
}

// writeSelfSignedCert writes a freshly generated, well-formed self-signed cert PEM to path
// (validity/trust chain is irrelevant to AppendCertsFromPEM's parsing).
func writeSelfSignedCert(t *testing.T, path string) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{Organization: []string{"catrust test"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)

	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	require.NoError(t, os.WriteFile(path, pemBytes, 0o600))
}
