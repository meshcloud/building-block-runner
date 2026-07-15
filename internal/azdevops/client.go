package azdevops

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"time"

	"github.com/meshcloud/building-block-runner/internal/httpclient"
	"github.com/meshcloud/building-block-runner/internal/meshapi"
)

// adoHTTPTimeout mirrors OkHttp's default connect/read timeouts (~10s each,
// AzureDevOpsClient.kt:45-48 uses the OkHttpClient.Builder() defaults) -- Go's zero-value
// http.Client never times out, so a hung Azure DevOps endpoint would otherwise stall a
// "30-min" poll forever. Load-bearing for poll resilience, not itself a frozen
// contract value.
const adoHTTPTimeout = 10 * time.Second

// NewHTTPClient builds a standalone external-API HTTP client (redirects never followed, via
// the shared internal/httpclient.NoRedirectClient builder, and bounded by timeout end to
// end). Kept for cmd/azdevops + cmd/bbrunner/azdevops wiring and tests that want a
// non-retrying client; the default wiring (NewHandler's nil-HTTP fallback) instead reuses
// meshapi.SharedHTTPClient, the process-wide bound+retrying+redirect-policy client.
func NewHTTPClient(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = adoHTTPTimeout
	}
	return httpclient.NoRedirectClient(timeout)
}

// ExternalCallError is the Go twin of Kotlin's MeshHttpException: every non-2xx Azure DevOps
// response becomes one of these. Defined package-local (not in the shared meshapi package) --
// a per-package typed error with the same fields, since the messages built from it are
// per-runner pins, not a shared wire contract.
type ExternalCallError struct {
	UserMessage  string
	StatusCode   int
	RequestUrl   string
	ResponseBody string
}

func (e ExternalCallError) Error() string {
	return fmt.Sprintf("%s (azure devops responded %d): %s", e.UserMessage, e.StatusCode, e.ResponseBody)
}

// adoClient is the unexported Azure DevOps wire client: package-local DTOs, no
// sanitization of baseUrl (azdevops never sanitizes its base URL, unlike
// gitlab/github; a trailing slash yields double-slash request URLs, preserved as-is). Every
// call routes through the meshapi facade (DoRequest), so redirects/retries/Debug body logging
// are the shared singleton's policy, not this client's.
type adoClient struct {
	baseUrl, organization, project, pipelineId, pat string
	http                                            *http.Client
	log                                             meshapi.Logger
}

// newADOClient builds the client from the run's decrypted implementation + PAT.
func newADOClient(baseUrl, organization, project, pipelineId, pat string, httpClient *http.Client, log meshapi.Logger) adoClient {
	return adoClient{
		baseUrl:      baseUrl,
		organization: organization,
		project:      project,
		pipelineId:   pipelineId,
		pat:          pat,
		http:         httpClient,
		log:          log,
	}
}

// --- trigger payload (frozen wire shape) ---------------------------------------------------

type repositoryRef struct {
	RefName string `json:"refName"`
}

type repositoriesResources struct {
	Self repositoryRef `json:"self"`
}

type pipelineResources struct {
	Repositories repositoriesResources `json:"repositories"`
}

// triggerPipelinePayload is the byte-twin of Jackson's NON_NULL TriggerPipelinePayload
// (AzureDevOpsClient.kt:39-43): TemplateParameters is never omitted (Kotlin's default is
// emptyMap(), always serialized as `{}`, never absent) -- only Resources is optional.
type triggerPipelinePayload struct {
	TemplateParameters map[string]string  `json:"templateParameters"`
	Resources          *pipelineResources `json:"resources,omitempty"`
}

// --- HTTP mechanics --------------------------------------------------------------------

// authOpts builds the three headers every Azure DevOps call sends: Basic auth from the PAT,
// Accept: application/json, and X-TFS-FedAuthRedirect: Suppress -- ADO's PAT-quirk header
// that forces a clean 401 on an expired/invalid PAT instead of a "203 Non-Authoritative" plus
// an HTML sign-in page (paired with WithStrictJSONSuccess on every call to guard the 203/HTML
// case even if the header were ever ignored).
func (c adoClient) authOpts() []meshapi.RequestOption {
	token := base64.StdEncoding.EncodeToString([]byte(":" + c.pat))
	return []meshapi.RequestOption{
		meshapi.WithHeader("Authorization", "Basic "+token),
		meshapi.WithHeader("Accept", "application/json"),
		meshapi.WithHeader("X-TFS-FedAuthRedirect", "Suppress"),
	}
}

