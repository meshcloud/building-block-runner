package tf

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	meshapi "github.com/meshcloud/building-block-runner/internal/meshapi"
)

type Worker struct {
	workerNumber         int
	workerDir            string
	timeout              time.Duration
	workerIn             chan workerToken
	workerOut            chan workerToken
	runApi               RunApi
	tfBinaries           *TfBinaries
	log                  *slog.Logger
	statusUpdateInterval time.Duration
	dec                  Decryptor
	// meter receives the D12 generic standalone-runner metrics (§4.3). A nil meter (the
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

func (w *Worker) work() {
	w.log.Info("Started")

	handleWork := true

	for handleWork {
		token := <-w.workerIn
		switch token {

		case stop:
			w.log.Info("Stopped")
			w.workerOut <- stopped
			handleWork = false

		case work:
			run, err := w.runApi.FetchRunDetails(fmt.Sprintf("worker-%d", w.workerNumber))
			if err != nil {
				w.handleFetchRunError(err)
			} else {
				w.meterOrNoop().RunClaimed()
				w.tfExecution(run)
				// Clear the runToken after execution to ensure next fetch uses basic auth
				w.runApi.ClearRunToken()
				w.workerOut <- done
			}

		default:
			panic(fmt.Sprintf("Encountered unknown token: %d, fix your code!", token))
		}
	}
}

func (w *Worker) handleFetchRunError(err error) {
	if he, isHTTP := meshapi.AsHttpError(err); isHTTP {
		switch {
		case he.IsNotFound():
			w.workerOut <- norun
		case he.IsConflict():
			w.log.Info("Conflict at coordinator-api.")
			w.workerOut <- norun
		default:
			w.log.Error("unexpected error fetching run", "error", err)
			w.meterOrNoop().PollError()
			w.workerOut <- failed
		}
	} else {
		// golang's net.http disallows multiple Transfer-Encoding headers, but we seem to do this when meshfed
		// proxies run requests to coordinator, this at least skips the error wait time and continues polling as usual
		// we ignore this error silently because it really is not a big deal
		if strings.Contains(err.Error(), "transport connection broken: too many transfer encodings: [\"chunked\" \"chunked\"]") {
			w.workerOut <- norun
		} else {
			w.log.Error("unexpected error fetching run", "error", err)
			w.meterOrNoop().PollError()
			w.workerOut <- failed
		}
	}
}

func (w *Worker) tfExecution(run *Run) {
	w.log.Info("Start execution of run", "run", run.Id, "behavior", run.Behavior.str(), "buildingBlock", run.BuildingBlockName)
	claimedAt := time.Now()

	// provide wd for this run
	cmdDir, err := os.MkdirTemp(w.workerDir, fmt.Sprintf("block-%s-*", run.BuildingBlockId))
	if err != nil {
		w.sendInitFail(run)
		w.meterOrNoop().RunFailed(time.Since(claimedAt))
		return
	}

	err = os.Mkdir(path.Join(cmdDir, "logs"), 0700)
	if err != nil {
		w.sendInitFail(run)
		w.meterOrNoop().RunFailed(time.Since(claimedAt))
		return
	}

	defer func() { _ = os.RemoveAll(cmdDir) }()

	runContextInfo, err := initRunContextInfo(run, w.log, cmdDir)
	if err != nil {
		w.log.Error("Failed to initialize run context", "error", err)
		w.sendInitFail(run)
		w.meterOrNoop().RunFailed(time.Since(claimedAt))
		return
	}
	run.Source.setLog(runContextInfo.logwrap)
	defer runContextInfo.logwrap.Close()

	// create context with timeout settings
	parentCtx, cancel := context.WithTimeout(context.Background(), w.timeout)
	defer cancel()
	workCtx, workCancel := context.WithCancel(parentCtx)

	// prep wait group and signalling channel
	var wg sync.WaitGroup
	wg.Add(2)
	workDoneChannel := make(chan bool)

	go w.workRoutine(workCtx, run, runContextInfo, &wg, workDoneChannel)

	go w.observerRoutine(workCtx, workCancel, run, runContextInfo, &wg, workDoneChannel)

	wg.Wait()

	// runContextInfo.progress reflects the run's actual terminal status here -- unlike the
	// locally-overridden copy observerRoutine reports for async runs (SUCCEEDED -> IN_PROGRESS
	// on the wire, §D4), so RunMetrics count the real outcome. Any other status (e.g. an
	// aborted run whose context was cancelled before tf ever reached a terminal state) is
	// neither a success nor a failure and is left uncounted.
	switch runContextInfo.progress.Snapshot().Status {
	case SUCCEEDED:
		w.meterOrNoop().RunSucceeded(time.Since(claimedAt))
	case FAILED:
		w.meterOrNoop().RunFailed(time.Since(claimedAt))
	}

	w.log.Info("Finished execution of run", "behavior", run.Behavior.str(), "run", run.Id, "buildingBlock", run.BuildingBlockName)
	w.log.Info("-----")
}

// this starts the actual tf command execution.
// sends out a done signal via the signalling channel, once terminated (status independent).
func (w *Worker) workRoutine(ctx context.Context, run *Run, runContextInfo *RunContextInfo, wg *sync.WaitGroup, doneSignallingChan chan bool) {
	defer wg.Done()
	defer func() { doneSignallingChan <- true }()

	params := &TfCmdParams{
		dir:                w.workerDir,
		buildingBlockId:    run.BuildingBlockId,
		tfVersion:          run.TerraformVersion,
		useWorkspaces:      true,
		suggestedWorkspace: run.toWorkspaceStr(),
		vars:               run.Vars,
		source:             run.Source,
		preRunScript:       run.PreRunScript,
		runMode:            run.Behavior.str(),
		planArtifactUrl:    run.PlanArtifactUrl,
		dec:                w.dec,
	}

	var tfCommand TfCmd

	switch run.Behavior {

	case APPLY:
		tfCommand = ApplyCmd(ctx, runContextInfo, params, w.tfBinaries, w.runApi)

	case DETECT:
		tfCommand = PlanCmd(ctx, runContextInfo, params, w.tfBinaries)

	case DESTROY:
		tfCommand = DestroyCmd(ctx, runContextInfo, params, w.tfBinaries)
	}

	tfCommand.initRunSteps()
	err := w.runApi.Register(runContextInfo.runStatus)
	if err != nil {
		runContextInfo.logwrap.PrintlnToLocalLogs("Failed to register as a source for runId: " + runContextInfo.runId)
		runContextInfo.logwrap.PrintlnToLocalLogs(err.Error())
		// Explicitly mark as FAILED so the observer sends the correct final status.
		// Without this, IN_PROGRESS would be sent as the final status, leaving
		// the run stuck until the coordinator eventually times it out.
		runContextInfo.progress.setStatus(FAILED)
	} else {
		runContextInfo.logwrap.PrintlnToLocalLogs(fmt.Sprintf("Registered '%s' as a source for runId: %s", AppConfig.RunnerUuid, runContextInfo.runId))
		tfCommand.execute()
	}
}

// this routine periodically sends out the status updates and finishes, once the workRoutine sends its done signal.
// then we send out a final status update.
func (w *Worker) observerRoutine(ctx context.Context, cancel context.CancelFunc, run *Run, runContextInfo *RunContextInfo, wg *sync.WaitGroup, doneSignallingChan chan bool) {
	defer wg.Done()

	ticker := time.NewTicker(w.statusUpdateInterval)
	defer ticker.Stop()

	for {
		select {

		// tf command is done
		// send out one last update and end routine
		case <-doneSignallingChan:

			// context has been cancelled, we omit the final update, nobody wants it.
			if err := ctx.Err(); err != nil && err == context.Canceled {
				return
			}

			// If we are an async run and we finished here with SUCCEEDED we still will signal a IN_PROGRESS
			// to the coordinator as we basically just handed over execution to the external pipeline.
			reportStatus := runContextInfo.progress.Snapshot()
			finalStatus := reportStatus.Status
			if run.IsAsync && reportStatus.Status == SUCCEEDED {
				finalStatus = IN_PROGRESS
			}
			reportStatus.Status = finalStatus

			// For the final update we do not care about the 'abort-run' flag
			w.log.Info("Sending final status update", "run", runContextInfo.runId, "status", finalStatus.str())
			_, err := w.runApi.UpdateState(&reportStatus)

			if err != nil {
				w.log.Error("Failed to send final status", "run", runContextInfo.runId, "error", err)
				runContextInfo.logwrap.PrintlnToLocalLogs(fmt.Sprintf("Failed to set final state: %s\n", err.Error()))
			} else {
				w.log.Info("Successfully sent final status", "run", runContextInfo.runId, "status", finalStatus.str())
			}

			return

		// send out all updates or if none are there, just an empty one as liveliness update
		// do NOT send in case we are in a terminal state, to prevent duplicate "final updates",
		// as this is handled in the previous case block (workRoutine done)
		case <-ticker.C:
			reportStatus := runContextInfo.progress.Snapshot()
			if !reportStatus.Status.isTerminalState() {
				abort, err := w.runApi.UpdateState(&reportStatus)
				if err != nil {
					runContextInfo.logwrap.PrintlnToLocalLogs(fmt.Sprintf("Failed to update state: %s", err.Error()))
				}

				// in case the run should be aborted, we cancel the work context,
				// so that all tf commands will get the signal
				if abort {
					w.log.Info("Received flag to abort run. Cancelling run context.")
					cancel()
					ticker.Stop()
				}
			}
		}
	}
}

func (w *Worker) sendInitFail(run *Run) {
	summary := "Something went wrong while starting the run."
	_, err := w.runApi.UpdateState(
		&RunStatus{
			RunId:   run.Id,
			Status:  FAILED,
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

// TODO rather inefficient version for now, but io.Seeker does not support Seek(..) on a file opened in append mode.
func fileContentOrEmpty(fileName string, startIdx, endIdx int64) string {
	if b, err := os.ReadFile(fileName); err != nil {
		return ""
	} else {
		return string(b[startIdx:endIdx])
	}
}
