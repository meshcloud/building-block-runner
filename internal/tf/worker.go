package tf

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path"
	"sync"
	"time"

	meshapi "github.com/meshcloud/building-block-runner/internal/meshapi"
	"github.com/meshcloud/building-block-runner/internal/report"
)

// Worker drives one claimed run's tf command execution (tfExecution) plus its periodic status
// reporting. The polling token-loop that used to wrap it (work/handleFetchRunError, driven by
// the deleted Manager) is gone: dispatch.Loop + Handler.Execute (handler.go) now claim runs and
// invoke tfExecution directly, once per run, each with its own Worker value.
type Worker struct {
	workerNumber         int
	workerDir            string
	timeout              time.Duration
	runApi               RunApi
	tfBinaries           *TfBinaries
	log                  *slog.Logger
	statusUpdateInterval time.Duration
	// cfg carries the runner config the execution path reads (timeouts, ssh host-key policy,
	// state-backend fallback URL, runner uuid), threaded explicitly in place of the former
	// AppConfig global.
	cfg execConfig
	// meter receives the generic standalone-runner metrics. A nil meter (the
	// zero value of every pre-existing Worker{} literal, incl. every scenario test) is
	// treated as NoopMeter -- see (*Worker).meterOrNoop -- so this field is strictly
	// additive.
	meter Meter
}

// meterOrNoop returns w.meter, or NoopMeter{} if none was set -- keeps every call site
// below nil-safe without forcing every Worker{} literal (tests included) to set one.
func (w *Worker) meterOrNoop() Meter {
	if w.meter == nil {
		return NoopMeter{}
	}
	return w.meter
}

