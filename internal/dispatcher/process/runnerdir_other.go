//go:build !unix

package process

import "io/fs"

func checkPrivate(fs.FileInfo) error { return nil }
