package controller

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	meshapi "github.com/meshcloud/building-block-runner/internal/meshapi"
)

// RegistrationApi interface for runner registration.
type RegistrationApi interface {
	RegisterController(metrics *MetricsCollector) error
}

// RegistrationApiClient implements the RegistrationApi. The wire mechanics of the PUT
// (URL, v1-preview media type, headers, transport) live in meshapi.RunnerClient
// (PLAN_DETAIL_03_shared_core.md §5.5/§5.2.4); this type keeps the controller-specific
// content: DTO construction (dtos.go), the 404 => "create it via the meshStack UI"
// mapping, and registration metrics/logging.
type RegistrationApiClient struct {
	namespace    string
	oidcIssuer   string
	runnerClient *meshapi.RunnerClient
	logger       *log.Logger
}

// NewRegistrationApi creates a new registration API client.
func NewRegistrationApi(logger *log.Logger) RegistrationApi {
	return &RegistrationApiClient{
		namespace:    AppConfig.Namespace,
		oidcIssuer:   DiscoveredOidcIssuer,
		runnerClient: meshapi.NewRunnerClient(AppConfig.Api.Url, AppConfig.Api.NewAuthProvider(AppConfig.Api.Url)),
		logger:       logger,
	}
}

// RegisterController updates the universal run controller registration via PUT. Must already exist.
func (api *RegistrationApiClient) RegisterController(metrics *MetricsCollector) error {
	dto := BuildRunnerRegistrationDTO(api.namespace, api.oidcIssuer)

	jsonBody, err := json.Marshal(dto)
	if err != nil {
		metrics.runnerRegistrationErrors.WithLabelValues(AppConfig.Uuid, ErrorTypeRegistrationMarshal).Inc()
		return fmt.Errorf("failed to marshal runner registration: %w", err)
	}

	api.logger.Printf("Updating controller registration %s", AppConfig.Uuid)
	statusCode, err := api.runnerClient.Update(AppConfig.Uuid, jsonBody)
	if err != nil {
		metrics.runnerRegistrationErrors.WithLabelValues(AppConfig.Uuid, ErrorTypeRegistrationPut).Inc()
		return err
	}

	if statusCode == http.StatusNotFound {
		return fmt.Errorf("controller %s not found in meshfed — create it via the meshStack UI or API before starting the run-controller", AppConfig.Uuid)
	}

	metrics.runnerRegistrationSuccess.WithLabelValues(AppConfig.Uuid).Inc()
	api.logger.Printf("Successfully updated controller registration %s", AppConfig.Uuid)
	return nil
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
