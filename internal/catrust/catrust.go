// Package catrust builds the trust pool this binary's outbound HTTPS uses. It is now the sole
// CA-setup mechanism, replacing the removed shell entrypoint's CA import (see PLAN.md "catrust").
// RootCAs is a pure, hermetically-testable pool builder consumed by every runner type's HTTP transport;
// SyncSystemStore (tf-only) is the real-I/O counterpart that makes the same certs visible
// to tf's subprocesses (tofu/git/curl/aws-cli), which read the on-disk store instead.
package catrust

import (
	"crypto/x509"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
)

// customCaCertsPathEnv names the directory of extra PEM files to trust, keeping the env var
// name the removed shell entrypoint used so existing deployments need no changes.
const customCaCertsPathEnv = "CUSTOM_CA_CERTS_PATH"

// RootCAs returns the trust pool for this process's outbound HTTPS: the OS trust store plus
// every PEM file found directly under the directory named by CUSTOM_CA_CERTS_PATH. An
// unset/empty env var returns the system pool unchanged (no directory read), matching
// entrypoint-go.sh's nullglob-empty no-op. A directory that does not exist is likewise a no-op:
// the scratch runtime sets CUSTOM_CA_CERTS_PATH by default as a mount point, so an absent dir
// just means nothing was mounted (again matching the nullglob behavior). A file that fails to
// parse as PEM is logged and skipped — one malformed cert must not take down the whole process's
// HTTPS egress — but any other directory read error is returned, since it signals a misconfigured mount.
func RootCAs(log *slog.Logger) (*x509.CertPool, error) {
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}

	dir := os.Getenv(customCaCertsPathEnv)
	if dir == "" {
		return pool, nil
	}

	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return pool, nil
	}
	if err != nil {
		return nil, err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		path := filepath.Join(dir, entry.Name())
		pem, err := os.ReadFile(path)
		if err != nil {
			log.Warn("catrust: skipping unreadable custom CA cert file", "path", path, "error", err)
			continue
		}
		if ok := pool.AppendCertsFromPEM(pem); !ok {
			log.Warn("catrust: skipping custom CA cert file with no valid PEM certificate", "path", path)
		}
	}

	return pool, nil
}
