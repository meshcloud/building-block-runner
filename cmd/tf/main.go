package main

import (
	"encoding/json"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/meshcloud/building-block-runner/internal/build"
	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/dispatch"
	meshapi "github.com/meshcloud/building-block-runner/internal/meshapi"
	"github.com/meshcloud/building-block-runner/internal/mgmt"
	"github.com/meshcloud/building-block-runner/internal/tf"
)

const (
	ENV_RUN_JSON_FILE_PATH = "RUN_JSON_FILE_PATH"
	ENV_EXECUTION_MODE     = "EXECUTION_MODE"
	// ENV_RUNNER_DISPATCHER selects the polling-mode dispatch backend (PLAN_DETAIL_05 §12):
	// "inprocess" opts into the dispatch.Loop + InProcess + tf handler path; any other value
	// (incl. unset) keeps the legacy Manager/Worker polling loop, which stays the default
	// until full characterization-through-loop equivalence is proven (run-log addendum).
	ENV_RUNNER_DISPATCHER = "RUNNER_DISPATCHER"
)

// useInProcessDispatcher reports whether the tf persona should run on the new dispatch.Loop
// path. It is opt-in (RUNNER_DISPATCHER=inprocess); the Manager path remains the default.
func useInProcessDispatcher() bool {
	return os.Getenv(ENV_RUNNER_DISPATCHER) == "inprocess"
}

func main() {
	// Persona identity carried as an attribute (§8.1) — replaces the former "[TF RUNNER]"
	// log prefix; the local-dev-stack readiness marker now keys on persona=tf-block-runner
	// (cross-repo lock-step, CROSS_REPO_TODO.md).
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil)).With("persona", "tf-block-runner")
	// Runner identity is now passed per client (meshapi.Identity, §5.2.2); tf.NewRunApi
	// stamps {"tf-block-runner", build.Version} at client construction.
	logger.Info("Build metadata", "version", build.Version)

	if err := tf.ReadConfig(logger); err != nil {
		logger.Error("cannot read config", "error", err)
		os.Exit(1)
	}

	// Check if running in single-run mode
	singleRunMode := isSingleRunMode()

	// Build the decryptor for sensitive inputs (D4 — replaces the former meshcrypto.Crypto global).
	// Single-run mode: the controller already decrypted everything, so use a no-op decryptor.
	// Polling mode: build a cert-based decryptor from the runner's private key (no key => no-op,
	// preserving the former "Crypto == nil" passthrough behavior).
	var dec tf.Decryptor = tf.NoopDecryptor{}
	if !singleRunMode && tf.AppConfig.PrivateKey != "" {
		var cryptoErr error
		dec, cryptoErr = tf.NewCertDecryptor(tf.AppConfig.PrivateKey)
		if cryptoErr != nil {
			logger.Error("failed to initialize crypto: private key could not be loaded", "error", cryptoErr)
			os.Exit(1)
		}
		logger.Info("Crypto initialized for polling mode")
	} else if singleRunMode {
		logger.Info("Single-run mode: skipping crypto initialization (controller handles decryption)")
	}

	// define tf binary provider
	tfBinaryProvider, err := tf.NewTfBin(tf.AppConfig.TfInstallDir, os.Stdout)
	if err != nil {
		panic(err)
	}

	// Check if running in single-run mode
	if singleRunMode {
		logger.Info("Running in single-run mode")
		os.Exit(executeSingleRun(logger, tfBinaryProvider, dec))
	}

	// Standard polling mode
	logger.Info("Running in polling mode")

	// D12 (§4.3): one listener serves /healthz + /metrics on MANAGEMENT_PORT, with PORT kept
	// working as a deprecated tf-persona alias (D10 -- the image's ENV PORT=8080 must resolve
	// unchanged). tf has no pre-existing default-registry metrics of its own, so it gets a
	// fresh registry (mgmt.NewRegistry) instead of reaching for the global one.
	mgmtLog := logger.With("component", "mgmt")
	mgmtPort, err := config.ManagementPort(mgmtLog, 8100, config.EnvAlias{Var: "PORT", Deprecated: true})
	if err != nil {
		logger.Error("invalid management port configuration", "error", err)
		os.Exit(1)
	}
	reg := mgmt.NewRegistry()
	meter := mgmt.NewRunMetrics(reg, tf.AppConfig.RunnerUuid)
	if err := mgmt.NewServer(mgmtLog, mgmtPort.Addr(), reg).Start(); err != nil {
		logger.Error("management server failed to start", "error", err)
		os.Exit(1)
	}

	// Dispatcher selection (§12): the new in-process dispatch.Loop path is opt-in; the legacy
	// Manager/Worker loop stays the default. The run_controller_* loop metrics register on the
	// same dedicated registry the tf persona already serves, via the injectable §5.6 seam.
	if useInProcessDispatcher() {
		logger.Info("using in-process dispatcher (dispatch.Loop)")
		metrics := dispatch.NewMetricsCollectorWithRegistry(reg)
		loop, inproc, err := tf.NewDispatchRunner(logger, tfBinaryProvider, dec, meter, metrics)
		if err != nil {
			logger.Error("failed to start in-process dispatcher", "error", err)
			os.Exit(1)
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
		os.Exit(0)
	}

	logger.Info("using legacy Manager polling loop")
	var wg sync.WaitGroup
	wg.Add(1)

	// start run manager with workers
	runManager := tf.NewManager(tfBinaryProvider, dec, meter, logger)
	runManager.Start(&wg)

	// listen for os signals to be able to shutdown gracefully
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-signalChan
		runManager.Stop()
	}()

	wg.Wait()
	os.Exit(0)
}