// asExternalCallError maps a facade HttpError into the package's ExternalCallError (the
// MeshHttpException twin); any other error (transport failure, ctx, ...) passes through
// unwrapped -- it has no Kotlin twin, matching the "any other exception" -> internal-error
// message path.
func asExternalCallError(err error, userMessage, requestURL string) error {
	if he, ok := meshapi.AsHttpError(err); ok {
		return ExternalCallError{
			UserMessage:  userMessage,
			StatusCode:   he.StatusCode,
			RequestUrl:   requestURL,
			ResponseBody: string(he.ResponseBody),
		}
	}
	return err
}

// TriggerPipeline is the trigger POST: URL exactly
// "{base}/{org}/{project}/_apis/pipelines/{pipelineId}/runs?api-version=7.1", body
// {templateParameters, resources?}. params are the already-rendered (stringified,
// environment-excluded, MESHSTACK_BEHAVIOR-injected) template parameters -- built by the
// caller (buildTemplateParameters), not this client (wire mechanics only). WithNoRedirect is
// mandatory here: decrypted sensitive template parameters ride in the body, and the path is
// not in the retry whitelist either, so a transport failure or non-2xx is never retried -- no
// risk of double-triggering the pipeline.
func (c adoClient) TriggerPipeline(ctx context.Context, params map[string]string, refName *string) (pipelineRun, error) {
	url := fmt.Sprintf("%s/%s/%s/_apis/pipelines/%s/runs?api-version=7.1", c.baseUrl, c.organization, c.project, c.pipelineId)

	if params == nil {
		params = map[string]string{}
	}
	payload := triggerPipelinePayload{TemplateParameters: params}
	if refName != nil {
		payload.Resources = &pipelineResources{Repositories: repositoriesResources{Self: repositoryRef{RefName: *refName}}}
	}

	opts := append(c.authOpts(), meshapi.WithJSONPayload(payload), meshapi.WithStrictJSONSuccess(), meshapi.WithNoRedirect())
	pr, err := meshapi.DoRequest[pipelineRun](ctx, c.http, c.log, http.MethodPost, url, opts...)
	if err != nil {
		return pipelineRun{}, asExternalCallError(err, "Failed to trigger Azure DevOps pipeline", url)
	}
	return pr, nil
}

// GetPipelineRun is the run-status GET. GETs are retried by the singleton transport
// (idempotent) in production.
func (c adoClient) GetPipelineRun(ctx context.Context, id int64) (pipelineRun, error) {
	url := fmt.Sprintf("%s/%s/%s/_apis/pipelines/%s/runs/%d?api-version=7.1", c.baseUrl, c.organization, c.project, c.pipelineId, id)

	opts := append(c.authOpts(), meshapi.WithStrictJSONSuccess())
	pr, err := meshapi.DoRequest[pipelineRun](ctx, c.http, c.log, http.MethodGet, url, opts...)
	if err != nil {
		return pipelineRun{}, asExternalCallError(err, fmt.Sprintf("Failed to get Azure DevOps pipeline run %d", id), url)
	}
	return pr, nil
}

// GetTimeline is the *build* timeline GET keyed by the pipeline-run id
// (…/_apis/build/builds/{runId}/timeline -- a different API family than
// pipelines/runs).
func (c adoClient) GetTimeline(ctx context.Context, id int64) ([]timelineRecord, error) {
	url := fmt.Sprintf("%s/%s/%s/_apis/build/builds/%d/timeline?api-version=7.1", c.baseUrl, c.organization, c.project, id)

	opts := append(c.authOpts(), meshapi.WithStrictJSONSuccess())
	tr, err := meshapi.DoRequest[timelineResponse](ctx, c.http, c.log, http.MethodGet, url, opts...)
	if err != nil {
		return nil, asExternalCallError(err, fmt.Sprintf("Failed to get Azure DevOps pipeline timeline for run %d", id), url)
	}
	return tr.Records, nil
}
