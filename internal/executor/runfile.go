package executor

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"runtime/debug"
	"strings"
	"uuid"

	"github.com/meshcloud/meshstack-cli/pkg/setting"
)

const (
	ExitStatusDelivered = 0
	ExitUndelivered     = 1
	ExitFailedDelivered = 3
)

var errStdinClosed = errors.New("the runner stopped this run")

func RunFile(ctx context.Context, path string) int {
	ctx, stopWatching := cancelWhenStdinCloses(ctx)
	defer stopWatching()
	status, err := runFile(ctx, path)
	switch {
	case err != nil:
		slog.ErrorContext(ctx, "the run did not deliver its final status", "error", err)
		return ExitUndelivered
	case status == StatusFailed:
		return ExitFailedDelivered
	}
	return ExitStatusDelivered
}

func runFile(ctx context.Context, path string) (Status, error) {
	run, err := ReadRunYaml(path)
	if err != nil {
		return "", err
	}
	runJson, err := encodeWithoutImplementation(run)
	if err != nil {
		return "", err
	}
	sourceId, err := uuid.Parse(os.Getenv(EnvRunnerUuid))
	if err != nil {
		return "", fmt.Errorf("%s: %w", EnvRunnerUuid, err)
	}
	slog.SetDefault(slog.Default().With("run", run.Metadata.Uuid))
	runnerType, err := RunnerTypeOf(run)
	if err != nil {
		return "", err
	}
	slog.InfoContext(ctx, "executing run", "behavior", run.Spec.Behavior, "runnerType", runnerType)
	execute, err := newExecutor(runnerType, os.Getenv(EnvConfig))
	if err != nil {
		return "", err
	}
	runner, err := newRunnerClient(os.Getenv(setting.Endpoint.EnvKey()), os.Getenv(setting.ApiToken.EnvKey()))
	if err != nil {
		return "", fmt.Errorf("build the meshStack client from %s and %s: %w", setting.Endpoint.EnvKey(), setting.ApiToken.EnvKey(), err)
	}
	api := runner.ForRun(run)
	return executeAndReport(ctx, execute, Run{MeshRunnerRun: run, Json: runJson, Api: api, Reporter: NewReporter(api, sourceId)})
}

func executeAndReport(ctx context.Context, execute Executor, run Run) (Status, error) {
	r := run.Reporter
	runCtx, cancelRun := context.WithCancelCause(ctx)
	defer cancelRun(nil)
	stopObserving := r.observe(runCtx, cancelRun)
	err := executeRecovering(runCtx, execute, run)
	stopObserving()
	if err != nil {
		slog.ErrorContext(ctx, "the run failed", "error", err)
		r.failUnlessFinal(failureMessage(runCtx, err))
	}
	return r.Finish(context.WithoutCancel(ctx))
}

func failureMessage(runCtx context.Context, err error) string {
	if cause := context.Cause(runCtx); cause != nil {
		return cause.Error()
	}
	return err.Error()
}

func executeRecovering(ctx context.Context, execute Executor, run Run) (err error) {
	defer func() {
		if p := recover(); p != nil {
			slog.ErrorContext(ctx, "the run panicked", "panic", p, "stack", string(debug.Stack()))
			err = fmt.Errorf("the run panicked: %v", p)
		}
	}()
	return execute(ctx, run)
}

func cancelWhenStdinCloses(ctx context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancelCause(ctx)
	if info, err := os.Stdin.Stat(); err == nil && info.Mode()&os.ModeNamedPipe != 0 {
		go func() { cancel(stopReason(os.Stdin)) }()
	}
	return ctx, func() { cancel(nil) }
}

func stopReason(stdin io.Reader) error {
	line, _ := bufio.NewReader(stdin).ReadString('\n')
	if reason := strings.TrimSpace(line); reason != "" {
		return errors.New(reason)
	}
	return errStdinClosed
}
