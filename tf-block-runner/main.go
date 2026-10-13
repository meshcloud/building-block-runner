package main

import (
	"context"
	"log"
	"net"
	gohttp "net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/dispatcher"
	_ "github.com/meshcloud/building-block-runner/internal/dispatcher/inprocess"
	"github.com/meshcloud/building-block-runner/internal/http"
	"github.com/meshcloud/building-block-runner/tf-block-runner/build"
)

func main() {
	logger := log.New(os.Stdout, "[TF RUNNER] ", log.LstdFlags)
	logger.Printf("Build metadata: version=%s", build.Version)

	cfg, err := config.LoadFile(config.FileFromEnv(), logger)
	if err != nil {
		logger.Fatal(err)
	}
	startHealthServer(logger)

	ctx, stopOnSignal := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	// A second signal ends the process without waiting for the runs.
	context.AfterFunc(ctx, stopOnSignal)

	userAgent := http.UserAgent{GitHubRepo: "meshcloud/building-block-runner", Version: build.Version}
	if err := dispatcher.Serve(ctx, cfg, logger, userAgent); err != nil {
		logger.Fatal(err)
	}
}

func startHealthServer(logger *log.Logger) {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8100"
	}
	addr := ":" + port
	mux := gohttp.NewServeMux()
	mux.HandleFunc("/healthz", func(w gohttp.ResponseWriter, r *gohttp.Request) {
		w.WriteHeader(gohttp.StatusOK)
		w.Write([]byte("OK"))
	})
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		logger.Fatalf("Health server failed to bind on %s: %v", addr, err)
	}
	logger.Printf("Health server listening on %s", addr)
	go func() {
		if err := gohttp.Serve(ln, mux); err != nil {
			logger.Fatalf("Health server error: %v", err)
		}
	}()
}
