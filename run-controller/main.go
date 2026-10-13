package main

import (
	"context"
	"log"
	gohttp "net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/meshcloud/building-block-runner/go-meshapi-client/meshapi"
	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/dispatcher"
	"github.com/meshcloud/building-block-runner/internal/http"
	"github.com/meshcloud/building-block-runner/run-controller/build"
)

const metricsAddr = ":2112"

func main() {
	logger := log.New(os.Stdout, "[RUN CONTROLLER] ", log.LstdFlags)
	meshapi.SetClientMetadata("run-controller", build.Version)
	logger.Printf("Build metadata: version=%s", build.Version)

	cfg, err := config.LoadFile(config.FileFromEnv(), logger)
	if err != nil {
		logger.Fatal(err)
	}
	serveMetrics(logger)

	ctx, stopOnSignal := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	// A second signal ends the process without waiting for the runs.
	context.AfterFunc(ctx, stopOnSignal)

	userAgent := http.UserAgent{GitHubRepo: "meshcloud/building-block-runner", Version: build.Version}
	if err := dispatcher.Serve(ctx, cfg, logger, userAgent); err != nil {
		logger.Fatal(err)
	}
}

func serveMetrics(logger *log.Logger) {
	gohttp.Handle("/metrics", promhttp.Handler())
	gohttp.HandleFunc("GET /healthz", func(w gohttp.ResponseWriter, _ *gohttp.Request) {
		_, _ = w.Write([]byte("OK"))
	})
	go func() {
		logger.Printf("Serving metrics on %s", metricsAddr)
		if err := gohttp.ListenAndServe(metricsAddr, nil); err != nil {
			logger.Printf("Serving metrics failed: %v", err)
		}
	}()
}
