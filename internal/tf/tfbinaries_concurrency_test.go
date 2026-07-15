package tf

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// Test_TfBinaries_ConcurrentGetTF_IsRaceFree pins the concurrency hazard where
// two (or more) concurrent runs requesting the same/different tofu versions race the
// shared install dir's stat/remove/install sequence. TfBinaries.GetTF already serializes
// that sequence behind its own mutex (tfbinaries.go:32-33,93-94) — this test verifies the
// existing mitigation under -race rather than rebuilding it (verify, don't rebuild).
// The pre-existing-binary fast path is used (fake binary files pre-created) so the test
// stays hermetic and fast: no network download, no gate-excluded code path exercised.
func Test_TfBinaries_ConcurrentGetTF_IsRaceFree(t *testing.T) {
	installDir := t.TempDir()
	uut, err := NewTfBin(installDir, io.Discard)
	require.NoError(t, err)

	const (
		version1 = "1.3.7"
		version2 = "1.3.8"
	)
	for _, ver := range []string{version1, version2} {
		versionDir := filepath.Join(installDir, ver)
		require.NoError(t, os.MkdirAll(versionDir, 0777))
		require.NoError(t, os.WriteFile(filepath.Join(versionDir, "terraform"), []byte("fake binary"), 0755))
	}

	// Two concurrent callers per version (2x2), each with its own
	// working dir so only the shared install-dir sequence is contended.
	type call struct {
		version    string
		workingDir string
	}
	calls := []call{
		{version1, t.TempDir()},
		{version1, t.TempDir()},
		{version2, t.TempDir()},
		{version2, t.TempDir()},
	}

	var wg sync.WaitGroup
	errs := make(chan error, len(calls))
	for _, c := range calls {
		wg.Add(1)
		go func(c call) {
			defer wg.Done()
			_, err := uut.GetTF(context.Background(), c.workingDir, c.version)
			errs <- err
		}(c)
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		require.NoError(t, err, "concurrent GetTF must not fail or race on the shared install dir")
	}
}
