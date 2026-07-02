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

	// maxArtifactBytes caps a streamed artifact download (e.g. a saved terraform plan) so a
	// misbehaving or compromised server cannot exhaust the runner's disk.
	maxArtifactBytes = 128 << 20 // 128 MiB
	// maxErrorBodyBytes caps how much of a non-2xx response body is read into memory for the
	// error message, so a huge error response cannot exhaust the runner's RAM.
	maxErrorBodyBytes = 16 << 20 // 16 MiB
)

var (
	runnerName    = "unknown-runner"
	runnerVersion = "dev"
)

// SetClientMetadata configures the runner identity headers sent on all requests.
// The version parameter should include commit information if available (e.g., "1.0.0-abc123").
func SetClientMetadata(name, version string) {
	if name != "" {
		runnerName = name
	}
	if version != "" {
		runnerVersion = version
	}
}

func userAgent() string {
	return fmt.Sprintf("meshcloud-%s/%s", runnerName, runnerVersion)
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

// DownloadArtifact performs an authenticated GET and streams the response body into w rather than
// buffering it, since artifacts like terraform plans can be large. A non-2xx response is returned
// as an error rather than treated as empty, so a missing/expired artifact fails the run visibly.
func (c *Client) DownloadArtifact(artifactURL string, w io.Writer) error {
	req, err := http.NewRequest(http.MethodGet, artifactURL, nil)
	if err != nil {
		return fmt.Errorf("download artifact %s: failed to create request: %w", artifactURL, err)
	}
	// Reuse the standard auth/runner headers, then request raw bytes instead of HAL+JSON.
	c.setHeaders(req)
	req.Header.Set("Accept", "application/octet-stream")
	req.Header.Del("Content-Type") // no request body

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("download artifact %s: request failed: %w", artifactURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
		if readErr != nil {
			return fmt.Errorf("download artifact %s returned HTTP %d (failed to read error body: %v)", artifactURL, resp.StatusCode, readErr)
		}
		return fmt.Errorf("download artifact %s returned HTTP %d: %s", artifactURL, resp.StatusCode, string(respBody))
	}

	// Bound the streamed copy so an unexpectedly huge response cannot fill the disk. We read one
	// byte past the limit so an oversized artifact is rejected rather than silently truncated.
	n, err := io.Copy(w, io.LimitReader(resp.Body, maxArtifactBytes+1))
	if err != nil {
		return fmt.Errorf("download artifact %s: failed to read body: %w", artifactURL, err)
	}
	if n > maxArtifactBytes {
		return fmt.Errorf("download artifact %s: artifact exceeds the maximum allowed size of %d bytes", artifactURL, maxArtifactBytes)
	}

	return nil
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
}
