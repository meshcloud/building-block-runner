package controller

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"

	meshapi "github.com/meshcloud/building-block-runner/go-meshapi-client/meshapi"
)

const (
	EP_RunnerWithUuid = "%s/api/meshobjects/meshbuildingblockrunners/%s"
	EP_Runners        = "%s/api/meshobjects/meshbuildingblockrunners"

	MeshBuildingBlockRunner_MediaType_V1Preview = "application/vnd.meshcloud.api.meshbuildingblockrunner.v1-preview.hal+json"
)

// RegistrationApi interface for runner registration
type RegistrationApi interface {
	RegisterController(metrics *MetricsCollector) error
}

// RegistrationApiClient implements the RegistrationApi
type RegistrationApiClient struct {
	url        string
	namespace  string
	auth       meshapi.AuthProvider
	oidcIssuer string
	client     *http.Client
	logger     *log.Logger
}

// NewRegistrationApi creates a new registration API client
func NewRegistrationApi(logger *log.Logger) RegistrationApi {
	return &RegistrationApiClient{
		url:        AppConfig.Api.Url,
		namespace:  AppConfig.Namespace,
		auth:       AppConfig.Api.NewAuthProvider(AppConfig.Api.Url),
		oidcIssuer: DiscoveredOidcIssuer,
		client:     &http.Client{},
		logger:     logger,
	}
}

// RegisterController registers or creates the universal run controller via the meshObject API.
// It first tries PUT (update), and if the controller doesn't exist (404), falls back to POST (create).
func (api *RegistrationApiClient) RegisterController(metrics *MetricsCollector) error {
	// Build the registration DTO with auto-constructed WIF
	dto := BuildRunnerRegistrationDTO(api.namespace, api.oidcIssuer)

	// Marshal to JSON
	jsonBody, err := json.Marshal(dto)
	if err != nil {
		metrics.runnerRegistrationErrors.WithLabelValues(AppConfig.Uuid, ErrorTypeRegistrationMarshal).Inc()
		return fmt.Errorf("failed to marshal runner registration: %w", err)
	}

	// Try PUT first (update existing controller registration)
	statusCode, err := api.putController(jsonBody)
	if err != nil {
		metrics.runnerRegistrationErrors.WithLabelValues(AppConfig.Uuid, ErrorTypeRegistrationPut).Inc()
		return err
	}

	// If controller doesn't exist, create it with POST
	if statusCode == http.StatusNotFound {
		api.logger.Printf("Controller %s not found, creating new registration", AppConfig.Uuid)
		err = api.postController(jsonBody)
		if err != nil {
			metrics.runnerRegistrationErrors.WithLabelValues(AppConfig.Uuid, ErrorTypeRegistrationPost).Inc()
			return err
		}
		metrics.runnerRegistrationSuccess.WithLabelValues(AppConfig.Uuid).Inc()
		return nil
	}

	metrics.runnerRegistrationSuccess.WithLabelValues(AppConfig.Uuid).Inc()
	api.logger.Printf("Successfully updated controller registration %s", AppConfig.Uuid)
	return nil
}

// putController attempts to update the existing controller registration via PUT
func (api *RegistrationApiClient) putController(jsonBody []byte) (int, error) {
	url := fmt.Sprintf(EP_RunnerWithUuid, api.url, AppConfig.Uuid)

	api.logger.Printf("Updating controller registration %s at %s", AppConfig.Uuid, url)

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

	// 404 means controller doesn't exist - caller should try POST
	if resp.StatusCode == http.StatusNotFound {
		return http.StatusNotFound, nil
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, fmt.Errorf("PUT failed with status %d: %s", resp.StatusCode, string(body))
	}

	return resp.StatusCode, nil
}

// postController creates a new controller registration via POST
func (api *RegistrationApiClient) postController(jsonBody []byte) error {
	url := fmt.Sprintf(EP_Runners, api.url)

	api.logger.Printf("Creating controller registration %s at %s", AppConfig.Uuid, url)

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

	api.logger.Printf("Successfully created controller registration %s", AppConfig.Uuid)
	return nil
}

// setHeaders sets the common headers for API requests
func (api *RegistrationApiClient) setHeaders(req *http.Request) {
	req.Header.Set("Authorization", api.auth.AuthHeader())
	req.Header.Set("Content-Type", MeshBuildingBlockRunner_MediaType_V1Preview)
	req.Header.Set("Accept", MeshBuildingBlockRunner_MediaType_V1Preview)
}

// RegisterController registers the universal run controller on startup.
func RegisterController(logger *log.Logger) error {
	if UseTestClient {
		logger.Println("Test mode enabled - skipping controller registration")
		return nil
	}

	api := NewRegistrationApi(logger)
	metrics := NewMetricsCollector()

	logger.Printf("Registering controller %s with implementationType ALL...", AppConfig.Uuid)
	if err := api.RegisterController(metrics); err != nil {
		return fmt.Errorf("controller registration failed: %w", err)
	}

	logger.Println("Controller registered successfully")
	return nil
}
