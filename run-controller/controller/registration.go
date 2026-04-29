package controller

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
)

const (
	EP_RunnerWithUuid = "%s/api/meshobjects/meshbuildingblockrunners/%s"
	EP_Runners        = "%s/api/meshobjects/meshbuildingblockrunners"

	MeshBuildingBlockRunner_MediaType_V1Preview = "application/vnd.meshcloud.api.meshbuildingblockrunner.v1-preview.hal+json"
)

// RegistrationApi interface for runner registration
type RegistrationApi interface {
	RegisterRunner(runner *RunnerConfig, metrics *MetricsCollector) error
}

// RegistrationApiClient implements the RegistrationApi
type RegistrationApiClient struct {
	url        string
	namespace  string
	username   string
	password   string
	oidcIssuer string
	client     *http.Client
	logger     *log.Logger
}

// NewRegistrationApi creates a new registration API client
func NewRegistrationApi(logger *log.Logger) RegistrationApi {
	return &RegistrationApiClient{
		url:        AppConfig.Api.Url,
		namespace:  AppConfig.Namespace,
		username:   AppConfig.Api.Username,
		password:   AppConfig.Api.Password,
		oidcIssuer: DiscoveredOidcIssuer,
		client:     &http.Client{},
		logger:     logger,
	}
}

// RegisterRunner registers or creates a runner via the meshObject API
// It first tries PUT (update), and if the runner doesn't exist (404), falls back to POST (create)
func (api *RegistrationApiClient) RegisterRunner(runner *RunnerConfig, metrics *MetricsCollector) error {
	// Build the registration DTO with auto-constructed WIF
	dto := BuildRunnerRegistrationDTO(runner, api.namespace, api.oidcIssuer)

	// Marshal to JSON
	jsonBody, err := json.Marshal(dto)
	if err != nil {
		metrics.runnerRegistrationErrors.WithLabelValues(runner.Uuid, runner.DisplayName, ErrorTypeRegistrationMarshal).Inc()
		return fmt.Errorf("failed to marshal runner registration: %w", err)
	}

	// Try PUT first (update existing runner)
	statusCode, err := api.putRunner(runner, jsonBody)
	if err != nil {
		metrics.runnerRegistrationErrors.WithLabelValues(runner.Uuid, runner.DisplayName, ErrorTypeRegistrationPut).Inc()
		return err
	}

	// If runner doesn't exist, create it with POST
	if statusCode == http.StatusNotFound {
		api.logger.Printf("Runner %s not found, creating new runner", runner.Uuid)
		err = api.postRunner(runner, jsonBody)
		if err != nil {
			metrics.runnerRegistrationErrors.WithLabelValues(runner.Uuid, runner.DisplayName, ErrorTypeRegistrationPost).Inc()
			return err
		}
		metrics.runnerRegistrationSuccess.WithLabelValues(runner.Uuid, runner.DisplayName).Inc()
		return nil
	}

	metrics.runnerRegistrationSuccess.WithLabelValues(runner.Uuid, runner.DisplayName).Inc()
	api.logger.Printf("Successfully updated runner %s (%s)", runner.DisplayName, runner.Uuid)
	return nil
}

// putRunner attempts to update an existing runner via PUT
func (api *RegistrationApiClient) putRunner(runner *RunnerConfig, jsonBody []byte) (int, error) {
	url := fmt.Sprintf(EP_RunnerWithUuid, api.url, runner.Uuid)

	api.logger.Printf("Updating runner %s (%s) at %s", runner.DisplayName, runner.Uuid, url)

	req, err := http.NewRequest(http.MethodPut, url, bytes.NewReader(jsonBody))
	if err != nil {
		return 0, fmt.Errorf("failed to create PUT request: %w", err)
	}

	api.setHeaders(req)

	resp, err := api.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("failed to execute PUT request: %w", err)
	}
	defer resp.Body.Close()

	// 404 means runner doesn't exist - caller should try POST
	if resp.StatusCode == http.StatusNotFound {
		return http.StatusNotFound, nil
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, fmt.Errorf("PUT failed with status %d: %s", resp.StatusCode, string(body))
	}

	return resp.StatusCode, nil
}

// postRunner creates a new runner via POST
func (api *RegistrationApiClient) postRunner(runner *RunnerConfig, jsonBody []byte) error {
	url := fmt.Sprintf(EP_Runners, api.url)

	api.logger.Printf("Creating runner %s (%s) at %s", runner.DisplayName, runner.Uuid, url)

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(jsonBody))
	if err != nil {
		return fmt.Errorf("failed to create POST request: %w", err)
	}

	api.setHeaders(req)

	resp, err := api.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to execute POST request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("POST failed with status %d: %s", resp.StatusCode, string(body))
	}

	api.logger.Printf("Successfully created runner %s (%s)", runner.DisplayName, runner.Uuid)
	return nil
}

// setHeaders sets the common headers for API requests
func (api *RegistrationApiClient) setHeaders(req *http.Request) {
	auth := "Basic " + base64.StdEncoding.EncodeToString([]byte(api.username+":"+api.password))
	req.Header.Set("Authorization", auth)
	req.Header.Set("Content-Type", MeshBuildingBlockRunner_MediaType_V1Preview)
	req.Header.Set("Accept", MeshBuildingBlockRunner_MediaType_V1Preview)
}

// RegisterAllRunners registers all configured runners
func RegisterAllRunners(logger *log.Logger) error {
	if UseTestClient {
		logger.Println("Test mode enabled - skipping runner registration")
		return nil
	}

	api := NewRegistrationApi(logger)
	metrics := NewMetricsCollector()

	logger.Println("Registering configured runners...")
	var lastError error

	for i := range AppConfig.Runners {
		runner := &AppConfig.Runners[i]
		if err := api.RegisterRunner(runner, metrics); err != nil {
			logger.Printf("Warning: Failed to register runner %s: %v", runner.Uuid, err)
			lastError = err
			// Continue with other runners even if one fails
		}
	}

	if lastError != nil {
		return fmt.Errorf("one or more runners failed to register")
	}

	logger.Println("All runners registered successfully")
	return nil
}
