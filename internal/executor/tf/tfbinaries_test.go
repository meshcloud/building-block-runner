package tf

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
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
		uut, err := NewBinaries(t.TempDir(), time.Minute, io.Discard)
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
		uut, err := NewBinaries(t.TempDir(), time.Minute, io.Discard)
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
		uut, err := NewBinaries(t.TempDir(), time.Minute, io.Discard)
		require.NoError(t, err)
		_, err = uut.GetTF(context.Background(), t.TempDir(), opentofuVersion)
		require.NoError(t, err)
		assert.ElementsMatch(t, listTfBinariesInstallDir(t, uut, opentofuVersion), []string{"terraform", "tofu"})
	})
}

func listTfBinariesInstallDir(t *testing.T, uut *Binaries, version string) (names []string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(uut.dir, version))
	require.NoError(t, err)
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return
}

func getTerraformBinaryModTime(t *testing.T, uut *Binaries, version string) time.Time {
	t.Helper()
	f, err := os.Stat(filepath.Join(uut.dir, version, "terraform"))
	require.NoError(t, err)
	return f.ModTime()
}
