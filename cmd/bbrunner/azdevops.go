//go:build !k8s

package main

import (
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
	"github.com/meshcloud/building-block-runner/internal/meshapi"
	"github.com/meshcloud/building-block-runner/internal/observability"
	"github.com/meshcloud/building-block-runner/internal/rundecrypt"
)

// This file (the azdevops type's fit bootstrap + superset handler builder) is gated `!k8s`: the
// default no-tag build is the in-process superset and links every type; the `-tags k8s` lean
// run-controller image links no type handlers (it only dispatches k8s Jobs). See registry.go.

func init() {
	registerType(runnerTypeAzdevops, typeRegistration{
		implType:        meshapi.RunnerTypeAzureDevOpsPipeline,
		fitBootstrap:    runAzdevopsPolling,
		supersetHandler: buildAzdevopsSupersetHandler,
	})
}

// buildAzdevopsSupersetHandler builds the azdevops type's dispatch.RunHandler for the
// controller/superset's in-process ALL-types dispatch (runControllerSuperset), reusing the
// controller's shared connection (uuid, api, crypto) rather than a separate config file.
func buildAzdevopsSupersetHandler(conn supersetConn) (dispatch.RunHandler, error) {
	id := meshapi.Identity{Name: "azure-devops-block-runner", Version: build.Version}
	return azdevops.NewHandler(azdevops.Config{}, azdevops.HandlerDeps{
		Reporters: azdevops.NewReporterFactory(conn.ApiURL, conn.RunnerUuid, id, conn.Log),
		Log:       conn.Log,
	}), nil
}

// runAzdevopsPolling forces the azure-devops-block-runner type in-process (`bbrunner
// azdevops`) for local-dev / the mux replacement. It runs the SAME
// polling wiring as the fit cmd/azdevops binary -- single-run mode is the k8s Job contract
// driven by the fit image, not a local-dev in-process path, so it is not offered here
// (mirroring runManualPolling/runTfPolling).
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

	handler := azdevops.NewHandler(cfg, azdevops.HandlerDeps{
		Reporters: azdevops.NewReporterFactory(cfg.Api.Url, cfg.Uuid, id, log),
		HTTP:      azdevops.NewHTTPClient(0),
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
