//go:build unix

package process

import (
	"fmt"
	"io/fs"
	"os"
	"syscall"
)

func checkPrivate(info fs.FileInfo) error {
	if stat, ok := info.Sys().(*syscall.Stat_t); !ok || int(stat.Uid) != os.Getuid() {
		return fmt.Errorf("is not owned by user %d", os.Getuid())
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		return fmt.Errorf("has mode %o, want 700", perm)
	}
	return nil
}
