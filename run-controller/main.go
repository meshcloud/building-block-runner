package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
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

	dispatcher := newDispatcher(logger)
	registerController(logger)

	var wg sync.WaitGroup
	wg.Add(1)
	ctrl := controller.NewController(dispatcher)
	ctrl.Start(&wg)
	http.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("OK"))
	})

	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-signalChan
		ctrl.Stop()
	}()

	wg.Wait()
	os.Exit(0)
}

// meshfed-api may still be starting.
func registerController(logger *log.Logger) {
	timeout := 10 * time.Minute
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
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
			logger.Fatalf("Failed to register controller after %s: %v", timeout, err)
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
