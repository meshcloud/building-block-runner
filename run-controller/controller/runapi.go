package controller

import (
	"encoding/base64"
	"fmt"
	"log"
	"net/http"
	"time"

	meshapi "github.com/meshcloud/building-block-runner/go-meshapi-client/meshapi"
)

var UseTestClient = false

const requesterPrefix = "run-controller"

type RunApiClient struct {
	url     string
	metrics *MetricsCollector
}

type RunApi interface {
	FetchRunDetails(nodePostfix string) (string, *meshapi.RunDetailsDTO, error)
	RegisterSource(runId string) error
	UpdateRunStatus(runId string, status string, summary string, stepMessage string) error
}

func newApi() RunApi {
	return &RunApiClient{
		url:     AppConfig.Api.Url,
		metrics: NewMetricsCollector(),
	}
}

func (api *RunApiClient) newMeshClient(nodeID string) *meshapi.Client {
	return meshapi.NewClient(api.url, nodeID, AppConfig.Api.NewAuthProvider(api.url))
}

func (api *RunApiClient) FetchRunDetails(nodePostfix string) (string, *meshapi.RunDetailsDTO, error) {
	requester := fmt.Sprintf("%s-%s", requesterPrefix, nodePostfix)

	// Measure fetch duration
	start := time.Now()
	defer func() {
		api.metrics.runsFetchDuration.WithLabelValues(AppConfig.Uuid).Observe(time.Since(start).Seconds())
	}()

	client := api.newMeshClient(requester)
	dto, rawBytes, err := client.FetchRun(AppConfig.Uuid)
	if err != nil {
		if statusErr, ok := err.(*meshapi.StatusError); ok && statusErr.Status != http.StatusNotFound {
			api.metrics.runsFetchErrors.WithLabelValues(AppConfig.Uuid, ErrorTypeFetchAPI).Inc()
		} else if !ok {
			api.metrics.runsFetchErrors.WithLabelValues(AppConfig.Uuid, ErrorTypeFetchAPI).Inc()
		}
		return "", nil, err
	}

	runJsonBase64 := base64.StdEncoding.EncodeToString(rawBytes)
	return runJsonBase64, dto, nil
}

// RegisterSource registers the run-controller as a status source for a run.
// Idempotent: if the source is already registered (HTTP 409 Conflict), the call succeeds silently.
func (api *RunApiClient) RegisterSource(runId string) error {
	requester := fmt.Sprintf("%s-%s", requesterPrefix, AppConfig.Uuid)
	client := api.newMeshClient(requester)

	registration := meshapi.RegistrationDTO{
		Source: meshapi.SourceDTO{
			Id: AppConfig.Uuid,
		},
		Steps: []meshapi.StepRegistrationDTO{
			{
				Id:          "validation",
				DisplayName: "Validation",
			},
		},
	}

	if err := client.RegisterSource(runId, registration); err != nil {
		return fmt.Errorf("register source failed: %w", err)
	}

	log.Printf("Successfully registered as status source for run %s", runId)
	return nil
}

// UpdateRunStatus sends a PATCH status update for a run.
// It must be called after RegisterSource has been called for the same run.
func (api *RunApiClient) UpdateRunStatus(runId string, status string, summary string, stepMessage string) error {
	requester := fmt.Sprintf("%s-%s", requesterPrefix, AppConfig.Uuid)
	client := api.newMeshClient(requester)

	dto := meshapi.StatusUpdateDTO{
		Status:  &status,
		Summary: &summary,
		Steps: []meshapi.StepStatusUpdateDTO{
			{
				Id:            "validation",
				DisplayName:   "Validation",
				Status:        &status,
				UserMessage:   &stepMessage,
				SystemMessage: &stepMessage,
			},
		},
	}

	if _, err := client.PatchStatus(runId, AppConfig.Uuid, dto); err != nil {
		return fmt.Errorf("update status failed: %w", err)
	}

	log.Printf("Successfully reported %s status for run %s", status, runId)
	return nil
}
