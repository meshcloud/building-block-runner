package meshapi

import (
	"bytes"
	"context"
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

// Option configures a RunClient (identity + logger). The retry transport is always
// applied by the constructors with the run-endpoint default policy (§5.2.3).
type Option func(*RunClient)

// WithIdentity sets the runner identity stamped on the User-Agent and
// X-Meshcloud-Runner-* headers. Without it, the zero Identity's defaults
// ("unknown-runner"/"dev") are used — byte-identical to the former un-set globals.
func WithIdentity(id Identity) Option {
	return func(c *RunClient) { c.id = id }
}

// WithLogger sets the request/response wire logger (§5.2.6). A nil logger is ignored
// (the noop default stays), so callers need not guard.
func WithLogger(l Logger) Option {
	return func(c *RunClient) {
		if l != nil {
			c.log = l
		}
	}
}

// RunClient is the runner-facing run-endpoint client (claim, register-source, status
// PATCH, artifact download), all under media type BlockRunMediaTypeV1. It is one half of
// the client split (§5.2.4); the runner meshObject registration PUT lives in RunnerClient.
//
// Client is kept as an alias for source compatibility during the phase-3 migration.
type RunClient struct {
	baseURL string
	nodeID  string
	auth    AuthProvider
	id      Identity
	log     Logger
	http    *http.Client
}

// Client is the pre-split name for the run-endpoint client; kept as an alias so existing
// call sites keep compiling while the codebase migrates to RunClient/RunnerClient.
type Client = RunClient

// NewRunClient creates a run-endpoint client using a default (retry-wrapped) http.Client.
// nodeID is sent as the X-Block-Runner-Node-Id header on every request.
func NewRunClient(baseURL, nodeID string, auth AuthProvider, opts ...Option) *RunClient {
	return newRunClient(baseURL, nodeID, auth, &http.Client{}, opts...)
}

// NewRunClientWithHTTP creates a run-endpoint client over a caller-supplied http.Client
// (useful for tests with custom transports or a shared connection pool). The supplied
// client's transport is wrapped with the retry transport without mutating the caller's
// client.
func NewRunClientWithHTTP(baseURL, nodeID string, auth AuthProvider, httpClient *http.Client, opts ...Option) *RunClient {
	return newRunClient(baseURL, nodeID, auth, httpClient, opts...)
}

// NewClient is the pre-split constructor name; delegates to NewRunClient.
func NewClient(baseURL, nodeID string, auth AuthProvider, opts ...Option) *RunClient {
	return NewRunClient(baseURL, nodeID, auth, opts...)
}

// NewClientWithHTTP is the pre-split constructor name; delegates to NewRunClientWithHTTP.
func NewClientWithHTTP(baseURL, nodeID string, auth AuthProvider, httpClient *http.Client, opts ...Option) *RunClient {
	return NewRunClientWithHTTP(baseURL, nodeID, auth, httpClient, opts...)
}

func newRunClient(baseURL, nodeID string, auth AuthProvider, httpClient *http.Client, opts ...Option) *RunClient {
	c := &RunClient{
		baseURL: baseURL,
		nodeID:  nodeID,
		auth:    auth,
		log:     noopLogger{},
	}
	for _, o := range opts {
		o(c)
	}

	// Wrap the (default or supplied) transport with retry without mutating the caller's
	// http.Client: copy the struct by value and replace only its Transport.
	wrapped := *httpClient
	wrapped.Transport = newRetryTransport(httpClient.Transport, defaultRunRetryOptions(), c.log)
	c.http = &wrapped

	return c
}

// FetchRun claims a pending building block run for the given runner UUID via POST.
// Returns the parsed DTO and the raw JSON bytes (the latter is useful for forwarding the
// run to a downstream executor without re-serialising). A non-2xx status yields an
// HttpError whose IsNotFound/IsConflict are the frozen "no run available" signals (D9).
// The claim POST is deliberately never retried (§5.2.3).
func (c *RunClient) FetchRun(runnerUUID string) (*RunDetailsDTO, []byte, error) {
	url := fmt.Sprintf(EPGetRun+"/create?forRunnerUuid=%s", c.baseURL, runnerUUID)

	req, err := http.NewRequest(http.MethodPost, url, nil)
	if err != nil {
		return nil, nil, err
	}
	c.setHeaders(req)
	c.logRequest(req, nil)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, nil, c.httpError(req, resp)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, err
	}
	c.logResponse(req, resp.StatusCode, data)

	dto, err := ParseRunDetails(data)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse run JSON: %w", err)
	}
	if dto.Metadata.Uuid == "" {
		return nil, nil, fmt.Errorf("fetched run has no UUID")
	}

	return dto, data, nil
}

