package main

import (
	"cmp"
	"context"
	"log"
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
)

func main() {
	logger := log.New(os.Stdout, "[RUNNER] ", log.LstdFlags)
	logger.Printf("Build metadata: version=%s", build.Version)

	cfg, err := config.LoadFile(config.FileFromEnv(), logger)
	if err != nil {
		logger.Fatal(err)
	}

	ctx, stopOnSignal := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	// A second signal ends the process without waiting for the runs.
	context.AfterFunc(ctx, stopOnSignal)

	if err := serveMetrics(ctx, logger, ":"+cmp.Or(os.Getenv("PORT"), "2112")); err != nil {
		logger.Fatal(err)
	}

	if err := dispatcher.Serve(ctx, cfg, logger); err != nil {
		logger.Fatal(err)
	}
}

func serveMetrics(ctx context.Context, logger *log.Logger, addr string) error {
	listener, err := new(net.ListenConfig).Listen(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	http.Handle("/metrics", promhttp.Handler())
	http.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("OK"))
	})
	go func() {
		logger.Printf("Serving metrics on %s", addr)
		server := &http.Server{ReadHeaderTimeout: 10 * time.Second}
		if err := server.Serve(listener); err != nil {
			logger.Printf("Serving metrics failed: %v", err)
		}
	}()
	return nil
}
