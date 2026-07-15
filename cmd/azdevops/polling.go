package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/meshcloud/building-block-runner/internal/azdevops"
	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/dispatch"
	"github.com/meshcloud/building-block-runner/internal/meshapi"
	"github.com/meshcloud/building-block-runner/internal/observability"
	"github.com/meshcloud/building-block-runner/internal/rundecrypt"
	"github.com/meshcloud/building-block-runner/internal/runmode"
)

// pollInterval is the 10s claim cadence shared by all five Kotlin-port runner types (Kotlin
// @Scheduled(fixedRate=10000)).
const pollInterval = 10 * time.Second

// runPolling wires the standalone in-process polling runner: a dispatch.Loop over an
// InProcess dispatcher holding the single AZURE_DEVOPS_PIPELINE handler, the shared
// StandaloneClaimClassifier, and the management listener (healthz + /metrics) on port
// 8101 (PORT alias, deprecation-logged). Shutdown cancels in-flight run contexts (via
// InProcess.Wait's grace period, default 120s, dispatch package) so a mid-poll sync run
// reports a terminal status promptly instead of holding shutdown for up to 30 minutes -- the
// cancellation/grace-period mechanics themselves are already
// generic in internal/dispatch; this wiring is identical in shape to cmd/manual's.
func runPolling(ctx context.Context, log *slog.Logger, cfg azdevops.Config, id meshapi.Identity) int {
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

	// The generic runner_* series + the loop's run_controller_* series share one dedicated,
	// process-local registry (observability.NewRegistry), which is what /metrics serves (the
	// injectable seam the controller/tf paths already use, off prometheus.DefaultRegisterer/
	// DefaultGatherer). Metric names, labels and help strings are unchanged.
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

	return runmode.Serve(ctx, loop, inproc)
}
