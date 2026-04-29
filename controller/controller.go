package controller

import (
	"errors"
	"log"
	"os"
	"sync"
	"time"

	"github.com/meshcloud/meshfed-release/buildingblocks/run-controller/crypto"
)

type Controller struct {
	logger         *log.Logger
	shutdownCalled bool
	runApi         RunApi
	k8sClient      *KubernetesClient
	cryptoMap      map[string]*crypto.MeshCertBasedCrypto // Map runner UUID to crypto instance
	metrics        *MetricsCollector
}

func NewController() *Controller {
	k8sClient, err := newKubernetesClient(AppConfig.Namespace,
		log.New(os.Stdout, "[K8S-CLIENT] ", log.LstdFlags))
	if err != nil {
		log.Fatalf("Failed to create Kubernetes client: %v", err)
	}

	// Initialize crypto instances for each runner
	cryptoMap := make(map[string]*crypto.MeshCertBasedCrypto)
	for _, runner := range AppConfig.Runners {
		cryptoInstance, err := crypto.NewCertBasedDecryptorWithValidation(
			runner.Crypto.PrivateKey,
			[]byte(runner.Crypto.PublicKey),
		)
		if err != nil {
			log.Fatalf("Failed to initialize crypto for runner %s: %v", runner.Uuid, err)
		}
		cryptoMap[runner.Uuid] = cryptoInstance
		log.Printf("Initialized crypto for runner: %s (keys validated)", runner.Uuid)
	}

	// Initialize metrics collector
	metrics := NewMetricsCollector()
	metrics.activeRunners.Set(float64(len(AppConfig.Runners)))

	return &Controller{
		logger:         log.New(os.Stdout, "[CONTROLLER] ", log.LstdFlags),
		shutdownCalled: false,
		runApi:         newApi(),
		k8sClient:      k8sClient,
		cryptoMap:      cryptoMap,
		metrics:        metrics,
	}
}

func (c *Controller) Start(wg *sync.WaitGroup) {
	c.logger.Println("Started")

	go func() {
		defer wg.Done()
		c.run()
	}()
}

func (c *Controller) Stop() {
	c.logger.Println("Shutdown requested")
	c.shutdownCalled = true
}

func (c *Controller) run() {
	c.logger.Println("Controller running - polling for building block runs")

	// Determine polling interval (default: 10 seconds)
	pollingInterval := 10
	if AppConfig.PollingIntervalSeconds > 0 {
		pollingInterval = AppConfig.PollingIntervalSeconds
	}
	c.logger.Printf("Polling interval: %d seconds", pollingInterval)

	ticker := time.NewTicker(time.Duration(pollingInterval) * time.Second)
	defer ticker.Stop()

	for !c.shutdownCalled {
		<-ticker.C
		c.metrics.controllerLoopIterations.Inc()
		c.processRuns()
	}

	c.logger.Println("Controller stopped")
}

func (c *Controller) processRuns() {
	// Process runs for each configured runner
	for _, runner := range AppConfig.Runners {
		c.processRunsForRunner(&runner)
	}
}

func (c *Controller) processRunsForRunner(runner *RunnerConfig) {
	// Fetch available runs from meshfed API
	runJsonBase64, runDetails, err := c.runApi.FetchRunDetails(AppConfig.ControllerId, runner)

	if err != nil {
		if !isNoRunError(err) {
			c.logger.Printf("Error fetching runs for runner %s: %v", runner.Uuid, err)
		}
		// 404 means no runs available - this is normal, don't log
		return
	}

	// Decrypt the run JSON using the runner's crypto instance
	cryptoInstance := c.cryptoMap[runner.Uuid]
	decryptedRunJsonBase64, err := decryptRunDetails(runJsonBase64, cryptoInstance)
	if err != nil {
		c.logger.Printf("Failed to decrypt run details for run %s: %v", runDetails.Metadata.Uuid, err)
		c.metrics.decryptionErrors.WithLabelValues(runner.Uuid, runner.DisplayName).Inc()
		return
	}

	c.logger.Printf("Processing run %s for runner %s", runDetails.Metadata.Uuid, runner.Uuid)

	// Create Kubernetes job for the run with decrypted data
	err = c.k8sClient.CreateRunnerJob(runDetails.GetRunInfo(), decryptedRunJsonBase64, runner, c.metrics)
	if err != nil {
		c.logger.Printf("Failed to create job for run %s: %v", runDetails.Metadata.Uuid, err)

		// Report the failure back to meshfed so the run is marked as failed instead of
		// being stuck in a pending state. Use a specific message for size-related errors.
		var runTooLargeErr *RunTooLargeError
		var errorMessage string
		if errors.As(err, &runTooLargeErr) {
			errorMessage = "Run data is too large to be passed to the runner. The run data exceeds the Kubernetes secret size limit of 1MiB. Please reduce the size of the building block inputs."
		} else {
			errorMessage = "Failed to create job for run: " + err.Error()
		}

		// When the job can't be created, no runner will register as a source,
		// so the run-controller must register itself and report the failure.
		if regErr := c.runApi.RegisterSource(runDetails.Metadata.Uuid, runner); regErr != nil {
			c.logger.Printf("Failed to register as status source for run %s: %v", runDetails.Metadata.Uuid, regErr)
			return
		}

		if statusErr := c.runApi.UpdateRunStatus(
			runDetails.Metadata.Uuid,
			runner,
			"FAILED",
			errorMessage,
			errorMessage,
		); statusErr != nil {
			c.logger.Printf("Failed to report error back to meshfed for run %s: %v", runDetails.Metadata.Uuid, statusErr)
		}

		return
	}
}

func isNoRunError(err error) bool {
	// Check if this is a "no runs available" error (HTTP 404)
	if statusError, ok := err.(*StatusError); ok {
		return statusError.status == 404
	}
	return false
}
