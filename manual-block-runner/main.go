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

	"github.com/meshcloud/building-block-runner/go-meshapi-client/meshapi"
	"github.com/meshcloud/building-block-runner/manual-block-runner/build"
	"github.com/meshcloud/building-block-runner/manual-block-runner/manual"
)

func main() {
	meshapi.SetClientMetadata("manual-block-runner", build.Version)
	slog.Info("starting manual-block-runner", "version", build.Version)

	cfg, err := manual.LoadConfig(cmp.Or(os.Getenv("RUNNER_CONFIG_FILE"), "runner-config.yml"))
	if err != nil {
		slog.Error("cannot read config", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if runFile := os.Getenv("RUN_JSON_FILE_PATH"); runFile != "" {
		if err := manual.ExecuteRunFromFile(ctx, cfg, runFile); err != nil {
			slog.Error("run failed", "err", err)
			os.Exit(1)
		}
		return
	}

	slog.Info("polling for runs", "runner", cfg.Uuid, "api", cfg.Api.Url)
	if err := startHealthServer(); err != nil {
		slog.Error("cannot start health server", "err", err)
		os.Exit(1)
	}
	manual.NewPoller(cfg).Run(ctx)
}

// startHealthServer listens on SERVER_PORT or PORT, which the Kotlin runner's Spring Boot read.
func startHealthServer() error {
	addr := ":" + cmp.Or(os.Getenv("SERVER_PORT"), os.Getenv("PORT"), "8104")
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("OK"))
	})
	go func() {
		err := http.Serve(listener, mux)
		slog.Error("health server stopped", "err", err)
		os.Exit(1)
	}()
	slog.Info("health server listening", "addr", addr)
	return nil
}
