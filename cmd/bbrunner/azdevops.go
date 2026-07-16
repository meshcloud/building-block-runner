//go:build !k8s && (type_azdevops || (!type_tf && !type_manual && !type_gitlab && !type_github))

package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/meshcloud/building-block-runner/internal/azdevops"
	"github.com/meshcloud/building-block-runner/internal/build"
	"github.com/meshcloud/building-block-runner/internal/catrust"
	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/dispatch"
	"github.com/meshcloud/building-block-runner/internal/meshapi"
	"github.com/meshcloud/building-block-runner/internal/observability"
	"github.com/meshcloud/building-block-runner/internal/rundecrypt"
	"github.com/meshcloud/building-block-runner/internal/runmode"
)

// This file (the azdevops type's fit bootstrap + superset handler builder) is gated `!k8s`: the
// default no-tag build is the in-process superset and links every type; the `-tags k8s` lean
// run-controller image links no type handlers (it only dispatches k8s Jobs). See registry.go.

func init() {
	registerType(runnerTypeAzdevops, typeRegistration{
		implType:           meshapi.RunnerTypeAzureDevOpsPipeline,
		fitBootstrap:       runAzdevopsPolling,
		singleRunBootstrap: runAzdevopsSingleRun,
		supersetHandler:    buildAzdevopsSupersetHandler,
	})
}

// buildAzdevopsSupersetHandler builds the azdevops type's dispatch.RunHandler for the
// controller/superset's in-process ALL-types dispatch (runControllerSuperset), reusing the
// controller's shared connection (uuid, api, crypto) rather than a separate config file.
func buildAzdevopsSupersetHandler(conn supersetConn) (dispatch.RunHandler, error) {
	id := meshapi.Identity{Name: "azure-devops-block-runner", Version: build.Version}
	return azdevops.NewHandler(azdevops.Config{}, azdevops.HandlerDeps{
		Reporters: azdevops.NewReporterFactory(conn.Api.Url, conn.Uuid, id, conn.Log),
		Log:       conn.Log,
	}), nil
}

// runAzdevopsPolling forces the azure-devops-block-runner type's *polling* bootstrap
// in-process (`bbrunner azdevops`) for local-dev / the mux replacement. It runs the SAME
// wiring as the former fit cmd/azdevops binary's polling path; single-run mode is its own
// bootstrap (runAzdevopsSingleRun), wired as this type's singleRunBootstrap sibling
// (mirroring gitlab.go/tf.go).
func runAzdevopsPolling() int {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	log.Info("starting azure-devops-block-runner (bbrunner subcommand)", "version", build.Version)

	cfg, err := azdevops.LoadConfig(log, build.Version, false)
	if err != nil {
		log.Error("cannot read config", "err", err)
		return 1
	}
	id := meshapi.Identity{Name: "azure-devops-block-runner", Version: cfg.Version}

	mgmtPort, err := config.ManagementPort(log, 8101, config.EnvAlias{Var: "PORT", Deprecated: true})
	if err != nil {
		log.Error("invalid management port configuration", "err", err)
		return 1
	}

	dec, err := meshapi.NewCertDecryptor(cfg.PrivateKey)
	if err != nil {
		log.Error("failed to initialize crypto: private key could not be loaded", "err", err)
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

	// This standalone client bypasses meshapi.SharedHTTPClient (see NewHTTPClient's doc), so
	// it needs its own CUSTOM_CA_CERTS_PATH trust anchor rather than inheriting main's
	// process-wide ConfigureRootCAs call; RootCAs is pure/cheap, so recomputing it here is
	// simpler than widening route's plumbing to carry the pool.
	pool, err := catrust.RootCAs(log)
	if err != nil {
		log.Error("failed to build root CA pool", "error", err)
		return 1
	}

	handler := azdevops.NewHandler(cfg, azdevops.HandlerDeps{
		Reporters: azdevops.NewReporterFactory(cfg.Api.Url, cfg.Uuid, id, log),
		HTTP:      azdevops.NewHTTPClient(0, pool),
		Log:       log,
	})
	inproc, err := dispatch.NewInProcess(
		map[meshapi.RunnerImplementationType]dispatch.RunHandler{
			meshapi.RunnerTypeAzureDevOpsPipeline: rundecrypt.Wrap(handler, dec),
		},
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

// runAzdevopsSingleRun forces the azure-devops-block-runner type's single-run bootstrap (the
// RUN_JSON_FILE_PATH k8s Job contract, formerly fit cmd/azdevops's own path) in-process. It
// reads config exactly as the former cmd/azdevops run() did, builds the same Handler polling
// uses, and runs it once via runmode.SingleRunFromFile. It reuses Handler as-is: the
// controller has already decrypted the PAT + sensitive inputs at the dispatch boundary, so no
// decryption seam is needed here -- no loop, no mgmt listener. A sync run may still hold the
// Job pod for up to 30 minutes (unchanged from Kotlin).
//
// Exit semantics, with the one sanctioned azdevops delta: exit 0 iff a terminal or handover
// status was reported (async handover, or a sync final/failure update including the pinned
// timeout failure); register/PATCH transport failure exits non-zero (Kotlin exit-1 parity);
// a pre-report file read/parse failure here exits non-zero too -- the sanctioned tightening
// of Kotlin's exit-0 swallow for THIS (outermost, pre-claim) failure surface. A post-register
// implementation-parse failure is instead reported run FAILED by Execute itself and so exits
// 0 (a Go decision, not this function's concern).
func runAzdevopsSingleRun() int {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	log.Info("starting azure-devops-block-runner single run (bbrunner subcommand)", "version", build.Version)

	cfg, err := azdevops.LoadConfig(log, build.Version, true)
	if err != nil {
		log.Error("cannot read config", "err", err)
		return 1
	}
	id := meshapi.Identity{Name: "azure-devops-block-runner", Version: cfg.Version}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	handler := azdevops.NewHandler(cfg, azdevops.HandlerDeps{
		Reporters: azdevops.NewReporterFactory(cfg.Api.Url, cfg.Uuid, id, log),
		Log:       log,
	})

	return runmode.SingleRunFromFile(ctx, log, cfg.Uuid, meshapi.RunnerTypeAzureDevOpsPipeline,
		func(ctx context.Context, run dispatch.ClaimedRun) error {
			return handler.Execute(ctx, run)
		})
}
