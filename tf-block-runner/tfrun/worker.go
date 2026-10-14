package tfrun

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path"
	"sync"
	"time"

	"github.com/meshcloud/meshstack-cli/client"

	"github.com/meshcloud/building-block-runner/internal/config"
)

type Worker struct {
	cfg                  config.Config
	runApi               RunApi
	tfBinaries           *TfBinaries
	log                  *log.Logger
	statusUpdateInterval time.Duration
	statusRequestTimeout time.Duration
	finalStatusRetry     retrySchedule
}

type contextKey struct{}

var runInfoContextKey = contextKey{}

var ErrFailureReported = errors.New("the run reported its failure")

func Execute(ctx context.Context, logger *log.Logger, cfg config.Config, run client.MeshRunnerRun, api client.MeshRunnerRunClient, tfBinaries *TfBinaries) error {
	tfRun, err := newRun(run, cfg.Tf.SkipHostKeyValidation)
	if err != nil {
		return fmt.Errorf("convert run: %w", err)
	}
	worker := &Worker{
		cfg:                  cfg,
		runApi:               NewRunApi(api, run.Metadata.Uuid, cfg.Uuid),
		tfBinaries:           tfBinaries,
		log:                  logger,
		statusUpdateInterval: time.Second * 10,
		statusRequestTimeout: statusRequestTimeout,
		finalStatusRetry:     finalStatusRetry,
	}
	return worker.ExecuteRun(ctx, tfRun)
}

func (w *Worker) ExecuteRun(ctx context.Context, run *Run) error {
	w.log.Printf("Start execution of run %s: %s %s\n", run.Id, run.Behavior.str(), run.BuildingBlockName)

	if err := os.MkdirAll(w.cfg.Tf.TfParentWorkingDir, 0777); err != nil {
		return fmt.Errorf("failed to create working directory: %w", err)
	}

	cmdDir, err := os.MkdirTemp(w.cfg.Tf.TfParentWorkingDir, fmt.Sprintf("block-%s-*", run.BuildingBlockId))
	if err != nil {
		w.sendInitFail(run)
		return fmt.Errorf("failed to create temp directory: %w: %w", err, ErrFailureReported)
	}

	err = os.Mkdir(path.Join(cmdDir, "logs"), 0700)
	if err != nil {
		w.sendInitFail(run)
		return fmt.Errorf("failed to create logs directory: %w: %w", err, ErrFailureReported)
	}

	defer os.RemoveAll(cmdDir)

	runContextInfo := initRunContextInfo(run, w.log.Prefix(), w.log.Writer(), cmdDir)
	run.Source.setLog(runContextInfo.logwrap)
	defer runContextInfo.logwrap.Close()

	workCtx, workCancel := context.WithCancel(context.WithValue(ctx, runInfoContextKey, runContextInfo))

	var wg sync.WaitGroup
	wg.Add(2)
	workDoneChannel := make(chan bool)

	go w.workRoutine(workCtx, run, &wg, workDoneChannel)
	go w.observerRoutine(workCtx, workCancel, run, &wg, workDoneChannel)

	wg.Wait()

	w.log.Printf("Finished execution of %s run %s: %s\n", run.Behavior.str(), run.Id, run.BuildingBlockName)

	return nil
}

func (w *Worker) workRoutine(ctx context.Context, run *Run, wg *sync.WaitGroup, doneSignallingChan chan bool) {
	defer wg.Done()
	defer func() { doneSignallingChan <- true }()

	runContextInfo := ctx.Value(runInfoContextKey).(*RunContextInfo)
	params := &TfCmdParams{
		cfg:                w.cfg,
		dir:                w.cfg.Tf.TfParentWorkingDir,
		buildingBlockId:    run.BuildingBlockId,
		tfVersion:          run.TerraformVersion,
		useWorkspaces:      true,
		suggestedWorkspace: run.toWorkspaceStr(),
		vars:               run.Vars,
		source:             run.Source,
		preRunScript:       run.PreRunScript,
		runMode:            run.Behavior.str(),
		planArtifactUrl:    run.PlanArtifactUrl,
		artifactUploadUrl:  run.ArtifactUploadUrl,
	}

	var tfCommand TfCmd

	switch run.Behavior {
	case APPLY:
		tfCommand = ApplyCmd(ctx, params, w.tfBinaries, w.runApi)
	case DETECT:
		tfCommand = PlanCmd(ctx, params, w.tfBinaries, w.runApi)
	case DESTROY:
		tfCommand = DestroyCmd(ctx, params, w.tfBinaries)
	}

	tfCommand.initRunSteps()
	err := w.runApi.Register(ctx, runContextInfo.runStatus)
	if err != nil {
		runContextInfo.logwrap.PrintlnToLocalLogs("Failed to register as a source for runId: " + runContextInfo.runId)
		runContextInfo.logwrap.PrintlnToLocalLogs(err.Error())
		// The observer sends the final status, which would stay IN_PROGRESS until meshStack times the run out.
		runContextInfo.reportFailed()
	} else {
		runContextInfo.logwrap.PrintlnToLocalLogs(fmt.Sprintf("Registered '%s' as a source for runId: %s", w.cfg.Uuid, runContextInfo.runId))
		tfCommand.execute()
	}
}

