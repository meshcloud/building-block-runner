package tf

import (
	"context"
	"fmt"
	"log/slog"
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
	log                  *slog.Logger
	statusUpdateInterval time.Duration
	dec                  Decryptor
	// cfg carries the runner config the execution path reads, threaded explicitly in place of
	// the former AppConfig global (FOLLOW_UP P2.3).
	cfg execConfig
}

// NewSingleRunWorker creates a new single-run worker. cfg is the runner config threaded
// explicitly (FOLLOW_UP P2.3); its RunApiBackend/RunnerUuid build the run API client and its
// execution values (timeouts, ssh host-key policy, backend URL) feed each run.
func NewSingleRunWorker(logger *slog.Logger, cfg TfRunnerConfig, workerDir string, timeoutMins int, tfbin *TfBinaries, dec Decryptor) *SingleRunWorker {
	return &SingleRunWorker{
		workerDir:            workerDir,
		timeout:              time.Minute * time.Duration(timeoutMins),
		runApi:               NewRunApi(cfg.RunApiBackend, cfg.RunnerUuid, dec),
		tfBinaries:           tfbin,
		log:                  logger,
		statusUpdateInterval: time.Second * 10,
		dec:                  dec,
		cfg:                  cfg.exec(),
	}
}

// NewSingleRunWorkerWithApi creates a new single-run worker with a provided API client
// This is used in Kubernetes mode where the API client needs the runToken from the run spec.
// cfg is the runner config threaded explicitly (FOLLOW_UP P2.3) for the execution path.
func NewSingleRunWorkerWithApi(logger *slog.Logger, cfg TfRunnerConfig, workerDir string, timeoutMins int, tfbin *TfBinaries, api RunApi, dec Decryptor) *SingleRunWorker {
	return &SingleRunWorker{
		workerDir:            workerDir,
		timeout:              time.Minute * time.Duration(timeoutMins),
		runApi:               api,
		tfBinaries:           tfbin,
		log:                  logger,
		statusUpdateInterval: time.Second * 10,
		dec:                  dec,
		cfg:                  cfg.exec(),
	}
}

// ExecuteRun executes a single run.
func (w *SingleRunWorker) ExecuteRun(run *Run) error {
	w.log.Info("Start execution of run", "run", run.Id, "behavior", run.Behavior.str(), "buildingBlock", run.BuildingBlockName)

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

	defer func() { _ = os.RemoveAll(cmdDir) }()

	runContextInfo, err := initRunContextInfo(run, w.log, cmdDir)
	if err != nil {
		w.sendInitFail(run)
		return fmt.Errorf("failed to initialize run context: %w", err)
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

	// registerErr carries a registration failure (if any) out of workRoutine's goroutine.
	// It is written at most once, by workRoutine, strictly before that goroutine's wg.Done();
	// wg.Wait() below happens-after every Done() call, so this read is race-free without
	// further synchronization (B11 fix, phase 2b — registration is "before tofu init/apply
	// begins", so its failure must make ExecuteRun return an error and the process exit
	// non-zero, unlike a failure once apply/init has actually started).
	var registerErr error

	go w.workRoutine(workCtx, run, runContextInfo, &wg, workDoneChannel, &registerErr)
	go w.observerRoutine(workCtx, workCancel, run, runContextInfo, &wg, workDoneChannel)

	wg.Wait()

	w.log.Info("Finished execution of run", "behavior", run.Behavior.str(), "run", run.Id, "buildingBlock", run.BuildingBlockName)

	if registerErr != nil {
		return fmt.Errorf("failed to register as a source for run %s: %w", run.Id, registerErr)
	}

	return nil
}

// workRoutine starts the actual tf command execution. If registration fails, the error is
// written to *registerErr before the run is marked FAILED — registration happens before
// tofu init/apply begins, so this is the one workRoutine failure ExecuteRun's caller must be
// able to observe (B11 fix, phase 2b).
func (w *SingleRunWorker) workRoutine(ctx context.Context, run *Run, runContextInfo *RunContextInfo, wg *sync.WaitGroup, doneSignallingChan chan bool, registerErr *error) {
	defer wg.Done()
	defer func() { doneSignallingChan <- true }()

	if run.Source != nil {
		run.Source.setSkipHostKeyValidation(w.cfg.SkipHostKeyValidation)
	}

	params := &TfCmdParams{
		dir:                   w.workerDir,
		buildingBlockId:       run.BuildingBlockId,
		tfVersion:             run.TerraformVersion,
		useWorkspaces:         true,
		suggestedWorkspace:    run.toWorkspaceStr(),
		vars:                  run.Vars,
		source:                run.Source,
		preRunScript:          run.PreRunScript,
		runMode:               run.Behavior.str(),
		planArtifactUrl:       run.PlanArtifactUrl,
		dec:                   w.dec,
		skipHostKeyValidation: w.cfg.SkipHostKeyValidation,
		tfCommandTimeoutMins:  w.cfg.TfCommandTimeoutMins,
		initTimeoutMins:       w.cfg.InitTimeoutMins,
		wsTimeoutMins:         w.cfg.WsTimeoutMins,
		apiBackendUrl:         w.cfg.ApiBackendUrl,
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
		*registerErr = err
	} else {
		runContextInfo.logwrap.PrintlnToLocalLogs(fmt.Sprintf("Registered '%s' as a source for runId: %s", w.cfg.RunnerUuid, runContextInfo.runId))
		tfCommand.execute()
	}
}

// observerRoutine periodically sends out status updates.
func (w *SingleRunWorker) observerRoutine(ctx context.Context, cancel context.CancelFunc, run *Run, runContextInfo *RunContextInfo, wg *sync.WaitGroup, doneSignallingChan chan bool) {
	defer wg.Done()

	ticker := time.NewTicker(w.statusUpdateInterval)
	defer ticker.Stop()

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
			reportStatus := runContextInfo.progress.Snapshot()
			finalStatus := reportStatus.Status
			if run.IsAsync && reportStatus.Status == SUCCEEDED {
				finalStatus = IN_PROGRESS
			}
			reportStatus.Status = finalStatus

			w.log.Info("Sending final status update", "run", runContextInfo.runId, "status", finalStatus.str())
			_, err := w.runApi.UpdateState(&reportStatus)

			if err != nil {
				w.log.Error("Failed to send final status", "run", runContextInfo.runId, "error", err)
				runContextInfo.logwrap.PrintlnToLocalLogs(fmt.Sprintf("Failed to set final state: %s\n", err.Error()))
			} else {
				w.log.Info("Successfully sent final status", "run", runContextInfo.runId, "status", finalStatus.str())
			}

			return

		// send out updates as liveliness update
		case <-ticker.C:
			reportStatus := runContextInfo.progress.Snapshot()
			if !reportStatus.Status.isTerminalState() {
				abort, err := w.runApi.UpdateState(&reportStatus)
				if err != nil {
					runContextInfo.logwrap.PrintlnToLocalLogs(fmt.Sprintf("Failed to update state: %s", err.Error()))
				}

				// in case the run should be aborted, cancel the work context
				if abort {
					w.log.Info("Received flag to abort run. Cancelling run context.")
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
		w.log.Error("Failed to update initial state", "error", err)
	}
}
