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

	"github.com/meshcloud/building-block-runner/internal/azdevops"
	"github.com/meshcloud/building-block-runner/internal/build"
	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/dispatch"
	"github.com/meshcloud/building-block-runner/internal/github"
	"github.com/meshcloud/building-block-runner/internal/gitlab"
	"github.com/meshcloud/building-block-runner/internal/manual"
	meshapi "github.com/meshcloud/building-block-runner/internal/meshapi"
	"github.com/meshcloud/building-block-runner/internal/mgmt"
	"github.com/meshcloud/building-block-runner/internal/tf"
)

// supersetTf* are the tf handler's execution-config defaults in superset mode, mirroring the
// shipped containers/tf-block-runner/runner-config.yml. Threading the full per-persona tf
// config into the controller bootstrap is deferred with the tf.AppConfig de-globalization
// follow-up (FOLLOW_UP P2.3); until then the superset uses these shipped defaults.
const (
	supersetTfInstallDir  = "/tmp/runner/tfbin"
	supersetTfWorkingDir  = "/tmp/runner/wd"
	supersetTfTimeoutMins = 60
)

// runControllerSuperset is bbrunner's out-of-cluster / RUNNER_DISPATCHER=inprocess mode: the
// all-types in-process runner that lets the run-controller image replace meshfed-release's
// multiplexing-block-runner (§1). It registers EVERY linked persona handler (tf + manual +
// gitlab + azdevops + github) into one dispatch.InProcess dispatcher, driven by one
// dispatch.Loop, serving one unified /healthz + /metrics listener. It claims under the
// controller's single ALL identity (the very identity the in-cluster controller registers) and
// dispatches each claimed run to the matching handler in-process -- the exact fan-out the mux
// performed (claim upstream as one runner, route by type), only in-process instead of proxied.
//
// It reuses the controller's own config (uuid, api, auth, crypto) as the one shared connection:
// the single-config run-controller image has exactly one api url, uuid and key, so loading five
// separate per-persona config files (which would collide on the shared RUNNER_* env vars and
// point reporters at the now-retired mux ports) is neither possible nor desired here. Per-run
// reporting still authenticates with the run's own runToken, never the controller's claim
// credentials (RunHandler contract, H5).
func runControllerSuperset(logger *slog.Logger, cfg *controllerConfig) int {
	logger.Info("starting in-process superset (all run types dispatched in-process; retires the multiplexing-block-runner)")

	// D12: one listener serves /healthz + /metrics on MANAGEMENT_PORT (default 2112), on a
	// dedicated registry carrying the run_controller_* series (byte-identical to the k8sjob
	// path). A bind failure is fatal.
	mgmtPort, err := config.ManagementPort(logger, 2112)
	if err != nil {
		logger.Error("invalid management port configuration", "error", err)
		return 1
	}

	reg := mgmt.NewRegistry()
	metrics := dispatch.NewMetricsCollectorWithRegistry(reg)
	metrics.SetActiveRunners(1)
	if err := mgmt.NewServer(logger.With("component", "mgmt"), mgmtPort.Addr(), reg).Start(); err != nil {
		logger.Error("failed to start management listener", "error", err)
		return 1
	}

	auth := cfg.Api.NewAuthProvider()

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

	// The tf handler's per-run RunApi reads the tf package globals for its base URL + node id
	// (tf.AppConfig is not yet threaded -- FOLLOW_UP P2.3), so point them at the controller's
	// single connection: tf runs then claim/report against the same meshStack the superset does.
	tf.AppConfig.RunnerUuid = cfg.Uuid
	tf.AppConfig.RunApiBackend = tf.RunApiConfig{
		Url:          cfg.Api.Url,
		User:         cfg.Api.Username,
		Password:     cfg.Api.Password,
		ClientId:     cfg.Api.ClientId,
		ClientSecret: cfg.Api.ClientSecret,
	}
	tf.AppConfig.TfInstallDir = supersetTfInstallDir
	tf.AppConfig.TfParentWorkingDir = supersetTfWorkingDir
	if err := os.MkdirAll(supersetTfWorkingDir, 0o777); err != nil {
		logger.Error("failed to create tf working directory", "dir", supersetTfWorkingDir, "error", err)
		return 1
	}
	tfBin, err := tf.NewTfBin(supersetTfInstallDir, os.Stdout)
	if err != nil {
		logger.Error("failed to initialize tf binary provider", "error", err)
		return 1
	}

	handlers, err := buildSupersetHandlers(cfg.Api.Url, cfg.Uuid, cfg.Crypto.PrivateKey, tfBin, logger)
	if err != nil {
		logger.Error("failed to build superset handlers", "error", err)
		return 1
	}
	logger.Info("in-process superset handlers registered", "types", len(handlers), "capability", meshapi.RunnerTypeAll)

	inproc, err := dispatch.NewInProcess(handlers, 0, logger.With("component", "dispatch"))
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

// buildSupersetHandlers constructs one dispatch.RunHandler per concrete run type from the
// controller's single shared connection (apiURL + runnerUuid + the controller's private-key
// PEM), so the InProcess superset serves ALL run types under one claim identity. Each handler
// reuses its persona package's own NewHandler + NewReporterFactory -- the same construction
// bbrunner <persona> / cmd/<persona> run -- differing only in that the connection/identity is
// the controller's rather than a separate per-persona config file (see runControllerSuperset).
//
// tfBin is injected (nil is tolerated for construction -- tf needs it only at Execute) so this
// stays hermetically unit-testable without the real tofu install-dir I/O.
func buildSupersetHandlers(apiURL, runnerUuid, privateKeyPEM string, tfBin *tf.TfBinaries, log *slog.Logger) (map[meshapi.RunnerImplementationType]dispatch.RunHandler, error) {
	ver := build.Version

	// Cert-based decryptors from the controller's private key (the superset polls and decrypts
	// like a standalone persona; single-run's NoOp decryptor is not used here). gitlab/azdevops
	// share meshapi.Decryptor; github and tf keep their own package-local decryptor types.
	sharedDec, err := meshapi.NewCertDecryptor(privateKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("building shared cert decryptor: %w", err)
	}
	githubDec, err := github.NewCertDecryptor(privateKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("building github cert decryptor: %w", err)
	}
	tfDec, err := tf.NewCertDecryptor(privateKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("building tf cert decryptor: %w", err)
	}

	manualHandler := manual.NewHandler(manual.Config{}, manual.HandlerDeps{
		Reporters: manual.NewReporterFactory(apiURL, runnerUuid, meshapi.Identity{Name: "manual-block-runner", Version: ver}, log),
		Log:       log,
	})
	gitlabHandler := gitlab.NewHandler(gitlab.Config{}, gitlab.HandlerDeps{
		Reporters: gitlab.NewReporterFactory(apiURL, runnerUuid, meshapi.Identity{Name: "gitlab-block-runner", Version: ver}, log),
		Decryptor: sharedDec,
		Log:       log,
	})
	azdevopsHandler := azdevops.NewHandler(azdevops.Config{}, azdevops.HandlerDeps{
		Reporters: azdevops.NewReporterFactory(apiURL, runnerUuid, meshapi.Identity{Name: "azure-devops-block-runner", Version: ver}, log),
		Decryptor: sharedDec,
		Log:       log,
	})
	githubHandler := github.NewHandler(github.Config{}, github.HandlerDeps{
		Reporters: github.NewReporterFactory(apiURL, runnerUuid, meshapi.Identity{Name: "github-block-runner", Version: ver}, log),
		Decryptor: githubDec,
		Log:       log,
	})
	tfHandler := tf.NewHandler(tf.HandlerConfig{
		WorkingDir:           supersetTfWorkingDir,
		TfCommandTimeoutMins: supersetTfTimeoutMins,
	}, tf.HandlerDeps{
		TfBinaries: tfBin,
		Decryptor:  tfDec,
		Log:        log,
	})

	return map[meshapi.RunnerImplementationType]dispatch.RunHandler{
		meshapi.RunnerTypeManual:              manualHandler,
		meshapi.RunnerTypeGitLabPipeline:      gitlabHandler,
		meshapi.RunnerTypeAzureDevOpsPipeline: azdevopsHandler,
		meshapi.RunnerTypeGitHubWorkflow:      githubHandler,
		meshapi.RunnerTypeTerraform:           tfHandler,
	}, nil
}
