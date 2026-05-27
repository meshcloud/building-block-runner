package controller

import (
	"errors"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	meshcrypto "github.com/meshcloud/building-block-runner/go-meshapi-client/crypto"
	meshapi "github.com/meshcloud/building-block-runner/go-meshapi-client/meshapi"
)

type Controller struct {
	logger         *log.Logger
	shutdownCalled bool
	runApi         RunApi
	k8sClient      *KubernetesClient
	crypto         *meshcrypto.MeshCertBasedCrypto
	metrics        *MetricsCollector
}

func NewController() *Controller {
	k8sClient, err := newKubernetesClient(AppConfig.Namespace,
		log.New(os.Stdout, "[K8S-CLIENT] ", log.LstdFlags))
	if err != nil {
		log.Fatalf("Failed to create Kubernetes client: %v", err)
	}

	// Initialize a single crypto instance for the universal controller
	cryptoInstance, err := meshcrypto.NewCertBasedDecryptorWithValidation(
		AppConfig.Crypto.PrivateKey,
		[]byte(AppConfig.Crypto.PublicKey),
	)
	if err != nil {
		log.Fatalf("Failed to initialize crypto for controller %s: %v", AppConfig.Uuid, err)
	}
	log.Printf("Initialized crypto for controller: %s (keys validated)", AppConfig.Uuid)

	// Initialize metrics collector
	metrics := NewMetricsCollector()
	metrics.activeRunners.Set(1)

	return &Controller{
		logger:         log.New(os.Stdout, "[CONTROLLER] ", log.LstdFlags),
		shutdownCalled: false,
		runApi:         newApi(),
		k8sClient:      k8sClient,
		crypto:         cryptoInstance,
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
	c.processNextRun()
}

func (c *Controller) processNextRun() {
	// Fetch the next available run for this universal controller
	runJsonBase64, runDetails, err := c.runApi.FetchRunDetails(AppConfig.Uuid)

	if err != nil {
		if !isNoRunError(err) {
			c.logger.Printf("Error fetching run: %v", err)
		}
		// 404 means no runs available - this is normal, don't log
		return
	}

	// Decrypt the run JSON using the controller's crypto instance
	decryptedRunJsonBase64, err := decryptRunDetails(runJsonBase64, c.crypto)
	if err != nil {
		c.logger.Printf("Failed to decrypt run details for run %s: %v", runDetails.Metadata.Uuid, err)
		c.metrics.decryptionErrors.WithLabelValues(AppConfig.Uuid).Inc()
		return
	}

	// Determine the implementation type from the fetched run
	implType, err := runDetails.Spec.Definition.Spec.GetImplementationType()
	if err != nil {
		c.logger.Printf("Failed to determine implementation type for run %s: %v", runDetails.Metadata.Uuid, err)
		c.reportRunFailure(runDetails.Metadata.Uuid, "Failed to determine implementation type: "+err.Error())
		return
	}

	// Map the implementation type to the corresponding runner type key used in the config
	runnerType := string(meshapi.ToRunnerType(implType))

	// Look up the job spec for this implementation type
	jobSpec, ok := AppConfig.Implementations[runnerType]
	if !ok {
		msg := fmt.Sprintf("no implementation handler configured for type '%s'", runnerType)
		c.logger.Printf("Cannot process run %s: %s", runDetails.Metadata.Uuid, msg)
		c.reportRunFailure(runDetails.Metadata.Uuid, msg)
		return
	}

	c.logger.Printf("Processing run %s (type: %s)", runDetails.Metadata.Uuid, runnerType)

	// Create Kubernetes job for the run with decrypted data
	err = c.k8sClient.CreateRunnerJob(runDetails.GetRunInfo(), decryptedRunJsonBase64, runnerType, &jobSpec, c.metrics)
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
		c.reportRunFailure(runDetails.Metadata.Uuid, errorMessage)
	}
}

// reportRunFailure registers the controller as a status source and marks the run as FAILED.
func (c *Controller) reportRunFailure(runId string, errorMessage string) {
	// When the job can't be created, no runner will register as a source,
	// so the run-controller must register itself and report the failure.
	if regErr := c.runApi.RegisterSource(runId); regErr != nil {
		c.logger.Printf("Failed to register as status source for run %s: %v", runId, regErr)
		return
	}

	if statusErr := c.runApi.UpdateRunStatus(runId, "FAILED", errorMessage, errorMessage); statusErr != nil {
		c.logger.Printf("Failed to report error back to meshfed for run %s: %v", runId, statusErr)
	}
}

func isNoRunError(err error) bool {
	// Check if this is a "no runs available" error (HTTP 404)
	if statusError, ok := err.(*meshapi.StatusError); ok {
		return statusError.Status == 404
	}
	return false
}
