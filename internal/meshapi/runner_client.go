package meshapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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

// NewRunnerClient creates a new runner-registration client over the process-wide
// sharedHTTPClient (retry is a transport-level concern of that singleton).
func NewRunnerClient(baseURL string, auth AuthProvider) *RunnerClient {
	return &RunnerClient{
		baseURL: baseURL,
		auth:    auth,
		http:    sharedHTTPClient,
	}
}

// NewRunnerClientWithHTTP creates a new runner-registration client with a caller-supplied
// http.Client, used as-is (no retry wrapping) — this constructor exists for tests that need
// to inject their own transport.
func NewRunnerClientWithHTTP(baseURL string, auth AuthProvider, httpClient *http.Client) *RunnerClient {
	return &RunnerClient{baseURL: baseURL, auth: auth, http: httpClient}
}

// Update PUTs an already-marshalled runner registration body. This method owns only the
// wire mechanics (URL, media type, headers, transport); DTO construction and
// status-code interpretation are the caller's concern — in particular a 404 here means
// "the runner meshObject doesn't exist yet", which the controller maps to its own
// actionable "create it via the meshStack UI" message rather than treating it as a
// generic transport error. The returned status code lets the caller apply that mapping
// without parsing error strings.
func (c *RunnerClient) Update(uuid string, jsonBody []byte) (int, error) {
	ctx := context.Background()
	url := fmt.Sprintf(EPRunnerWithUuid, c.baseURL, uuid)

	_, err := DoAuthorizedRequest[json.RawMessage](ctx, c.http, noopLogger{}, authorizationOf(c.auth), http.MethodPut, url,
		WithHeader("Accept", RunnerMediaTypeV1Preview), WithBody(bytes.NewReader(jsonBody), RunnerMediaTypeV1Preview))
	if err != nil {
		if he, ok := AsHttpError(err); ok {
			if he.IsNotFound() {
				// 404 means the runner meshObject doesn't exist yet - caller decides what that means.
				return http.StatusNotFound, nil
			}
			return he.StatusCode, err
		}
		return 0, err
	}

	return http.StatusOK, nil
}
