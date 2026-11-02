package tf

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func skipUnlessAcc(t *testing.T) {
	t.Helper()
	if os.Getenv("BUILDING_BLOCK_RUNNER_TEST_ACC") != "1" {
		t.Skip("acceptance tests are off. Run them with BUILDING_BLOCK_RUNNER_TEST_ACC=1 go test ./internal/executor/tf/ -run TestAcc")
	}
}

func TestAccGetTF(t *testing.T) {
	skipUnlessAcc(t)
	const (
		terraformVersion1 = "1.3.7"
		terraformVersion2 = "1.3.8"
		terraformVersion3 = "1.5.5"
		opentofuVersion   = "1.11.0"
	)

	t.Run("install two different TF versions", func(t *testing.T) {
		uut, err := NewBinaries(t.TempDir(), time.Minute)
		require.NoError(t, err)
		_, err = uut.GetTF(context.Background(), t.TempDir(), terraformVersion1)
		require.NoError(t, err)
		_, err = uut.GetTF(context.Background(), t.TempDir(), terraformVersion2)
		require.NoError(t, err)
		_, err = uut.GetTF(context.Background(), t.TempDir(), terraformVersion3)
		require.NoError(t, err)

		assert.ElementsMatch(t, listTfBinariesInstallDir(t, uut, terraformVersion1), []string{"terraform"})
		assert.ElementsMatch(t, listTfBinariesInstallDir(t, uut, terraformVersion2), []string{"terraform"})
		assert.ElementsMatch(t, listTfBinariesInstallDir(t, uut, terraformVersion3), []string{"terraform"})
	})

	t.Run("uses existing terraform", func(t *testing.T) {
		uut, err := NewBinaries(t.TempDir(), time.Minute)
		require.NoError(t, err)

		_, err = uut.GetTF(t.Context(), t.TempDir(), terraformVersion1)
		require.NoError(t, err)
		installed := time.Now().Add(-time.Hour).Truncate(time.Second)
		require.NoError(t, os.Chtimes(filepath.Join(uut.dir, terraformVersion1, "terraform"), installed, installed))

		_, err = uut.GetTF(t.Context(), t.TempDir(), terraformVersion1)
		require.NoError(t, err)
		assert.True(t, installed.Equal(getTerraformBinaryModTime(t, uut, terraformVersion1)), "the second GetTF installed again")
	})

	t.Run("uses opentofu", func(t *testing.T) {
		uut, err := NewBinaries(t.TempDir(), time.Minute)
		require.NoError(t, err)
		_, err = uut.GetTF(context.Background(), t.TempDir(), opentofuVersion)
		require.NoError(t, err)
		assert.ElementsMatch(t, listTfBinariesInstallDir(t, uut, opentofuVersion), []string{"terraform", "tofu"})
		_, err = os.Stat(filepath.Join(uut.dir, opentofuVersion, "terraform"))
		assert.NoError(t, err, "the terraform link does not resolve")
	})
}

func Test_Lock_HoldsTheInstallOfAVersionAgainstAnotherHolder(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		uut, err := NewBinaries(t.TempDir(), time.Minute)
		require.NoError(t, err)
		unlock, err := uut.lock(t.Context(), "1.11.0")
		require.NoError(t, err)

		ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
		defer cancel()
		_, err = uut.lock(ctx, "1.11.0")
		require.ErrorIs(t, err, context.DeadlineExceeded)

		unlock()
		unlockAgain, err := uut.lock(t.Context(), "1.11.0")
		require.NoError(t, err)
		unlockAgain()
	})
}

func Test_Install_UsesOnlyCompleteVersionDirs(t *testing.T) {
	uut, err := NewBinaries(t.TempDir(), time.Minute)
	require.NoError(t, err)
	var downloads []string
	uut.download = func(_ context.Context, ver, dir string) (string, error) {
		downloads = append(downloads, ver)
		if ver == "1.12.0" {
			return "", errors.New("connection reset")
		}
		return tofuBinaryName, os.WriteFile(filepath.Join(dir, tofuBinaryName), nil, 0o600)
	}
	binDirEntries := func(t *testing.T) []string {
		t.Helper()
		entries, err := os.ReadDir(uut.dir)
		require.NoError(t, err)
		var names []string
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		return names
	}

	t.Run("removes an interrupted install and installs again", func(t *testing.T) {
		require.NoError(t, os.MkdirAll(filepath.Join(uut.dir, "1.11.0.tmp-123", tofuBinaryName), 0o700))

		execPath, err := uut.install(t.Context(), "1.11.0")

		require.NoError(t, err)
		assert.Equal(t, filepath.Join(uut.dir, "1.11.0", tofuBinaryName), execPath)
		assert.FileExists(t, execPath)
		assert.Equal(t, []string{"1.11.0"}, binDirEntries(t))
	})

	t.Run("a failed download leaves nothing behind", func(t *testing.T) {
		_, err := uut.install(t.Context(), "1.12.0")

		require.EqualError(t, err, "download tf 1.12.0: connection reset")
		assert.Equal(t, []string{"1.11.0"}, binDirEntries(t))
	})

	t.Run("uses tofu from the older layout", func(t *testing.T) {
		versionDir := filepath.Join(uut.dir, "1.10.0")
		require.NoError(t, os.Mkdir(versionDir, 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(versionDir, "terraform"), nil, 0o600))
		require.NoError(t, os.Symlink(filepath.Join(versionDir, "terraform"), filepath.Join(versionDir, tofuBinaryName)))

		execPath, err := uut.install(t.Context(), "1.10.0")

		require.NoError(t, err)
		assert.Equal(t, filepath.Join(versionDir, tofuBinaryName), execPath)
	})

	t.Run("uses an installed Terraform", func(t *testing.T) {
		versionDir := filepath.Join(uut.dir, "1.5.5")
		require.NoError(t, os.Mkdir(versionDir, 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(versionDir, "terraform"), nil, 0o600))

		execPath, err := uut.install(t.Context(), "1.5.5")

		require.NoError(t, err)
		assert.Equal(t, filepath.Join(versionDir, "terraform"), execPath)
	})

	assert.Equal(t, []string{"1.11.0", "1.12.0"}, downloads)
}

func listTfBinariesInstallDir(t *testing.T, uut Binaries, version string) (names []string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(uut.dir, version))
	require.NoError(t, err)
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return
}

func getTerraformBinaryModTime(t *testing.T, uut Binaries, version string) time.Time {
	t.Helper()
	f, err := os.Stat(filepath.Join(uut.dir, version, "terraform"))
	require.NoError(t, err)
	return f.ModTime()
}
