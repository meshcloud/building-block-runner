package main

import (
	"encoding/json"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"

	meshapi "github.com/meshcloud/building-block-runner/go-meshapi-client/meshapi"

	"github.com/meshcloud/building-block-runner/tf-block-runner/build"
	"github.com/meshcloud/building-block-runner/tf-block-runner/tfrun"
)

const (
	ENV_RUN_JSON_FILE_PATH = "RUN_JSON_FILE_PATH"
	ENV_EXECUTION_MODE     = "EXECUTION_MODE"
)

func main() {
	logger := log.New(os.Stdout, "[TF RUNNER] ", log.LstdFlags)
	meshapi.SetClientMetadata("tf-block-runner", build.Version)
	logger.Printf("Build metadata: version=%s", build.Version)

	if err := tfrun.ReadConfig(logger); err != nil {
		logger.Fatalf("cannot read config: %s", err.Error())
	}

	// Check if running in single-run mode
	singleRunMode := isSingleRunMode()

	// Build the decryptor for sensitive inputs (D4 — replaces the former meshcrypto.Crypto global).
	// Single-run mode: the controller already decrypted everything, so use a no-op decryptor.
	// Polling mode: build a cert-based decryptor from the runner's private key (no key => no-op,
	// preserving the former "Crypto == nil" passthrough behavior).
	var dec tfrun.Decryptor = tfrun.NoopDecryptor{}
	if !singleRunMode && tfrun.AppConfig.PrivateKey != "" {
		var cryptoErr error
		dec, cryptoErr = tfrun.NewCertDecryptor(tfrun.AppConfig.PrivateKey)
		if cryptoErr != nil {
			logger.Fatalf("failed to initialize crypto: private key could not be loaded: %s", cryptoErr.Error())
		}
		logger.Println("Crypto initialized for polling mode")
	} else if singleRunMode {
		logger.Println("Single-run mode: skipping crypto initialization (controller handles decryption)")
	}

	// define tf binary provider
	tfBinaryProvider, err := tfrun.NewTfBin(tfrun.AppConfig.TfInstallDir, os.Stdout)
	if err != nil {
		panic(err)
	}

	// Check if running in single-run mode
	if singleRunMode {
		logger.Println("Running in single-run mode")
		executeSingleRun(logger, tfBinaryProvider, dec)
		return
	}

	// Standard polling mode
	logger.Println("Running in polling mode")
	startHealthServer(logger)
	var wg sync.WaitGroup
	wg.Add(1)

	// start run manager with workers
	runManager := tfrun.NewManager(tfBinaryProvider, dec)
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

func startHealthServer(logger *log.Logger) {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8100"
	}
	addr := ":" + port
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		logger.Fatalf("Health server failed to bind on %s: %v", addr, err)
	}
	logger.Printf("Health server listening on %s", addr)
	go func() {
		if err := http.Serve(ln, mux); err != nil {
			logger.Fatalf("Health server error: %v", err)
		}
	}()
}

func executeSingleRun(logger *log.Logger, tfBinaryProvider *tfrun.TfBinaries, dec tfrun.Decryptor) {
	// Read RUN_JSON_FILE_PATH from environment - extract the file path of the K8S secret file that is mounted
	runJsonFilePath := os.Getenv(ENV_RUN_JSON_FILE_PATH)
	if runJsonFilePath == "" {
		logger.Fatalf("RUN_JSON_FILE_PATH environment variable is required in single-run mode")
	}

	// Read JSON from file
	runJsonBytes, err := os.ReadFile(runJsonFilePath)
	if err != nil {
		logger.Fatalf("Failed to read run JSON file from %s: %v", runJsonFilePath, err)
	}

	// Parse JSON into RunDetailsDTO
	var runDetails meshapi.RunDetailsDTO
	if err := json.Unmarshal(runJsonBytes, &runDetails); err != nil {
		logger.Fatalf("Failed to parse run JSON: %v", err)
	}

	// Convert to internal Run structure (without decryption)
	run, err := tfrun.ToInternalWithoutDecryption(&runDetails, dec)
	if err != nil {
		logger.Fatalf("Failed to convert run details: %v", err)
	}

	logger.Printf("Executing single run: %s - %s", run.Id, run.BuildingBlockName)

	// Create API client and set the runToken from the run spec
	// In Kubernetes mode, the runToken is used for authentication instead of basic auth
	api := tfrun.NewRunApi(dec)
	logger.Println("Using runToken from run spec for authentication")
	api.SetRunToken(runDetails.Spec.RunToken)

	// Execute the run using a single worker with the configured API client
	worker := tfrun.NewSingleRunWorkerWithApi(
		logger,
		tfrun.AppConfig.TfParentWorkingDir,
		tfrun.AppConfig.TfCommandTimeoutMins,
		tfBinaryProvider,
		api,
		dec,
	)

	if err := worker.ExecuteRun(run); err != nil {
		logger.Printf("Run execution failed: %v", err)
	}

	logger.Println("Single run completed")
}
