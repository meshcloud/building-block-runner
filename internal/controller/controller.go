package controller

import (
	"errors"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	meshcrypto "github.com/meshcloud/building-block-runner/internal/crypto"
	meshapi "github.com/meshcloud/building-block-runner/internal/meshapi"
)

// JobManager abstracts the Kubernetes operations the controller depends on, so the control
// flow (drain loop, capacity guard) can be unit-tested with a fake. *KubernetesClient is the
// production implementation.
type JobManager interface {
	CreateRunnerJob(runInfo meshapi.RunInfo, runJsonBase64 string, implType string, jobSpec *JobSpecTemplate, metrics *MetricsCollector) error
	CountActiveJobs() (int, error)
}

type Controller struct {
	logger         *log.Logger
	shutdownCalled bool
	runApi         RunApi
	k8sClient      JobManager
	crypto         *meshcrypto.MeshCertBasedCrypto
	metrics        *MetricsCollector
}

// processResult describes the outcome of attempting to process a single run, so the drain
// loop knows whether to keep going (a job was created) or stop until the next polling cycle.
type processResult int

const (
	// runProcessed means a run was fetched and its job was created successfully.
	runProcessed processResult = iota
	// noRunAvailable means there was no run to process (404 or a fetch error); nothing was claimed.
	noRunAvailable
	// processFailed means a run was claimed but could not be turned into a job; it was reported as FAILED.
	processFailed
)

// maxDrainPerCycleUnlimited bounds how many runs are drained in a single polling cycle when
// concurrency is configured as unlimited. It is a safety backstop against an infinite loop if
// the API were to keep returning runs indefinitely; draining stops naturally on the first 404.
const maxDrainPerCycleUnlimited = 10

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
		c.drainRuns()
	}

	c.logger.Println("Controller stopped")
}

// drainRuns processes as many queued runs as the controller has capacity for in a single
// polling cycle, back-to-back. It determines the available capacity once up front, then keeps
// creating jobs until there are no more runs, a run fails to process, capacity is exhausted, or
// a shutdown is requested. Draining back-to-back (instead of one run per polling interval) lets
// a backlog drain quickly without waiting a full interval between each run.
func (c *Controller) drainRuns() {
	capacity := c.availableCapacity()
	if capacity <= 0 {
		c.logger.Printf("At job capacity (max %d concurrent jobs); skipping run fetch this cycle", AppConfig.MaxConcurrentJobs)
		c.metrics.jobsAtCapacitySkips.WithLabelValues(AppConfig.Uuid).Inc()
		return
	}

	for created := 0; created < capacity && !c.shutdownCalled; {
		// Only fetch (and thereby claim) a run while we still have capacity budget remaining,
		// so we don't claim runs we'd be unable to place and have to mark as FAILED.
		switch c.processNextRun() {
		case runProcessed:
			created++
		default:
			// noRunAvailable: backlog drained, wait for the next polling cycle.
			// processFailed: stop draining this cycle; the run was already reported as FAILED.
			return
		}
	}
}

// availableCapacity returns how many additional runner jobs the controller may create right now.
// A negative MaxConcurrentJobs means unlimited. If the active job count cannot be determined we
// return 0 (skip this cycle) rather than risk claiming runs we cannot place: the same API that
// failed the count would likely fail job creation, which would force runs to be reported FAILED.
func (c *Controller) availableCapacity() int {
	maxJobs := AppConfig.MaxConcurrentJobs
	if maxJobs < 0 {
		return maxDrainPerCycleUnlimited
	}

	active, err := c.k8sClient.CountActiveJobs()
	if err != nil {
		c.logger.Printf("Failed to determine active job count; skipping run fetch this cycle: %v", err)
		return 0
	}

	available := maxJobs - active
	if available < 0 {
		return 0
	}
	return available
}

func (c *Controller) processNextRun() processResult {
	// Fetch the next available run for this universal controller
	runJsonBase64, runDetails, err := c.runApi.FetchRunDetails(AppConfig.Uuid)

	if err != nil {
		if !isNoRunError(err) {
			c.logger.Printf("Error fetching run: %v", err)
		}
		// 404 means no runs available - this is normal, don't log
		return noRunAvailable
	}

	// Decrypt the run JSON using the controller's crypto instance
	decryptedRunJsonBase64, err := decryptRunDetails(runJsonBase64, c.crypto)
	if err != nil {
		c.logger.Printf("Failed to decrypt run details for run %s: %v", runDetails.Metadata.Uuid, err)
		c.metrics.decryptionErrors.WithLabelValues(AppConfig.Uuid).Inc()
		return processFailed
	}

	// Determine the implementation type from the fetched run
	implType, err := runDetails.Spec.Definition.Spec.GetImplementationType()
	if err != nil {
		c.logger.Printf("Failed to determine implementation type for run %s: %v", runDetails.Metadata.Uuid, err)
		c.reportRunFailure(runDetails.Metadata.Uuid, "Failed to determine implementation type: "+err.Error())
		return processFailed
	}

	// Map the implementation type to the corresponding runner type key used in the config
	runnerType := string(meshapi.ToRunnerType(implType))

	// Look up the job spec for this implementation type
	jobSpec, ok := AppConfig.Implementations[runnerType]
	if !ok {
		msg := fmt.Sprintf("no implementation handler configured for type '%s'", runnerType)
		c.logger.Printf("Cannot process run %s: %s", runDetails.Metadata.Uuid, msg)
		c.reportRunFailure(runDetails.Metadata.Uuid, msg)
		return processFailed
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
		return processFailed
	}

	return runProcessed
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
	// Check if this is a "no runs available" error (HTTP 404).
	if he, ok := meshapi.AsHttpError(err); ok {
		return he.IsNotFound()
	}
	return false
}
