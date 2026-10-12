package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/meshcloud/building-block-runner/go-meshapi-client/meshapi"
	"github.com/meshcloud/building-block-runner/run-controller/build"
	"github.com/meshcloud/building-block-runner/run-controller/controller"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {
	logger := log.New(os.Stdout, "[RUN CONTROLLER] ", log.LstdFlags)
	meshapi.SetClientMetadata("run-controller", build.Version)
	logger.Printf("Build metadata: version=%s", build.Version)

	controller.ReadConfig(logger)

	metricsPort := ":2112"
	http.Handle("/metrics", promhttp.Handler())
	go func() {
		logger.Printf("Starting metrics endpoint on %s", metricsPort)
		if err := http.ListenAndServe(metricsPort, nil); err != nil {
			logger.Printf("Failed to start HTTP server: %v", err)
		}
	}()

	ctx, stopOnSignal := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)

	dispatcher := newDispatcher(logger)
	registerController(ctx, logger)

	http.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("OK"))
	})

	controller.NewController(dispatcher).Run(ctx)
	// A second signal ends the process without waiting for the runs.
	stopOnSignal()
	dispatcher.Wait()
}

// meshfed-api may still be starting.
func registerController(ctx context.Context, logger *log.Logger) {
	timeout := 10 * time.Minute
	ctx, cancel := context.WithTimeoutCause(ctx, timeout, fmt.Errorf("gave up after %s", timeout))
	defer cancel()

	retryInterval := 10 * time.Second
	for {
		err := controller.RegisterController(logger)
		if err == nil {
			return
		}
		logger.Printf("Controller registration failed, retrying in %s: %v", retryInterval, err)
		select {
		case <-ctx.Done():
			logger.Fatalf("Failed to register controller (%v): %v", context.Cause(ctx), err)
		case <-time.After(retryInterval):
		}
	}
}

func newDispatcher(logger *log.Logger) build.Dispatcher {
	name := os.Getenv("RUNCONTROLLER_DISPATCHER")
	if name == "" {
		return controller.NewKubernetesDispatcher()
	}
	newNamedDispatcher, found := build.Dispatchers[name]
	if !found {
		logger.Fatalf("This binary does not contain the dispatcher %q; build it with -tags inprocess", name)
	}
	dispatcher, err := newNamedDispatcher()
	if err != nil {
		logger.Fatalf("Failed to create the %s dispatcher: %v", name, err)
	}
	logger.Printf("Using dispatcher %s", name)
	return dispatcher
}
