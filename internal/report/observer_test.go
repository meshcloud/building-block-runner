package report

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeReporter records every Report call and can be told to return an abort flag or an error on
// a specific (1-indexed) call number.
type fakeReporter struct {
	mu       sync.Mutex
	reports  []RunStatus
	abortOn  int
	errOn    int
	err      error
	registry []RunStatus
}

func (f *fakeReporter) Register(s RunStatus) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.registry = append(f.registry, s)
	return nil
}

func (f *fakeReporter) Report(s RunStatus) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reports = append(f.reports, s)
	n := len(f.reports)

	if f.errOn != 0 && n == f.errOn {
		return false, f.err
	}
	return f.abortOn != 0 && n == f.abortOn, nil
}

func (f *fakeReporter) allReports() []RunStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]RunStatus(nil), f.reports...)
}

// waitForNthTimeout bounds waitForNth; every call site currently wants the same budget.
const waitForNthTimeout = time.Second

// waitForNth polls until the reporter has recorded at least n reports and returns the nth one
// (1-indexed), or fails the test after waitForNthTimeout. Polling (rather than a fixed sleep)
// keeps the test fast on a quiet machine and robust on a loaded one.
func (f *fakeReporter) waitForNth(t *testing.T, n int) RunStatus {
	t.Helper()
	deadline := time.Now().Add(waitForNthTimeout)
	for time.Now().Before(deadline) {
		if got := f.allReports(); len(got) >= n {
			return got[n-1]
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for report #%d; got %d reports", n, len(f.allReports()))
	return RunStatus{}
}

func stepNames(steps []StepStatus) []string {
	names := make([]string, len(steps))
	for i, s := range steps {
		names[i] = s.Name
	}
	return names
}

func TestObserver_Run_TicksSendOnlyChangedSteps(t *testing.T) {
	reporter := &fakeReporter{}
	p := NewProgress(RunStatus{
		RunId:  "run-1",
		Status: IN_PROGRESS,
		Steps:  []StepStatus{{Name: "a", Status: IN_PROGRESS}, {Name: "b", Status: PENDING}},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	o := Observer{Interval: 5 * time.Millisecond, Reporter: reporter}

	go o.Run(ctx, cancel, done, p)

	first := reporter.waitForNth(t, 1)
	assert.ElementsMatch(t, []string{"a", "b"}, stepNames(first.Steps), "the first tick has nothing to diff against, so it sends every step")

	p.Mutate(func(r *RunStatus) { r.Steps[1].Status = SUCCEEDED })

	second := reporter.waitForNth(t, 2)
	assert.Equal(t, []string{"b"}, stepNames(second.Steps), "only the changed step is sent on the next tick")

	close(done)
	final := reporter.waitForNth(t, 3)
	assert.Empty(t, final.Steps, "nothing changed between the second tick and completion")
}

func TestObserver_Run_SkipsTicksWhileTerminal(t *testing.T) {
	// A terminal snapshot observed on a tick means the work goroutine is about to signal done;
	// the tick must be a no-op so the final send (below) is the only terminal-state send.
	reporter := &fakeReporter{}
	p := NewProgress(RunStatus{RunId: "run-1", Status: SUCCEEDED})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	o := Observer{Interval: 5 * time.Millisecond, Reporter: reporter}

	go o.Run(ctx, cancel, done, p)

	time.Sleep(30 * time.Millisecond)
	assert.Empty(t, reporter.allReports(), "no tick should send while the status is already terminal")

	close(done)
	reporter.waitForNth(t, 1)
}

func TestObserver_Run_FinalMapsAsyncSucceededToInProgress(t *testing.T) {
	reporter := &fakeReporter{}
	p := NewProgress(RunStatus{RunId: "run-1", Status: IN_PROGRESS})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	// A long interval keeps the ticker from ever firing, isolating this test to the final send.
	o := Observer{Interval: time.Hour, Reporter: reporter, Async: true}

	go o.Run(ctx, cancel, done, p)

	p.Mutate(func(r *RunStatus) { r.Status = SUCCEEDED })
	close(done)

	final := reporter.waitForNth(t, 1)
	assert.Equal(t, IN_PROGRESS, final.Status, "an async run's SUCCEEDED final status is downgraded to IN_PROGRESS (D9): completion here only means handover")
}

func TestObserver_Run_SyncFinalKeepsSucceeded(t *testing.T) {
	reporter := &fakeReporter{}
	p := NewProgress(RunStatus{RunId: "run-1", Status: IN_PROGRESS})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	o := Observer{Interval: time.Hour, Reporter: reporter, Async: false}

	go o.Run(ctx, cancel, done, p)

	p.Mutate(func(r *RunStatus) { r.Status = SUCCEEDED })
	close(done)

	final := reporter.waitForNth(t, 1)
	assert.Equal(t, SUCCEEDED, final.Status, "a synchronous run's SUCCEEDED final status is sent as-is")
}

func TestObserver_Run_AbortCancelsContext(t *testing.T) {
	reporter := &fakeReporter{abortOn: 1}
	p := NewProgress(RunStatus{RunId: "run-1", Status: IN_PROGRESS})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	o := Observer{Interval: 5 * time.Millisecond, Reporter: reporter}

	go o.Run(ctx, cancel, done, p)

	reporter.waitForNth(t, 1)

	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("expected the run context to be cancelled after an abort response")
	}

	close(done)
	// D9: a final update must never follow an abort-cancelled context.
	time.Sleep(20 * time.Millisecond)
	assert.Len(t, reporter.allReports(), 1)
}

func TestObserver_Run_SkipsFinalWhenCtxAlreadyCancelled(t *testing.T) {
	// Exercises the D9 pin ("cancelled ctx => no final update") when the context is cancelled
	// for a reason other than an abort response (e.g. an external shutdown/timeout) — Observer
	// must not distinguish the cancellation's cause, only that ctx.Err() != nil at done-time.
	reporter := &fakeReporter{}
	p := NewProgress(RunStatus{RunId: "run-1", Status: IN_PROGRESS})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	o := Observer{Interval: time.Hour, Reporter: reporter}

	go o.Run(ctx, cancel, done, p)

	cancel()
	close(done)

	time.Sleep(20 * time.Millisecond)
	assert.Empty(t, reporter.allReports())
}

func TestObserver_Run_RetriesSameDiffAfterReportError(t *testing.T) {
	reporter := &fakeReporter{errOn: 1, err: errors.New("boom")}
	p := NewProgress(RunStatus{RunId: "run-1", Status: IN_PROGRESS, Steps: []StepStatus{{Name: "a"}}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	o := Observer{Interval: 5 * time.Millisecond, Reporter: reporter}

	go o.Run(ctx, cancel, done, p)

	reporter.waitForNth(t, 1)
	second := reporter.waitForNth(t, 2)
	assert.Equal(t, []string{"a"}, stepNames(second.Steps), "a failed send must not be treated as delivered — the same diff is retried")

	close(done)
}

func TestDiffSteps(t *testing.T) {
	t.Run("nothing previously sent sends everything", func(t *testing.T) {
		curr := []StepStatus{{Name: "a"}, {Name: "b"}}
		got := diffSteps(nil, curr)
		assert.Equal(t, curr, got)
	})

	t.Run("unchanged steps are omitted", func(t *testing.T) {
		prev := []StepStatus{{Name: "a", Status: IN_PROGRESS}}
		curr := []StepStatus{{Name: "a", Status: IN_PROGRESS}}
		assert.Nil(t, diffSteps(prev, curr))
	})

	t.Run("a status change is included", func(t *testing.T) {
		prev := []StepStatus{{Name: "a", Status: IN_PROGRESS}}
		curr := []StepStatus{{Name: "a", Status: SUCCEEDED}}
		got := diffSteps(prev, curr)
		require.Len(t, got, 1)
		assert.Equal(t, SUCCEEDED, got[0].Status)
	})

	t.Run("a message-only change is included (the running step whose log grew)", func(t *testing.T) {
		prev := []StepStatus{{Name: "a", LogStartIdx: 0}}
		curr := []StepStatus{{Name: "a", LogStartIdx: 42}}
		got := diffSteps(prev, curr)
		require.Len(t, got, 1)
		assert.Equal(t, int64(42), got[0].LogStartIdx)
	})

	t.Run("a new step not present before is included", func(t *testing.T) {
		prev := []StepStatus{{Name: "a"}}
		curr := []StepStatus{{Name: "a"}, {Name: "b"}}
		got := diffSteps(prev, curr)
		require.Len(t, got, 1)
		assert.Equal(t, "b", got[0].Name)
	})

	t.Run("empty current yields nil", func(t *testing.T) {
		assert.Nil(t, diffSteps([]StepStatus{{Name: "a"}}, nil))
	})
}
