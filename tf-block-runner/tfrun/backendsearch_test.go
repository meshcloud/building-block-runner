package tfrun_test

import (
	"embed"
	"io/fs"
	"path"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/tf-block-runner/tfrun"
)

var (
	//go:embed testdata/backendsearch
	testBackendSearchDir embed.FS
)

func TestFindBackendConfig(t *testing.T) {
	getTestFs := func(t *testing.T) fs.FS {
		t.Helper()
		dir := path.Join("testdata/backendsearch", path.Base(t.Name()))
		fsys, err := fs.Sub(testBackendSearchDir, dir)
		require.NoError(t, err)
		return fsys
	}

	t.Run("hcl", func(t *testing.T) {
		found, match, diags := tfrun.FindBackendConfig(getTestFs(t))
		require.Empty(t, diags)
		assert.True(t, found)
		assert.Equal(t, "test.tf", match)
	})

	t.Run("json", func(t *testing.T) {
		found, match, diags := tfrun.FindBackendConfig(getTestFs(t))
		require.Empty(t, diags)
		assert.True(t, found)
		assert.Equal(t, "test.tf.json", match)
	})

	t.Run("json list", func(t *testing.T) {
		found, match, diags := tfrun.FindBackendConfig(getTestFs(t))
		require.Empty(t, diags)
		assert.True(t, found)
		assert.Equal(t, "testlist.tf.json", match)
	})

	t.Run("commented out", func(t *testing.T) {
		found, match, diags := tfrun.FindBackendConfig(getTestFs(t))
		require.Empty(t, diags)
		assert.False(t, found)
		assert.Empty(t, match)
	})

	t.Run("broken config", func(t *testing.T) {
		found, match, diags := tfrun.FindBackendConfig(getTestFs(t))
		require.Len(t, diags, 4)
		assert.False(t, found)
		assert.Empty(t, match)
	})
}
