package controller

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"net/http"
	"time"

	meshapi "github.com/meshcloud/building-block-runner/go-meshapi-client/meshapi"
)

const requesterPrefix = "run-controller"

type RunApiClient struct {
	url     string
	metrics *MetricsCollector
}

type RunApi interface {
	FetchRunDetails(nodePostfix string) (string, *meshapi.RunDetailsDTO, error)
	RegisterSource(runId string) error
	UpdateRunStatus(ctx context.Context, runId string, status string, summary string, stepMessage string) error
}

func newApi() RunApi {
	return newRunApiClient()
}

func newRunApiClient() *RunApiClient {
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

// Must follow RegisterSource for the same run.
func (api *RunApiClient) UpdateRunStatus(ctx context.Context, runId string, status string, summary string, stepMessage string) error {
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

	if _, err := client.PatchStatus(ctx, runId, AppConfig.Uuid, dto); err != nil {
		return fmt.Errorf("update status failed: %w", err)
	}

	log.Printf("Successfully reported %s status for run %s", status, runId)
	return nil
}

const wifTokenStepId = "meshstack-wif-token"

// A source of its own, because a runner whose source already exists does not register its steps.
func (api *RunApiClient) ReportWifTokenMissing(ctx context.Context, runId string, reason string) error {
	sourceId := AppConfig.Uuid + "-wif-token"
	client := api.newMeshClient(fmt.Sprintf("%s-%s", requesterPrefix, AppConfig.Uuid))

	registration := meshapi.RegistrationDTO{
		Source: meshapi.SourceDTO{Id: sourceId},
		Steps:  []meshapi.StepRegistrationDTO{{Id: wifTokenStepId, DisplayName: "meshStack workload identity token"}},
	}
	if err := client.RegisterSourceAt(ctx, fmt.Sprintf(meshapi.EPRunSourceRegistration, api.url, runId), registration); err != nil {
		return fmt.Errorf("register source: %w", err)
	}

	status := "FAILED"
	message := fmt.Sprintf("meshStack could not issue the workload identity token for this run: %s\n\n"+
		"The run continues without %s. A step that signs in to a cloud with this file fails. "+
		"The other workload identity tokens of this run are not affected.", reason, wifTokenPath)
	update := meshapi.StatusUpdateDTO{
		Steps: []meshapi.StepStatusUpdateDTO{{
			Id:            wifTokenStepId,
			DisplayName:   "meshStack workload identity token",
			Status:        &status,
			SystemMessage: &message,
		}},
	}
	if _, err := client.PatchStatus(ctx, runId, sourceId, update); err != nil {
		return fmt.Errorf("update status: %w", err)
	}
	return nil
}
