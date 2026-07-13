package main

import (
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/dispatch"
	"github.com/meshcloud/building-block-runner/internal/gitlab"
	"github.com/meshcloud/building-block-runner/internal/meshapi"
	"github.com/meshcloud/building-block-runner/internal/mgmt"
)

// pollInterval is the 10s claim cadence shared by all four Kotlin-port personas (Kotlin
// @Scheduled(fixedRate=10000); umbrella §7.3).
const pollInterval = 10 * time.Second

// runPolling wires the standalone in-process polling runner: a dispatch.Loop over an
// InProcess dispatcher holding the single GITLAB_PIPELINE handler, the shared
// StandaloneClaimClassifier, and the D12 management listener (healthz + /metrics) on port
// 8103 (PORT alias, deprecation-logged). It mirrors cmd/manual's polling bootstrap (06A
// template); `bbrunner gitlab` runs the identical wiring (cmd/bbrunner/gitlab.go).
func runPolling(log *slog.Logger, cfg gitlab.Config, id meshapi.Identity) int {
	mgmtPort, err := config.ManagementPort(log, 8103, config.EnvAlias{Var: "PORT", Deprecated: true})
	if err != nil {
		log.Error("invalid management port configuration", "err", err)
		return 1
	}

	// The generic runner_* series + the loop's run_controller_* series share one dedicated,
	// process-local registry (mgmt.NewRegistry), which is what /metrics serves (P3.2 -- the
	// injectable seam the controller/tf paths already use, off prometheus.DefaultRegisterer/
	// DefaultGatherer). Metric names, labels and help strings are unchanged.
	reg := mgmt.NewRegistry()
	_ = mgmt.NewRunMetrics(reg, cfg.Uuid)
	metrics := dispatch.NewMetricsCollectorWithRegistry(reg)
	if err := mgmt.NewServer(log, mgmtPort.Addr(), reg).Start(); err != nil {
		log.Error("failed to start management listener", "err", err)
		return 1
	}

	auth := cfg.Api.NewAuthProvider("")
	claimClient := dispatch.NewRunClaimClient(cfg.Api.Url, cfg.Uuid, id.Name, auth, id, metrics)

	decryptor, err := meshapi.NewCertDecryptor(cfg.PrivateKeyPEM)
	if err != nil {
		log.Error("failed to build cert-based decryptor from the resolved private key", "err", err)
		return 1
	}

	handler := gitlab.NewHandler(cfg, gitlab.HandlerDeps{
		Reporters: gitlab.NewReporterFactory(cfg.Api.Url, cfg.Uuid, id, log),
		Decryptor: decryptor,
		Log:       log,
	})
	inproc, err := dispatch.NewInProcess(
		map[meshapi.RunnerImplementationType]dispatch.RunHandler{meshapi.RunnerTypeGitLabPipeline: handler},
		0, log.With("component", "dispatch"))
	if err != nil {
		log.Error("failed to build in-process dispatcher", "err", err)
		return 1
	}

	loop := dispatch.NewLoop(dispatch.LoopConfig{
		PollInterval:  pollInterval,
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
