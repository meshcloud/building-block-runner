package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/dispatch"
	meshapi "github.com/meshcloud/building-block-runner/internal/meshapi"
	"github.com/meshcloud/building-block-runner/internal/observability"
	"github.com/meshcloud/building-block-runner/internal/rundecrypt"
)

// runControllerSuperset is bbrunner's out-of-cluster / RUNNER_DISPATCHER=inprocess mode: the
// all-types in-process runner that lets the run-controller image replace meshfed-release's
// multiplexing-block-runner. It registers EVERY LINKED type handler (typeRegistry -- normally
// tf + manual + gitlab + azdevops + github, fewer if a lean image build excluded some via
// build tags, see registry.go) into one dispatch.InProcess dispatcher, driven by one
// dispatch.Loop, serving one unified /healthz + /metrics listener. It claims under the
// controller's single ALL identity (the very identity the in-cluster controller registers) and
// dispatches each claimed run to the matching handler in-process -- the exact fan-out the mux
// performed (claim upstream as one runner, route by type), only in-process instead of proxied.
//
// It reuses the controller's own config (uuid, api, auth, crypto) as the one shared connection:
// the single-config run-controller image has exactly one api url, uuid and key, so loading five
// separate per-type config files (which would collide on the shared RUNNER_* env vars and
// point reporters at the now-retired mux ports) is neither possible nor desired here. Per-run
// reporting still authenticates with the run's own runToken, never the controller's claim
// credentials (RunHandler contract).
func runControllerSuperset(logger *slog.Logger, cfg *config.ControllerConfig) int {
	logger.Info("starting in-process superset (all run types dispatched in-process; retires the multiplexing-block-runner)")

	// One listener serves /healthz + /metrics on MANAGEMENT_PORT (default 2112), on a
	// dedicated registry carrying the run_controller_* series (byte-identical to the k8sjob
	// path). A bind failure is fatal.
	mgmtPort, err := config.ManagementPort(logger, 2112)
	if err != nil {
		logger.Error("invalid management port configuration", "error", err)
		return 1
	}

	reg := observability.NewRegistry()
	metrics := dispatch.NewMetricsCollectorWithRegistry(reg)
	metrics.SetActiveRunners(1)
	if err := observability.NewServer(logger.With("component", "mgmt"), mgmtPort.Addr(), reg).Start(); err != nil {
		logger.Error("failed to start management listener", "error", err)
		return 1
	}

	auth := cfg.Api.NewAuthProvider(cfg.Api.Url)

	// Self-register as capability ALL (the superset can serve every run type), WIF-less: a
	// controller running outside a cluster has no projected service-account tokens, so no OIDC
	// issuer is discovered (empty issuer => BuildRunnerRegistrationDTO emits no WIF block). Same
	// PUT + retry the in-cluster controller uses; a 404 means the runner object must be created
	// in meshStack first.
	timeout := 10 * time.Minute
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	retryInterval := 10 * time.Second
	for {
		if err := registerController(logger, cfg, auth, "", metrics); err != nil {
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

	conn := supersetConn{
		BaseConfig:    config.BaseConfig{Uuid: cfg.Uuid, Api: cfg.Api},
		PrivateKeyPEM: cfg.Crypto.PrivateKey,
		Log:           logger,
	}
	handlers, err := buildSupersetHandlers(conn)
	if err != nil {
		logger.Error("failed to build superset handlers", "error", err)
		return 1
	}
	logger.Info("in-process superset handlers registered", "types", len(handlers), "capability", meshapi.RunnerTypeAll)

	// A non-positive grace falls back to dispatch.DefaultShutdownGrace (120s) inside NewInProcess,
	// so an unset RUNNER_SHUTDOWN_GRACE / shutdownGraceSeconds preserves the historical default.
	shutdownGrace := time.Duration(cfg.ShutdownGraceSeconds) * time.Second
	effectiveGrace := shutdownGrace
	if effectiveGrace <= 0 {
		effectiveGrace = dispatch.DefaultShutdownGrace
	}
	logger.Info("shutdown drain grace configured", "grace", effectiveGrace)

	inproc, err := dispatch.NewInProcess(handlers, shutdownGrace, logger.With("component", "dispatch"))
	if err != nil {
		logger.Error("failed to build in-process dispatcher", "error", err)
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
		Dispatcher: inproc,
		StatusApi:  claimClient,
		Classify:   dispatch.ControllerClaimClassifier,
		Metrics:    metrics,
		Logger:     logger.With("component", "dispatch"),
	})

	var wg sync.WaitGroup
	wg.Add(1)
	loop.Start(&wg)

	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-signalChan
		loop.Stop()
	}()

	wg.Wait()
	inproc.Wait()
	return 0
}

// buildSupersetHandlers constructs one dispatch.RunHandler per LINKED runner type (typeRegistry,
// registry.go) from the controller's single shared connection, so the InProcess superset serves
// every linked type under one claim identity. Each type's own supersetHandler builder (wired
// from its tag-gated file's init()) reuses that runner type package's own NewHandler +
// NewReporterFactory -- the same construction `bbrunner <type>` / `cmd/<type>` run -- differing
// only in that the connection/identity is the controller's rather than a separate per-type
// config file (see runControllerSuperset). This function itself never imports a runner type
// package, so it -- and the fat/lean image choice it serves -- compiles under every build-tag
// combination ("leaner run-controller image via build tags").
func buildSupersetHandlers(conn supersetConn) (map[meshapi.RunnerImplementationType]dispatch.RunHandler, error) {
	// One decryptor for the whole claim boundary: every type's handler now receives an
	// already-plaintext ClaimedRun, so no per-type handler may hold its own real decryptor (it
	// would re-decrypt already-plaintext values and fail). An empty PrivateKeyPEM leaves dec nil,
	// and rundecrypt.Wrap passes the handler through unwrapped (identity, matching NoopDecryptor).
	var dec meshapi.Decryptor
	if conn.PrivateKeyPEM != "" {
		var err error
		dec, err = meshapi.NewCertDecryptor(conn.PrivateKeyPEM)
		if err != nil {
			return nil, fmt.Errorf("building shared cert decryptor: %w", err)
		}
	}

	handlers := make(map[meshapi.RunnerImplementationType]dispatch.RunHandler, len(typeRegistry))
	for token, reg := range typeRegistry {
		handler, err := reg.supersetHandler(conn)
		if err != nil {
			return nil, fmt.Errorf("building %s handler: %w", token, err)
		}
		handlers[reg.implType] = rundecrypt.Wrap(handler, dec)
	}
	return handlers, nil
}
