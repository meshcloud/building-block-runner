package main

import (
	"cmp"
	"context"
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
	cfg, err := config.LoadFile(ctx, config.FileFromEnv())
	if err != nil {
		exit(ctx, err)
	}

	if err := serveMetrics(ctx, ":"+cmp.Or(os.Getenv("PORT"), "2112")); err != nil {
		exit(ctx, err)
	}

	if err := dispatcher.Serve(ctx, cfg); err != nil {
		exit(ctx, err)
	}
}

func exit(ctx context.Context, err error) {
	slog.ErrorContext(ctx, "runner failed", "error", err)
	os.Exit(1)
}

func serveMetrics(ctx context.Context, addr string) error {
	listener, err := new(net.ListenConfig).Listen(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	http.Handle("/metrics", promhttp.Handler())
	http.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("OK"))
	})
	go func() {
		slog.InfoContext(ctx, "serving metrics", "addr", addr)
		server := &http.Server{ReadHeaderTimeout: 10 * time.Second}
		if err := server.Serve(listener); err != nil {
			slog.ErrorContext(ctx, "serving metrics failed", "error", err)
		}
	}()
	return nil
}
