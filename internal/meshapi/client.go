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
// applied by the constructors with the run-endpoint default policy.
type Option func(*RunClient)

// WithIdentity sets the runner identity stamped on the User-Agent and
// X-Meshcloud-Runner-* headers. Without it, the zero Identity's defaults
// ("unknown-runner"/"dev") are used — byte-identical to the former un-set globals.
func WithIdentity(id Identity) Option {
	return func(c *RunClient) { c.id = id }
}

// WithLogger sets the request/response wire logger. A nil logger is ignored
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
// the client split; the runner meshObject registration PUT lives in RunnerClient.
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

// NewRunClient creates a run-endpoint client over the process-wide sharedHTTPClient (retry
// is a transport-level concern of that singleton, not of this client). nodeID is sent as
// the X-Block-Runner-Node-Id header on every request.
func NewRunClient(baseURL, nodeID string, auth AuthProvider, opts ...Option) *RunClient {
	return newRunClient(baseURL, nodeID, auth, opts...)
}

// NewRunClientWithHTTP creates a run-endpoint client over a caller-supplied http.Client,
// used as-is (no retry wrapping) — this constructor exists for tests that need to inject
// their own transport.
func NewRunClientWithHTTP(baseURL, nodeID string, auth AuthProvider, httpClient *http.Client, opts ...Option) *RunClient {
	c := &RunClient{
		baseURL: baseURL,
		nodeID:  nodeID,
		auth:    auth,
		log:     noopLogger{},
		http:    httpClient,
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// NewClient is the pre-split constructor name; delegates to NewRunClient.
func NewClient(baseURL, nodeID string, auth AuthProvider, opts ...Option) *RunClient {
	return NewRunClient(baseURL, nodeID, auth, opts...)
}

// NewClientWithHTTP is the pre-split constructor name; delegates to NewRunClientWithHTTP.
func NewClientWithHTTP(baseURL, nodeID string, auth AuthProvider, httpClient *http.Client, opts ...Option) *RunClient {
	return NewRunClientWithHTTP(baseURL, nodeID, auth, httpClient, opts...)
}

func newRunClient(baseURL, nodeID string, auth AuthProvider, opts ...Option) *RunClient {
	c := &RunClient{
		baseURL: baseURL,
		nodeID:  nodeID,
		auth:    auth,
		log:     noopLogger{},
		http:    sharedHTTPClient,
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// runIdentityOptions returns the run-identity headers stamped on every request: the
// runner-node id and the User-Agent/X-Meshcloud-Runner-* triple derived from c.id.
func (c *RunClient) runIdentityOptions() []RequestOption {
	return []RequestOption{
		WithHeader("X-Block-Runner-Node-Id", c.nodeID),
		WithHeader("User-Agent", c.id.UserAgent()),
		WithHeader("X-Meshcloud-Runner-Name", c.id.name()),
		WithHeader("X-Meshcloud-Runner-Version", c.id.version()),
	}
}

// FetchRun claims a pending building block run for the given runner UUID via POST.
// Returns the parsed DTO and the raw JSON bytes (the latter is useful for forwarding the
// run to a downstream executor without re-serialising). A non-2xx status yields an
// HttpError whose IsNotFound/IsConflict are the frozen "no run available" signals.
// The claim POST is deliberately never retried.
func (c *RunClient) FetchRun(runnerUUID string) (*Run, []byte, error) {
	ctx := context.Background()
	url := fmt.Sprintf(EPGetRun+"/create?forRunnerUuid=%s", c.baseURL, runnerUUID)

	raw, err := DoAuthorizedRequest[json.RawMessage](ctx, c.http, c.log, authorizationOf(c.auth), http.MethodPost, url,
		append(c.runIdentityOptions(), WithHeader("Accept", BlockRunMediaTypeV1), WithHeader("Content-Type", BlockRunMediaTypeV1))...)
	if err != nil {
		return nil, nil, err
	}

	dto, err := ParseRunDetails(raw)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse run JSON: %w", err)
	}
	if dto.Metadata.Uuid == "" {
		return nil, nil, fmt.Errorf("fetched run has no UUID")
	}

	return dto, []byte(raw), nil
}

// DownloadArtifact performs an authenticated GET and streams the response body into w
// rather than buffering it, since artifacts like terraform plans can be large. A non-2xx
// response is returned as an HttpError (wrapped with the URL for context) rather than
// treated as empty, so a missing/expired artifact fails the run visibly. The streamed
// body is never routed through the wire-body logger, so DEBUG logging cannot
// exhaust memory on a large artifact.
func (c *RunClient) DownloadArtifact(artifactURL string, w io.Writer) error {
	ctx := context.Background()

	_, err := DoAuthorizedRequest[json.RawMessage](ctx, c.http, c.log, authorizationOf(c.auth), http.MethodGet, artifactURL,
		append(c.runIdentityOptions(), WithHeader("Accept", "application/octet-stream"), WithResponseSink(w, maxArtifactBytes+1))...)
	if err != nil {
		return fmt.Errorf("download artifact %s: %w", artifactURL, err)
	}

	return nil
}

// RegisterSource registers the caller as a status source for the given run via POST.
// If the source is already registered (HTTP 409 Conflict) the call is treated as a no-op.
// This POST is whitelisted for retry — safe because a 409-on-replay is success.
func (c *RunClient) RegisterSource(runID string, registration RegistrationDTO) error {
	ctx := context.Background()
	url := fmt.Sprintf(EPRunSourceRegistration, c.baseURL, runID)

	body, err := json.Marshal(registration)
	if err != nil {
		return fmt.Errorf("failed to marshal registration: %w", err)
	}

	_, err = DoAuthorizedRequest[json.RawMessage](ctx, c.http, c.log, authorizationOf(c.auth), http.MethodPost, url,
		append(c.runIdentityOptions(), WithHeader("Accept", BlockRunMediaTypeV1), WithBody(bytes.NewReader(body), BlockRunMediaTypeV1))...)
	if err != nil {
		// 409 Conflict means the source is already registered — treat as success.
		if he, ok := AsHttpError(err); ok && he.IsConflict() {
			return nil
		}
		return err
	}

	return nil
}

// PatchStatus sends a status update (PATCH) for a run to the given sourceID endpoint.
// payload is JSON-marshalled and sent as the request body. The raw response body is
// returned so callers can parse response fields (e.g. runAborted). The PATCH is
// deliberately never retried: the observer re-sends status on its own cadence.
func (c *RunClient) PatchStatus(runID, sourceID string, payload any) ([]byte, error) {
	ctx := context.Background()
	url := fmt.Sprintf(EPRunSourceUpdate, c.baseURL, runID, sourceID)

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal status payload: %w", err)
	}

	raw, err := DoAuthorizedRequest[json.RawMessage](ctx, c.http, c.log, authorizationOf(c.auth), http.MethodPatch, url,
		append(c.runIdentityOptions(), WithHeader("Accept", BlockRunMediaTypeV1), WithBody(bytes.NewReader(body), BlockRunMediaTypeV1))...)
	return raw, err
}
