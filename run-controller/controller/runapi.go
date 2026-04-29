package controller

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

var UseTestClient = false

type StatusError struct {
	status int
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("Unexpected HTTP status: %d", e.status)
}

type RunApiClient struct {
	cid      string
	url      string
	username string
	password string
	client   *http.Client
	metrics  *MetricsCollector
}

const (
	EP_GetRun       = "%s/api/meshobjects/meshbuildingblockruns"
	EP_Registration = "%s/api/meshobjects/meshbuildingblockruns/%s/status/source"
	EP_Update       = "%s/api/meshobjects/meshbuildingblockruns/%s/status/source/%s"

	BlockRun_Type_V1 = "application/vnd.meshcloud.api.meshbuildingblockrun.v1.hal+json"
)

// getRunEndpoint returns the appropriate endpoint URL with conditional selector
func getRunEndpoint(baseUrl string, runnerUuid string) string {
	return fmt.Sprintf(EP_GetRun+"/create?forRunnerUuid=%s", baseUrl, runnerUuid)
}

type RunApi interface {
	FetchRunDetails(nodePostfix string, runner *RunnerConfig) (string, *RunDetailsDTO, error)
	RegisterSource(runId string, runner *RunnerConfig) error
	UpdateRunStatus(runId string, runner *RunnerConfig, status string, summary string, stepMessage string) error
}

func newApi() RunApi {
	return &RunApiClient{
		cid:      AppConfig.ControllerId,
		url:      AppConfig.Api.Url,
		username: AppConfig.Api.Username,
		password: AppConfig.Api.Password,
		client:   &http.Client{},
		metrics:  NewMetricsCollector(),
	}
}

func (api *RunApiClient) FetchRunDetails(nodePostfix string, runner *RunnerConfig) (string, *RunDetailsDTO, error) {
	requester := fmt.Sprintf("%s-%s", api.cid, nodePostfix)
	url := getRunEndpoint(api.url, runner.Uuid)

	// Measure fetch duration
	start := time.Now()
	defer func() {
		api.metrics.runsFetchDuration.WithLabelValues(runner.Uuid, runner.DisplayName).Observe(time.Since(start).Seconds())
	}()

	// Create basic auth header using runner-specific credentials
	auth := api.buildAuthHeader(runner)

	req, err := http.NewRequest(http.MethodPost, url, nil)
	if err != nil {
		return "", nil, err
	}

	req.Header.Add("Accept", BlockRun_Type_V1)
	req.Header.Add("Content-Type", BlockRun_Type_V1)
	req.Header.Add("X-Block-Runner-Node-Id", requester)
	req.Header.Add("Authorization", auth)

	resp, err := api.client.Do(req)
	if err != nil {
		api.metrics.runsFetchErrors.WithLabelValues(runner.Uuid, runner.DisplayName, ErrorTypeFetchAPI).Inc()
		return "", nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 && resp.StatusCode != 201 {
		if resp.StatusCode != 404 {
			// Don't count 404 as error - it just means no runs available
			api.metrics.runsFetchErrors.WithLabelValues(runner.Uuid, runner.DisplayName, ErrorTypeFetchAPI).Inc()
		}
		return "", nil, &StatusError{resp.StatusCode}
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, err
	}

	var runDetails RunDetailsDTO
	if err := json.Unmarshal(data, &runDetails); err != nil {
		return "", nil, fmt.Errorf("failed to parse run JSON: %w", err)
	}
	// should not happen, but just in case
	if runDetails.Metadata.Uuid == "" {
		return "", nil, fmt.Errorf("Fetched run has no UUID")
	}

	// Base64 encode the JSON data
	runJsonBase64 := base64.StdEncoding.EncodeToString(data)

	return runJsonBase64, &runDetails, nil
}

// buildAuthHeader constructs the Basic auth header using runner-specific credentials
func (api *RunApiClient) buildAuthHeader(runner *RunnerConfig) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(runner.Api.Username+":"+runner.Api.Password))
}

// RegisterSource registers the run-controller as a status source for a run.
// This must be called before any status updates can be sent via UpdateRunStatus.
//
// The registration declares what steps the source will report on. Steps are
// registered without a status — actual status updates happen via UpdateRunStatus.
//
// Idempotent: if the source is already registered (HTTP 409 Conflict), the
// call succeeds silently so that retries don't cause failures.
func (api *RunApiClient) RegisterSource(runId string, runner *RunnerConfig) error {
	auth := api.buildAuthHeader(runner)
	url := fmt.Sprintf(EP_Registration, api.url, runId)

	dto := SourceRegistrationDTO{
		Source: SourceDTO{
			Id: runner.Uuid,
		},
		Steps: []StepRegistrationDTO{
			{
				Id:          "validation",
				DisplayName: "Validation",
			},
		},
	}

	body, err := json.Marshal(dto)
	if err != nil {
		return fmt.Errorf("failed to marshal registration: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewBuffer(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Add("Accept", BlockRun_Type_V1)
	req.Header.Add("Content-Type", BlockRun_Type_V1)
	req.Header.Add("Authorization", auth)

	resp, err := api.client.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	// 409 Conflict means the source is already registered — treat as success.
	if resp.StatusCode == http.StatusConflict {
		log.Printf("Status source already registered for run %s, continuing", runId)
		return nil
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("register source returned HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	log.Printf("Successfully registered as status source for run %s", runId)
	return nil
}

// UpdateRunStatus sends a status update (PATCH) for a run that was previously
// registered via RegisterSource.
//
// Parameters:
//   - status: overall run status (e.g. "SUCCEEDED", "FAILED")
//   - summary: human-readable summary shown on the run
//   - stepMessage: message shown on the "validation" step (both user and system message)
func (api *RunApiClient) UpdateRunStatus(runId string, runner *RunnerConfig, status string, summary string, stepMessage string) error {
	auth := api.buildAuthHeader(runner)
	url := fmt.Sprintf(EP_Update, api.url, runId, runner.Uuid)

	dto := StatusUpdateDTO{
		Status:  &status,
		Summary: &summary,
		Steps: []StepUpdateDTO{
			{
				Id:            "validation",
				DisplayName:   "Validation",
				Status:        &status,
				UserMessage:   &stepMessage,
				SystemMessage: &stepMessage,
			},
		},
	}

	body, err := json.Marshal(dto)
	if err != nil {
		return fmt.Errorf("failed to marshal status update: %w", err)
	}

	req, err := http.NewRequest(http.MethodPatch, url, bytes.NewBuffer(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Add("Accept", BlockRun_Type_V1)
	req.Header.Add("Content-Type", BlockRun_Type_V1)
	req.Header.Add("Authorization", auth)

	resp, err := api.client.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("update status returned HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	log.Printf("Successfully reported %s status for run %s", status, runId)
	return nil
}
