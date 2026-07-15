// Package runmode holds the bootstrap shared by every cmd/* entrypoint: the single-run vs
// polling dispatch, the one signal-derived shutdown context both modes drain against, and the
// RUN_JSON_FILE_PATH single-run file scaffold.
package runmode

import (
	"context"
	"encoding/base64"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/meshcloud/building-block-runner/internal/dispatch"
	"github.com/meshcloud/building-block-runner/internal/meshapi"
	"github.com/meshcloud/building-block-runner/internal/observability"
)

// RunJsonFilePathEnv is the frozen k8s single-run contract: the controller mounts the run
// JSON here (default /var/run/secrets/meshstack/run.json) and injects the path.
const RunJsonFilePathEnv = "RUN_JSON_FILE_PATH"

// NewLogger is the shared plain text logger every runner type starts from; tf additionally
// wraps it with .With("type", "tf-block-runner").
func NewLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stdout, nil))
}

// Runner is the per-type wiring Main dispatches into: exactly one of SingleRun/Poll runs,
// under the same shutdown context.
type Runner struct {
	Name    string
	Version string
	Log     *slog.Logger

	SingleRun func(context.Context) int
	Poll      func(context.Context) int
}

// Main derives the one signal-driven shutdown ctx shared by both single-run and polling
// mode, then dispatches into r.SingleRun or r.Poll.
func Main(singleRun bool, r Runner) int {
	r.Log.Info("starting "+r.Name, "version", r.Version)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if singleRun {
		r.Log.Info("running in single-run mode")
		return r.SingleRun(ctx)
	}

	r.Log.Info("running in polling mode")
	return r.Poll(ctx)
}

// looper and drainer are local, minimal interfaces (rather than the concrete *dispatch.Loop /
// *dispatch.InProcess, which satisfy them) so Serve stays hermetically testable without
// widening dispatch's exported surface.
type looper interface {
	Start(*sync.WaitGroup)
	Stop()
}

type drainer interface {
	Wait()
}

// Serve drives the polling drain off the same ctx as single-run: it starts loop, stops it on
// ctx.Done, waits for its wait-group, then drains the in-process dispatcher.
func Serve(ctx context.Context, loop looper, inproc drainer) int {
	var wg sync.WaitGroup
	wg.Add(1)
	loop.Start(&wg)

	go func() {
		<-ctx.Done()
		loop.Stop()
	}()

	wg.Wait()
	inproc.Wait()
	return 0
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
		Details: dto,
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
