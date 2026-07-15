package main

import (
	"context"
	"log/slog"

	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/dispatch"
	"github.com/meshcloud/building-block-runner/internal/observability"
	"github.com/meshcloud/building-block-runner/internal/runmode"
	"github.com/meshcloud/building-block-runner/internal/tf"
)

// runPolling wires the standalone in-process polling runner: a dispatch.Loop over an
// InProcess dispatcher holding the single TERRAFORM handler (self-registration, 60s claim
// backoff, tf's ClaimClassifier, ShutdownGraceSeconds drain window), plus the management
// listener (healthz + /metrics) on port 8100 (PORT alias). `bbrunner tf` runs the identical
// wiring via the same tf.NewDispatchRunner.
func runPolling(ctx context.Context, logger *slog.Logger, cfg tf.TfRunnerConfig, tfBin *tf.TfBinaries) int {
	// Build the decryptor for sensitive inputs: the run JSON claimed from the backend still
	// arrives with sensitive values encrypted, so the polling path decrypts them at the claim
	// boundary (rundecrypt.Wrap). No key configured => no-op passthrough.
	var dec tf.Decryptor = tf.NoopDecryptor{}
	if cfg.PrivateKey != "" {
		var cryptoErr error
		dec, cryptoErr = tf.NewCertDecryptor(cfg.PrivateKey)
		if cryptoErr != nil {
			logger.Error("failed to initialize crypto: private key could not be loaded", "error", cryptoErr)
			return 1
		}
		logger.Info("Crypto initialized for polling mode")
	}

	// One listener serves /healthz + /metrics on MANAGEMENT_PORT, with PORT kept
	// working as a deprecated tf-type alias (the image's ENV PORT=8080 must resolve
	// unchanged). tf has no pre-existing default-registry metrics of its own, so it gets a
	// fresh registry (observability.NewRegistry) instead of reaching for the global one.
	mgmtLog := logger.With("component", "mgmt")
	mgmtPort, err := config.ManagementPort(mgmtLog, 8100, config.EnvAlias{Var: "PORT", Deprecated: true})
	if err != nil {
		logger.Error("invalid management port configuration", "error", err)
		return 1
	}
	reg := observability.NewRegistry()
	meter := observability.NewRunMetrics(reg, cfg.RunnerUuid)
	if err := observability.NewServer(mgmtLog, mgmtPort.Addr(), reg).Start(); err != nil {
		logger.Error("management server failed to start", "error", err)
		return 1
	}

	// tf polls via the backend-agnostic dispatch.Loop + in-process dispatcher (the same path
	// every other runner type uses); the historic Manager/Worker token loop has been deleted.
	// The run_controller_* loop metrics register on the same dedicated registry the tf type
	// already serves, via the injectable seam.
	logger.Info("using in-process dispatcher (dispatch.Loop)")
	metrics := dispatch.NewMetricsCollectorWithRegistry(reg)
	loop, inproc, err := tf.NewDispatchRunner(cfg, logger, tfBin, dec, meter, metrics)
	if err != nil {
		logger.Error("failed to start in-process dispatcher", "error", err)
		return 1
	}

	return runmode.Serve(ctx, loop, inproc)
}
