package tf

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/terraform-exec/tfexec"

	"github.com/meshcloud/building-block-runner/internal/dispatch"
	meshapi "github.com/meshcloud/building-block-runner/internal/meshapi"
	"github.com/meshcloud/building-block-runner/internal/report"
)

// TestHandler_ShutdownGraceExpiryReportsAborted is the characterization: an in-flight tf run
// that outlives dispatch.InProcess's shutdown grace gets its tofu command cancelled and is
// reported terminal ABORTED, not left as a stale IN_PROGRESS or as a plain FAILED (the status a
// cancelled tofu command would otherwise produce, see worker.go's tfcmd.fail() ctx.Canceled
// branch).
func TestHandler_ShutdownGraceExpiryReportsAborted(t *testing.T) {
	repoPath := "modules/github/repository/buildingblock"
	repo := makeLocalGitRepo(t, map[string]string{
		repoPath + "/main.tf": "# fixture terraform source, not executed (mock tf facade)\n",
	})

	applyStarted := make(chan struct{})
	mock := &MockedTfFacade{}
	mock.initMockFuncs()
	// Simulate a wedged/long-running apply: it only returns once its ctx is cancelled -- exactly
	// what dispatch.InProcess.Wait does to the run's execCtx once the shutdown grace expires.
	mock.applyFunc = func(ctx context.Context, opts ...tfexec.ApplyOption) error {
		close(applyStarted)
		<-ctx.Done()
		return ctx.Err()
	}

	tfBin, err := ForTestNewTfBin(t.TempDir(), io.Discard, mock)
	if err != nil {
		t.Fatalf("ForTestNewTfBin: %v", err)
	}

	rec := newConcurrencyRecorder()
	const runToken = "token-shutdown"

	handler := NewHandler(HandlerConfig{
		WorkingDir:       t.TempDir(),
		TfCommandTimeout: time.Minute, // long enough that only the shutdown cancels the run
		InitTimeout:      3 * time.Minute,
		WsTimeout:        5 * time.Minute,
		RunnerUuid:       "shutdown-runner",
	}, HandlerDeps{
		TfBinaries: tfBin,
		Meter:      &fakeMeter{},
		Log:        testLogger(),
		NewRunApi: func(token string) RunApi {
			return &recordingRunApi{runToken: token, rec: rec, barrier: waitGroupDone()}
		},
	})

	dto := runDetailsDTO(withRepo(repo.Path, repoPath), withRunToken(runToken))
	raw, mErr := json.Marshal(dto)
	if mErr != nil {
		t.Fatalf("marshal run: %v", mErr)
	}
	cr := dispatch.ClaimedRun{
		Id:      dispatch.RunId("run-shutdown"),
		Type:    meshapi.RunnerTypeTerraform,
		Details: dto,
		RawJson: base64.StdEncoding.EncodeToString(raw),
	}

	// A tiny grace: the apply is deliberately wedged (blocks on ctx.Done()), so Wait must expire
	// the grace and cancel it rather than draining naturally.
	inproc, err := dispatch.NewInProcess(
		map[meshapi.RunnerImplementationType]dispatch.RunHandler{meshapi.RunnerTypeTerraform: handler},
		20*time.Millisecond, testLogger())
	if err != nil {
		t.Fatalf("NewInProcess: %v", err)
	}

	if dErr := inproc.Dispatch(cr); dErr != nil {
		t.Fatalf("Dispatch: %v", dErr)
	}

	select {
	case <-applyStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("apply never started")
	}

	drained := make(chan struct{})
	go func() { inproc.Wait(); close(drained) }()
	select {
	case <-drained:
	case <-time.After(10 * time.Second):
		t.Fatal("Wait did not return after grace expiry (deadlock?)")
	}

	statuses := rec.statuses[runToken]
	if !statuses[report.ABORTED.String()] {
		t.Errorf("runToken %q never reported %s; statuses seen: %v", runToken, report.ABORTED.String(), statuses)
	}
	// The observer's own final update must be SUPPRESSED on a shutdown so ABORTED is the run's
	// sole terminal report. If a terminal FAILED is also reported, live it lands first, drives the
	// run terminal, and the coordinator deletes the ephemeral run key -- so the ABORTED override
	// then 401s and the run ends FAILED (the live-only failure the mock RunApi cannot surface).
	if statuses[report.FAILED.String()] {
		t.Errorf("runToken %q reported terminal %s on shutdown; the observer final must be suppressed so ABORTED overrides cleanly (statuses seen: %v)", runToken, report.FAILED.String(), statuses)
	}
}

// waitGroupDone returns an already-satisfied sync.WaitGroup so recordingRunApi.Register never
// blocks: this test has only one run in flight, so there is no need for the multi-run barrier
// TestHandler_ConcurrentRunsIsolateRunToken uses to force overlap.
func waitGroupDone() *sync.WaitGroup {
	var wg sync.WaitGroup
	wg.Add(1)
	return &wg
}

// failingReportRunApi always fails Report -- the collaborator TestWorker_ShutdownAbortedReport_LogsSendFailure
// uses to exercise the "the ABORTED override itself couldn't be sent" branch in
// Worker.tfExecution (worker.go), which the happy-path shutdown test above never reaches.
type failingReportRunApi struct{}

func (failingReportRunApi) Register(report.RunStatus) error { return nil }
func (failingReportRunApi) Report(report.RunStatus) (bool, error) {
	return false, errors.New("simulated: coordinator unreachable")
}
func (failingReportRunApi) DownloadPredecessorArtifact(string, io.Writer) error { return nil }

// TestWorker_ShutdownAbortedReport_LogsSendFailure drives Worker.tfExecution with an
// already-cancelled shutdown ctx and a RunApi whose Report always fails: the ABORTED override
// (worker.go, right after wg.Wait()) must swallow that failure into a log line rather than panic
// or otherwise abort the goroutine -- there is nothing more tfExecution can do once the run is
// already finishing on a cancelled shutdown context.
func TestWorker_ShutdownAbortedReport_LogsSendFailure(t *testing.T) {
	repoPath := "modules/github/repository/buildingblock"
	repo := makeLocalGitRepo(t, map[string]string{
		repoPath + "/main.tf": "# fixture terraform source, not executed (mock tf facade)\n",
	})

	mock := &MockedTfFacade{}
	mock.initMockFuncs()

	tfBin, err := ForTestNewTfBin(t.TempDir(), io.Discard, mock)
	if err != nil {
		t.Fatalf("ForTestNewTfBin: %v", err)
	}

	var logBuf bytes.Buffer
	w := &Worker{
		workerDir:            t.TempDir(),
		timeout:              time.Minute,
		runApi:               failingReportRunApi{},
		tfBinaries:           tfBin,
		log:                  slog.New(slog.NewTextHandler(&logBuf, nil)),
		statusUpdateInterval: 10 * time.Second,
		meter:                &fakeMeter{},
	}

	dto := runDetailsDTO(withRepo(repo.Path, repoPath))
	run, err := RunDTOToInternal(dto)
	if err != nil {
		t.Fatalf("RunDTOToInternal: %v", err)
	}

	// Already-cancelled: tfExecution must take the shutdown/ABORTED-override branch immediately,
	// without the mock apply ever needing to block on ctx.Done() (unlike the InProcess-driven test
	// above, which forces genuine mid-flight cancellation via the shutdown grace).
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_ = w.tfExecution(ctx, run)

	if !strings.Contains(logBuf.String(), "failed to report ABORTED status on shutdown") {
		t.Errorf("expected a logged ABORTED-report-failure warning, got log:\n%s", logBuf.String())
	}
}