// tfExecution runs one claimed run to completion. ctx is the caller's shutdown context:
// on the dispatch.Loop path it is dispatch.InProcess's runCtx, cancelled once
// dispatch.InProcess.Wait's grace period expires with this run still in flight; the single-run
// path passes a signal.NotifyContext ctx from cmd/tf. See the ctx.Err() check after wg.Wait()
// below for what happens on that cancellation.
//
// The returned error is scoped to pre-flight failures only (workdir/logs-dir creation, run-context
// init, and registration) and is nil once tofu init/apply has begun; callers needing the actual
// run outcome must read the terminal status reported via runApi, not this return value.
//
// behavior/impl/source/vars are the tf-specific values Handler.Execute derives from run
// (BehaviorFor/ParseTerraformImplementation/terraformImplToGitSource/VariablesFor): run itself
// stays the wire-shaped, runner-agnostic meshapi.Run, so tfExecution takes its own derived
// interpretation of it explicitly rather than a parallel tf-only DTO.
func (w *Worker) tfExecution(ctx context.Context, run *meshapi.Run, behavior Behavior, impl meshapi.TerraformImplementation, source *GitSource, vars map[string]*Variable, runJsonBase64 string) error {
	bbId := run.Spec.BuildingBlock.Uuid
	w.log.Info("Start execution of run", "behavior", behavior.str(), "buildingBlock", run.Spec.BuildingBlock.Spec.DisplayName)
	claimedAt := time.Now()

	// provide wd for this run
	cmdDir, err := os.MkdirTemp(w.workerDir, fmt.Sprintf("block-%s-*", bbId))
	if err != nil {
		w.sendInitFail(run)
		w.meterOrNoop().RunFailed(time.Since(claimedAt))
		return fmt.Errorf("failed to create temp directory: %w", err)
	}

	err = os.Mkdir(path.Join(cmdDir, "logs"), 0700)
	if err != nil {
		w.sendInitFail(run)
		w.meterOrNoop().RunFailed(time.Since(claimedAt))
		return fmt.Errorf("failed to create logs directory: %w", err)
	}

	defer func() { _ = os.RemoveAll(cmdDir) }()

	runContextInfo, err := initRunContextInfo(run, behavior, impl, runJsonBase64, w.log, cmdDir)
	if err != nil {
		w.log.Error("Failed to initialize run context", "error", err)
		w.sendInitFail(run)
		w.meterOrNoop().RunFailed(time.Since(claimedAt))
		return fmt.Errorf("failed to initialize run context: %w", err)
	}
	source.setLog(runContextInfo.logwrap)
	defer runContextInfo.logwrap.Close()

	// execCtx carries the run's execution deadline (TfCommandTimeout) AND abort cancellation; the
	// tofu command runs under it. It is a child of ctx (the caller's shutdown context) so a
	// shutdown that outlives the drain grace also stops the running tofu command, not just the
	// coordinator-abort and timeout cases this context already carried.
	execCtx, execCancel := context.WithTimeout(ctx, w.timeout)
	defer execCancel()
	// abortCtx makes report.Observer skip its final update (its no-final-after-cancel pin) in the
	// two cases where the run must NOT end on the interrupted command's own FAILED: a coordinator
	// abort response (abortRun cancels abortCtx) and a shutdown (abortCtx is a child of ctx, the
	// shutdown context, so ctx's cancellation propagates to it). It must NOT be tied to the execution
	// TIMEOUT: a timed-out run still owes the coordinator a terminal FAILED, so the deadline must
	// leave abortCtx alive. Deriving abortCtx from ctx gives exactly that -- ctx is cancelled only on
	// shutdown, never by execCtx's own TfCommandTimeout deadline (WithTimeout(ctx, ...) fires the
	// child, not the parent). Suppressing the observer's final on shutdown is what lets the explicit
	// ABORTED override below be the run's SOLE terminal report; otherwise the observer's terminal
	// FAILED lands first, the coordinator marks the run terminal and deletes its ephemeral run key,
	// and the ABORTED override then fails auth (a live-only failure the mock RunApi never surfaced).
	abortCtx, abortCancel := context.WithCancel(ctx)
	defer abortCancel()
	abortRun := func() { abortCancel(); execCancel() }

	// prep wait group and completion signal
	var wg sync.WaitGroup
	wg.Add(2)
	workDone := make(chan struct{})

	// registerErr carries a registration failure (if any) out of workRoutine's goroutine.
	// It is written at most once, by workRoutine, strictly before that goroutine's wg.Done();
	// wg.Wait() below happens-after every Done() call, so this read is race-free without
	// further synchronization (registration is "before tofu init/apply
	// begins", so its failure must make tfExecution return an error).
	var registerErr error

	go w.workRoutine(execCtx, run, behavior, impl, source, vars, runContextInfo, &wg, workDone, &registerErr)

	// report.Observer replaces tf's bespoke observerRoutine: it drives the same 10s ticker but
	// PATCHes only the steps that CHANGED since its last send (diffed steps), not the full step
	// snapshot every tick. Its Report/final-send/async-downgrade/abort-cancel behavior matches the
	// former observerRoutine, so the wire body shape (RunStatusUpdateDTO) is unchanged -- only the
	// per-PATCH step SET shrinks. See report.Observer + docs/DEPRECATIONS.md.
	observer := report.Observer{
		Interval: w.statusUpdateInterval,
		Reporter: w.runApi,
		Async:    impl.Async,
		Log:      w.log,
	}
	go func() {
		defer wg.Done()
		observer.Run(abortCtx, abortRun, workDone, runContextInfo.progress)
	}()

	wg.Wait()

	// ctx.Err() != nil here can only mean the caller's shutdown context was cancelled:
	// neither the coordinator-abort path (abortRun, which never touches ctx) nor a plain
	// TfCommandTimeout (execCtx's own deadline, not ctx's) sets it. Force the terminal status to
	// ABORTED. Because abortCtx is a child of ctx, the observer already SUPPRESSED its own final
	// update on this same shutdown, so this is the run's SOLE terminal report -- the coordinator
	// sees ABORTED, never a FAILED that would both mislabel a shutdown as a genuine execution
	// failure and, being terminal, delete the run's ephemeral key before this override could send.
	if ctx.Err() != nil {
		runContextInfo.progress.Mutate(func(s *report.RunStatus) { s.Status = report.ABORTED })
		if _, err := w.runApi.Report(runContextInfo.progress.Snapshot()); err != nil {
			w.log.Warn("failed to report ABORTED status on shutdown", "runId", runContextInfo.runId, "error", err)
		}
	}

	// runContextInfo.progress reflects the run's actual terminal status here -- unlike the
	// locally-overridden copy the observer reports for async runs (SUCCEEDED -> IN_PROGRESS
	// on the wire), so RunMetrics count the real outcome. Any other status (e.g. an
	// aborted run whose context was cancelled before tf ever reached a terminal state) is
	// neither a success nor a failure and is left uncounted.
	switch runContextInfo.progress.Snapshot().Status {
	case report.SUCCEEDED:
		w.meterOrNoop().RunSucceeded(time.Since(claimedAt))
	case report.FAILED:
		w.meterOrNoop().RunFailed(time.Since(claimedAt))
	}

	w.log.Info("Finished execution of run", "behavior", behavior.str(), "buildingBlock", run.Spec.BuildingBlock.Spec.DisplayName)
	w.log.Info("-----")

	return registerErr
}

