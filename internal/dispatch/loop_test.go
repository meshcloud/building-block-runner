package dispatch

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	meshapi "github.com/meshcloud/building-block-runner/internal/meshapi"
)

// fakeDispatcher is a test double for Dispatcher that records every Dispatch call and
// reports a configurable in-flight count -- the dissolution-era heir of the former
// internal/controller fakeJobManager (controller_capacity_test.go).
type fakeDispatcher struct {
	// mu guards the fields below for TestLoop_StartRunsAndStopExits, the one test that reads
	// them concurrently with the loop's own goroutine; every other test in this file drives
	// the fake synchronously (no Start/goroutine involved) and never needs it.
	mu            sync.Mutex
	dispatchCalls []ClaimedRun
	dispatchErr   error

	inFlight    int
	inFlightErr error
}

func (f *fakeDispatcher) Dispatch(run ClaimedRun) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.dispatchErr != nil {
		return f.dispatchErr
	}
	f.dispatchCalls = append(f.dispatchCalls, run)
	f.inFlight++ // a freshly dispatched run counts towards the in-flight total
	return nil
}

func (f *fakeDispatcher) InFlight() (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.inFlightErr != nil {
		return 0, f.inFlightErr
	}
	return f.inFlight, nil
}

// dispatchCount returns the number of successful Dispatch calls so far, safe to call
// concurrently with the loop goroutine.
func (f *fakeDispatcher) dispatchCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.dispatchCalls)
}

// fakeStatusApi is a test double for StatusApi.
type fakeStatusApi struct {
	registerSourceErr error
	updateStatusErr   error

	registeredSourceRunId RunId
	updatedStatusRunId    RunId
	updatedStatus         string
	updatedMessage        string
}

func (f *fakeStatusApi) RegisterSource(runId RunId) error {
	f.registeredSourceRunId = runId
	return f.registerSourceErr
}

func (f *fakeStatusApi) UpdateRunStatus(runId RunId, status, summary, stepMessage string) error {
	f.updatedStatusRunId = runId
	f.updatedStatus = status
	f.updatedMessage = summary
	return f.updateStatusErr
}

// queueClaimer serves a fixed queue of runs and then returns 404 (no run available), like
// the real API draining a backlog.
type queueClaimer struct {
	queue []ClaimedRun
	err   error // returned once the queue is drained, or immediately if set and queue is empty
	idx   int
}

func (q *queueClaimer) Claim() (ClaimedRun, error) {
	if q.idx >= len(q.queue) {
		if q.err != nil {
			return ClaimedRun{}, q.err
		}
		return ClaimedRun{}, meshapi.HttpError{StatusCode: 404}
	}
	item := q.queue[q.idx]
	q.idx++
	return item, nil
}

// buildClaimedRun builds a minimal ClaimedRun for a given implementation type, mirroring
// the former controller_test.go's buildRunDetailsWithImplType.
func buildClaimedRun(t *testing.T, id string) ClaimedRun {
	t.Helper()
	implJSON, err := json.Marshal(map[string]string{"type": "TERRAFORM"})
	if err != nil {
		t.Fatalf("failed to marshal implementation: %v", err)
	}

	dto := &meshapi.RunDetailsDTO{
		Metadata: meshapi.RunMetaDTO{Uuid: id},
		Spec: meshapi.RunSpecDTO{
			BuildingBlock: meshapi.BuildingBlockSpecDTO{
				Uuid: "bb-uuid-1",
			},
			Definition: meshapi.DefinitionSpecDTO{
				Uuid: "def-uuid-1",
				Spec: meshapi.DefinitionDetailsSpecDTO{
					Implementation: json.RawMessage(implJSON),
				},
			},
		},
	}

	rawBytes, err := json.Marshal(dto)
	if err != nil {
		t.Fatalf("failed to marshal run details: %v", err)
	}

	return ClaimedRun{
		Id:      RunId(id),
		Details: dto,
		RawJson: base64.StdEncoding.EncodeToString(rawBytes),
	}
}

