package gitlab

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
)

// gitlabTriggerPathFmt is the frozen customer-facing endpoint (GitLabClient.kt:52,
// umbrella §8): POST {sanitizedBaseUrl}/api/v4/projects/{projectId}/trigger/pipeline.
const gitlabTriggerPathFmt = "%s/api/v4/projects/%s/trigger/pipeline"

// triggerPipeline POSTs the multipart trigger payload. The caller's *http.Client is
// expected to have redirects disabled at construction (CheckRedirect returning
// http.ErrUseLastResponse, G-P10/§2.2.1 parity) -- this function does not itself
// configure that, so a 3xx response is read as any other non-2xx status and classified
// accordingly (a redirect followed instead would silently change which server receives
// the pipeline-trigger token).
//
// A 2xx response returns nil; any other status is classified via classifyGitlabError
// into an *ExternalCallError (errors.As-selectable by the caller, §2.3). A transport-level
// failure (DNS, connection refused, ...) returns a plain wrapped error -- the caller's row-C
// ("internal error") path, matching Kotlin's catch-all around any non-MeshHttpException.
func triggerPipeline(ctx context.Context, httpClient *http.Client, baseURL, projectID string, body *bytes.Buffer, contentType string) error {
	url := fmt.Sprintf(gitlabTriggerPathFmt, baseURL, projectID)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return fmt.Errorf("building GitLab trigger request: %w", err)
	}
	req.Header.Set("Content-Type", contentType)

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("triggering GitLab pipeline: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading GitLab trigger response: %w", err)
	}

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return classifyGitlabError(resp.StatusCode, url, respBody)
}

// noFollowRedirectClient returns an *http.Client with redirects disabled
// (followRedirects(false), GitLabClient.kt:35-38) -- constructed once, in the persona's
// wiring (cmd/gitlab), and injected as the handler's external-API HTTP seam (fakeable in
// tests via httptest, umbrella §5.3).
func noFollowRedirectClient() *http.Client {
	return &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}
