package harness

import (
	"io/fs"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/v2/internal/testacc/runnerproc"
)

type Case struct {
	// Files is the case folder, with bootstrap/ and buildingblock/ when the case has them.
	Files fs.FS
	// Bootstrap replaces the bootstrap/ of Files for a case that borrows the bootstrap of another.
	Bootstrap            fs.FS
	ImplementationType   string
	Runner               runnerproc.Options
	Vars                 map[string]any
	WithoutBuildingBlock bool
	TerraformVersion     string
}

func (c Case) bootstrap(t *testing.T) fs.FS {
	t.Helper()
	if c.Bootstrap != nil {
		return c.Bootstrap
	}
	return subIfExists(t, c.Files, "bootstrap")
}

func (c Case) module(t *testing.T) fs.FS {
	t.Helper()
	return subIfExists(t, c.Files, "buildingblock")
}

func subIfExists(t *testing.T, fsys fs.FS, dir string) fs.FS {
	t.Helper()
	if fsys == nil {
		return nil
	}
	if _, err := fs.Stat(fsys, dir); err != nil {
		return nil
	}
	sub, err := fs.Sub(fsys, dir)
	require.NoError(t, err)
	return sub
}