func newTestLoop(claimer Claimer, dispatcher Dispatcher, statusApi StatusApi, cfg LoopConfig) *Loop {
	if cfg.PollInterval == 0 {
		cfg.PollInterval = 1 // never actually ticks in these tests (processNextRun/drainRuns called directly)
	}
	return NewLoop(cfg, LoopDeps{
		RunnerUuid: "runner-uuid",
		Claimer:    claimer,
		Dispatcher: dispatcher,
		StatusApi:  statusApi,
		Classify:   ControllerClaimClassifier,
		Metrics:    NewMetricsCollector(),
	})
}

func TestProcessNextRun_NoRunAvailable(t *testing.T) {
	claimer := &queueClaimer{}
	status := &fakeStatusApi{}
	l := newTestLoop(claimer, &fakeDispatcher{}, status, LoopConfig{MaxConcurrent: 10})

	if got := l.processNextRun(); got != noRunAvailable {
		t.Errorf("expected noRunAvailable, got %v", got)
	}
	if status.registeredSourceRunId != "" {
		t.Error("expected no RegisterSource call for 404 fetch")
	}
}

func TestProcessNextRun_FetchError_LogsAndReturns(t *testing.T) {
	claimer := &queueClaimer{err: errors.New("connection refused")}
	status := &fakeStatusApi{}
	l := newTestLoop(claimer, &fakeDispatcher{}, status, LoopConfig{MaxConcurrent: 10})

	if got := l.processNextRun(); got != noRunAvailable {
		t.Errorf("expected noRunAvailable, got %v", got)
	}
	if status.registeredSourceRunId != "" {
		t.Error("expected no RegisterSource for non-404 fetch error")
	}
}

func TestProcessNextRun_ImplTypeParseFailure_ReportsFailure(t *testing.T) {
	claimer := &queueClaimer{queue: []ClaimedRun{{
		Id:      "run-bad-impl",
		Details: &meshapi.RunDetailsDTO{Metadata: meshapi.RunMetaDTO{Uuid: "run-bad-impl"}},
		RawJson: "",
	}}}
	status := &fakeStatusApi{}
	l := newTestLoop(claimer, &fakeDispatcher{}, status, LoopConfig{MaxConcurrent: 10})

	if got := l.processNextRun(); got != processFailed {
		t.Errorf("expected processFailed, got %v", got)
	}
	if status.updatedStatus != "FAILED" {
		t.Errorf("expected FAILED status, got %q", status.updatedStatus)
	}
}

func TestProcessNextRun_UnhandledType_ReportsFailure(t *testing.T) {
	run := buildClaimedRun(t, "run-uuid-1")
	claimer := &queueClaimer{queue: []ClaimedRun{run}}
	status := &fakeStatusApi{}
	dispatcher := &fakeDispatcher{dispatchErr: &UnhandledTypeError{
		Type:    meshapi.RunnerTypeTerraform,
		Message: "no implementation handler configured for type 'TERRAFORM'",
	}}
	l := newTestLoop(claimer, dispatcher, status, LoopConfig{MaxConcurrent: 10})

	if got := l.processNextRun(); got != processFailed {
		t.Errorf("expected processFailed, got %v", got)
	}
	if status.registeredSourceRunId != run.Id {
		t.Errorf("expected RegisterSource for run %q, got %q", run.Id, status.registeredSourceRunId)
	}
	if status.updatedStatus != "FAILED" {
		t.Errorf("expected status FAILED, got %q", status.updatedStatus)
	}
}

