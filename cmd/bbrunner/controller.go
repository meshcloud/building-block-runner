package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/meshcloud/building-block-runner/internal/build"
	"github.com/meshcloud/building-block-runner/internal/config"
	meshcrypto "github.com/meshcloud/building-block-runner/internal/crypto"
	"github.com/meshcloud/building-block-runner/internal/dispatch"
	"github.com/meshcloud/building-block-runner/internal/k8sjob"
	meshapi "github.com/meshcloud/building-block-runner/internal/meshapi"
	"github.com/meshcloud/building-block-runner/internal/observability"
)

// controllerRequesterPrefix is stamped into the claim node-id ("run-controller-<uuid>",
// frozen) and the runner identity headers.
const controllerRequesterPrefix = "run-controller"

func controllerIdentity() meshapi.Identity {
	return meshapi.Identity{Name: controllerRequesterPrefix, Version: build.Version}
}

// runController is bbrunner's default (no-subcommand) bootstrap: the run-controller/superset.
// Behavior-verbatim for the k8s dispatch path: the claim
// wire, decryption order, Job/Secret/ServiceAccount manifests, registration PUT and metric
// names are all byte-identical to the former controller; only the internal seam changed
// (internal/controller dissolved into internal/dispatch + internal/k8sjob). Returns the
// process exit code; the fatal paths log at error level and return a non-zero code (the
// former stdlib log.Fatalf os.Exit(1) is retired with the [RUN CONTROLLER] prefix in favor of
// the slog runner type attribute).
func runController() int {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil)).With("type", controllerRequesterPrefix)
	logger.Info("build metadata", "version", build.Version)

	cfg := readControllerConfig(logger)

	// Dispatcher auto-detect: in-cluster => k8sjob (hand each run to a Kubernetes
	// Job running the matching runner type image); outside a cluster, or with an explicit
	// RUNNER_DISPATCHER=inprocess, => the InProcess superset, running every linked type
	// handler in one process so the run-controller image can replace meshfed-release's
	// multiplexing-block-runner. The in-cluster default (KUBERNETES_SERVICE_HOST present,
	// no override) is unchanged and byte-identical -- it still builds runControllerK8sJob.
	detected := resolveDispatcherKind()
	logger.Info("dispatcher auto-detect", "detected", detected)
	if detected == dispatcherInProcess {
		return runControllerSuperset(logger, cfg)
	}
	return runControllerK8sJob(logger, cfg)
}

