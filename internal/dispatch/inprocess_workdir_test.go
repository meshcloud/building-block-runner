package dispatch_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/internal/dispatch"
	"github.com/meshcloud/building-block-runner/internal/meshapi"
)

// Test_InProcess_ConcurrentRuns_UseIsolatedWorkingDirs pins the concurrency invariant:
// concurrent runs must never write into each other's working directory. The handler
// below stands in for the not-yet-landed tf run handler and follows the
// same pattern it must: one working dir per run ("block-<runId>", mirroring worker.go's
// existing os.MkdirTemp-per-run isolation) written to, read back, and removed -- never
// touching a path it did not create itself.
func Test_InProcess_ConcurrentRuns_UseIsolatedWorkingDirs(t *testing.T) {
	baseDir := t.TempDir()

	type result struct {
		dir      string
		ownWrite bool
	}
	var mu sync.Mutex
	results := map[dispatch.RunId]result{}

	handler := concurrencyTestHandlerFunc(func(ctx context.Context, run dispatch.ClaimedRun) error {
		// baseDir is already a fresh per-test directory (t.TempDir()) and every run.Id is
		// unique, so a plain Mkdir under it is a sufficient stand-in for the real handler's
		// os.MkdirTemp(workerDir, "block-<bbId>-*") -- what this test exercises is isolation
		// between concurrently-running dirs, not name collision avoidance.
		dir := filepath.Join(baseDir, "block-"+string(run.Id))
		if err := os.Mkdir(dir, 0o755); err != nil {
			return err
		}
		defer func() {
			if err := os.RemoveAll(dir); err != nil {
				panic(fmt.Sprintf("cleanup: remove %s: %v", dir, err)) // test-fixture bug, not a hazard finding
			}
		}()

		marker := filepath.Join(dir, "marker.txt")
		if err := os.WriteFile(marker, []byte(run.Id), 0o644); err != nil {
			return err
		}

		// Widen the window in which a broken implementation sharing state across runs
		// could let another goroutine's write bleed into this directory.
		runtime.Gosched()
		time.Sleep(5 * time.Millisecond)

		data, err := os.ReadFile(marker)
		if err != nil {
			return err
		}

		mu.Lock()
		results[run.Id] = result{dir: dir, ownWrite: string(data) == string(run.Id)}
		mu.Unlock()
		return nil
	})

	in, err := dispatch.NewInProcess(
		map[meshapi.RunnerImplementationType]dispatch.RunHandler{meshapi.RunnerTypeTerraform: handler},
		time.Second, discardLogger())
	require.NoError(t, err)

	const n = 8
	dirsUsed := make([]string, n)
	for i := 0; i < n; i++ {
		run := newClaimedRun(fmt.Sprintf("run-%d", i), "tok")
		require.NoError(t, in.Dispatch(run))
	}
	in.Wait()

	require.Len(t, results, n, "every run must have recorded a result")
	seen := map[string]bool{}
	for i := 0; i < n; i++ {
		id := dispatch.RunId(fmt.Sprintf("run-%d", i))
		r, ok := results[id]
		require.True(t, ok, "missing result for %s", id)
		assert.True(t, r.ownWrite, "run %s must read back only what it wrote, never another run's data", id)
		assert.False(t, seen[r.dir], "working dir %s must not be reused by two runs", r.dir)
		seen[r.dir] = true
		dirsUsed[i] = r.dir
	}

	for _, dir := range dirsUsed {
		_, err := os.Stat(dir)
		assert.True(t, os.IsNotExist(err), "working dir %s must be removed once its run completes", dir)
	}
}