// TestProcessNextRun_DecryptStyleFailure_IsReported is the flipped test (was
// TestProcessNextRun_SilentDispatchFailure_NeverReports): the former silent decrypt-failure
// quirk is gone, so every dispatch error -- including one carrying decrypt-failure guidance --
// now registers the source and reports FAILED with the error text (never suppress
// silently).
func TestProcessNextRun_DecryptStyleFailure_IsReported(t *testing.T) {
	run := buildClaimedRun(t, "run-uuid-2")
	claimer := &queueClaimer{queue: []ClaimedRun{run}}
	status := &fakeStatusApi{}
	const msg = "Failed to decrypt run details for run run-uuid-2: key mismatch"
	dispatcher := &fakeDispatcher{dispatchErr: errors.New(msg)}
	l := newTestLoop(claimer, dispatcher, status, LoopConfig{MaxConcurrent: 10})

	if got := l.processNextRun(); got != processFailed {
		t.Errorf("expected processFailed, got %v", got)
	}
	if status.registeredSourceRunId != run.Id {
		t.Errorf("expected RegisterSource for run %q, got %q", run.Id, status.registeredSourceRunId)
	}
	if status.updatedStatus != "FAILED" {
		t.Errorf("expected FAILED, got %q", status.updatedStatus)
	}
	if status.updatedMessage != msg {
		t.Errorf("expected message %q, got %q", msg, status.updatedMessage)
	}
}

func TestProcessNextRun_OtherDispatchError_ReportsErrorTextVerbatim(t *testing.T) {
	run := buildClaimedRun(t, "run-uuid-3")
	claimer := &queueClaimer{queue: []ClaimedRun{run}}
	status := &fakeStatusApi{}
	dispatcher := &fakeDispatcher{dispatchErr: errors.New("Failed to create job for run: quota exceeded")}
	l := newTestLoop(claimer, dispatcher, status, LoopConfig{MaxConcurrent: 10})

	if got := l.processNextRun(); got != processFailed {
		t.Errorf("expected processFailed, got %v", got)
	}
	if status.updatedStatus != "FAILED" {
		t.Errorf("expected FAILED, got %q", status.updatedStatus)
	}
}

func TestProcessNextRun_SuccessReturnsRunProcessed(t *testing.T) {
	run := buildClaimedRun(t, "run-uuid-4")
	claimer := &queueClaimer{queue: []ClaimedRun{run}}
	dispatcher := &fakeDispatcher{}
	l := newTestLoop(claimer, dispatcher, &fakeStatusApi{}, LoopConfig{MaxConcurrent: 10})

	if got := l.processNextRun(); got != runProcessed {
		t.Errorf("expected runProcessed, got %v", got)
	}
	if len(dispatcher.dispatchCalls) != 1 {
		t.Fatalf("expected 1 dispatch call, got %d", len(dispatcher.dispatchCalls))
	}
	if dispatcher.dispatchCalls[0].Type != meshapi.RunnerTypeTerraform {
		t.Errorf("expected resolved Type TERRAFORM, got %q", dispatcher.dispatchCalls[0].Type)
	}
}

func TestReportRunFailure_RegistersSourceThenUpdatesStatus(t *testing.T) {
	status := &fakeStatusApi{}
	l := newTestLoop(&queueClaimer{}, &fakeDispatcher{}, status, LoopConfig{})

	l.reportRunFailure("run-id-42", "some error occurred")

	if status.registeredSourceRunId != "run-id-42" {
		t.Errorf("expected RegisterSource called with %q, got %q", "run-id-42", status.registeredSourceRunId)
	}
	if status.updatedStatusRunId != "run-id-42" {
		t.Errorf("expected UpdateRunStatus called with %q, got %q", "run-id-42", status.updatedStatusRunId)
	}
	if status.updatedStatus != "FAILED" {
		t.Errorf("expected status %q, got %q", "FAILED", status.updatedStatus)
	}
}

func TestReportRunFailure_StopsIfRegisterSourceFails(t *testing.T) {
	status := &fakeStatusApi{registerSourceErr: errors.New("network error")}
	l := newTestLoop(&queueClaimer{}, &fakeDispatcher{}, status, LoopConfig{})

	l.reportRunFailure("run-id-99", "some error")

	if status.updatedStatusRunId != "" {
		t.Error("expected UpdateRunStatus NOT called when RegisterSource fails")
	}
}

