package report

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProgress_NewProgress_PublishesAnIndependentCopy(t *testing.T) {
	initial := RunStatus{RunId: "run-1", Steps: []StepStatus{{Name: "a"}}}
	p := NewProgress(initial)

	initial.Steps[0].Name = "mutated-after-construction"

	got := p.Snapshot()
	assert.Equal(t, "a", got.Steps[0].Name, "NewProgress must clone initial, not alias its Steps")
}

func TestProgress_Mutate_PublishesTheResult(t *testing.T) {
	p := NewProgress(RunStatus{RunId: "run-1", Status: PENDING})

	p.Mutate(func(r *RunStatus) {
		r.Status = IN_PROGRESS
		r.Steps = append(r.Steps, StepStatus{Name: "a"})
	})

	got := p.Snapshot()
	assert.Equal(t, IN_PROGRESS, got.Status)
	require.Len(t, got.Steps, 1)
	assert.Equal(t, "a", got.Steps[0].Name)
}

func TestProgress_Snapshot_IsIndependentOfSubsequentMutation(t *testing.T) {
	p := NewProgress(RunStatus{RunId: "run-1", Steps: []StepStatus{{Name: "a", Status: PENDING}}})

	snap := p.Snapshot()

	p.Mutate(func(r *RunStatus) {
		r.Steps[0].Status = SUCCEEDED
	})

	assert.Equal(t, PENDING, snap.Steps[0].Status, "a Snapshot taken before a Mutate must not observe it")
}

func TestProgress_Snapshot_ReturnedSliceIsNotAliasedAcrossCalls(t *testing.T) {
	p := NewProgress(RunStatus{Steps: []StepStatus{{Name: "a"}}})

	first := p.Snapshot()
	first.Steps[0].Name = "mutated-by-caller"

	second := p.Snapshot()
	assert.Equal(t, "a", second.Steps[0].Name, "callers mutating one Snapshot must not affect a later Snapshot")
}

// TestProgress_ConcurrentMutateAndSnapshot exercises the exact race the mutex-guarded
// clone-under-lock design exists to prevent (B10): one goroutine repeatedly mutates while
// another repeatedly snapshots. Run with -race (A5) to prove there is no data race.
func TestProgress_ConcurrentMutateAndSnapshot(t *testing.T) {
	p := NewProgress(RunStatus{Steps: []StepStatus{{Name: "a"}}})

	const iterations = 200
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			p.Mutate(func(r *RunStatus) {
				r.Steps[0].LogStartIdx = int64(i)
			})
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			snap := p.Snapshot()
			// Reading here (under -race) is what proves the isolation; the value itself is
			// only constrained to be one of the published values.
			_ = snap.Steps[0].LogStartIdx
		}
	}()

	wg.Wait()
}
