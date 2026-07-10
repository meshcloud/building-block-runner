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

	meshapi "github.com/meshcloud/building-block-runner/go-meshapi-client/meshapi"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/meshcloud/building-block-runner/run-controller/build"
	controller "github.com/meshcloud/building-block-runner/run-controller/controller"
)

func main() {
	logger := log.New(os.Stdout, "[RUN CONTROLLER] ", log.LstdFlags)
	meshapi.SetClientMetadata("run-controller", build.Version)
	logger.Printf("Build metadata: version=%s", build.Version)

	controller.ReadConfig(logger)

	// Start Prometheus metrics endpoint
	metricsPort := ":2112"
	http.Handle("/metrics", promhttp.Handler())

	go func() {
		logger.Printf("Starting metrics endpoint on %s", metricsPort)
		if err := http.ListenAndServe(metricsPort, nil); err != nil {
			logger.Printf("Failed to start HTTP server: %v", err)
		}
	}()

	// Auto-discover OIDC issuer from Kubernetes API for WIF configuration
	logger.Println("Discovering OIDC issuer from Kubernetes API...")
	controller.DiscoveredOidcIssuer = controller.DiscoverOIDCIssuer(logger)
	if controller.DiscoveredOidcIssuer != "" {
		logger.Printf("WIF enabled with OIDC issuer: %s", controller.DiscoveredOidcIssuer)
	} else {
		logger.Println("OIDC issuer discovery failed - WIF will not be configured for runners")
	}

	// Self-register all configured runners on startup.
	// Meshfed may not be available yet, so retry with a timeout.
	timeout := 10 * time.Minute
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	retryInterval := 10 * time.Second
	for {
		if err := controller.RegisterController(logger); err != nil {
			logger.Printf("Controller registration failed, retrying in %s: %v", retryInterval, err)
			select {
			case <-ctx.Done():
				logger.Fatalf("Failed to register controller after %s: %v", timeout, err)
			case <-time.After(retryInterval):
				continue
			}
		}
		break
	}

	var wg sync.WaitGroup
	wg.Add(1)

	// start controller
	ctrl := controller.NewController()
	ctrl.Start(&wg)

	// listen for os signals to be able to shutdown gracefully
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-signalChan
		ctrl.Stop()
	}()

	wg.Wait()
	os.Exit(0)
}
