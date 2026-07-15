package main

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/dispatch"
	"github.com/meshcloud/building-block-runner/internal/github"
	"github.com/meshcloud/building-block-runner/internal/meshapi"
	"github.com/meshcloud/building-block-runner/internal/observability"
	"github.com/meshcloud/building-block-runner/internal/rundecrypt"
	"github.com/meshcloud/building-block-runner/internal/runmode"
)

// pollInterval is the 10s claim cadence shared by all five Kotlin-port runner types.
const pollInterval = 10 * time.Second

// runPolling wires the standalone in-process polling runner: a dispatch.Loop over an
// InProcess dispatcher holding the single GITHUB_WORKFLOW handler, the shared
// StandaloneClaimClassifier, and the management listener on port 8102 (PORT alias,
// deprecation-logged). It mirrors cmd/manual's polling bootstrap.
func runPolling(ctx context.Context, log *slog.Logger, cfg github.Config, id meshapi.Identity) int {
	mgmtPort, err := config.ManagementPort(log, 8102, config.EnvAlias{Var: "PORT", Deprecated: true})
	if err != nil {
		log.Error("invalid management port configuration", "err", err)
		return 1
	}

	// The github handler decrypts appPem + sensitive inputs in polling mode: build the
	// cert-based decryptor from the resolved private key.
	decryptor, err := github.NewCertDecryptor(cfg.PrivateKey)
	if err != nil {
		log.Error("cannot build decryptor from the configured private key", "err", err)
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

	// Redirects disabled on the external-API client.
	httpClient := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	handler := github.NewHandler(cfg, github.HandlerDeps{
		Reporters: github.NewReporterFactory(cfg.Api.Url, cfg.Uuid, id, log),
		HTTP:      httpClient,
		Log:       log,
	})
	inproc, err := dispatch.NewInProcess(
		map[meshapi.RunnerImplementationType]dispatch.RunHandler{
			meshapi.RunnerTypeGitHubWorkflow: rundecrypt.Wrap(handler, decryptor),
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