func isSingleRunMode() bool {
	mode := os.Getenv(ENV_EXECUTION_MODE)
	return mode == "single-run"
}

// executeSingleRun drives one single-run execution and returns the process exit code.
//
// B11 fix (phase 2b): a single-run failure used to always fall through to exit 0, so the k8s
// Job the controller dispatched was reported "succeeded" even when the run never got off the
// ground. SingleRunWorker.ExecuteRun only returns an error for failures before the run's first
// potentially state-mutating step (workdir setup, run-JSON parse, registration — see its doc
// comment); once tofu init/apply has begun, ExecuteRun always returns nil, even on failure, and
// this function keeps returning 0 in that case. That scoping matters operationally: the
// controller's Job template uses BackoffLimit:1 + RestartPolicy:Never
// (run-controller/controller/kubernetes.go), so a blanket non-zero exit on any failure would
// make k8s re-run a failed terraform run once — a second, automatic APPLY/DESTROY against
// real infrastructure. Re-triggering stateful terraform must stay a deliberate user action, so
// only the pre-flight failure class (which never touched terraform) exits non-zero here.
func executeSingleRun(logger *slog.Logger, tfBinaryProvider *tf.TfBinaries, dec tf.Decryptor) int {
	// Read RUN_JSON_FILE_PATH from environment - extract the file path of the K8S secret file that is mounted
	runJsonFilePath := os.Getenv(ENV_RUN_JSON_FILE_PATH)
	if runJsonFilePath == "" {
		logger.Error("RUN_JSON_FILE_PATH environment variable is required in single-run mode")
		return 1
	}

	// Read JSON from file
	runJsonBytes, err := os.ReadFile(runJsonFilePath)
	if err != nil {
		logger.Error("Failed to read run JSON file", "path", runJsonFilePath, "error", err)
		return 1
	}

	// Parse JSON into RunDetailsDTO
	var runDetails meshapi.RunDetailsDTO
	if err := json.Unmarshal(runJsonBytes, &runDetails); err != nil {
		logger.Error("Failed to parse run JSON", "error", err)
		return 1
	}

	// Convert to internal Run structure (without decryption)
	run, err := tf.ToInternalWithoutDecryption(&runDetails, dec)
	if err != nil {
		logger.Error("Failed to convert run details", "error", err)
		return 1
	}

	logger.Info("Executing single run", "run", run.Id, "buildingBlock", run.BuildingBlockName)

	// Create API client and set the runToken from the run spec
	// In Kubernetes mode, the runToken is used for authentication instead of basic auth
	api := tf.NewRunApi(dec)
	logger.Info("Using runToken from run spec for authentication")
	api.SetRunToken(runDetails.Spec.RunToken)

	// Execute the run using a single worker with the configured API client
	worker := tf.NewSingleRunWorkerWithApi(
		logger,
		tf.AppConfig.TfParentWorkingDir,
		tf.AppConfig.TfCommandTimeoutMins,
		tfBinaryProvider,
		api,
		dec,
	)

	exitCode := 0
	if err := worker.ExecuteRun(run); err != nil {
		logger.Error("Run execution failed", "error", err)
		exitCode = 1
	}

	logger.Info("Single run completed")
	return exitCode
}
