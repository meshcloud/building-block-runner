package gitlab

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/meshcloud/building-block-runner/internal/meshapi"
)

// gitlabTriggerPathFmt is the frozen customer-facing endpoint
// (GitLabClient.kt:52): POST {sanitizedBaseUrl}/api/v4/projects/{projectId}/trigger/pipeline.
const gitlabTriggerPathFmt = "%s/api/v4/projects/%s/trigger/pipeline"

// triggerPipeline POSTs the multipart trigger payload via the shared meshapi facade.
// WithNoRedirect is mandatory here: the multipart body carries the trigger token
// (buildTriggerForm), and a followed 3xx could silently retarget which server receives it.
// The trigger path is also not in the retry whitelist, so a transport failure or non-2xx
// is never retried -- no risk of double-triggering the pipeline.
//
// A 2xx response returns nil; a classified *ExternalCallError (via classifyGitlabError)
// is the caller's row-B path; any other error (transport failure, ...) is the caller's
// row-C ("internal error") path, matching Kotlin's catch-all around any non-MeshHttpException.
func triggerPipeline(ctx context.Context, httpClient *http.Client, log meshapi.Logger, baseURL, projectID string, body *bytes.Buffer, contentType string) error {
	url := fmt.Sprintf(gitlabTriggerPathFmt, baseURL, projectID)

	_, err := meshapi.DoRequest[json.RawMessage](ctx, httpClient, log, http.MethodPost, url,
		meshapi.WithBody(body, contentType), meshapi.WithNoRedirect())
	if err != nil {
		if he, ok := meshapi.AsHttpError(err); ok {
			return classifyGitlabError(he.StatusCode, url, he.ResponseBody)
		}
		return fmt.Errorf("triggering GitLab pipeline: %w", err)
	}
	return nil
}
