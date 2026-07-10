package main

import (
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/dispatch"
	"github.com/meshcloud/building-block-runner/internal/github"
	"github.com/meshcloud/building-block-runner/internal/meshapi"
	"github.com/meshcloud/building-block-runner/internal/mgmt"
)

// pollInterval is the 10s claim cadence shared by all four Kotlin-port personas (umbrella §7.3).
const pollInterval = 10 * time.Second

// runPolling wires the standalone in-process polling runner: a dispatch.Loop over an
// InProcess dispatcher holding the single GITHUB_WORKFLOW handler, the shared
// StandaloneClaimClassifier, and the D12 management listener on port 8102 (PORT alias,
// deprecation-logged). It mirrors cmd/manual's polling bootstrap.
func runPolling(log *slog.Logger, cfg github.Config, id meshapi.Identity) int {
	mgmtPort, err := config.ManagementPort(log, 8102, config.EnvAlias{Var: "PORT", Deprecated: true})
	if err != nil {
		log.Error("invalid management port configuration", "err", err)
		return 1
	}

	// The github handler decrypts appPem + sensitive inputs in polling mode: build the
	// cert-based decryptor from the resolved private key (D7).
	decryptor, err := github.NewCertDecryptor(cfg.PrivateKey)
	if err != nil {
		log.Error("cannot build decryptor from the configured private key", "err", err)
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

	// Redirects disabled on the external-API client (§2.3).
	httpClient := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	handler := github.NewHandler(cfg, github.HandlerDeps{
		Reporters: github.NewReporterFactory(cfg.Api.Url, cfg.Uuid, id, log),
		Decryptor: decryptor,
		HTTP:      httpClient,
		Log:       log,
	})
	inproc, err := dispatch.NewInProcess(
		map[meshapi.RunnerImplementationType]dispatch.RunHandler{meshapi.RunnerTypeGitHubWorkflow: handler},
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
