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
	"github.com/meshcloud/building-block-runner/run-controller/build"
	controller "github.com/meshcloud/building-block-runner/run-controller/controller"
	"github.com/meshcloud/building-block-runner/run-controller/inprocess"
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

	if controller.AppConfig.Dispatcher == controller.DispatchToKubernetes {
		discoverOidcIssuer(logger)
	}
	registerController(logger)

	var wg sync.WaitGroup
	wg.Add(1)
	ctrl := controller.NewController(newDispatcher(logger))
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

func discoverOidcIssuer(logger *log.Logger) {
	logger.Println("Discovering OIDC issuer from Kubernetes API...")
	controller.DiscoveredOidcIssuer = controller.DiscoverOIDCIssuer(logger)
	if controller.DiscoveredOidcIssuer != "" {
		logger.Printf("WIF enabled with OIDC issuer: %s", controller.DiscoveredOidcIssuer)
	} else {
		logger.Println("OIDC issuer discovery failed - WIF will not be configured for runners")
	}
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

func newDispatcher(logger *log.Logger) controller.Dispatcher {
	if controller.AppConfig.Dispatcher != controller.DispatchInProcess {
		return controller.NewKubernetesDispatcher()
	}
	dispatcher, err := inprocess.NewDispatcher(controller.AppConfig.Uuid, controller.AppConfig.Api.Url)
	if err != nil {
		logger.Fatalf("Failed to create the in-process dispatcher: %v", err)
	}
	return dispatcher
}
