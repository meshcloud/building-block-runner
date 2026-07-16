package tf

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/meshcloud/building-block-runner/internal/dispatch"
	meshapi "github.com/meshcloud/building-block-runner/internal/meshapi"
	"github.com/meshcloud/building-block-runner/internal/report"
)

// concurrencyRecorder records, per runToken, which run ids reported through the RunApi built for
// that token and every status they carried. It is the assertion surface for per-run isolation:
// the RunApi handed to run A must only ever see run A's id/status, never run B's.
type concurrencyRecorder struct {
	mu       sync.Mutex
	runIds   map[string]map[string]bool // runToken -> set of run ids seen
	statuses map[string]map[string]bool // runToken -> set of statuses seen
}

func newConcurrencyRecorder() *concurrencyRecorder {
	return &concurrencyRecorder{
		runIds:   make(map[string]map[string]bool),
		statuses: make(map[string]map[string]bool),
	}
}

func (r *concurrencyRecorder) record(runToken, runId, status string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.runIds[runToken] == nil {
		r.runIds[runToken] = make(map[string]bool)
		r.statuses[runToken] = make(map[string]bool)
	}
	r.runIds[runToken][runId] = true
	r.statuses[runToken][status] = true
}

// recordingRunApi is one claimed run's run-scoped RunApi. It is bound to a single runToken at
// construction (never mutated), stamps every Register/UpdateState into the shared recorder tagged
// with that token, and blocks the first Register on a shared barrier so all N runs are provably
// in flight at once before any finishes -- forcing real overlap for the -race check.
type recordingRunApi struct {
	runToken string
	rec      *concurrencyRecorder
	barrier  *sync.WaitGroup
	arrived  sync.Once
}

func (a *recordingRunApi) Register(s report.RunStatus) error {
	a.arrived.Do(func() { a.barrier.Done() })
	a.barrier.Wait() // block until every concurrent run has also registered
	a.rec.record(a.runToken, s.RunId, s.Status.String())
	return nil
}

func (a *recordingRunApi) Report(s report.RunStatus) (bool, error) {
	a.rec.record(a.runToken, s.RunId, s.Status.String())
	return false, nil
}

func (a *recordingRunApi) DownloadPredecessorArtifact(_ string, _ io.Writer) error { return nil }

// TestHandler_ConcurrentRunsIsolateRunToken is the N-concurrent smoke (run with -race).
//
// It stands in for RUNNER_MAX_CONCURRENT_RUNS=2: two overlapping tf runs share ONE Handler (and
// its TfBinaries) through the SAME dispatch.InProcess dispatcher the tf loop wires, exactly as a
// MaxConcurrent=2 loop would dispatch them. Each claimed run must build its OWN RunApi bound to
// its OWN runToken -- the guarantee that replaced the deleted Manager's single mutable
// SetRunToken/ClearRunToken slot -- so run A can never report under run B's token. A registration
// barrier forces genuine simultaneity; giving each run a fresh mock tf facade (ForTestNewTfBin
// with a nil mock) keeps the two goroutines free of shared mutable execution state so -race can
// prove the isolation rather than merely the happy path.
func TestHandler_ConcurrentRunsIsolateRunToken(t *testing.T) {
	const n = 2

	repoPath := "modules/github/repository/buildingblock"
	repo := makeLocalGitRepo(t, map[string]string{
		repoPath + "/main.tf": "# fixture terraform source, not executed (mock tf facade)\n",
	})

	// nil mock => TfBinaries.GetTF hands each run its OWN default MockedTfFacade, so the two
	// concurrent runs never share the facade's mutable stdout/workingDir state.
	tfBin, err := ForTestNewTfBin(t.TempDir(), io.Discard, nil)
	if err != nil {
		t.Fatalf("ForTestNewTfBin: %v", err)
	}

	rec := newConcurrencyRecorder()
	var barrier sync.WaitGroup
	barrier.Add(n)

	meter := &fakeMeter{}

	handler := NewHandler(HandlerConfig{
		WorkingDir:       t.TempDir(),
		TfCommandTimeout: 30 * time.Minute,
		InitTimeout:      3 * time.Minute,
		WsTimeout:        5 * time.Minute,
		RunnerUuid:       "concurrency-runner",
	}, HandlerDeps{
		TfBinaries: tfBin,
		Meter:      meter,
		Log:        testLogger(),
		NewRunApi: func(runToken string) RunApi {
			return &recordingRunApi{runToken: runToken, rec: rec, barrier: &barrier}
		},
	})

	mkRun := func(id, token string) dispatch.ClaimedRun {
		dto := runDetailsDTO(withRepo(repo.Path, repoPath), withRunToken(token))
		dto.Metadata.Uuid = id
		raw, mErr := json.Marshal(dto)
		if mErr != nil {
			t.Fatalf("marshal run %s: %v", id, mErr)
		}
		return dispatch.ClaimedRun{
			Id:      dispatch.RunId(id),
			Type:    meshapi.RunnerTypeTerraform,
			Run:     dto,
			RawJson: base64.StdEncoding.EncodeToString(raw),
		}
	}

	runs := []dispatch.ClaimedRun{
		mkRun("run-a", "token-a"),
		mkRun("run-b", "token-b"),
	}

	inproc, err := dispatch.NewInProcess(
		map[meshapi.RunnerImplementationType]dispatch.RunHandler{meshapi.RunnerTypeTerraform: handler},
		0, testLogger())
	if err != nil {
		t.Fatalf("NewInProcess: %v", err)
	}

	for _, cr := range runs {
		if dErr := inproc.Dispatch(cr); dErr != nil {
			t.Fatalf("Dispatch %s: %v", cr.Id, dErr)
		}
	}

	// Wait for both runs to drain, but never hang the suite if the barrier wedges.
	drained := make(chan struct{})
	go func() { inproc.Wait(); close(drained) }()
	select {
	case <-drained:
	case <-time.After(60 * time.Second):
		t.Fatal("concurrent runs did not drain within 60s (deadlock?)")
	}

	// Per-run isolation: each runToken's RunApi saw ONLY its own run id and reached SUCCEEDED.
	want := map[string]string{"token-a": "run-a", "token-b": "run-b"}
	for token, wantRunId := range want {
		ids := rec.runIds[token]
		if len(ids) != 1 || !ids[wantRunId] {
			t.Errorf("runToken %q RunApi saw run ids %v, want exactly {%q} -- token/run cross-contamination", token, ids, wantRunId)
		}
		if !rec.statuses[token][report.SUCCEEDED.String()] {
			t.Errorf("runToken %q never reached %s; statuses seen: %v", token, report.SUCCEEDED.String(), rec.statuses[token])
		}
	}

	// Both runs metered exactly once as claimed and once as succeeded (no lost/double counts
	// under concurrency).
	if got := meter.snapshot(); got != (meterCounts{claimed: n, succeeded: n}) {
		t.Errorf("meter = %+v, want {claimed:%d succeeded:%d}", got, n, n)
	}
}
