package main

import (
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/meshcloud/building-block-runner/internal/azdevops"
	"github.com/meshcloud/building-block-runner/internal/build"
	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/dispatch"
	"github.com/meshcloud/building-block-runner/internal/meshapi"
	"github.com/meshcloud/building-block-runner/internal/mgmt"
)

// runAzdevopsPolling forces the azure-devops-block-runner persona in-process (`bbrunner
// azdevops`) for local-dev / the mux replacement (§4.1, umbrella §5.5). It runs the SAME
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

	_ = mgmt.NewRunMetrics(prometheus.DefaultRegisterer, cfg.Uuid)
	metrics := dispatch.NewMetricsCollector()
	if err := mgmt.NewServer(log, mgmtPort.Addr(), prometheus.DefaultGatherer).Start(); err != nil {
		log.Error("failed to start management listener", "err", err)
		return 1
	}

	auth := cfg.Api.NewAuthProvider("")
	claimClient := dispatch.NewRunClaimClient(cfg.Api.Url, cfg.Uuid, id.Name, auth, id, metrics)

	handler := azdevops.NewHandler(cfg, azdevops.HandlerDeps{
		Reporters: azdevops.NewReporterFactory(cfg.Api.Url, cfg.Uuid, id, log),
		Decryptor: dec,
		HTTP:      azdevops.NewHTTPClient(0),
		Log:       log,
	})
	inproc, err := dispatch.NewInProcess(
		map[meshapi.RunnerImplementationType]dispatch.RunHandler{meshapi.RunnerTypeAzureDevOpsPipeline: handler},
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
