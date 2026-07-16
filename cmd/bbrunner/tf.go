//go:build !k8s && (type_tf || (!type_manual && !type_gitlab && !type_azdevops && !type_github))

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

	"github.com/meshcloud/building-block-runner/internal/build"
	"github.com/meshcloud/building-block-runner/internal/catrust"
	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/dispatch"
	meshapi "github.com/meshcloud/building-block-runner/internal/meshapi"
	"github.com/meshcloud/building-block-runner/internal/observability"
	"github.com/meshcloud/building-block-runner/internal/rundecrypt"
	"github.com/meshcloud/building-block-runner/internal/runmode"
	"github.com/meshcloud/building-block-runner/internal/tf"
)

// This whole file (the tf type's fit bootstrap AND its superset handler builder) is gated `!k8s`:
// the default no-tag build is the in-process superset and links every runner type, including tf
// (go-git + the OpenTofu toolchain); the `-tags k8s` lean run-controller image links none of the
// type handlers — it only dispatches k8s Jobs and executes no run in-process — so tf's toolchain
// stays out of that image entirely. See registry.go.

// supersetTf* are the tf handler's execution-config defaults in superset mode. They are derived
// from the single compiled-in default source (tf.DefaultTf*/DefaultWorkingDir/DefaultTfInstallDir
// in internal/tf/config.go, which ReadConfig also applies) so the two default sources cannot
// drift; the shared fit runner-config.yml no longer carries these keys. The runner identity + API
// connection are threaded from the controller's shared supersetConn.
//
// All three timeouts MUST be set: an unset tf.HandlerConfig timeout defaults to a zero
// time.Duration, which context.WithTimeout treats as an already-expired deadline, failing every
// run at init in ~1ms.
const (
	supersetTfInstallDir     = tf.DefaultTfInstallDir
	supersetTfWorkingDir     = tf.DefaultWorkingDir
	supersetTfCommandTimeout = tf.DefaultTfCommandTimeoutMins * time.Minute
	supersetTfInitTimeout    = tf.DefaultInitTimeoutMins * time.Minute
	supersetTfWsTimeout      = tf.DefaultWsTimeoutMins * time.Minute
)

func init() {
	registerType(runnerTypeTf, typeRegistration{
		implType:           meshapi.RunnerTypeTerraform,
		fitBootstrap:       runTfPolling,
		singleRunBootstrap: runTfSingleRun,
		supersetHandler:    buildTfSupersetHandler,
	})
}

// buildTfSupersetHandler builds the tf type's dispatch.RunHandler for the controller/superset's
// in-process ALL-types dispatch (runControllerSuperset), reusing the controller's shared
// connection (uuid, api, crypto) rather than a separate config file -- the same reasoning
// buildSupersetHandlers documents for every linked type.
func buildTfSupersetHandler(conn supersetConn) (dispatch.RunHandler, error) {
	if err := os.MkdirAll(supersetTfWorkingDir, 0o777); err != nil {
		return nil, fmt.Errorf("creating tf working directory %q: %w", supersetTfWorkingDir, err)
	}
	tfBin, err := tf.NewTfBin(supersetTfInstallDir, os.Stdout)
	if err != nil {
		return nil, fmt.Errorf("initializing tf binary provider: %w", err)
	}

	return tf.NewHandler(tf.HandlerConfig{
		WorkingDir:       supersetTfWorkingDir,
		TfCommandTimeout: supersetTfCommandTimeout,
		InitTimeout:      supersetTfInitTimeout,
		WsTimeout:        supersetTfWsTimeout,
		RunnerUuid:       conn.Uuid,
		ApiBackend:       conn.Api,
	}, tf.HandlerDeps{
		TfBinaries: tfBin,
		Log:        conn.Log,
	}), nil
}