func TestReportRunFailure_LogsWhenUpdateStatusFails(t *testing.T) {
	status := &fakeStatusApi{updateStatusErr: errors.New("patch failed")}
	l := newTestLoop(&queueClaimer{}, &fakeDispatcher{}, status, LoopConfig{})

	// Exercises the branch where RegisterSource succeeds but UpdateRunStatus fails; there is
	// nothing further to assert on the loop's exported state (the failure is logged), but the
	// call must not panic and RegisterSource must still have been attempted.
	l.reportRunFailure("run-id-1", "boom")

	if status.registeredSourceRunId != "run-id-1" {
		t.Error("expected RegisterSource to still be attempted")
	}
}

func TestLoop_StartRunsAndStopExits(t *testing.T) {
	claimer := &queueClaimer{queue: []ClaimedRun{buildClaimedRun(t, "r1")}}
	dispatcher := &fakeDispatcher{}
	l := newTestLoop(claimer, dispatcher, &fakeStatusApi{}, LoopConfig{MaxConcurrent: 10, PollInterval: time.Millisecond})

	var wg sync.WaitGroup
	wg.Add(1)
	l.Start(&wg)

	// Poll until the seeded run has been dispatched, then stop the loop and wait for run() to
	// exit -- exercising Start/run/Stop end to end (they only run in production today).
	deadline := time.Now().Add(2 * time.Second)
	for dispatcher.dispatchCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if dispatcher.dispatchCount() == 0 {
		t.Fatal("expected the seeded run to be dispatched before the deadline")
	}

	l.Stop()
	waitDone := make(chan struct{})
	go func() {
		wg.Wait()
		close(waitDone)
	}()
	select {
	case <-waitDone:
	case <-time.After(2 * time.Second):
		t.Fatal("expected the loop goroutine to exit after Stop")
	}
}

func TestDrainRuns_ProcessesBacklogBackToBack(t *testing.T) {
	claimer := &queueClaimer{queue: []ClaimedRun{
		buildClaimedRun(t, "r1"),
		buildClaimedRun(t, "r2"),
		buildClaimedRun(t, "r3"),
	}}
	dispatcher := &fakeDispatcher{}
	l := newTestLoop(claimer, dispatcher, &fakeStatusApi{}, LoopConfig{MaxConcurrent: 10})

	l.drainRuns()

	if len(dispatcher.dispatchCalls) != 3 {
		t.Errorf("expected 3 runs dispatched in one drain cycle, got %d", len(dispatcher.dispatchCalls))
	}
}

func TestDrainRuns_StopsAtCapacity(t *testing.T) {
	claimer := &queueClaimer{queue: []ClaimedRun{
		buildClaimedRun(t, "r1"),
		buildClaimedRun(t, "r2"),
		buildClaimedRun(t, "r3"),
		buildClaimedRun(t, "r4"),
		buildClaimedRun(t, "r5"),
	}}
	dispatcher := &fakeDispatcher{inFlight: 1} // 1 already in flight -> only 2 slots free
	l := newTestLoop(claimer, dispatcher, &fakeStatusApi{}, LoopConfig{MaxConcurrent: 3})

	l.drainRuns()

	if len(dispatcher.dispatchCalls) != 2 {
		t.Errorf("expected 2 runs dispatched (capacity 3 minus 1 in flight), got %d", len(dispatcher.dispatchCalls))
	}
	if claimer.idx != 2 {
		t.Errorf("expected only 2 runs claimed from the API, got %d", claimer.idx)
	}
}

