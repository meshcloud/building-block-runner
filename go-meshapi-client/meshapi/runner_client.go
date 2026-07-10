package meshapi

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
)

const (
	// EPRunnerWithUuid is the meshObject endpoint for a single building block runner
	// registration (PUT to update; the object itself must already exist — the meshStack
	// API returns 404 otherwise, which callers map to an actionable message).
	EPRunnerWithUuid = "%s/api/meshobjects/meshbuildingblockrunners/%s"

	// RunnerMediaTypeV1Preview is the HAL+JSON media type for the runner meshObject,
	// distinct from BlockRunMediaTypeV1 used by the run-claim/status endpoints.
	RunnerMediaTypeV1Preview = "application/vnd.meshcloud.api.meshbuildingblockrunner.v1-preview.hal+json"
)

// RunnerClient talks to the meshBuildingBlockRunner meshObject endpoint (runner
// self-registration) — distinct from RunClient/Client, which serve the run-claim and
// status-reporting endpoints. Kept as its own type because the two resources use
// different media types and lifecycles (a runner registers itself once at startup; a run
// is claimed and reported on repeatedly).
type RunnerClient struct {
	baseURL string
	auth    AuthProvider
	http    *http.Client
}

// NewRunnerClient creates a new runner-registration client. Its transport is wrapped with
// the retry transport (the PUT is idempotent, so it retries by method, §5.2.3).
func NewRunnerClient(baseURL string, auth AuthProvider) *RunnerClient {
	return &RunnerClient{
		baseURL: baseURL,
		auth:    auth,
		http:    &http.Client{Transport: newRetryTransport(nil, defaultRunRetryOptions(), noopLogger{})},
	}
}

// NewRunnerClientWithHTTP creates a new runner-registration client with a custom
// http.Client. Useful for tests exercising custom transports. The supplied client's
// transport is wrapped with the retry transport without mutating the caller's client.
func NewRunnerClientWithHTTP(baseURL string, auth AuthProvider, httpClient *http.Client) *RunnerClient {
	wrapped := *httpClient
	wrapped.Transport = newRetryTransport(httpClient.Transport, defaultRunRetryOptions(), noopLogger{})
	return &RunnerClient{baseURL: baseURL, auth: auth, http: &wrapped}
}

// Update PUTs an already-marshalled runner registration body. This method owns only the
// wire mechanics (URL, media type, headers, transport); DTO construction and
// status-code interpretation are the caller's concern — in particular a 404 here means
// "the runner meshObject doesn't exist yet", which the controller maps to its own
// actionable "create it via the meshStack UI" message rather than treating it as a
// generic transport error. The returned status code lets the caller apply that mapping
// without parsing error strings.
func (c *RunnerClient) Update(uuid string, jsonBody []byte) (int, error) {
	url := fmt.Sprintf(EPRunnerWithUuid, c.baseURL, uuid)

	req, err := http.NewRequest(http.MethodPut, url, bytes.NewReader(jsonBody))
	if err != nil {
		return 0, fmt.Errorf("failed to create PUT request: %w", err)
	}
	c.setHeaders(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf("failed to execute PUT request: %w", err)
	}
	defer resp.Body.Close()

	// 404 means the runner meshObject doesn't exist yet - caller decides what that means.
	if resp.StatusCode == http.StatusNotFound {
		return http.StatusNotFound, nil
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, fmt.Errorf("PUT failed with status %d: %s", resp.StatusCode, string(body))
	}

	return resp.StatusCode, nil
}

func (c *RunnerClient) setHeaders(req *http.Request) {
	req.Header.Set("Authorization", c.auth.AuthHeader())
	req.Header.Set("Content-Type", RunnerMediaTypeV1Preview)
	req.Header.Set("Accept", RunnerMediaTypeV1Preview)
}
