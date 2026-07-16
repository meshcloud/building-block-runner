//go:build !k8s && (type_github || (!type_tf && !type_manual && !type_gitlab && !type_azdevops))

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
	"github.com/meshcloud/building-block-runner/internal/catrust"
	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/dispatch"
	"github.com/meshcloud/building-block-runner/internal/github"
	"github.com/meshcloud/building-block-runner/internal/httpclient"
	"github.com/meshcloud/building-block-runner/internal/meshapi"
	"github.com/meshcloud/building-block-runner/internal/observability"
	"github.com/meshcloud/building-block-runner/internal/rundecrypt"
	"github.com/meshcloud/building-block-runner/internal/runmode"
)

// This file (the github type's fit bootstrap + superset handler builder) is gated `!k8s`: the
// default no-tag build is the in-process superset and links every type; the `-tags k8s` lean
// run-controller image links no type handlers (it only dispatches k8s Jobs). See registry.go.

func init() {
	registerType(runnerTypeGithub, typeRegistration{
		implType:           meshapi.RunnerTypeGitHubWorkflow,
		fitBootstrap:       runGithubPolling,
		singleRunBootstrap: runGithubSingleRun,
		supersetHandler:    buildGithubSupersetHandler,
	})
}

// buildGithubSupersetHandler builds the github type's dispatch.RunHandler for the
// controller/superset's in-process ALL-types dispatch (runControllerSuperset), reusing the
// controller's shared connection (uuid, api, crypto) rather than a separate config file. github
// keeps its own package-local decryptor type (not meshapi's), unlike gitlab/azdevops.
func buildGithubSupersetHandler(conn supersetConn) (dispatch.RunHandler, error) {
	id := meshapi.Identity{Name: "github-block-runner", Version: build.Version}
	return github.NewHandler(github.Config{}, github.HandlerDeps{
		Reporters: github.NewReporterFactory(conn.Api.Url, conn.Uuid, id, conn.Log),
		Log:       conn.Log,
	}), nil
}

// runGithubPolling forces the github-block-runner type's *polling* bootstrap in-process
// (`bbrunner github`) for local-dev / the mux replacement. It runs the SAME wiring as the
// former fit cmd/github binary's polling path; single-run mode is its own bootstrap
// (runGithubSingleRun), wired as this type's singleRunBootstrap sibling (mirroring
// azdevops.go/tf.go).
func runGithubPolling() int {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	log.Info("starting github-block-runner (bbrunner subcommand)", "version", build.Version)

	cfg, err := github.LoadConfig(log, build.Version, false)
	if err != nil {
		log.Error("cannot read config", "err", err)
		return 1
	}
	id := meshapi.Identity{Name: "github-block-runner", Version: cfg.Version}

	mgmtPort, err := config.ManagementPort(log, 8102, config.EnvAlias{Var: "PORT", Deprecated: true})
	if err != nil {
		log.Error("invalid management port configuration", "err", err)
		return 1
	}

	decryptor, err := github.NewCertDecryptor(cfg.PrivateKey)
	if err != nil {
		log.Error("cannot build decryptor from the configured private key", "err", err)
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

	// This standalone client bypasses meshapi.SharedHTTPClient, so it needs its own
	// CUSTOM_CA_CERTS_PATH trust anchor rather than inheriting main's process-wide
	// ConfigureRootCAs call; RootCAs is pure/cheap, so recomputing it here is simpler than
	// widening route's plumbing to carry the pool.
	pool, err := catrust.RootCAs(log)
	if err != nil {
		log.Error("failed to build root CA pool", "error", err)
		return 1
	}
	httpClient := httpclient.NoRedirectClient(0, httpclient.WithRootCAs(pool))

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

// runGithubSingleRun forces the github-block-runner type's single-run bootstrap (the
// RUN_JSON_FILE_PATH k8s Job contract, formerly fit cmd/github's own path) in-process. It reads
// config exactly as the former cmd/github run() did, builds the same Handler polling uses, and
// runs it once via runmode.SingleRunFromFile. The run JSON is ALREADY decrypted (the controller
// pre-decrypted appPem + inputs), so the handler needs no decryptor at all.
//
// Exit semantics: exit 0 iff a terminal OR IN_PROGRESS-handover update was reported (Execute
// returns nil in both cases — for async single-run the handover IS the job's success).
// Pre-report fetch/parse failures exit non-zero: the sanctioned tightening of the Kotlin
// exit-0 swallow, so k8s retries a run meshStack never heard about.
func runGithubSingleRun() int {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	log.Info("starting github-block-runner single run (bbrunner subcommand)", "version", build.Version)

	cfg, err := github.LoadConfig(log, build.Version, true)
	if err != nil {
		log.Error("cannot read config", "err", err)
		return 1
	}
	id := meshapi.Identity{Name: "github-block-runner", Version: cfg.Version}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	handler := github.NewHandler(cfg, github.HandlerDeps{
		Reporters: github.NewReporterFactory(cfg.Api.Url, cfg.Uuid, id, log),
		Log:       log,
	})

	return runmode.SingleRunFromFile(ctx, log, cfg.Uuid, meshapi.RunnerTypeGitHubWorkflow,
		func(ctx context.Context, run dispatch.ClaimedRun) error {
			return handler.Execute(ctx, run)
		})
}
