package main

import (
	"cmp"
	"context"
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

func cancelOnSignal(parent context.Context) context.Context {
	ctx, cancel := context.WithCancelCause(parent)
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, slices.Collect(maps.Keys(signalNames))...)
	go func() {
		received := <-signals
		// signal.Stop lets a second signal end the process without waiting for the runs.
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
	return poller.Poll(ctx, cfg)
}
