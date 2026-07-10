package tfrun

import (
	"fmt"
	"io"
	"log"
	"path"
)

type RunContextInfo struct {
	runId                  string
	runJsonBase64          string
	bbId                   string
	workspaceIdentifier    string
	asyncRun               bool
	useMeshBackendFallback bool
	workingDirectory       string
	filename_state         string
	logFile_name           string
	logwrap                *logwrap
	runStatus              *RunStatus
	// progress is the concurrency-safe published view of runStatus, read by the observer
	// goroutine while the work goroutine mutates runStatus (B10 fix — replaces the former
	// shallow-copy reportStatus).
	progress         *progress
	artifactFilePath string
	runToken         string
	meshstackBaseUrl string
}

func initRunContextInfo(run *Run, logPrefix string, logWriter io.Writer, wd string) *RunContextInfo {
	log := log.New(logWriter, fmt.Sprintf("%s[%s] [%s] ", logPrefix, run.Behavior.str(), run.Id), log.LstdFlags)
	outFile := path.Join(wd, "logs", fmt.Sprintf("logs-%s.txt", run.Id))

	// A run is IN_PROGRESS by definition from the moment the runner starts executing it.
	// Using PENDING here would cause a 500 from the coordinator if the status is ever
	// sent before execute() has a chance to call commitStatus() (e.g. on registration failure).
	status := &RunStatus{
		RunId:            run.Id,
		Status:           IN_PROGRESS,
		Steps:            nil,
		Summary:          nil,
		CurrentStepIndex: -1,
	}

	runContextInfo := &RunContextInfo{
		runId:                  run.Id,
		bbId:                   run.BuildingBlockId,
		workspaceIdentifier:    *run.WorkspaceIdentifier,
		runJsonBase64:          run.RunJsonBase64,
		asyncRun:               run.IsAsync,
		useMeshBackendFallback: run.UseMeshBackendFallback,
		workingDirectory:       wd,
		artifactFilePath:       path.Join(wd, "plan.tfplan"),
		runStatus:              status,
		progress:               newProgress(*status),
		logFile_name:           outFile,
		logwrap:                NewLogWrap(log, outFile),
		runToken:               run.RunToken,
		meshstackBaseUrl:       run.MeshstackBaseUrl,
	}

	return runContextInfo
}
