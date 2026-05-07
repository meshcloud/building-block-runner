package meshapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

const (
	EPGetRun                = "%s/api/meshobjects/meshbuildingblockruns"
	EPRunSourceRegistration = "%s/api/meshobjects/meshbuildingblockruns/%s/status/source"
	EPRunSourceUpdate       = "%s/api/meshobjects/meshbuildingblockruns/%s/status/source/%s"

	BlockRunMediaTypeV1 = "application/vnd.meshcloud.api.meshbuildingblockrun.v1.hal+json"
)

var (
	runnerName    = "unknown-runner"
	runnerVersion = "dev"
	runnerCommit  = "unknown"
)

// SetClientMetadata configures the runner identity headers sent on all requests.
func SetClientMetadata(name, version, commit string) {
	if name != "" {
		runnerName = name
	}
	if version != "" {
		runnerVersion = version
	}
	if commit != "" {
		runnerCommit = commit
	}
}

func userAgent() string {
	if runnerCommit == "" || runnerCommit == "unknown" {
		return fmt.Sprintf("meshcloud-%s/%s", runnerName, runnerVersion)
	}

	return fmt.Sprintf("meshcloud-%s/%s (%s)", runnerName, runnerVersion, runnerCommit)
}

// StatusError is returned when the meshfed API responds with an unexpected HTTP status code.
type StatusError struct {
	Status int
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("unexpected HTTP status: %d", e.Status)
}

// Client is a shared HTTP client for interacting with the meshfed building block run API.
// It handles authentication, required headers, and endpoint construction.
type Client struct {
	baseURL string
	nodeID  string
	auth    AuthProvider
	http    *http.Client
}

// NewClient creates a new meshfed API client.
// nodeID is sent as the X-Block-Runner-Node-Id header on every request.
func NewClient(baseURL, nodeID string, auth AuthProvider) *Client {
	return &Client{
		baseURL: baseURL,
		nodeID:  nodeID,
		auth:    auth,
		http:    &http.Client{},
	}
}

// NewClientWithHTTP creates a new meshfed API client with a custom http.Client.
// This is useful for testing with custom transports or timeouts.
func NewClientWithHTTP(baseURL, nodeID string, auth AuthProvider, httpClient *http.Client) *Client {
	return &Client{
		baseURL: baseURL,
		nodeID:  nodeID,
		auth:    auth,
		http:    httpClient,
	}
}

// FetchRun fetches a pending building block run for the given runner UUID via POST.
// Returns the parsed DTO and the raw JSON bytes (the latter is useful for forwarding
// the run to a downstream executor without re-serialising).
func (c *Client) FetchRun(runnerUUID string) (*RunDetailsDTO, []byte, error) {
	url := fmt.Sprintf(EPGetRun+"/create?forRunnerUuid=%s", c.baseURL, runnerUUID)

	req, err := http.NewRequest(http.MethodPost, url, nil)
	if err != nil {
		return nil, nil, err
	}
	c.setHeaders(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, nil, &StatusError{Status: resp.StatusCode}
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, err
	}

	dto, err := ParseRunDetails(data)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse run JSON: %w", err)
	}
	if dto.Metadata.Uuid == "" {
		return nil, nil, fmt.Errorf("fetched run has no UUID")
	}

	return dto, data, nil
}

// RegisterSource registers the caller as a status source for the given run via POST.
// If the source is already registered (HTTP 409 Conflict) the call is treated as a no-op.
func (c *Client) RegisterSource(runID string, registration RegistrationDTO) error {
	url := fmt.Sprintf(EPRunSourceRegistration, c.baseURL, runID)

	body, err := json.Marshal(registration)
	if err != nil {
		return fmt.Errorf("failed to marshal registration: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewBuffer(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	c.setHeaders(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	// 409 Conflict means the source is already registered — treat as success.
	if resp.StatusCode == http.StatusConflict {
		return nil
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("register source returned HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// PatchStatus sends a status update (PATCH) for a run to the given sourceID endpoint.
// payload is JSON-marshalled and sent as the request body.
// The raw response body is returned so callers can parse response fields (e.g. runAborted).
func (c *Client) PatchStatus(runID, sourceID string, payload interface{}) ([]byte, error) {
	url := fmt.Sprintf(EPRunSourceUpdate, c.baseURL, runID, sourceID)

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal status payload: %w", err)
	}

	req, err := http.NewRequest(http.MethodPatch, url, bytes.NewBuffer(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	c.setHeaders(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("patch status returned HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	return data, nil
}

func (c *Client) setHeaders(req *http.Request) {
	req.Header.Set("Authorization", c.auth.AuthHeader())
	req.Header.Set("Accept", BlockRunMediaTypeV1)
	req.Header.Set("Content-Type", BlockRunMediaTypeV1)
	req.Header.Set("X-Block-Runner-Node-Id", c.nodeID)
	req.Header.Set("User-Agent", userAgent())
	req.Header.Set("X-Meshcloud-Runner-Name", runnerName)
	req.Header.Set("X-Meshcloud-Runner-Version", runnerVersion)
	req.Header.Set("X-Meshcloud-Runner-Commit", runnerCommit)
}
