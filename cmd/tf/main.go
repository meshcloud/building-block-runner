package main

import (
	"encoding/json"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/meshcloud/building-block-runner/internal/build"
	"github.com/meshcloud/building-block-runner/internal/config"
	meshapi "github.com/meshcloud/building-block-runner/internal/meshapi"
	"github.com/meshcloud/building-block-runner/internal/mgmt"
	"github.com/meshcloud/building-block-runner/internal/tf"
)

const (
	ENV_RUN_JSON_FILE_PATH = "RUN_JSON_FILE_PATH"
	ENV_EXECUTION_MODE     = "EXECUTION_MODE"
)

func main() {
	logger := log.New(os.Stdout, "[TF RUNNER] ", log.LstdFlags)
	// Runner identity is now passed per client (meshapi.Identity, §5.2.2); tf.NewRunApi
	// stamps {"tf-block-runner", build.Version} at client construction.
	logger.Printf("Build metadata: version=%s", build.Version)

	if err := tf.ReadConfig(logger); err != nil {
		logger.Fatalf("cannot read config: %s", err.Error())
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
			logger.Fatalf("failed to initialize crypto: private key could not be loaded: %s", cryptoErr.Error())
		}
		logger.Println("Crypto initialized for polling mode")
	} else if singleRunMode {
		logger.Println("Single-run mode: skipping crypto initialization (controller handles decryption)")
	}

	// define tf binary provider
	tfBinaryProvider, err := tf.NewTfBin(tf.AppConfig.TfInstallDir, os.Stdout)
	if err != nil {
		panic(err)
	}

	// Check if running in single-run mode
	if singleRunMode {
		logger.Println("Running in single-run mode")
		os.Exit(executeSingleRun(logger, tfBinaryProvider, dec))
	}

	// Standard polling mode
	logger.Println("Running in polling mode")

	// D12 (§4.3): one listener serves /healthz + /metrics on MANAGEMENT_PORT, with PORT kept
	// working as a deprecated tf-persona alias (D10 -- the image's ENV PORT=8080 must resolve
	// unchanged). tf has no pre-existing default-registry metrics of its own, so it gets a
	// fresh registry (mgmt.NewRegistry) instead of reaching for the global one.
	mgmtLog := slog.New(slog.NewTextHandler(os.Stdout, nil))
	mgmtPort, err := config.ManagementPort(mgmtLog, 8100, config.EnvAlias{Var: "PORT", Deprecated: true})
	if err != nil {
		logger.Fatalf("invalid management port configuration: %s", err.Error())
	}
	reg := mgmt.NewRegistry()
	meter := mgmt.NewRunMetrics(reg, tf.AppConfig.RunnerUuid)
	if err := mgmt.NewServer(mgmtLog, mgmtPort.Addr(), reg).Start(); err != nil {
		logger.Fatalf("%s", err.Error())
	}

	var wg sync.WaitGroup
	wg.Add(1)

	// start run manager with workers
	runManager := tf.NewManager(tfBinaryProvider, dec, meter)
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
func executeSingleRun(logger *log.Logger, tfBinaryProvider *tf.TfBinaries, dec tf.Decryptor) int {
	// Read RUN_JSON_FILE_PATH from environment - extract the file path of the K8S secret file that is mounted
	runJsonFilePath := os.Getenv(ENV_RUN_JSON_FILE_PATH)
	if runJsonFilePath == "" {
		logger.Println("RUN_JSON_FILE_PATH environment variable is required in single-run mode")
		return 1
	}

	// Read JSON from file
	runJsonBytes, err := os.ReadFile(runJsonFilePath)
	if err != nil {
		logger.Printf("Failed to read run JSON file from %s: %v", runJsonFilePath, err)
		return 1
	}

	// Parse JSON into RunDetailsDTO
	var runDetails meshapi.RunDetailsDTO
	if err := json.Unmarshal(runJsonBytes, &runDetails); err != nil {
		logger.Printf("Failed to parse run JSON: %v", err)
		return 1
	}

	// Convert to internal Run structure (without decryption)
	run, err := tf.ToInternalWithoutDecryption(&runDetails, dec)
	if err != nil {
		logger.Printf("Failed to convert run details: %v", err)
		return 1
	}

	logger.Printf("Executing single run: %s - %s", run.Id, run.BuildingBlockName)

	// Create API client and set the runToken from the run spec
	// In Kubernetes mode, the runToken is used for authentication instead of basic auth
	api := tf.NewRunApi(dec)
	logger.Println("Using runToken from run spec for authentication")
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
		logger.Printf("Run execution failed: %v", err)
		exitCode = 1
	}

	logger.Println("Single run completed")
	return exitCode
}
