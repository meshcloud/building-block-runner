package tfrun

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
)

type StatusError struct {
	status int
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("Unexpected HTTP status: %d", e.status)
}

type RunApiClient struct {
	rid      string
	url      string
	auth     string  // Basic auth header or empty
	runToken *string // Bearer token from run spec
	client   *http.Client
}

const (
	EP_GetRun       = "%s/api/meshobjects/meshbuildingblockruns"
	EP_Registration = "%s/api/meshobjects/meshbuildingblockruns/%s/status/source"
	EP_Update       = "%s/api/meshobjects/meshbuildingblockruns/%s/status/source/%s"
	EP_State        = "%s/api/terraform/state/workspace/%s/buildingBlock/%s"

	BlockRun_Type_V1 = "application/vnd.meshcloud.api.meshbuildingblockrun.v1.hal+json"
)

// getRunEndpoint returns the endpoint URL using the runner UUID
func getRunEndpoint(baseUrl, requester string) string {
	return fmt.Sprintf(EP_GetRun+"/create?forRunnerUuid=%s", baseUrl, AppConfig.RunnerUuid)
}

type RunApi interface {
	FetchRunDetails(nodePostfix string) (*Run, error)
	UpdateState(status *RunStatus) (bool, error)
	Register(status *RunStatus) error
	SetRunToken(token string) // Set the runToken from the fetched run
	ClearRunToken()           // Clear the runToken to force basic auth for next fetch
}

func NewRunApi() RunApi {
	var authHeader string
	// Only set basic auth if credentials are configured
	if AppConfig.RunApiBackend.User != "" && AppConfig.RunApiBackend.Password != "" {
		authHeader = AppConfig.RunApiBackend.basicAuthHeader()
	}

	return &RunApiClient{
		rid:      AppConfig.RunnerUuid,
		url:      AppConfig.RunApiBackend.Url,
		auth:     authHeader,
		runToken: nil,
		client:   &http.Client{},
	}
}

// SetRunToken sets the runToken from the fetched run for subsequent API calls
func (api *RunApiClient) SetRunToken(token string) {
	api.runToken = &token
}

// ClearRunToken clears the runToken to force basic auth for the next fetch.
// This should be called after a run completes to ensure the next FetchRunDetails
// uses basic auth instead of the previous run's token.
func (api *RunApiClient) ClearRunToken() {
	api.runToken = nil
}

// getAuthHeader returns the appropriate authorization header
// Priority: Run Token (from fetched run) > Basic Auth (fallback)
// The runToken is always present in fetched runs and should be used for all
// operations after the initial fetch. Basic auth is only used as a fallback.
func (api *RunApiClient) getAuthHeader() (string, error) {
	// Priority 1: Run Token from spec (if available) - always use if present
	if api.runToken != nil && *api.runToken != "" {
		log.Printf("[AUTH] Using Bearer token for run-specific operations")
		return fmt.Sprintf("Bearer %s", *api.runToken), nil
	}

	// Priority 2: Basic Auth (if configured) - fallback
	if api.auth != "" {
		log.Printf("[AUTH] Using Basic auth for API requests")
		return api.auth, nil
	}

	// No authentication available
	return "", fmt.Errorf("no authentication method available: configure basic auth or ensure runToken is present in run spec")
}

// doRequestWithAuth performs an HTTP request with automatic token refresh on 403 responses
func (api *RunApiClient) doRequestWithAuth(req *http.Request) (*http.Response, error) {
	authHeader, err := api.getAuthHeader()
	if err != nil {
		return nil, err
	}

	// Set the authorization header
	req.Header.Set("Authorization", authHeader)

	// Perform the request
	resp, err := api.client.Do(req)
	if err != nil {
		log.Printf("[API] Request failed with error: %v for %s %s", err, req.Method, req.URL.Path)
		return nil, err
	}

	log.Printf("[API] %d %s %s", resp.StatusCode, req.Method, req.URL.Path)

	return resp, nil
}

func (api *RunApiClient) FetchRunDetails(nodePostfix string) (*Run, error) {
	requester := fmt.Sprintf("%s-%s", api.rid, nodePostfix)
	url := getRunEndpoint(api.url, requester)

	req, err := http.NewRequest(http.MethodPost, url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Add("Accept", BlockRun_Type_V1)
	req.Header.Add("Content-Type", BlockRun_Type_V1)
	req.Header.Add("X-Block-Runner-Node-Id", requester)

	resp, err := api.doRequestWithAuth(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 && resp.StatusCode != 201 {
		return nil, &StatusError{resp.StatusCode}
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var details RunDetailsDTO
	err = json.Unmarshal(data, &details)
	if err != nil {
		return nil, err
	}

	// Extract and store the runToken from the spec for subsequent API calls
	// The runToken is always present in fetched runs and will be used with priority
	// for all Register and UpdateState calls
	api.SetRunToken(details.Spec.RunToken)

	return details.toInternal()
}

func (api *RunApiClient) Register(runStatus *RunStatus) error {
	url := fmt.Sprintf(EP_Registration, api.url, runStatus.RunId)

	steps := make([]StepsRegistrationDTO, 0)
	for _, s := range runStatus.Steps {
		steps = append(steps, StepsRegistrationDTO{
			Id:          s.Name,
			DisplayName: s.DisplayName,
			Status:      nil,
		})
	}

	dto := RegistrationDTO{
		Source: SourceRegistrationDTO{
			Id:          AppConfig.RunnerUuid,
			ExternalId:  nil,
			ExternalUrl: nil,
		},
		Steps: steps,
	}

	body, err := json.Marshal(dto)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewBuffer(body))
	if err != nil {
		return err
	}

	// The update endpoint still is on V1 regardless of custom predicates.
	req.Header.Add("Accept", BlockRun_Type_V1)
	req.Header.Add("Content-Type", BlockRun_Type_V1)
	req.Header.Add("X-Block-Runner-Node-Id", api.rid)

	resp, err := api.doRequestWithAuth(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// 409 Conflict means the source was already registered (e.g. a Kubernetes pod retry).
	// This is idempotent: the source exists and we can continue executing normally.
	if resp.StatusCode == http.StatusConflict {
		log.Printf("[RUNNER] Source already registered (409 Conflict) for run %s - continuing execution", runStatus.RunId)
		return nil
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &StatusError{resp.StatusCode}
	}

	_, err = io.ReadAll(resp.Body)
	return err

}

func (api *RunApiClient) UpdateState(status *RunStatus) (bool, error) {
	url := fmt.Sprintf(EP_Update, api.url, status.RunId, AppConfig.RunnerUuid)

	dto, err := status.toExternal()
	if err != nil {
		return false, err
	}

	body, err := json.Marshal(dto)
	if err != nil {
		return false, err
	}

	req, err := http.NewRequest(http.MethodPatch, url, bytes.NewBuffer(body))
	if err != nil {
		return false, err
	}

	// The update endpoint still is on V1 regardless of custom predicates.
	req.Header.Add("Accept", BlockRun_Type_V1)
	req.Header.Add("Content-Type", BlockRun_Type_V1)
	req.Header.Add("X-Block-Runner-Node-Id", api.rid)

	resp, err := api.doRequestWithAuth(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	// Check for HTTP error status codes
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, &StatusError{resp.StatusCode}
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, err
	}

	var runUpdateResponse RunUpdateResponseDTO
	err = json.Unmarshal(data, &runUpdateResponse)

	return runUpdateResponse.Abort, err
}
