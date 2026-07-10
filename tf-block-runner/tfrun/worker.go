package tfrun

import (
	"context"
	"fmt"
	"log"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	meshapi "github.com/meshcloud/building-block-runner/go-meshapi-client/meshapi"
)

type Worker struct {
	workerNumber         int
	workerDir            string
	timeout              time.Duration
	workerIn             chan workerToken
	workerOut            chan workerToken
	runApi               RunApi
	tfBinaries           *TfBinaries
	log                  *log.Logger
	statusUpdateInterval time.Duration
}

// defining custom type for context key is best practice
// to avoid possible collisions downstream.
// unlikely in our case, though.
type contextKey struct{}

var runInfoContextKey = contextKey{}

func (w *Worker) work() {
	w.log.Println("Started")

	handleWork := true

	for handleWork {
		token := <-w.workerIn
		switch token {

		case stop:
			w.log.Println("Stopped")
			w.workerOut <- stopped
			handleWork = false

		case work:
			run, err := w.runApi.FetchRunDetails(fmt.Sprintf("worker-%d", w.workerNumber))
			if err != nil {
				w.handleFetchRunError(err)
			} else {
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
	statusError, isStatusError := err.(*meshapi.StatusError)

	if isStatusError {
		switch statusError.Status {
		case 404:
			w.workerOut <- norun
		case 409:
			w.log.Printf("Conflict at coordinator-api.")
			w.workerOut <- norun
		default:
			w.log.Printf("unexpected error: %s\n", err.Error())
			w.workerOut <- failed
		}
	} else {
		// golang's net.http disallows multiple Transfer-Encoding headers, but we seem to do this when meshfed
		// proxies run requests to coordinator, this at least skips the error wait time and continues polling as usual
		// we ignore this error silently because it really is not a big deal
		if strings.Contains(err.Error(), "transport connection broken: too many transfer encodings: [\"chunked\" \"chunked\"]") {
			w.workerOut <- norun
		} else {
			w.log.Printf("unexpected error: %s\n", err.Error())
			w.workerOut <- failed
		}
	}
}

func (w *Worker) tfExecution(run *Run) {
	w.log.Printf("Start execution of run %s: %s %s\n", run.Id, run.Behavior.str(), run.BuildingBlockName)

	// provide wd for this run
	cmdDir, err := os.MkdirTemp(w.workerDir, fmt.Sprintf("block-%s-*", run.BuildingBlockId))
	if err != nil {
		w.sendInitFail(run)
		return
	}

	err = os.Mkdir(path.Join(cmdDir, "logs"), 0700)
	if err != nil {
		w.sendInitFail(run)
		return
	}

	defer os.RemoveAll(cmdDir)

	runContextInfo := initRunContextInfo(run, w.log.Prefix(), w.log.Writer(), cmdDir)
	run.Source.setLog(runContextInfo.logwrap)
	defer runContextInfo.logwrap.Close()

	// create context with timeout settings
	parentCtx, cancel := context.WithTimeout(context.Background(), w.timeout)
	defer cancel()
	workCtx, workCancel := context.WithCancel(context.WithValue(parentCtx, runInfoContextKey, runContextInfo))

	// prep wait group and signalling channel
	var wg sync.WaitGroup
	wg.Add(2)
	workDoneChannel := make(chan bool)

	go w.workRoutine(workCtx, run, &wg, workDoneChannel)

	go w.observerRoutine(workCtx, workCancel, run, &wg, workDoneChannel)

	wg.Wait()

	w.log.Printf("Finished execution of %s run %s: %s\n", run.Behavior.str(), run.Id, run.BuildingBlockName)
	w.log.Println("-----")
}

// this starts the actual tf command execution.
// sends out a done signal via the signalling channel, once terminated (status independent).
func (w *Worker) workRoutine(ctx context.Context, run *Run, wg *sync.WaitGroup, doneSignallingChan chan bool) {
	defer wg.Done()
	defer func() { doneSignallingChan <- true }()

	runContextInfo := ctx.Value(runInfoContextKey).(*RunContextInfo)
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
	}

	var tfCommand TfCmd

	switch run.Behavior {

	case APPLY:
		tfCommand = ApplyCmd(ctx, params, w.tfBinaries, w.runApi)

	case DETECT:
		tfCommand = PlanCmd(ctx, params, w.tfBinaries)

	case DESTROY:
		tfCommand = DestroyCmd(ctx, params, w.tfBinaries)
	}

	tfCommand.initRunSteps()
	err := w.runApi.Register(runContextInfo.runStatus)
	if err != nil {
		runContextInfo.logwrap.PrintlnToLocalLogs("Failed to register as a source for runId: " + runContextInfo.runId)
		runContextInfo.logwrap.PrintlnToLocalLogs(err.Error())
		// Explicitly mark as FAILED so the observer sends the correct final status.
		// Without this, IN_PROGRESS would be sent as the final status, leaving
		// the run stuck until the coordinator eventually times it out.
		runContextInfo.reportStatus.Status = FAILED
	} else {
		runContextInfo.logwrap.PrintlnToLocalLogs(fmt.Sprintf("Registered '%s' as a source for runId: %s", AppConfig.RunnerUuid, runContextInfo.runId))
		tfCommand.execute()
	}
}

// this routine periodically sends out the status updates and finishes, once the workRoutine sends its done signal.
// then we send out a final status update.
func (w *Worker) observerRoutine(ctx context.Context, cancel context.CancelFunc, run *Run, wg *sync.WaitGroup, doneSignallingChan chan bool) {
	defer wg.Done()

	ticker := time.NewTicker(w.statusUpdateInterval)
	defer ticker.Stop()

	runContextInfo := ctx.Value(runInfoContextKey).(*RunContextInfo)

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
			finalStatus := runContextInfo.reportStatus.Status
			if run.IsAsync && runContextInfo.reportStatus.Status == SUCCEEDED {
				finalStatus = IN_PROGRESS
			}

			reportStatus := runContextInfo.reportStatus
			reportStatus.Status = finalStatus

			// For the final update we do not care about the 'abort-run' flag
			w.log.Printf("Sending final status update for run %s: %s", runContextInfo.runId, finalStatus.str())
			_, err := w.runApi.UpdateState(&reportStatus)

			if err != nil {
				w.log.Printf("ERROR: Failed to send final status for run %s: %v", runContextInfo.runId, err)
				runContextInfo.logwrap.PrintlnToLocalLogs(fmt.Sprintf("Failed to set final state: %s\n", err.Error()))
			} else {
				w.log.Printf("Successfully sent final status for run %s: %s", runContextInfo.runId, finalStatus.str())
			}

			return

		// send out all updates or if none are there, just an empty one as liveliness update
		// do NOT send in case we are in a terminal state, to prevent duplicate "final updates",
		// as this is handled in the previous case block (workRoutine done)
		case <-ticker.C:
			if !runContextInfo.reportStatus.Status.isTerminalState() {
				abort, err := w.runApi.UpdateState(&runContextInfo.reportStatus)
				if err != nil {
					runContextInfo.logwrap.PrintlnToLocalLogs(fmt.Sprintf("Failed to update state: %s", err.Error()))
				}

				// in case the run should be aborted, we cancel the work context,
				// so that all tf commands will get the signal
				if abort {
					w.log.Printf("Received flag to abort run. Cancelling run context.")
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
		w.log.Printf("Failed to update initial state: %s\n", err.Error())
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
