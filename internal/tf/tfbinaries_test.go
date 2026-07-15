//go:build e2e

// Test_GetTF drives the real tofu/terraform binary download (hc-install / opentofu releases)
// over the network -- the only non-hermetic test left, since everything else uses
// mocks or local testdata. It is gated behind the `e2e` build tag so
// the default `task test` (and every PR CI run) stays offline and fast; run it via
// `task test:e2e` or the opt-in e2e.yml workflow. tfbinaries.go is on the coverage exclusion
// list, so gating this file does not move the coverage-gate denominator.
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

func Test_GetTF(t *testing.T) {
	// These tests verify the behavior of the "real" tfBinaries download
	// without using the MockedTerraform implementation of the TfFacade
	const (
		terraformVersion1 = "1.3.7"
		terraformVersion2 = "1.3.8"
		terraformVersion3 = "1.5.5"
		opentofuVersion   = "1.11.0"
	)

	t.Run("install two different TF versions", func(t *testing.T) {
		uut, err := NewTfBin(t.TempDir(), io.Discard)
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
		uut, err := NewTfBin(t.TempDir(), io.Discard)
		require.NoError(t, err)

		_, err = uut.GetTF(context.Background(), t.TempDir(), terraformVersion1)
		require.NoError(t, err)
		fileModDat := getTerraformBinaryModTime(t, uut, terraformVersion1)
		time.Sleep(1 * time.Second) // ensure modification time is really different on second attempt

		// get the same version again, expect no mod time change!
		_, err = uut.GetTF(context.Background(), t.TempDir(), terraformVersion1)
		require.NoError(t, err)
		assert.Equal(t, fileModDat, getTerraformBinaryModTime(t, uut, terraformVersion1), "file must not be modified by second ")
	})

	t.Run("uses opentofu", func(t *testing.T) {
		uut, err := NewTfBin(t.TempDir(), io.Discard)
		require.NoError(t, err)
		_, err = uut.GetTF(context.Background(), t.TempDir(), opentofuVersion)
		require.NoError(t, err)
		assert.ElementsMatch(t, listTfBinariesInstallDir(t, uut, opentofuVersion), []string{"terraform", "tofu"})
	})
}

func listTfBinariesInstallDir(t *testing.T, uut *TfBinaries, version string) (names []string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(uut.dir, version))
	require.NoError(t, err)
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return
}

func getTerraformBinaryModTime(t *testing.T, uut *TfBinaries, version string) time.Time {
	t.Helper()
	f, err := os.Stat(filepath.Join(uut.dir, version, "terraform"))
	require.NoError(t, err)
	return f.ModTime()
}