// runTfPolling forces the tf-block-runner type in-process (`bbrunner tf`) for local-dev / the
// mux replacement. It mirrors cmd/tf's *polling* bootstrap with the runner type's own
// type=tf-block-runner log attribute and the canonical "tf-block-runner" identity (stamped
// downstream in internal/tf). Single-run mode is its own bootstrap (runTfSingleRun), wired as
// this type's singleRunBootstrap sibling. The superset (= run-controller image) may link the tf
// toolchain — the accepted fat-image trade-off.
func runTfPolling() int {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil)).With("type", "tf-block-runner")
	logger.Info("Build metadata", "version", build.Version)

	cfg, err := tf.ReadConfig(logger)
	if err != nil {
		logger.Error("cannot read config", "error", err)
		return 1
	}

	// Tofu/git/curl/aws-cli subprocesses read the on-disk trust store, not our in-process
	// x509.CertPool, so any CUSTOM_CA_CERTS_PATH certs must be synced in before they run.
	if err := catrust.SyncSystemStore(context.Background(), logger); err != nil {
		logger.Error("failed to sync custom CA certs into system trust store", "error", err)
		return 1
	}

	// Build the decryptor for sensitive inputs. Polling mode always decrypts with the
	// runner's private key; no key => no-op, preserving the former "Crypto == nil" passthrough.
	var dec tf.Decryptor = tf.NoopDecryptor{}
	if cfg.PrivateKey != "" {
		var cryptoErr error
		dec, cryptoErr = tf.NewCertDecryptor(cfg.PrivateKey)
		if cryptoErr != nil {
			logger.Error("failed to initialize crypto: private key could not be loaded", "error", cryptoErr)
			return 1
		}
		logger.Info("Crypto initialized for polling mode")
	}

	tfBinaryProvider, err := tf.NewTfBin(cfg.TfInstallDir, os.Stdout)
	if err != nil {
		logger.Error("cannot initialize tf binary provider", "error", err)
		return 1
	}

	logger.Info("Running in polling mode")

	// One listener serves /healthz + /metrics on MANAGEMENT_PORT, PORT kept as a
	// deprecated tf-type alias. Byte-identical wiring to cmd/tf/main.go's polling path
	// -- `bbrunner tf` is the same type forced in-process.
	mgmtLog := logger.With("component", "mgmt")
	mgmtPort, err := config.ManagementPort(mgmtLog, 8100, config.EnvAlias{Var: "PORT", Deprecated: true})
	if err != nil {
		logger.Error("invalid management port configuration", "error", err)
		return 1
	}
	reg := observability.NewRegistry()
	meter := observability.NewRunMetrics(reg, cfg.Uuid)
	if err := observability.NewServer(mgmtLog, mgmtPort.Addr(), reg).Start(); err != nil {
		logger.Error("management server failed to start", "error", err)
		return 1
	}

	// Same dispatch path as cmd/tf: the backend-agnostic dispatch.Loop + in-process dispatcher.
	// The historic Manager/Worker token loop has been deleted -- `bbrunner tf` is the same type
	// forced in-process, so it wires the identical stack. The assembly is inlined here (package
	// main) mirroring the other four runner types (see manual.go); internal/tf owns only the
	// type-specific pieces (Register, NewHandler, NewClaimClassifier, the frozen cadence consts).
	logger.Info("using in-process dispatcher (dispatch.Loop)")
	metrics := dispatch.NewMetricsCollectorWithRegistry(reg)

	auth := cfg.Api.NewAuthProvider("")

	// Opt-in self-registration: absent registration section => never self-registers.
	if err := tf.Register(logger, cfg, auth); err != nil {
		logger.Error("tf runner registration failed", "error", err)
		return 1
	}

	identity := meshapi.Identity{Name: "tf-block-runner", Version: build.Version}
	claimClient := dispatch.NewRunClaimClient(
		cfg.Api.Url, cfg.Uuid, "", auth, identity, metrics,
		dispatch.WithRequester(func(uuid string) string { return uuid + "-" + tf.ClaimNodePostfix }),
	)

	handler := tf.NewHandler(tf.HandlerConfig{
		WorkingDir:            cfg.TfParentWorkingDir,
		TfCommandTimeout:      time.Duration(cfg.TfCommandTimeoutMins) * time.Minute,
		InitTimeout:           time.Duration(cfg.InitTimeoutMins) * time.Minute,
		WsTimeout:             time.Duration(cfg.WsTimeoutMins) * time.Minute,
		RunnerUuid:            cfg.Uuid,
		ApiBackend:            cfg.Api,
		SkipHostKeyValidation: cfg.SkipHostKeyValidation,
	}, tf.HandlerDeps{
		TfBinaries: tfBinaryProvider,
		Meter:      meter,
		Log:        logger,
	})

	// ShutdownGraceSeconds is tf's SIGINT/SIGTERM drain window: in-flight runs get this
	// long to finish on their own before InProcess.Wait cancels them and Handler.Execute ->
	// Worker.tfExecution reports a terminal ABORTED (see worker.go).
	shutdownGrace := time.Duration(cfg.ShutdownGraceSeconds) * time.Second
	inproc, err := dispatch.NewInProcess(
		map[meshapi.RunnerImplementationType]dispatch.RunHandler{
			meshapi.RunnerTypeTerraform: rundecrypt.Wrap(handler, dec),
		},
		shutdownGrace, logger.With("component", "dispatch"))
	if err != nil {
		logger.Error("failed to build in-process dispatcher", "error", err)
		return 1
	}

	// Ensure the working dir exists (the Manager did this in Start).
	if err := os.MkdirAll(cfg.TfParentWorkingDir, 0o777); err != nil {
		logger.Error("failed to create working directory", "dir", cfg.TfParentWorkingDir, "error", err)
	}

	loop := dispatch.NewLoop(dispatch.LoopConfig{
		// PollInterval == the old NORUN_WORKER_DELAY (10s idle poll); ClaimBackoff == the old
		// FAILED_WORKER_DELAY (60s after a fetch error) -- see tf.NewClaimClassifier.
		PollInterval:  tf.NORUN_WORKER_DELAY,
		ClaimBackoff:  tf.FAILED_WORKER_DELAY,
		MaxConcurrent: cfg.MaxConcurrentRuns,
	}, dispatch.LoopDeps{
		RunnerUuid: cfg.Uuid,
		Claimer:    claimClient,
		Dispatcher: inproc,
		StatusApi:  claimClient,
		Classify:   tf.NewClaimClassifier(meter),
		Metrics:    metrics,
		Standalone: meter,
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

// runTfSingleRun forces the tf-block-runner type's single-run bootstrap (the RUN_JSON_FILE_PATH
// k8s Job contract, formerly fit cmd/tf's own path) in-process. It reads config and builds the
// tf binary provider exactly as runTfPolling does, then drives one run via tf.Handler.Execute
// (the same handler polling uses), through the shared runmode.SingleRunResultFromFile scaffold.
//
// A single-run failure used to always fall through to exit 0, so the k8s Job the controller
// dispatched was reported "succeeded" even when the run never got off the ground.
// Handler.Execute only returns an error for failures before the run's first potentially
// state-mutating step (workdir setup, run registration -- see handler.go); once tofu init/apply
// has begun, Execute always returns nil, even on failure. That scoping matters operationally: the
// controller's Job template uses BackoffLimit:1 + RestartPolicy:Never
// (run-controller/controller/kubernetes.go), so a blanket non-zero exit on any failure would make
// k8s re-run a failed terraform run once -- a second, automatic APPLY/DESTROY against real
// infrastructure. Re-triggering stateful terraform must stay a deliberate user action, so only the
// pre-flight failure class (which never touched terraform) exits non-zero here.
func runTfSingleRun() int {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil)).With("type", "tf-block-runner")
	logger.Info("Build metadata", "version", build.Version)

	cfg, err := tf.ReadConfig(logger)
	if err != nil {
		logger.Error("cannot read config", "error", err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Tofu/git/curl/aws-cli subprocesses read the on-disk trust store, not our in-process
	// x509.CertPool, so any CUSTOM_CA_CERTS_PATH certs must be synced in before they run.
	if err := catrust.SyncSystemStore(ctx, logger); err != nil {
		logger.Error("failed to sync custom CA certs into system trust store", "error", err)
		return 1
	}

	tfBinaryProvider, err := tf.NewTfBin(cfg.TfInstallDir, os.Stdout)
	if err != nil {
		logger.Error("cannot initialize tf binary provider", "error", err)
		return 1
	}

	return runmode.SingleRunResultFromFile(ctx, logger, cfg.Uuid, meshapi.RunnerTypeTerraform, func(ctx context.Context, cr dispatch.ClaimedRun) (bool, error) {
		runLog := logger.With("runId", cr.Run.Metadata.Uuid)
		runLog.Info("Executing single run", "buildingBlock", cr.Run.Spec.BuildingBlock.Spec.DisplayName)

		// The engine (Worker.tfExecution) does not MkdirAll the parent working dir; the polling
		// path relies on runTfPolling's MkdirAll (in this file), so single-run must
		// create it itself here (preserves the old ExecuteRun MkdirAll / "creates workerDir if
		// missing").
		if err := os.MkdirAll(cfg.TfParentWorkingDir, 0o777); err != nil {
			runLog.Error("Failed to create working dir", "error", err)
			return false, err
		}

		sm := &tf.SuccessMeter{}
		handler := tf.NewHandler(tf.HandlerConfig{
			WorkingDir:            cfg.TfParentWorkingDir,
			TfCommandTimeout:      time.Duration(cfg.TfCommandTimeoutMins) * time.Minute,
			InitTimeout:           time.Duration(cfg.InitTimeoutMins) * time.Minute,
			WsTimeout:             time.Duration(cfg.WsTimeoutMins) * time.Minute,
			RunnerUuid:            cfg.Uuid,
			ApiBackend:            cfg.Api,
			SkipHostKeyValidation: cfg.SkipHostKeyValidation,
		}, tf.HandlerDeps{
			TfBinaries: tfBinaryProvider,
			Meter:      sm,
			Log:        runLog,
		})

		execErr := handler.Execute(ctx, cr)
		// success reflects a real terminal status (RunSucceeded fired via sm), not execErr:
		// execErr is pre-flight-only (see the doc comment above). runmode's
		// InstrumentSingleRunResult meters and pushes this on its own fresh runner_* registry
		// (PUT-on-fail, PUT+DELETE-on-success).
		return sm.Succeeded, execErr
	})
}
