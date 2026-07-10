package dispatch_test

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/internal/dispatch"
	"github.com/meshcloud/building-block-runner/internal/meshapi"
)

// Test_InProcess_ConcurrentRuns_LogsAreIsolated pins concurrency hazard H3 (plan-05 §7):
// concurrent runs sharing one log stream (as every persona's runs realistically do --
// process stdout, one file, one *slog.Logger) must never bleed one run's identity onto
// another run's log lines. The handler below builds a per-run child logger
// (base.With("runId", ...)) exactly as the eventual tf/handler ports must (§7 H3): if a
// per-run logger were accidentally shared or captured by reference across goroutines (the
// classic loop-variable-capture bug), lines would show up tagged with the wrong runId or
// out-of-order sequence numbers, which this test would catch.
func Test_InProcess_ConcurrentRuns_LogsAreIsolated(t *testing.T) {
	var buf bytes.Buffer // safe for concurrent writes: slog's handler serializes calls to Handle
	base := slog.New(slog.NewTextHandler(&buf, nil))

	const linesPerRun = 5

	handler := concurrencyTestHandlerFunc(func(ctx context.Context, run dispatch.ClaimedRun) error {
		logger := base.With("runId", string(run.Id))
		for seq := 0; seq < linesPerRun; seq++ {
			logger.Info("step", "seq", seq)
			runtime.Gosched()
		}
		return nil
	})

	in, err := dispatch.NewInProcess(
		map[meshapi.RunnerImplementationType]dispatch.RunHandler{meshapi.RunnerTypeTerraform: handler},
		time.Second, discardLogger())
	require.NoError(t, err)

	const n = 6
	for i := 0; i < n; i++ {
		require.NoError(t, in.Dispatch(newClaimedRun(fmt.Sprintf("run-%d", i), "tok")))
	}
	in.Wait()

	perRunSeqs := map[string][]int{}
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		runId, seq := parseLogIsolationLine(t, line)
		perRunSeqs[runId] = append(perRunSeqs[runId], seq)
	}

	require.Len(t, perRunSeqs, n, "every run must have logged under its own runId attribute")
	for i := 0; i < n; i++ {
		runId := fmt.Sprintf("run-%d", i)
		seqs, ok := perRunSeqs[runId]
		require.True(t, ok, "no log lines found for %s", runId)
		assert.Equal(t, []int{0, 1, 2, 3, 4}, seqs,
			"run %s's log lines must be exactly its own sequence, in order -- never another run's", runId)
	}
}

// parseLogIsolationLine extracts the runId/seq attributes from one slog TextHandler output
// line ("time=... level=INFO msg=step runId=run-3 seq=2").
func parseLogIsolationLine(t *testing.T, line string) (runId string, seq int) {
	t.Helper()
	for _, field := range strings.Fields(line) {
		key, value, ok := strings.Cut(field, "=")
		if !ok {
			continue
		}
		value = strings.Trim(value, `"`)
		switch key {
		case "runId":
			runId = value
		case "seq":
			n, err := strconv.Atoi(value)
			require.NoError(t, err, "seq attribute must parse as an int: %q", line)
			seq = n
		}
	}
	require.NotEmpty(t, runId, "log line missing a runId attribute: %q", line)
	return runId, seq
}