// runControllerK8sJob is the run-controller's in-cluster production path: it dispatches each
// claimed run as a Kubernetes Job running the matching runner type image. This is the
// published run-controller image's default mode; its behavior -- the claim wire, decryption
// order, Job/Secret/ServiceAccount manifests, registration PUT and metric names -- is
// byte-identical (extracted verbatim from runController, no logic change).
func runControllerK8sJob(logger *slog.Logger, cfg *config.ControllerConfig) int {
	// One listener serves /healthz + /metrics on MANAGEMENT_PORT (default 2112). A bind
	// failure is fatal: a liveness-probed listener that fails to bind silently would defeat
	// the point of adding healthz.
	mgmtPort, err := config.ManagementPort(logger, 2112)
	if err != nil {
		logger.Error("invalid management port configuration", "error", err)
		return 1
	}

	// main constructs exactly one MetricsCollector against an injected registry and
	// threads it into the loop, claim client, k8s dispatcher and registration path (rather
	// than three call sites aliasing a process-global singleton). A dedicated observability.NewRegistry
	// keeps the run_controller_* series + the standard Go/process collectors byte-identical on
	// the wire while removing the reliance on prometheus.DefaultRegisterer/DefaultGatherer.
	reg := observability.NewRegistry()
	metrics := dispatch.NewMetricsCollectorWithRegistry(reg)
	metrics.SetActiveRunners(1)

	if err := observability.NewServer(logger.With("component", "mgmt"), mgmtPort.Addr(), reg).Start(); err != nil {
		logger.Error("failed to start management listener", "error", err)
		return 1
	}

	// Auto-discover OIDC issuer from Kubernetes API for WIF configuration. discoverOIDCIssuer
	// is the k8s dispatcher factory seam (k8s_dispatcher.go / k8s_dispatcher_noop.go): a
	// Kubernetes-free lean build (`-tags lean`, no `k8s`) never links internal/k8sjob's
	// client-go here.
	logger.Info("discovering OIDC issuer from Kubernetes API")
	oidcIssuer := discoverOIDCIssuer(logger.With("component", "k8sjob"))
	if oidcIssuer != "" {
		logger.Info("WIF enabled", "oidcIssuer", oidcIssuer)
	} else {
		logger.Info("OIDC issuer discovery failed - WIF will not be configured for runners")
	}

	auth := cfg.Api.NewAuthProvider(cfg.Api.Url)

	// Self-register on startup. Meshfed may not be available yet, so retry with a timeout.
	timeout := 10 * time.Minute
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	retryInterval := 10 * time.Second
	for {
		if err := registerController(logger, cfg, auth, oidcIssuer, metrics); err != nil {
			logger.Warn("controller registration failed, retrying", "retryInterval", retryInterval, "error", err)
			select {
			case <-ctx.Done():
				logger.Error("failed to register controller before timeout", "timeout", timeout, "error", err)
				return 1
			case <-time.After(retryInterval):
				continue
			}
		}
		break
	}

	cryptoInstance, err := meshcrypto.NewCertBasedDecryptorWithValidation(cfg.Crypto.PrivateKey, []byte(cfg.Crypto.PublicKey))
	if err != nil {
		logger.Error("failed to initialize crypto for controller", "uuid", cfg.Uuid, "error", err)
		return 1
	}
	logger.Info("initialized crypto for controller (keys validated)", "uuid", cfg.Uuid)

	// newK8sJobDispatcher is the k8s dispatcher factory seam (k8s_dispatcher.go /
	// k8s_dispatcher_noop.go): a Kubernetes-free lean build fails this fast with
	// errK8sNotBuilt instead of linking client-go.
	jobDispatcher, err := newK8sJobDispatcher(cfg, cryptoInstance, metrics, logger.With("component", "k8sjob"))
	if err != nil {
		logger.Error("failed to create Kubernetes client", "error", err)
		return 1
	}

	pollingInterval := 10
	if cfg.PollingIntervalSeconds > 0 {
		pollingInterval = cfg.PollingIntervalSeconds
	}
	logger.Info("polling interval configured", "seconds", pollingInterval)

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
		Logger:     logger.With("component", "dispatch"),
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
// RegistrationApiClient: DTO construction is k8sjob's (registration.go,
// tightly coupled to the Job/SA WIF subject pattern); the PUT transport is
// meshapi.RunnerClient (already shared); the 404 "create it via the meshStack UI"
// mapping and registration metrics are this type's own startup orchestration (only
// main wires).
func registerController(logger *slog.Logger, cfg *config.ControllerConfig, auth meshapi.AuthProvider, oidcIssuer string, metrics *dispatch.MetricsCollector) error {
	dto := k8sjob.BuildRunnerRegistrationDTO(k8sjob.RegistrationInfo{
		Uuid:             cfg.Uuid,
		OwnedByWorkspace: cfg.OwnedByWorkspace,
		DisplayName:      cfg.DisplayName,
		PublicKey:        cfg.Crypto.PublicKey,
		Namespace:        cfg.K8sJobConfig.Namespace,
		OidcIssuer:       oidcIssuer,
	})

	jsonBody, err := json.Marshal(dto)
	if err != nil {
		metrics.IncRegistrationError(cfg.Uuid, dispatch.ErrorTypeRegistrationMarshal)
		return fmt.Errorf("failed to marshal runner registration: %w", err)
	}

	logger.Info("registering controller with implementationType ALL", "uuid", cfg.Uuid)
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
	logger.Info("controller registered successfully")
	return nil
}
