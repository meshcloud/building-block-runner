package catrust

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
)

// SyncSystemStore is RootCAs' on-disk twin: it makes the same CUSTOM_CA_CERTS_PATH certs
// visible to tf's subprocesses (tofu/git/curl/aws-cli), which read the system trust store
// from disk rather than through this process's *x509.CertPool. It reproduces the nullglob-guarded
// update-ca-certificates call the removed shell entrypoint used to make; tf-only, since no other
// runner type shells out to a process that consults the system store.
func SyncSystemStore(ctx context.Context, log *slog.Logger) error {
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	dir := os.Getenv(customCaCertsPathEnv)
	if dir == "" {
		return nil
	}

	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return nil
	}

	log.Info("catrust: syncing custom CA certs into system trust store", "path", dir)

	out, err := exec.CommandContext(ctx, "update-ca-certificates").CombinedOutput()
	if err != nil {
		return fmt.Errorf("update-ca-certificates: %w: %s", err, out)
	}

	return nil
}
