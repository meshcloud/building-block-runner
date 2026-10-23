package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"os/signal"
	"slices"
	"syscall"

	"github.com/meshcloud/building-block-runner/internal/build"
	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/dispatcher"
	"github.com/meshcloud/building-block-runner/internal/executor"
	"github.com/meshcloud/building-block-runner/internal/metrics"
	"github.com/meshcloud/building-block-runner/internal/poller"
)

const (
	envLogLevel  = "RUNNER_LOG_LEVEL"
	envLogFormat = "RUNNER_LOG_FORMAT"
)

var (
	logLevels  = map[string]slog.Level{"debug": slog.LevelDebug, "info": slog.LevelInfo, "warn": slog.LevelWarn, "error": slog.LevelError}
	logFormats = map[string]func(io.Writer, *slog.HandlerOptions) slog.Handler{
		"text": func(w io.Writer, options *slog.HandlerOptions) slog.Handler { return slog.NewTextHandler(w, options) },
		"json": func(w io.Writer, options *slog.HandlerOptions) slog.Handler { return slog.NewJSONHandler(w, options) },
	}
)

func main() {
	handler, err := logHandler()
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	ctx := cancelOnSignal(context.Background())

	if runYmlFilePath := os.Getenv(executor.EnvRunYml); runYmlFilePath != "" {
		slog.SetDefault(slog.New(handler))
		os.Exit(executor.RunFile(ctx, runYmlFilePath))
	}
	slog.SetDefault(slog.New(dispatcher.NewLogHandler(handler)))

	slog.InfoContext(ctx, "starting runner", "version", build.Version)
	if err := run(ctx); err != nil {
		slog.ErrorContext(ctx, "runner failed", "error", err)
		os.Exit(1)
	}
}

var signalNames = map[os.Signal]string{syscall.SIGINT: "SIGINT", syscall.SIGTERM: "SIGTERM"}

// cancelOnSignal names the signal in the cause, which becomes the user message of a run that it stops. A second
// signal ends the process without waiting for the runs.
func cancelOnSignal(parent context.Context) context.Context {
	ctx, cancel := context.WithCancelCause(parent)
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, slices.Collect(maps.Keys(signalNames))...)
	go func() {
		received := <-signals
		signal.Stop(signals)
		cancel(fmt.Errorf("the runner process was terminated by %s", signalNames[received]))
	}()
	return ctx
}

func logHandler() (slog.Handler, error) {
	level, ok := logLevels[cmp.Or(os.Getenv(envLogLevel), "info")]
	if !ok {
		return nil, fmt.Errorf("%s=%q is none of debug, info, warn and error", envLogLevel, os.Getenv(envLogLevel))
	}
	newHandler, ok := logFormats[cmp.Or(os.Getenv(envLogFormat), "text")]
	if !ok {
		return nil, fmt.Errorf("%s=%q is neither text nor json", envLogFormat, os.Getenv(envLogFormat))
	}
	return newHandler(os.Stdout, &slog.HandlerOptions{Level: level}), nil
}

func run(ctx context.Context) error {
	cfg, err := config.LoadFile(ctx, config.FileFromEnv())
	if err != nil {
		return err
	}
	if cfg.Metrics == nil {
		slog.InfoContext(ctx, "serving no metrics, as metrics.port is not set")
		return poller.Poll(ctx, cfg)
	}

	metricsServer, err := metrics.Listen(ctx, cfg)
	if err != nil {
		return err
	}
	// Metrics stay up while the poller waits for its runs after a signal.
	metricsCtx, stopMetrics := context.WithCancel(context.WithoutCancel(ctx))
	defer stopMetrics()
	metricsStopped := make(chan error, 1)
	go func() { metricsStopped <- metricsServer.Serve(metricsCtx) }()

	serveCtx, stopServing := context.WithCancelCause(ctx)
	defer stopServing(nil)
	served := make(chan error, 1)
	go func() { served <- poller.Poll(serveCtx, cfg) }()
	select {
	case err := <-metricsStopped:
		metricsErr := fmt.Errorf("serve metrics: %w", err)
		stopServing(metricsErr)
		return errors.Join(metricsErr, <-served)
	case err := <-served:
		stopMetrics()
		return errors.Join(err, <-metricsStopped)
	}
}