func (w *Worker) observerRoutine(ctx context.Context, cancel context.CancelFunc, run *Run, wg *sync.WaitGroup, doneSignallingChan chan bool) {
	defer wg.Done()

	ticker := time.NewTicker(w.statusUpdateInterval)
	defer ticker.Stop()

	runContextInfo := ctx.Value(runInfoContextKey).(*RunContextInfo)
	abortRequested := false

	for {
		select {
		case <-doneSignallingChan:
			sendFinalStatus(w.runApi, run, runContextInfo, abortRequested, w.finalStatusRetry, w.log)
			return

		case <-ticker.C:
			// The final status goes out once the work routine is done, so a terminal status is not sent twice.
			if status := runContextInfo.reportedStatus(); !status.Status.isTerminalState() {
				abort, err := updateStateWithin(context.Background(), w.runApi, &status, w.statusRequestTimeout)
				if err != nil {
					runContextInfo.logwrap.PrintlnToLocalLogs(fmt.Sprintf("Failed to update state: %s", err.Error()))
				}

				if abort && !abortRequested {
					w.log.Printf("Received flag to abort run. Cancelling run context.")
					abortRequested = true
					cancel()
				}
			}
		}
	}
}

// sendFinalStatus runs after an abort as well, once tofu has exited: meshStack keeps an aborted synchronous
// run in progress, with a valid run key, until the runner reports ABORTED, so tofu can still save its
// partial state and release its state lock.
func sendFinalStatus(api RunApi, run *Run, runContextInfo *RunContextInfo, abortRequested bool, retry retrySchedule, logger *log.Logger) {
	status := runContextInfo.reportedStatus()
	switch {
	case abortRequested:
		status.Status = ABORTED
	case run.IsAsync && status.Status == SUCCEEDED:
		// The run continues in the external pipeline, which reports its end itself.
		status.Status = IN_PROGRESS
	}

	logger.Printf("Sending final status update for run %s: %s", runContextInfo.runId, status.Status.str())
	err := updateStateWithRetry(api, &status, retry, logger)
	switch {
	case err == nil:
		logger.Printf("Successfully sent final status for run %s: %s", runContextInfo.runId, status.Status.str())
	case abortRequested && isClientError(err):
		logger.Printf("meshStack refused the ABORTED status for run %s. A meshStack that does not wait for the runner has already ended the aborted run: %v", runContextInfo.runId, err)
	default:
		logger.Printf("ERROR: Failed to send final status for run %s: %v", runContextInfo.runId, err)
		runContextInfo.logwrap.PrintlnToLocalLogs(fmt.Sprintf("Failed to set final state: %s\n", err.Error()))
	}
}

func (w *Worker) sendInitFail(run *Run) {
	summary := "Something went wrong while starting the run."
	err := updateStateWithRetry(
		w.runApi,
		&RunStatus{
			RunId:   run.Id,
			Status:  FAILED,
			Steps:   nil,
			Summary: &summary,
		},
		w.finalStatusRetry,
		w.log,
	)
	if err != nil {
		w.log.Printf("Failed to update initial state: %s\n", err.Error())
	}
}

// TODO rather inefficient version for now, but io.Seeker does not support Seek(..) on a file opened in append mode
func fileContentOrEmpty(fileName string, startIdx, endIdx int64) string {
	if b, err := os.ReadFile(fileName); err != nil {
		return ""
	} else {
		return string(b[startIdx:endIdx])
	}
}
