package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/meshcloud/building-block-runner/internal/build"
	"github.com/meshcloud/building-block-runner/internal/config"
	meshcrypto "github.com/meshcloud/building-block-runner/internal/crypto"
	"github.com/meshcloud/building-block-runner/internal/dispatch"
	"github.com/meshcloud/building-block-runner/internal/k8sjob"
	meshapi "github.com/meshcloud/building-block-runner/internal/meshapi"
	"github.com/meshcloud/building-block-runner/internal/mgmt"
)

// controllerRequesterPrefix is stamped into the claim node-id ("run-controller-<uuid>",
// frozen, D9) and the runner identity headers.
const controllerRequesterPrefix = "run-controller"

func controllerIdentity() meshapi.Identity {
	return meshapi.Identity{Name: controllerRequesterPrefix, Version: build.Version}
}

// runController is bbrunner's default (no-subcommand) bootstrap: the run-controller/superset.
// Behavior-verbatim for the k8s dispatch path (PLAN_DETAIL_05_dispatcher.md §12): the claim
// wire, decryption order, Job/Secret/ServiceAccount manifests, registration PUT and metric
// names are all byte-identical to the pre-phase-5 controller; only the internal seam changed
// (internal/controller dissolved into internal/dispatch + internal/k8sjob, §5). Returns the
// process exit code; the fatal paths keep using logger.Fatalf (os.Exit(1) directly), matching
// the legacy main.
func runController() int {
	logger := log.New(os.Stdout, "[RUN CONTROLLER] ", log.LstdFlags)
	logger.Printf("Build metadata: version=%s", build.Version)

	cfg := readControllerConfig(logger)

	// D12: one listener serves /healthz + /metrics on MANAGEMENT_PORT (default 2112). A bind
	// failure is fatal: a liveness-probed listener that fails to bind silently would defeat
	// the point of adding healthz.
	mgmtLog := slog.New(slog.NewTextHandler(os.Stdout, nil))
	mgmtPort, err := config.ManagementPort(mgmtLog, 2112)
	if err != nil {
		logger.Fatalf("invalid management port configuration: %v", err)
	}

	metrics := dispatch.NewMetricsCollector()
	metrics.SetActiveRunners(1)

	if err := mgmt.NewServer(mgmtLog, mgmtPort.Addr(), prometheus.DefaultGatherer).Start(); err != nil {
		logger.Fatalf("%v", err)
	}

	// Auto-discover OIDC issuer from Kubernetes API for WIF configuration.
	logger.Println("Discovering OIDC issuer from Kubernetes API...")
	oidcIssuer := k8sjob.DiscoverOIDCIssuer(mgmtLog.With("component", "k8sjob"))
	if oidcIssuer != "" {
		logger.Printf("WIF enabled with OIDC issuer: %s", oidcIssuer)
	} else {
		logger.Println("OIDC issuer discovery failed - WIF will not be configured for runners")
	}

	auth := cfg.Api.NewAuthProvider()

	// Self-register on startup. Meshfed may not be available yet, so retry with a timeout.
	timeout := 10 * time.Minute
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	retryInterval := 10 * time.Second
	for {
		if err := registerController(logger, cfg, auth, oidcIssuer, metrics); err != nil {
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

	cryptoInstance, err := meshcrypto.NewCertBasedDecryptorWithValidation(cfg.Crypto.PrivateKey, []byte(cfg.Crypto.PublicKey))
	if err != nil {
		logger.Fatalf("Failed to initialize crypto for controller %s: %v", cfg.Uuid, err)
	}
	logger.Printf("Initialized crypto for controller: %s (keys validated)", cfg.Uuid)

	jobDispatcher, err := k8sjob.NewKubernetesJobDispatcher(cfg.Config, cfg.Uuid, cfg.Api.Url, cryptoInstance, metrics, mgmtLog.With("component", "k8sjob"))
	if err != nil {
		logger.Fatalf("Failed to create Kubernetes client: %v", err)
	}

	pollingInterval := 10
	if cfg.PollingIntervalSeconds > 0 {
		pollingInterval = cfg.PollingIntervalSeconds
	}
	logger.Printf("Polling interval: %d seconds", pollingInterval)

	claimClient := dispatch.NewRunClaimClient(cfg.Api.Url, cfg.Uuid, controllerRequesterPrefix, auth, controllerIdentity(), metrics)

	loop := dispatch.NewLoop(dispatch.LoopConfig{
		PollInterval:  time.Duration(pollingInterval) * time.Second,
		MaxConcurrent: cfg.MaxConcurrentJobs,
	}, dispatch.LoopDeps{
		RunnerUuid: cfg.Uuid,
		Claimer:    claimClient,
		Dispatcher: jobDispatcher,
		StatusApi:  claimClient,
		Classify:   dispatch.ControllerClaimClassifier,
		Metrics:    metrics,
		Logger:     mgmtLog.With("component", "dispatch"),
	})

	var wg sync.WaitGroup
	wg.Add(1)

	loop.Start(&wg)

	// listen for os signals to be able to shutdown gracefully
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-signalChan
		loop.Stop()
	}()

	wg.Wait()
	return 0
}

// registerController updates the universal run controller registration via PUT (must
// already exist). Moved from the former internal/controller.RegisterController +
// RegistrationApiClient (PLAN_DETAIL_05 §5): DTO construction is k8sjob's (registration.go,
// tightly coupled to the Job/SA WIF subject pattern); the PUT transport is
// meshapi.RunnerClient (already shared, plan 03); the 404 "create it via the meshStack UI"
// mapping and registration metrics are this persona's own startup orchestration (D11: only
// main wires).
func registerController(logger *log.Logger, cfg *controllerConfig, auth meshapi.AuthProvider, oidcIssuer string, metrics *dispatch.MetricsCollector) error {
	if UseTestClient {
		logger.Println("Test mode enabled - skipping controller registration")
		return nil
	}

	dto := k8sjob.BuildRunnerRegistrationDTO(k8sjob.RegistrationInfo{
		Uuid:             cfg.Uuid,
		OwnedByWorkspace: cfg.OwnedByWorkspace,
		DisplayName:      cfg.DisplayName,
		PublicKey:        cfg.Crypto.PublicKey,
		Namespace:        cfg.Namespace,
		OidcIssuer:       oidcIssuer,
	})

	jsonBody, err := json.Marshal(dto)
	if err != nil {
		metrics.IncRegistrationError(cfg.Uuid, dispatch.ErrorTypeRegistrationMarshal)
		return fmt.Errorf("failed to marshal runner registration: %w", err)
	}

	logger.Printf("Registering controller %s with implementationType ALL...", cfg.Uuid)
	runnerClient := meshapi.NewRunnerClient(cfg.Api.Url, auth)
	statusCode, err := runnerClient.Update(cfg.Uuid, jsonBody)
	if err != nil {
		metrics.IncRegistrationError(cfg.Uuid, dispatch.ErrorTypeRegistrationPut)
		return fmt.Errorf("controller registration failed: %w", err)
	}

	if statusCode == http.StatusNotFound {
		return fmt.Errorf("controller %s not found in meshfed — create it via the meshStack UI or API before starting the run-controller", cfg.Uuid)
	}

	metrics.IncRegistrationSuccess(cfg.Uuid)
	logger.Println("Controller registered successfully")
	return nil
}
