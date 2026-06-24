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

// observerRoutine periodically sends out status updates
func (w *SingleRunWorker) observerRoutine(ctx context.Context, cancel context.CancelFunc, run *Run, wg *sync.WaitGroup, doneSignallingChan chan bool) {
	defer wg.Done()

	ticker := time.NewTicker(w.statusUpdateInterval)
	defer ticker.Stop()

	runContextInfo := ctx.Value(runInfoContextKey).(*RunContextInfo)

	for {
		select {
		// tf command is done - send out one last update and end routine
		case <-doneSignallingChan:
			// context has been cancelled, we omit the final update
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

			w.log.Printf("Sending final status update for run %s: %s", runContextInfo.runId, finalStatus.str())
			_, err := w.runApi.UpdateState(&reportStatus)

			if err != nil {
				w.log.Printf("ERROR: Failed to send final status for run %s: %v", runContextInfo.runId, err)
				runContextInfo.logwrap.PrintlnToLocalLogs(fmt.Sprintf("Failed to set final state: %s\n", err.Error()))
			} else {
				w.log.Printf("Successfully sent final status for run %s: %s", runContextInfo.runId, finalStatus.str())
			}

			return

		// send out updates as liveliness update
		case <-ticker.C:
			if !runContextInfo.reportStatus.Status.isTerminalState() {
				abort, err := w.runApi.UpdateState(&runContextInfo.reportStatus)
				if err != nil {
					runContextInfo.logwrap.PrintlnToLocalLogs(fmt.Sprintf("Failed to update state: %s", err.Error()))
				}

				// in case the run should be aborted, cancel the work context
				if abort {
					w.log.Printf("Received flag to abort run. Cancelling run context.")
					cancel()
					ticker.Stop()
				}
			}
		}
	}
}

func (w *SingleRunWorker) sendInitFail(run *Run) {
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
