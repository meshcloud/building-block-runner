//go:build !k8s && (type_manual || (!type_tf && !type_gitlab && !type_azdevops && !type_github))

package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/meshcloud/building-block-runner/internal/build"
	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/dispatch"
	"github.com/meshcloud/building-block-runner/internal/manual"
	"github.com/meshcloud/building-block-runner/internal/meshapi"
	"github.com/meshcloud/building-block-runner/internal/observability"
	"github.com/meshcloud/building-block-runner/internal/runmode"
)

// This file (the manual type's fit bootstrap + superset handler builder) is gated `!k8s`: the
// default no-tag build is the in-process superset and links every type; the `-tags k8s` lean
// run-controller image links no type handlers (it only dispatches k8s Jobs). See registry.go.

func init() {
	registerType(runnerTypeManual, typeRegistration{
		implType:           meshapi.RunnerTypeManual,
		fitBootstrap:       runManualPolling,
		singleRunBootstrap: runManualSingleRun,
		supersetHandler:    buildManualSupersetHandler,
	})
}

// buildManualSupersetHandler builds the manual type's dispatch.RunHandler for the
// controller/superset's in-process ALL-types dispatch (runControllerSuperset), reusing the
// controller's shared connection rather than a separate config file. manual never decrypts
// sensitive inputs, so it needs nothing from conn beyond the connection/identity fields.
func buildManualSupersetHandler(conn supersetConn) (dispatch.RunHandler, error) {
	id := meshapi.Identity{Name: "manual-block-runner", Version: build.Version}
	return manual.NewHandler(manual.Config{}, manual.HandlerDeps{
		Reporters: manual.NewReporterFactory(conn.Api.Url, conn.Uuid, id, conn.Log),
		Log:       conn.Log,
	}), nil
}

// runManualPolling forces the manual-block-runner type's *polling* bootstrap in-process
// (`bbrunner manual`) for local-dev / the mux replacement. It runs the SAME wiring as the
// former fit cmd/manual binary's polling path; single-run mode is its own bootstrap
// (runManualSingleRun), wired as this type's singleRunBootstrap sibling (mirroring tf.go).
func runManualPolling() int {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	log.Info("starting manual-block-runner (bbrunner subcommand)", "version", build.Version)

	cfg, err := manual.LoadConfig(log, build.Version, false)
	if err != nil {
		log.Error("cannot read config", "err", err)
		return 1
	}
	id := meshapi.Identity{Name: "manual-block-runner", Version: cfg.Version}

	mgmtPort, err := config.ManagementPort(log, 8104, config.EnvAlias{Var: "PORT", Deprecated: true})
	if err != nil {
		log.Error("invalid management port configuration", "err", err)
		return 1
	}

	// One dedicated, process-local registry carries both the generic runner_* series and the
	// loop's run_controller_* series and is what /metrics serves (the injectable seam
	// the controller/tf paths already use, off prometheus.DefaultRegisterer/DefaultGatherer).
	// Metric names, labels and help strings are unchanged.
	reg := observability.NewRegistry()
	_ = observability.NewRunMetrics(reg, cfg.Uuid)
	metrics := dispatch.NewMetricsCollectorWithRegistry(reg)
	if err := observability.NewServer(log, mgmtPort.Addr(), reg).Start(); err != nil {
		log.Error("failed to start management listener", "err", err)
		return 1
	}

	auth := cfg.Api.NewAuthProvider("")
	claimClient := dispatch.NewRunClaimClient(cfg.Api.Url, cfg.Uuid, id.Name, auth, id, metrics)

	handler := manual.NewHandler(cfg, manual.HandlerDeps{
		Reporters: manual.NewReporterFactory(cfg.Api.Url, cfg.Uuid, id, log),
		Log:       log,
	})
	inproc, err := dispatch.NewInProcess(
		map[meshapi.RunnerImplementationType]dispatch.RunHandler{meshapi.RunnerTypeManual: handler},
		0, log.With("component", "dispatch"))
	if err != nil {
		log.Error("failed to build in-process dispatcher", "err", err)
		return 1
	}

	loop := dispatch.NewLoop(dispatch.LoopConfig{
		PollInterval:  10 * time.Second,
		ClaimBackoff:  0,
		MaxConcurrent: cfg.MaxConcurrentRuns,
	}, dispatch.LoopDeps{
		RunnerUuid: cfg.Uuid,
		Claimer:    claimClient,
		Dispatcher: inproc,
		StatusApi:  claimClient,
		Classify:   dispatch.StandaloneClaimClassifier,
		Metrics:    metrics,
		Logger:     log.With("component", "dispatch"),
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

// runManualSingleRun forces the manual-block-runner type's single-run bootstrap (the
// RUN_JSON_FILE_PATH k8s Job contract, formerly fit cmd/manual's own path) in-process. It reads
// config exactly as the former cmd/manual run() did, builds the same Handler polling uses, and
// runs it once via runmode.SingleRunFromFile. Reporting uses the run's own runToken against
// cfg.Api.Url (the controller injects RUNNER_API_URL; it strips nothing — the k8s trust model
// is unchanged). No decryptor: the controller already decrypted.
//
// Exit semantics: exit 0 iff a terminal status was reported. register/update failure ⇒ exit
// 1 (Kotlin parity); file missing/parse failure ⇒ exit 1 — the deliberate tightening of the
// Kotlin exit-0 swallow, so k8s (BackoffLimit:1) retries a run meshStack never heard about.
func runManualSingleRun() int {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	log.Info("starting manual-block-runner single run (bbrunner subcommand)", "version", build.Version)

	cfg, err := manual.LoadConfig(log, build.Version, true)
	if err != nil {
		log.Error("cannot read config", "err", err)
		return 1
	}
	id := meshapi.Identity{Name: "manual-block-runner", Version: cfg.Version}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	handler := manual.NewHandler(cfg, manual.HandlerDeps{
		Reporters: manual.NewReporterFactory(cfg.Api.Url, cfg.Uuid, id, log),
		Log:       log,
	})

	return runmode.SingleRunFromFile(ctx, log, cfg.Uuid, meshapi.RunnerTypeManual,
		func(ctx context.Context, run dispatch.ClaimedRun) error {
			return handler.Execute(ctx, run)
		})
}
