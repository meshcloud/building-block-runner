package tfrun

import (
	"context"
	"fmt"
	"log"
	"os"
	"path"
	"sync"
	"time"
)

// SingleRunWorker executes a single terraform run without polling or fetching from an API.
// It's designed for use in Kubernetes where the run details are provided via environment variable.
type SingleRunWorker struct {
	workerDir            string
	timeout              time.Duration
	runApi               RunApi
	tfBinaries           *TfBinaries
	log                  *log.Logger
	statusUpdateInterval time.Duration
	statusRequestTimeout time.Duration
	finalStatusRetry     retrySchedule
}

// NewSingleRunWorker creates a new single-run worker
func NewSingleRunWorker(logger *log.Logger, workerDir string, timeoutMins int, tfbin *TfBinaries) *SingleRunWorker {
	return &SingleRunWorker{
		workerDir:            workerDir,
		timeout:              time.Minute * time.Duration(timeoutMins),
		runApi:               NewRunApi(),
		tfBinaries:           tfbin,
		log:                  logger,
		statusUpdateInterval: time.Second * 10,
		statusRequestTimeout: statusRequestTimeout,
		finalStatusRetry:     finalStatusRetry,
	}
}

// NewSingleRunWorkerWithApi creates a new single-run worker with a provided API client
// This is used in Kubernetes mode where the API client needs the runToken from the run spec
func NewSingleRunWorkerWithApi(logger *log.Logger, workerDir string, timeoutMins int, tfbin *TfBinaries, api RunApi) *SingleRunWorker {
	return &SingleRunWorker{
		workerDir:            workerDir,
		timeout:              time.Minute * time.Duration(timeoutMins),
		runApi:               api,
		tfBinaries:           tfbin,
		log:                  logger,
		statusUpdateInterval: time.Second * 10,
		statusRequestTimeout: statusRequestTimeout,
		finalStatusRetry:     finalStatusRetry,
	}
}

// ExecuteRun executes a single run
func (w *SingleRunWorker) ExecuteRun(run *Run) error {
	w.log.Printf("Start execution of run %s: %s %s\n", run.Id, run.Behavior.str(), run.BuildingBlockName)

	// Ensure working directory exists, this is required otherwise we cannot create temp dirs inside it
	if err := os.MkdirAll(w.workerDir, 0777); err != nil {
		return fmt.Errorf("failed to create working directory: %w", err)
	}

	// provide wd for this run
	cmdDir, err := os.MkdirTemp(w.workerDir, fmt.Sprintf("block-%s-*", run.BuildingBlockId))
	if err != nil {
		w.sendInitFail(run)
		return fmt.Errorf("failed to create temp directory: %w", err)
	}

	err = os.Mkdir(path.Join(cmdDir, "logs"), 0700)
	if err != nil {
		w.sendInitFail(run)
		return fmt.Errorf("failed to create logs directory: %w", err)
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

	return nil
}

// workRoutine starts the actual tf command execution
func (w *SingleRunWorker) workRoutine(ctx context.Context, run *Run, wg *sync.WaitGroup, doneSignallingChan chan bool) {
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
	err := w.runApi.Register(runContextInfo.runStatus)
	if err != nil {
		runContextInfo.logwrap.PrintlnToLocalLogs("Failed to register as a source for runId: " + runContextInfo.runId)
		runContextInfo.logwrap.PrintlnToLocalLogs(err.Error())
		// Explicitly mark as FAILED so the observer sends the correct final status.
		// Without this, IN_PROGRESS would be sent as the final status, leaving
		// the run stuck until the coordinator eventually times it out.
		runContextInfo.reportFailed()
	} else {
		runContextInfo.logwrap.PrintlnToLocalLogs(fmt.Sprintf("Registered '%s' as a source for runId: %s", AppConfig.RunnerUuid, runContextInfo.runId))
		tfCommand.execute()
	}
}

// observerRoutine periodically sends out status updates
func (w *SingleRunWorker) observerRoutine(ctx context.Context, cancel context.CancelFunc, run *Run, wg *sync.WaitGroup, doneSignallingChan chan bool) {
	defer wg.Done()

	ticker := time.NewTicker(w.statusUpdateInterval)
	defer ticker.Stop()

	runContextInfo := ctx.Value(runInfoContextKey).(*RunContextInfo)
	abortRequested := false

	for {
		select {
		// tf command is done - send out one last update and end routine
		case <-doneSignallingChan:
			sendFinalStatus(w.runApi, run, runContextInfo, abortRequested, w.finalStatusRetry, w.log)
			return

		// send out updates as liveliness update
		case <-ticker.C:
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

func (w *SingleRunWorker) sendInitFail(run *Run) {
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
