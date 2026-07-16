package tf

import (
	"fmt"
	"log/slog"
	"path"

	meshapi "github.com/meshcloud/building-block-runner/internal/meshapi"
	"github.com/meshcloud/building-block-runner/internal/report"
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
	runStatus              *report.RunStatus
	// progress is the concurrency-safe published view of runStatus, read by the observer
	// goroutine while the work goroutine mutates runStatus (replaces the former
	// shallow-copy reportStatus).
	progress         *report.Progress
	artifactFilePath string
	runToken         string
	meshstackBaseUrl string
}

// initRunContextInfo builds the run's execution context straight off the wire-shaped
// meshapi.Run plus the caller's already-derived behavior/impl (BehaviorFor/
// ParseTerraformImplementation), since meshapi.Run itself stays runner-agnostic and carries
// neither the parsed Behavior nor the tf-specific TerraformImplementation.
func initRunContextInfo(run *meshapi.Run, behavior Behavior, impl meshapi.TerraformImplementation, runJsonBase64 string, logger *slog.Logger, wd string) (*RunContextInfo, error) {
	runId := run.Metadata.Uuid

	// Run-scoped attributes replace the former "[behavior] [runId]" logger prefix. The
	// runId itself is expected to already be attached to logger by the caller (via
	// .With("runId", ...)) so it appears exactly once per line; only "behavior" is added here.
	runLog := logger.With("behavior", behavior.str())
	outFile := path.Join(wd, "logs", fmt.Sprintf("logs-%s.txt", runId))

	logwrap, err := NewLogWrap(runLog, outFile)
	if err != nil {
		return nil, fmt.Errorf("initializing run context for run %s: %w", runId, err)
	}

	// A run is IN_PROGRESS by definition from the moment the runner starts executing it.
	// Using PENDING here would cause a 500 from the coordinator if the status is ever
	// sent before execute() has a chance to call commitStatus() (e.g. on registration failure).
	status := &report.RunStatus{
		RunId:            runId,
		Status:           report.IN_PROGRESS,
		Steps:            nil,
		Summary:          nil,
		CurrentStepIndex: -1,
	}

	runContextInfo := &RunContextInfo{
		runId:                  runId,
		bbId:                   run.Spec.BuildingBlock.Uuid,
		workspaceIdentifier:    run.Spec.BuildingBlock.Spec.WorkspaceIdentifier,
		runJsonBase64:          runJsonBase64,
		asyncRun:               impl.Async,
		useMeshBackendFallback: impl.UseMeshHttpBackendFallback,
		workingDirectory:       wd,
		artifactFilePath:       path.Join(wd, "plan.tfplan"),
		runStatus:              status,
		progress:               report.NewProgress(*status),
		logFile_name:           outFile,
		logwrap:                logwrap,
		runToken:               run.Spec.RunToken,
		meshstackBaseUrl:       run.Links.MeshstackBaseUrl.Href,
	}

	return runContextInfo, nil
}