// this starts the actual tf command execution.
// sends out a done signal via the signalling channel, once terminated (status independent).
// If registration fails, the error is written to *registerErr before the run is marked
// FAILED -- registration happens before tofu init/apply begins, so this is the one
// workRoutine failure tfExecution's caller must be able to observe.
func (w *Worker) workRoutine(ctx context.Context, run *meshapi.Run, behavior Behavior, impl meshapi.TerraformImplementation, source *GitSource, vars map[string]*Variable, runContextInfo *RunContextInfo, wg *sync.WaitGroup, doneSignallingChan chan struct{}, registerErr *error) {
	defer wg.Done()
	defer func() { doneSignallingChan <- struct{}{} }()

	if source != nil {
		source.setSkipHostKeyValidation(w.cfg.SkipHostKeyValidation)
	}

	params := &TfCmdParams{
		dir:                   w.workerDir,
		buildingBlockId:       run.Spec.BuildingBlock.Uuid,
		tfVersion:             impl.TerraformVersion,
		useWorkspaces:         true,
		suggestedWorkspace:    toWorkspaceStr(run),
		vars:                  vars,
		source:                source,
		preRunScript:          impl.PreRunScript,
		runMode:               behavior.str(),
		planArtifactUrl:       run.Links.PlanArtifact.Href,
		skipHostKeyValidation: w.cfg.SkipHostKeyValidation,
		tfCommandTimeout:      w.cfg.TfCommandTimeout,
		initTimeout:           w.cfg.InitTimeout,
		wsTimeout:             w.cfg.WsTimeout,
		apiBackendUrl:         w.cfg.ApiBackendUrl,
	}

	var tfCommand TfCmd

	switch behavior {

	case APPLY:
		tfCommand = ApplyCmd(ctx, runContextInfo, params, w.tfBinaries, w.runApi)

	case DETECT:
		tfCommand = PlanCmd(ctx, runContextInfo, params, w.tfBinaries)

	case DESTROY:
		tfCommand = DestroyCmd(ctx, runContextInfo, params, w.tfBinaries)
	}

	tfCommand.initRunSteps()
	err := w.runApi.Register(*runContextInfo.runStatus)
	if err != nil {
		runContextInfo.logwrap.PrintlnToLocalLogs("Failed to register as a source for runId: " + runContextInfo.runId)
		runContextInfo.logwrap.PrintlnToLocalLogs(err.Error())
		// Explicitly mark as FAILED so the observer sends the correct final status.
		// Without this, IN_PROGRESS would be sent as the final status, leaving
		// the run stuck until the coordinator eventually times it out. Only the status is
		// overridden; the steps stay as last published (nil before any commit).
		runContextInfo.progress.Mutate(func(s *report.RunStatus) { s.Status = report.FAILED })
		*registerErr = err
	} else {
		runContextInfo.logwrap.PrintlnToLocalLogs(fmt.Sprintf("Registered '%s' as a source for runId: %s", w.cfg.RunnerUuid, runContextInfo.runId))
		tfCommand.execute()
	}
}

func (w *Worker) sendInitFail(run *meshapi.Run) {
	summary := "Something went wrong while starting the run."
	_, err := w.runApi.Report(
		report.RunStatus{
			RunId:   run.Metadata.Uuid,
			Status:  report.FAILED,
			Steps:   nil,
			Summary: &summary,
		},
	)
	if err != nil {
		w.log.Error("Failed to update initial state", "error", err)
	}
}

// to inline string pointers.
func message(text string) *string {
	return &text
}

// fileContentOrEmpty reads the byte range [startIdx, endIdx) from fileName. It opens its own
// read handle and uses ReadAt rather than Seek+Read, since the writer keeps the log open in
// append mode, which breaks io.Seeker on that fd; a fresh os.Open sidesteps that entirely.
// An endIdx (or startIdx) past the current file size is clamped to the bytes actually read
// (ReadAt's short read on io.EOF) instead of panicking on a racing writer's stale size estimate.
func fileContentOrEmpty(fileName string, startIdx, endIdx int64) string {
	if startIdx < 0 || endIdx < startIdx {
		return ""
	}

	f, err := os.Open(fileName)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()

	buf := make([]byte, endIdx-startIdx)
	n, err := f.ReadAt(buf, startIdx)
	if err != nil && n == 0 {
		return ""
	}

	return string(buf[:n])
}
