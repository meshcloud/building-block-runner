//go:build !k8s

package main

import (
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/meshcloud/building-block-runner/internal/build"
	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/dispatch"
	"github.com/meshcloud/building-block-runner/internal/gitlab"
	"github.com/meshcloud/building-block-runner/internal/meshapi"
	"github.com/meshcloud/building-block-runner/internal/observability"
	"github.com/meshcloud/building-block-runner/internal/rundecrypt"
)

// This file (the gitlab type's fit bootstrap + superset handler builder) is gated `!k8s`: the
// default no-tag build is the in-process superset and links every type; the `-tags k8s` lean
// run-controller image links no type handlers (it only dispatches k8s Jobs). See registry.go.

func init() {
	registerType(runnerTypeGitlab, typeRegistration{
		implType:        meshapi.RunnerTypeGitLabPipeline,
		fitBootstrap:    runGitlabPolling,
		supersetHandler: buildGitlabSupersetHandler,
	})
}

// buildGitlabSupersetHandler builds the gitlab type's dispatch.RunHandler for the
// controller/superset's in-process ALL-types dispatch (runControllerSuperset), reusing the
// controller's shared connection (uuid, api, crypto) rather than a separate config file.
func buildGitlabSupersetHandler(conn supersetConn) (dispatch.RunHandler, error) {
	id := meshapi.Identity{Name: "gitlab-block-runner", Version: build.Version}
	return gitlab.NewHandler(gitlab.Config{}, gitlab.HandlerDeps{
		Reporters: gitlab.NewReporterFactory(conn.ApiURL, conn.RunnerUuid, id, conn.Log),
		Log:       conn.Log,
	}), nil
}

// runGitlabPolling forces the gitlab-block-runner type in-process (`bbrunner gitlab`)
// for local-dev / the mux replacement. It runs the SAME polling
// wiring as the fit cmd/gitlab binary; single-run mode is the k8s Job contract driven by
// the fit image, not a local-dev in-process path (mirroring runManualPolling/runTfPolling).
func runGitlabPolling() int {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	log.Info("starting gitlab-block-runner (bbrunner subcommand)", "version", build.Version)

	cfg, err := gitlab.LoadConfig(log, build.Version, false)
	if err != nil {
		log.Error("cannot read config", "err", err)
		return 1
	}
	id := meshapi.Identity{Name: "gitlab-block-runner", Version: cfg.Version}

	mgmtPort, err := config.ManagementPort(log, 8103, config.EnvAlias{Var: "PORT", Deprecated: true})
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

	decryptor, err := meshapi.NewCertDecryptor(cfg.PrivateKeyPEM)
	if err != nil {
		log.Error("failed to build cert-based decryptor from the resolved private key", "err", err)
		return 1
	}

	handler := gitlab.NewHandler(cfg, gitlab.HandlerDeps{
		Reporters: gitlab.NewReporterFactory(cfg.Api.Url, cfg.Uuid, id, log),
		Log:       log,
	})
	inproc, err := dispatch.NewInProcess(
		map[meshapi.RunnerImplementationType]dispatch.RunHandler{
			meshapi.RunnerTypeGitLabPipeline: rundecrypt.Wrap(handler, decryptor),
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
