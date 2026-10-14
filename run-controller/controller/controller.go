package controller

import (
	"context"
	"errors"
	"log"
	"os"
	"time"

	meshapi "github.com/meshcloud/building-block-runner/go-meshapi-client/meshapi"
	"github.com/meshcloud/building-block-runner/internal/crypto"
	"github.com/meshcloud/building-block-runner/run-controller/build"
)

type Controller struct {
	logger     *log.Logger
	runApi     RunApi
	dispatcher build.Dispatcher
	crypto     crypto.Crypto
	metrics    *MetricsCollector
}

type processResult int

const (
	runProcessed processResult = iota
	noRunAvailable
	processFailed
)

// Guards against an API that never runs out of runs.
const maxDrainPerCycleUnlimited = 10

func NewController(dispatcher build.Dispatcher) *Controller {
	var privateKey crypto.PrivateKey
	if err := privateKey.UnmarshalText([]byte(AppConfig.Crypto.PrivateKey)); err != nil {
		log.Fatalf("Failed to initialize crypto for controller %s: %v", AppConfig.Uuid, err)
	}
	log.Printf("Initialized crypto for controller: %s", AppConfig.Uuid)

	metrics := NewMetricsCollector()
	metrics.activeRunners.Set(1)

	return &Controller{
		logger:     log.New(os.Stdout, "[CONTROLLER] ", log.LstdFlags),
		runApi:     newApi(),
		dispatcher: dispatcher,
		crypto:     crypto.Crypto{PrivateKeys: crypto.PrivateKeys{privateKey}},
		metrics:    metrics,
	}
}

func (c *Controller) Run(ctx context.Context) {
	c.logger.Println("Controller running - polling for building block runs")

	pollingInterval := 10
	if AppConfig.PollingIntervalSeconds > 0 {
		pollingInterval = AppConfig.PollingIntervalSeconds
	}
	c.logger.Printf("Polling interval: %d seconds", pollingInterval)

	ticker := time.NewTicker(time.Duration(pollingInterval) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			c.logger.Println("Controller stopped")
			return
		case <-ticker.C:
			c.metrics.controllerLoopIterations.Inc()
			c.drainRuns(ctx)
		}
	}
}

func (c *Controller) drainRuns(ctx context.Context) {
	capacity := c.availableCapacity()
	if capacity <= 0 {
		c.logger.Printf("At job capacity (max %d concurrent jobs); skipping run fetch this cycle", AppConfig.MaxConcurrentJobs)
		c.metrics.jobsAtCapacitySkips.WithLabelValues(AppConfig.Uuid).Inc()
		return
	}

	for dispatched := 0; dispatched < capacity && ctx.Err() == nil; dispatched++ {
		// Shutdown must not cut off a claim or a failure report, or the run stays claimed without a status.
		if c.processNextRun(context.WithoutCancel(ctx)) != runProcessed {
			return
		}
	}
}

// A run claimed without capacity to place it would have to be reported FAILED.
func (c *Controller) availableCapacity() int {
	maxJobs := AppConfig.MaxConcurrentJobs
	if maxJobs < 0 {
		return maxDrainPerCycleUnlimited
	}

	active, err := c.dispatcher.CountActive()
	if err != nil {
		c.logger.Printf("Failed to determine active job count; skipping run fetch this cycle: %v", err)
		return 0
	}
	return max(maxJobs-active, 0)
}

func (c *Controller) processNextRun(ctx context.Context) processResult {
	runJsonBase64, runDetails, err := c.runApi.FetchRunDetails(AppConfig.Uuid)
	if err != nil {
		if !isNoRunError(err) {
			c.logger.Printf("Error fetching run: %v", err)
		}
		return noRunAvailable
	}

	decryptedRunJsonBase64, err := decryptRunDetails(runJsonBase64, c.crypto)
	if err != nil {
		c.logger.Printf("Failed to decrypt run details for run %s: %v", runDetails.Metadata.Uuid, err)
		c.metrics.decryptionErrors.WithLabelValues(AppConfig.Uuid).Inc()
		return processFailed
	}

	implType, err := runDetails.Spec.Definition.Spec.GetImplementationType()
	if err != nil {
		c.logger.Printf("Failed to determine implementation type for run %s: %v", runDetails.Metadata.Uuid, err)
		c.reportRunFailure(ctx, runDetails.Metadata.Uuid, "Failed to determine implementation type: "+err.Error())
		return processFailed
	}
	runnerType := string(meshapi.ToRunnerType(implType))

	c.logger.Printf("Processing run %s (type: %s)", runDetails.Metadata.Uuid, runnerType)
	if err := c.dispatcher.Dispatch(ctx, runDetails, decryptedRunJsonBase64, runnerType); err != nil {
		c.logger.Printf("Failed to dispatch run %s: %v", runDetails.Metadata.Uuid, err)
		c.reportRunFailure(ctx, runDetails.Metadata.Uuid, dispatchFailureMessage(err))
		return processFailed
	}
	return runProcessed
}

func dispatchFailureMessage(err error) string {
	if _, ok := errors.AsType[*NoHandlerError](err); ok {
		return err.Error()
	}
	if _, ok := errors.AsType[*RunTooLargeError](err); ok {
		return "Run data is too large to be passed to the runner. The run data exceeds the Kubernetes secret size limit of 1MiB. Please reduce the size of the building block inputs."
	}
	return "Failed to dispatch run: " + err.Error()
}

// No runner registers as a source for a run that was never dispatched, so the controller does.
func (c *Controller) reportRunFailure(ctx context.Context, runId string, errorMessage string) {
	if regErr := c.runApi.RegisterSource(runId); regErr != nil {
		c.logger.Printf("Failed to register as status source for run %s: %v", runId, regErr)
		return
	}

	if statusErr := c.runApi.UpdateRunStatus(ctx, runId, "FAILED", errorMessage, errorMessage); statusErr != nil {
		c.logger.Printf("Failed to report error back to meshfed for run %s: %v", runId, statusErr)
	}
}

func isNoRunError(err error) bool {
	statusError, ok := errors.AsType[*meshapi.StatusError](err)
	return ok && statusError.Status == 404
}