func TestDrainRuns_SkipsWhenAlreadyAtCapacity(t *testing.T) {
	claimer := &queueClaimer{queue: []ClaimedRun{
		buildClaimedRun(t, "r1"),
		buildClaimedRun(t, "r2"),
	}}
	dispatcher := &fakeDispatcher{inFlight: 3} // already at the limit
	l := newTestLoop(claimer, dispatcher, &fakeStatusApi{}, LoopConfig{MaxConcurrent: 3})

	l.drainRuns()

	if len(dispatcher.dispatchCalls) != 0 {
		t.Errorf("expected no runs dispatched when at capacity, got %d", len(dispatcher.dispatchCalls))
	}
	if claimer.idx != 0 {
		t.Errorf("expected no runs claimed when at capacity, got %d", claimer.idx)
	}
}

func TestDrainRuns_StopsOnProcessFailure(t *testing.T) {
	claimer := &queueClaimer{queue: []ClaimedRun{
		buildClaimedRun(t, "r1"),
		buildClaimedRun(t, "r2"),
		buildClaimedRun(t, "r3"),
	}}
	status := &fakeStatusApi{}
	dispatcher := &fakeDispatcher{dispatchErr: errors.New("quota exceeded")}
	l := newTestLoop(claimer, dispatcher, status, LoopConfig{MaxConcurrent: 10})

	l.drainRuns()

	if claimer.idx != 1 {
		t.Errorf("expected draining to stop after the first failure, claimed %d runs", claimer.idx)
	}
	if status.updatedStatus != "FAILED" {
		t.Errorf("expected the failed run to be reported FAILED, got %q", status.updatedStatus)
	}
}

func TestAvailableCapacity(t *testing.T) {
	t.Run("partial capacity", func(t *testing.T) {
		l := newTestLoop(&queueClaimer{}, &fakeDispatcher{inFlight: 4}, &fakeStatusApi{}, LoopConfig{MaxConcurrent: 10})
		if got := l.availableCapacity(); got != 6 {
			t.Errorf("expected 6 available, got %d", got)
		}
	})

	t.Run("at capacity returns zero", func(t *testing.T) {
		l := newTestLoop(&queueClaimer{}, &fakeDispatcher{inFlight: 5}, &fakeStatusApi{}, LoopConfig{MaxConcurrent: 5})
		if got := l.availableCapacity(); got != 0 {
			t.Errorf("expected 0 available, got %d", got)
		}
	})

	t.Run("over capacity returns zero, never negative", func(t *testing.T) {
		l := newTestLoop(&queueClaimer{}, &fakeDispatcher{inFlight: 8}, &fakeStatusApi{}, LoopConfig{MaxConcurrent: 5})
		if got := l.availableCapacity(); got != 0 {
			t.Errorf("expected 0 available, got %d", got)
		}
	})

	t.Run("unlimited when negative", func(t *testing.T) {
		l := newTestLoop(&queueClaimer{}, &fakeDispatcher{inFlight: 999}, &fakeStatusApi{}, LoopConfig{MaxConcurrent: -1})
		if got := l.availableCapacity(); got != maxDrainPerCycleUnlimited {
			t.Errorf("expected unlimited (%d), got %d", maxDrainPerCycleUnlimited, got)
		}
	})

	t.Run("count error skips cycle", func(t *testing.T) {
		l := newTestLoop(&queueClaimer{}, &fakeDispatcher{inFlightErr: errors.New("api down")}, &fakeStatusApi{}, LoopConfig{MaxConcurrent: 10})
		if got := l.availableCapacity(); got != 0 {
			t.Errorf("expected 0 available on count error, got %d", got)
		}
	})
}

func TestDrainRuns_SkipsDuringBackoff(t *testing.T) {
	claimer := &queueClaimer{queue: []ClaimedRun{buildClaimedRun(t, "r1")}}
	dispatcher := &fakeDispatcher{}
	l := newTestLoop(claimer, dispatcher, &fakeStatusApi{}, LoopConfig{MaxConcurrent: 10})
	l.backoffUntil = time.Now().Add(time.Hour)

	l.drainRuns()

	if len(dispatcher.dispatchCalls) != 0 {
		t.Errorf("expected drainRuns to skip claiming while backoffUntil is in the future, got %d dispatches", len(dispatcher.dispatchCalls))
	}
}
