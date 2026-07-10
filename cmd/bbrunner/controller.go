package main

import (
	"context"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/meshcloud/building-block-runner/internal/build"
	"github.com/meshcloud/building-block-runner/internal/config"
	controller "github.com/meshcloud/building-block-runner/internal/controller"
	"github.com/meshcloud/building-block-runner/internal/mgmt"
)

// runController is bbrunner's default (no-subcommand) bootstrap: the run-controller/superset.
// Body is the behavior-verbatim post-phase-3 run-controller main, except for the D12 listener
// unification (mgmt.NewServer replaces the former ad-hoc metrics-only listener, §4.3). Phase-4
// scope: KubernetesJobDispatcher only — the auto-detect + InProcessDispatcher arrive in phase 5
// (§4.1). Returns the process exit code; the fatal paths keep using logger.Fatalf (os.Exit(1)
// directly), byte-identical to the legacy main.
func runController() int {
	logger := log.New(os.Stdout, "[RUN CONTROLLER] ", log.LstdFlags)
	// Runner identity is now passed per client (meshapi.Identity, §5.2.2), stamped from
	// build.Version at the client construction sites in the controller package.
	logger.Printf("Build metadata: version=%s", build.Version)

	controller.ReadConfig(logger)

	// D12 (§4.3): one listener now serves /healthz (new -- the controller had none before) +
	// /metrics on MANAGEMENT_PORT (default 2112, no legacy alias -- PORT was never read by the
	// controller, so honoring it now would change deployed behavior). A bind failure is now
	// fatal (was silent-continue): a liveness-probed listener that fails to bind silently
	// defeats the point of adding healthz (sanctioned behavior change, plan-04 §6).
	// controller.NewMetricsCollector() still self-registers via promauto against the
	// process-default registry (plan-03 step 7's undelivered debt, plan-04 §1.1 A4), so this
	// wires against prometheus.DefaultGatherer -- byte-identical to the former
	// promhttp.Handler() call.
	mgmtLog := slog.New(slog.NewTextHandler(os.Stdout, nil))
	mgmtPort, err := config.ManagementPort(mgmtLog, 2112)
	if err != nil {
		logger.Fatalf("invalid management port configuration: %v", err)
	}
	if err := mgmt.NewServer(mgmtLog, mgmtPort.Addr(), prometheus.DefaultGatherer).Start(); err != nil {
		logger.Fatalf("%v", err)
	}

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
	return 0
}
