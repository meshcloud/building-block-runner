//go:build !k8s

package main

import (
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/meshcloud/building-block-runner/internal/build"
	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/dispatch"
	meshapi "github.com/meshcloud/building-block-runner/internal/meshapi"
	"github.com/meshcloud/building-block-runner/internal/observability"
	"github.com/meshcloud/building-block-runner/internal/tf"
)

// This whole file (the tf type's fit bootstrap AND its superset handler builder) is gated `!k8s`:
// the default no-tag build is the in-process superset and links every runner type, including tf
// (go-git + the OpenTofu toolchain); the `-tags k8s` lean run-controller image links none of the
// type handlers — it only dispatches k8s Jobs and executes no run in-process — so tf's toolchain
// stays out of that image entirely. See registry.go.

// supersetTf* are the tf handler's execution-config defaults in superset mode, mirroring the
// shipped containers/tf-block-runner/runner-config.yml (install/working dirs + timeouts). The
// runner identity + API connection are threaded from the controller's shared supersetConn.
//
// All three timeouts MUST be set: an unset tf.HandlerConfig timeout defaults to a zero
// time.Duration, which context.WithTimeout treats as an already-expired deadline, failing every
// run at init in ~1ms. The values mirror runner-config.yml (timeoutMins 60, initTimeoutMins 3,
// wsTimeoutMins 5).
const (
	supersetTfInstallDir     = "/tmp/runner/tfbin"
	supersetTfWorkingDir     = "/tmp/runner/wd"
	supersetTfCommandTimeout = 60 * time.Minute
	supersetTfInitTimeout    = 3 * time.Minute
	supersetTfWsTimeout      = 5 * time.Minute
)

func init() {
	registerType(runnerTypeTf, typeRegistration{
		implType:        meshapi.RunnerTypeTerraform,
		fitBootstrap:    runTfPolling,
		supersetHandler: buildTfSupersetHandler,
	})
}

// buildTfSupersetHandler builds the tf type's dispatch.RunHandler for the controller/superset's
// in-process ALL-types dispatch (runControllerSuperset), reusing the controller's shared
// connection (uuid, api, crypto) rather than a separate config file -- the same reasoning
// buildSupersetHandlers documents for every linked type.
func buildTfSupersetHandler(conn supersetConn) (dispatch.RunHandler, error) {
	if err := os.MkdirAll(supersetTfWorkingDir, 0o777); err != nil {
		return nil, fmt.Errorf("creating tf working directory %q: %w", supersetTfWorkingDir, err)
	}
	tfBin, err := tf.NewTfBin(supersetTfInstallDir, os.Stdout)
	if err != nil {
		return nil, fmt.Errorf("initializing tf binary provider: %w", err)
	}

	return tf.NewHandler(tf.HandlerConfig{
		WorkingDir:       supersetTfWorkingDir,
		TfCommandTimeout: supersetTfCommandTimeout,
		InitTimeout:      supersetTfInitTimeout,
		WsTimeout:        supersetTfWsTimeout,
		RunnerUuid:       conn.RunnerUuid,
		ApiBackend: tf.RunApiConfig{
			Url:          conn.ApiURL,
			User:         conn.ApiUsername,
			Password:     conn.ApiPassword,
			ClientId:     conn.ApiClientId,
			ClientSecret: conn.ApiClientSecret,
		},
	}, tf.HandlerDeps{
		TfBinaries: tfBin,
		Log:        conn.Log,
	}), nil
}

// runTfPolling forces the tf-block-runner type in-process (`bbrunner tf`) for local-dev / the
// mux replacement. It mirrors cmd/tf's *polling* bootstrap with the runner type's own
// type=tf-block-runner log attribute and the canonical "tf-block-runner" identity (stamped
// downstream in internal/tf). Single-run mode is deliberately not offered here: it is the
// k8s Job contract driven by the fit cmd/tf image (RUN_JSON_FILE_PATH etc.), not a local-dev
// in-process path. The superset (= run-controller image) may link the tf toolchain — the accepted
// fat-image trade-off.
func runTfPolling() int {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil)).With("type", "tf-block-runner")
	logger.Info("Build metadata", "version", build.Version)

	cfg, err := tf.ReadConfig(logger)
	if err != nil {
		logger.Error("cannot read config", "error", err)
		return 1
	}

	// Build the decryptor for sensitive inputs. Polling mode always decrypts with the
	// runner's private key; no key => no-op, preserving the former "Crypto == nil" passthrough.
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

	tfBinaryProvider, err := tf.NewTfBin(cfg.TfInstallDir, os.Stdout)
	if err != nil {
		logger.Error("cannot initialize tf binary provider", "error", err)
		return 1
	}

	logger.Info("Running in polling mode")

	// One listener serves /healthz + /metrics on MANAGEMENT_PORT, PORT kept as a
	// deprecated tf-type alias. Byte-identical wiring to cmd/tf/main.go's polling path
	// -- `bbrunner tf` is the same type forced in-process.
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

	// Same dispatch path as cmd/tf: the backend-agnostic dispatch.Loop + in-process dispatcher.
	// The historic Manager/Worker token loop has been deleted -- `bbrunner tf` is the same type
	// forced in-process, so it wires the identical stack.
	logger.Info("using in-process dispatcher (dispatch.Loop)")
	metrics := dispatch.NewMetricsCollectorWithRegistry(reg)
	loop, inproc, err := tf.NewDispatchRunner(cfg, logger, tfBinaryProvider, dec, meter, metrics)
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
