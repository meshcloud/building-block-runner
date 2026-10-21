package process

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// openRunnerDir refuses a dir that another user could have created or can write to, as TMPDIR is often shared.
func openRunnerDir(dir string) (*os.Root, error) {
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return nil, err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("runner dir %s is not a directory", dir)
	}
	if err = checkPrivate(info); err != nil {
		return nil, fmt.Errorf("runner dir %s: %w", dir, err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	opened, err := root.Stat(".")
	if err == nil && !os.SameFile(info, opened) {
		err = fmt.Errorf("runner dir %s changed while it was opened", dir)
	}
	if err != nil {
		_ = root.Close()
		return nil, err
	}
	return root, nil
}
