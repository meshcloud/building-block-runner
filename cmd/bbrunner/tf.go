package main

import (
	"log"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/meshcloud/building-block-runner/internal/build"
	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/mgmt"
	"github.com/meshcloud/building-block-runner/internal/tf"
)

// runTfPolling forces the tf-block-runner persona in-process (`bbrunner tf`) for local-dev / the
// mux replacement (§4.1). It mirrors cmd/tf's *polling* bootstrap with the persona's own
// [TF RUNNER] logger prefix and the canonical "tf-block-runner" identity (stamped downstream in
// internal/tf, §4.2). Single-run mode is deliberately not offered here: it is the k8s Job
// contract driven by the fit cmd/tf image (RUN_JSON_FILE_PATH etc.), not a local-dev in-process
// path. The superset (= run-controller image) may link the tf toolchain — the accepted fat-image
// trade-off (§3.6).
func runTfPolling() int {
	logger := log.New(os.Stdout, "[TF RUNNER] ", log.LstdFlags)
	logger.Printf("Build metadata: version=%s", build.Version)

	if err := tf.ReadConfig(logger); err != nil {
		logger.Printf("cannot read config: %s", err.Error())
		return 1
	}

	// Build the decryptor for sensitive inputs (D4). Polling mode always decrypts with the
	// runner's private key; no key => no-op, preserving the former "Crypto == nil" passthrough.
	var dec tf.Decryptor = tf.NoopDecryptor{}
	if tf.AppConfig.PrivateKey != "" {
		var cryptoErr error
		dec, cryptoErr = tf.NewCertDecryptor(tf.AppConfig.PrivateKey)
		if cryptoErr != nil {
			logger.Printf("failed to initialize crypto: private key could not be loaded: %s", cryptoErr.Error())
			return 1
		}
		logger.Println("Crypto initialized for polling mode")
	}

	tfBinaryProvider, err := tf.NewTfBin(tf.AppConfig.TfInstallDir, os.Stdout)
	if err != nil {
		logger.Printf("cannot initialize tf binary provider: %s", err.Error())
		return 1
	}

	logger.Println("Running in polling mode")

	// D12 (§4.3): one listener serves /healthz + /metrics on MANAGEMENT_PORT, PORT kept as a
	// deprecated tf-persona alias (D10). Byte-identical wiring to cmd/tf/main.go's polling path
	// -- `bbrunner tf` is the same persona forced in-process (§4.1).
	mgmtLog := slog.New(slog.NewTextHandler(os.Stdout, nil))
	mgmtPort, err := config.ManagementPort(mgmtLog, 8100, config.EnvAlias{Var: "PORT", Deprecated: true})
	if err != nil {
		logger.Printf("invalid management port configuration: %s", err.Error())
		return 1
	}
	reg := mgmt.NewRegistry()
	meter := mgmt.NewRunMetrics(reg, tf.AppConfig.RunnerUuid)
	if err := mgmt.NewServer(mgmtLog, mgmtPort.Addr(), reg).Start(); err != nil {
		logger.Printf("%s", err.Error())
		return 1
	}

	var wg sync.WaitGroup
	wg.Add(1)

	runManager := tf.NewManager(tfBinaryProvider, dec, meter)
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
