// Package runmode holds the RUN_JSON_FILE_PATH single-run detection and file scaffold shared
// by every cmd/* entrypoint.
package runmode

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"os"

	"github.com/meshcloud/building-block-runner/internal/dispatch"
	"github.com/meshcloud/building-block-runner/internal/meshapi"
	"github.com/meshcloud/building-block-runner/internal/observability"
)

// RunJsonFilePathEnv is the frozen k8s single-run contract: the controller mounts the run
// JSON here (default /var/run/secrets/meshstack/run.json) and injects the path.
const RunJsonFilePathEnv = "RUN_JSON_FILE_PATH"

// detectSingleRun resolves the run mode from RUN_JSON_FILE_PATH alone: unset => poll; set and
// resolving to a non-empty file => single-run; set but missing/empty => a fail-fast error,
// never a silent fall-through to polling. getenv/stat are injected so this is unit-testable
// without touching the real environment or filesystem.
func detectSingleRun(getenv func(string) string, stat func(string) (os.FileInfo, error)) (bool, error) {
	path := getenv(RunJsonFilePathEnv)
	if path == "" {
		return false, nil
	}

	info, err := stat(path)
	if err != nil {
		return false, fmt.Errorf("%s=%q is set but the run file could not be read: %w", RunJsonFilePathEnv, path, err)
	}
	if info.Size() == 0 {
		return false, fmt.Errorf("%s=%q is set but the run file is empty", RunJsonFilePathEnv, path)
	}

	return true, nil
}

// DetectSingleRun is detectSingleRun over the real process environment and filesystem.
func DetectSingleRun() (bool, error) {
	return detectSingleRun(os.Getenv, os.Stat)
}

// SingleRunResultFromFile is the shared RUN_JSON_FILE_PATH scaffold: read the env var, read
// and parse the file into a ClaimedRun, run fn under observability.InstrumentSingleRunResult,
// and translate the (handled, error) result into a process exit code. This is the B5 variant
// used where fn also reports whether the run reached a terminal state; SingleRunFromFile
// wraps it for the simpler error-only callers.
func SingleRunResultFromFile(
	ctx context.Context,
	log *slog.Logger,
	uuid string,
	t meshapi.RunnerImplementationType,
	fn func(context.Context, dispatch.ClaimedRun) (bool, error),
) int {
	path := os.Getenv(RunJsonFilePathEnv)
	if path == "" {
		log.Error("single-run mode requires RUN_JSON_FILE_PATH")
		return 1
	}

	data, err := os.ReadFile(path)
	if err != nil {
		log.Error("failed to read run JSON file", "path", path, "err", err)
		return 1
	}

	dto, err := meshapi.ParseRunDetails(data)
	if err != nil {
		log.Error("failed to parse run JSON", "path", path, "err", err)
		return 1
	}

	run := dispatch.ClaimedRun{
		Id:      dispatch.RunId(dto.Metadata.Uuid),
		Type:    t,
		Run:     dto,
		RawJson: base64.StdEncoding.EncodeToString(data),
	}

	err = observability.InstrumentSingleRunResult(log, uuid, string(run.Id), func() (bool, error) {
		return fn(ctx, run)
	})
	if err != nil {
		log.Error("single run failed", "runId", run.Id, "err", err)
		return 1
	}

	log.Info("single run completed", "runId", run.Id)
	return 0
}

// SingleRunFromFile is the four-type error-only variant of SingleRunResultFromFile: any
// error means the run did not reach a terminal state.
func SingleRunFromFile(
	ctx context.Context,
	log *slog.Logger,
	uuid string,
	t meshapi.RunnerImplementationType,
	fn func(context.Context, dispatch.ClaimedRun) error,
) int {
	return SingleRunResultFromFile(ctx, log, uuid, t, func(ctx context.Context, run dispatch.ClaimedRun) (bool, error) {
		err := fn(ctx, run)
		return err == nil, err
	})
}