// DownloadArtifact performs an authenticated GET and streams the response body into w
// rather than buffering it, since artifacts like terraform plans can be large. A non-2xx
// response is returned as an HttpError (wrapped with the URL for context) rather than
// treated as empty, so a missing/expired artifact fails the run visibly. The streamed
// body is never routed through the wire-body logger (§5.2.6), so DEBUG logging cannot
// exhaust memory on a large artifact.
func (c *RunClient) DownloadArtifact(artifactURL string, w io.Writer) error {
	req, err := http.NewRequest(http.MethodGet, artifactURL, nil)
	if err != nil {
		return fmt.Errorf("download artifact %s: failed to create request: %w", artifactURL, err)
	}
	// Reuse the standard auth/runner headers, then request raw bytes instead of HAL+JSON.
	c.setHeaders(req)
	req.Header.Set("Accept", "application/octet-stream")
	req.Header.Del("Content-Type") // no request body
	c.logRequest(req, nil)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("download artifact %s: request failed: %w", artifactURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("download artifact %s: %w", artifactURL, c.httpError(req, resp))
	}
	// Metadata only for the artifact stream — never the body.
	c.log.Debug(req.Context(), "meshapi artifact response",
		"status", resp.StatusCode, "contentLength", resp.ContentLength)

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
// This POST is whitelisted for retry (§5.2.3) — safe because a 409-on-replay is success.
func (c *RunClient) RegisterSource(runID string, registration RegistrationDTO) error {
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
	c.logRequest(req, body)

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
		return c.httpError(req, resp)
	}

	return nil
}

// PatchStatus sends a status update (PATCH) for a run to the given sourceID endpoint.
// payload is JSON-marshalled and sent as the request body. The raw response body is
// returned so callers can parse response fields (e.g. runAborted). The PATCH is
// deliberately never retried (§5.2.3): the observer re-sends status on its own cadence.
func (c *RunClient) PatchStatus(runID, sourceID string, payload any) ([]byte, error) {
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
	c.logRequest(req, body)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, c.httpError(req, resp)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}
	c.logResponse(req, resp.StatusCode, data)

	return data, nil
}

func (c *RunClient) setHeaders(req *http.Request) {
	req.Header.Set("Authorization", c.auth.AuthHeader())
	req.Header.Set("Accept", BlockRunMediaTypeV1)
	req.Header.Set("Content-Type", BlockRunMediaTypeV1)
	req.Header.Set("X-Block-Runner-Node-Id", c.nodeID)
	req.Header.Set("User-Agent", c.id.UserAgent())
	req.Header.Set("X-Meshcloud-Runner-Name", c.id.name())
	req.Header.Set("X-Meshcloud-Runner-Version", c.id.version())
}

// httpError reads the (capped) error body and builds an HttpError, logging the response.
func (c *RunClient) httpError(req *http.Request, resp *http.Response) error {
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
	c.logResponse(req, resp.StatusCode, respBody)
	return HttpError{StatusCode: resp.StatusCode, ResponseBody: respBody}
}

func (c *RunClient) logRequest(req *http.Request, body []byte) {
	c.log.Debug(reqCtx(req), "meshapi request",
		"method", req.Method, "url", req.URL.String(),
		"headers", loggedHeaders(req.Header), "body", loggedBody(body))
}

func (c *RunClient) logResponse(req *http.Request, status int, body []byte) {
	c.log.Debug(reqCtx(req), "meshapi response", "status", status, "body", loggedBody(body))
}

func reqCtx(req *http.Request) context.Context {
	if ctx := req.Context(); ctx != nil {
		return ctx
	}
	return context.Background()
}
