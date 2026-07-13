package main

import (
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/meshcloud/building-block-runner/internal/build"
	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/dispatch"
	"github.com/meshcloud/building-block-runner/internal/mgmt"
	"github.com/meshcloud/building-block-runner/internal/tf"
)

// runTfPolling forces the tf-block-runner persona in-process (`bbrunner tf`) for local-dev / the
// mux replacement (§4.1). It mirrors cmd/tf's *polling* bootstrap with the persona's own
// persona=tf-block-runner log attribute and the canonical "tf-block-runner" identity (stamped
// downstream in internal/tf, §4.2). Single-run mode is deliberately not offered here: it is the
// k8s Job contract driven by the fit cmd/tf image (RUN_JSON_FILE_PATH etc.), not a local-dev
// in-process path. The superset (= run-controller image) may link the tf toolchain — the accepted
// fat-image trade-off (§3.6).
func runTfPolling() int {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil)).With("persona", "tf-block-runner")
	logger.Info("Build metadata", "version", build.Version)

	if err := tf.ReadConfig(logger); err != nil {
		logger.Error("cannot read config", "error", err)
		return 1
	}

	// Build the decryptor for sensitive inputs (D4). Polling mode always decrypts with the
	// runner's private key; no key => no-op, preserving the former "Crypto == nil" passthrough.
	var dec tf.Decryptor = tf.NoopDecryptor{}
	if tf.AppConfig.PrivateKey != "" {
		var cryptoErr error
		dec, cryptoErr = tf.NewCertDecryptor(tf.AppConfig.PrivateKey)
		if cryptoErr != nil {
			logger.Error("failed to initialize crypto: private key could not be loaded", "error", cryptoErr)
			return 1
		}
		logger.Info("Crypto initialized for polling mode")
	}

	tfBinaryProvider, err := tf.NewTfBin(tf.AppConfig.TfInstallDir, os.Stdout)
	if err != nil {
		logger.Error("cannot initialize tf binary provider", "error", err)
		return 1
	}

	logger.Info("Running in polling mode")

	// D12 (§4.3): one listener serves /healthz + /metrics on MANAGEMENT_PORT, PORT kept as a
	// deprecated tf-persona alias (D10). Byte-identical wiring to cmd/tf/main.go's polling path
	// -- `bbrunner tf` is the same persona forced in-process (§4.1).
	mgmtLog := logger.With("component", "mgmt")
	mgmtPort, err := config.ManagementPort(mgmtLog, 8100, config.EnvAlias{Var: "PORT", Deprecated: true})
	if err != nil {
		logger.Error("invalid management port configuration", "error", err)
		return 1
	}
	reg := mgmt.NewRegistry()
	meter := mgmt.NewRunMetrics(reg, tf.AppConfig.RunnerUuid)
	if err := mgmt.NewServer(mgmtLog, mgmtPort.Addr(), reg).Start(); err != nil {
		logger.Error("management server failed to start", "error", err)
		return 1
	}

	// Same dispatcher selection as cmd/tf (§12): in-process dispatch.Loop opt-in via
	// RUNNER_DISPATCHER=inprocess, else the legacy Manager loop (default).
	if os.Getenv("RUNNER_DISPATCHER") == "inprocess" {
		logger.Info("using in-process dispatcher (dispatch.Loop)")
		metrics := dispatch.NewMetricsCollectorWithRegistry(reg)
		loop, inproc, err := tf.NewDispatchRunner(tf.AppConfig, logger, tfBinaryProvider, dec, meter, metrics)
		if err != nil {
			logger.Error("failed to start in-process dispatcher", "error", err)
			return 1
		}
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

	logger.Info("using legacy Manager polling loop")
	var wg sync.WaitGroup
	wg.Add(1)

	runManager := tf.NewManager(tf.AppConfig, tfBinaryProvider, dec, meter, logger)
	runManager.Start(&wg)

	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-signalChan
		runManager.Stop()
	}()

	wg.Wait()
	return 0
}
