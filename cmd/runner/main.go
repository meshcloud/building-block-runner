package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/meshcloud/building-block-runner/internal/build"
	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/dispatcher"
	"github.com/meshcloud/building-block-runner/internal/logctx"
)

func main() {
	slog.SetDefault(slog.New(logctx.NewHandler(slog.NewTextHandler(os.Stdout, nil))))

	ctx, stopOnSignal := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	// A second signal ends the process without waiting for the runs.
	context.AfterFunc(ctx, stopOnSignal)

	slog.InfoContext(ctx, "starting runner", "version", build.Version)
	if err := run(ctx); err != nil {
		slog.ErrorContext(ctx, "runner failed", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	cfg, err := config.LoadFile(ctx, config.FileFromEnv())
	if err != nil {
		return err
	}

	port := os.Getenv("PORT")
	if port == "" {
		slog.InfoContext(ctx, "serving no metrics, as PORT is not set")
		return dispatcher.Serve(ctx, cfg)
	}
	listener, err := new(net.ListenConfig).Listen(ctx, "tcp", ":"+port)
	if err != nil {
		return err
	}
	metrics := newMetricsServer()
	metricsStopped := make(chan error, 1)
	go func() { metricsStopped <- metrics.Serve(listener) }()
	slog.InfoContext(ctx, "serving metrics", "port", port)

	serveCtx, stopServing := context.WithCancelCause(ctx)
	defer stopServing(nil)
	served := make(chan error, 1)
	go func() { served <- dispatcher.Serve(serveCtx, cfg) }()
	select {
	case err := <-metricsStopped:
		metricsErr := fmt.Errorf("serve metrics: %w", err)
		stopServing(metricsErr)
		return errors.Join(metricsErr, <-served)
	case err := <-served:
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		return errors.Join(err, metrics.Shutdown(shutdownCtx))
	}
}

func newMetricsServer() *http.Server {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("OK"))
	})
	return &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
}
