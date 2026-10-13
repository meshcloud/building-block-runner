package main

import (
	"cmp"
	"context"
	"log"
	gohttp "net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/meshcloud/building-block-runner/internal/build"
	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/dispatcher"
	"github.com/meshcloud/building-block-runner/internal/http"
)

func main() {
	logger := log.New(os.Stdout, "[RUNNER] ", log.LstdFlags)
	logger.Printf("Build metadata: version=%s", build.Version)

	cfg, err := config.LoadFile(config.FileFromEnv(), logger)
	if err != nil {
		logger.Fatal(err)
	}
	serveMetrics(logger, ":"+cmp.Or(os.Getenv("PORT"), "2112"))

	ctx, stopOnSignal := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	// A second signal ends the process without waiting for the runs.
	context.AfterFunc(ctx, stopOnSignal)

	userAgent := http.UserAgent{GitHubRepo: "meshcloud/building-block-runner", Version: build.Version}
	if err := dispatcher.Serve(ctx, cfg, logger, userAgent); err != nil {
		logger.Fatal(err)
	}
}

func serveMetrics(logger *log.Logger, addr string) {
	gohttp.Handle("/metrics", promhttp.Handler())
	gohttp.HandleFunc("GET /healthz", func(w gohttp.ResponseWriter, _ *gohttp.Request) {
		_, _ = w.Write([]byte("OK"))
	})
	go func() {
		logger.Printf("Serving metrics on %s", addr)
		if err := gohttp.ListenAndServe(addr, nil); err != nil {
			logger.Printf("Serving metrics failed: %v", err)
		}
	}()
}
