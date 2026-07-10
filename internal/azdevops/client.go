package azdevops

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// adoHTTPTimeout mirrors OkHttp's default connect/read timeouts (~10s each,
// AzureDevOpsClient.kt:45-48 uses the OkHttpClient.Builder() defaults) -- Go's zero-value
// http.Client never times out, so a hung Azure DevOps endpoint would otherwise stall a
// "30-min" poll forever (§16.10). Load-bearing for poll resilience, not itself a frozen
// contract value.
const adoHTTPTimeout = 10 * time.Second

// NewHTTPClient builds the external-API HTTP client the azdevops persona wiring injects
// (HandlerDeps.HTTP): redirects are never followed (the OkHttp followRedirects(false) twin,
// A-P6) and every request is bounded by timeout end to end.
func NewHTTPClient(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = adoHTTPTimeout
	}
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// ExternalCallError is the Go twin of Kotlin's MeshHttpException (umbrella §4 row 14, 06A
// §4.4): every non-2xx Azure DevOps response becomes one of these. Defined package-local
// (not in the shared meshapi package) per the umbrella's explicit choice -- "per-package
// typed error with the same fields", since the messages built from it are per-runner pins,
// not a shared wire contract.
type ExternalCallError struct {
	UserMessage  string
	StatusCode   int
	RequestUrl   string
	ResponseBody string
}

func (e ExternalCallError) Error() string {
	return fmt.Sprintf("%s (azure devops responded %d): %s", e.UserMessage, e.StatusCode, e.ResponseBody)
}

// adoClient is the unexported Azure DevOps wire client (§4.2): package-local DTOs, no
// sanitization of baseUrl (§16.7 -- azdevops never sanitizes its base URL, unlike
// gitlab/github; a trailing slash yields double-slash request URLs, preserved as-is).
type adoClient struct {
	baseUrl, organization, project, pipelineId, pat string
	http                                            *http.Client
}

// newADOClient builds the client from the run's decrypted implementation + PAT.
func newADOClient(baseUrl, organization, project, pipelineId, pat string, httpClient *http.Client) adoClient {
	return adoClient{
		baseUrl:      baseUrl,
		organization: organization,
		project:      project,
		pipelineId:   pipelineId,
		pat:          pat,
		http:         httpClient,
	}
}

// --- trigger payload (frozen wire shape, A-P4) ---------------------------------------------

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

// renderValue reproduces Kotlin's `value.toString()` stringification of a
// json.Decoder.UseNumber()-decoded input value (C2/§16.6): strings verbatim, json.Number
// literal, bools "true"/"false". Composite/exotic values (arrays, objects) are rendered as
// compact JSON rather than Kotlin/Java collection toString() -- a deliberate, flagged byte
// change (§16.6) that also resolves the otherwise-unpinnable exotic-numeric-literal edge by
// pinning its JSON form instead.
func renderValue(v any) string {
	switch val := v.(type) {
	case nil:
		return "null"
	case string:
		return val
	case json.Number:
		return val.String()
	case bool:
		if val {
			return "true"
		}
		return "false"
	default:
		b, err := json.Marshal(val)
		if err != nil {
			return fmt.Sprintf("%v", val)
		}
		return string(b)
	}
}

// --- HTTP mechanics --------------------------------------------------------------------

func (c adoClient) setAuthHeader(req *http.Request) {
	token := base64.StdEncoding.EncodeToString([]byte(":" + c.pat))
	req.Header.Set("Authorization", "Basic "+token)
}

// do executes req and returns the response body on 2xx; a non-2xx response becomes an
// ExternalCallError carrying status/url/body (A-P6). A transport-level failure (connection
// refused, timeout, ...) is returned as a plain wrapped error -- it has no Kotlin
// MeshHttpException twin, matching the "any other exception" -> internal-error message path
// (§2.1.3/§2.7).
func (c adoClient) do(req *http.Request, userMessage string) ([]byte, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("calling azure devops: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, ExternalCallError{
			UserMessage:  userMessage,
			StatusCode:   resp.StatusCode,
			RequestUrl:   req.URL.String(),
			ResponseBody: string(body),
		}
	}
	return body, nil
}

// TriggerPipeline is the trigger POST (§2.5): URL exactly
// "{base}/{org}/{project}/_apis/pipelines/{pipelineId}/runs?api-version=7.1", body
// {templateParameters, resources?}. params are the already-rendered (stringified,
// environment-excluded, MESHSTACK_BEHAVIOR-injected) template parameters -- built by the
// caller (buildTemplateParameters), not this client (wire mechanics only, P3).
func (c adoClient) TriggerPipeline(ctx context.Context, params map[string]string, refName *string) (pipelineRun, error) {
	url := fmt.Sprintf("%s/%s/%s/_apis/pipelines/%s/runs?api-version=7.1", c.baseUrl, c.organization, c.project, c.pipelineId)

	if params == nil {
		params = map[string]string{}
	}
	payload := triggerPipelinePayload{TemplateParameters: params}
	if refName != nil {
		payload.Resources = &pipelineResources{Repositories: repositoriesResources{Self: repositoryRef{RefName: *refName}}}
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return pipelineRun{}, fmt.Errorf("marshaling trigger payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return pipelineRun{}, fmt.Errorf("building trigger request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	c.setAuthHeader(req)

	respBody, err := c.do(req, "Failed to trigger Azure DevOps pipeline")
	if err != nil {
		return pipelineRun{}, err
	}

	var pr pipelineRun
	if err := json.Unmarshal(respBody, &pr); err != nil {
		return pipelineRun{}, fmt.Errorf("parsing trigger response: %w", err)
	}
	return pr, nil
}

// GetPipelineRun is the run-status GET (§2.5).
func (c adoClient) GetPipelineRun(ctx context.Context, id int64) (pipelineRun, error) {
	url := fmt.Sprintf("%s/%s/%s/_apis/pipelines/%s/runs/%d?api-version=7.1", c.baseUrl, c.organization, c.project, c.pipelineId, id)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return pipelineRun{}, fmt.Errorf("building get-run request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	c.setAuthHeader(req)

	body, err := c.do(req, fmt.Sprintf("Failed to get Azure DevOps pipeline run %d", id))
	if err != nil {
		return pipelineRun{}, err
	}

	var pr pipelineRun
	if err := json.Unmarshal(body, &pr); err != nil {
		return pipelineRun{}, fmt.Errorf("parsing pipeline run response: %w", err)
	}
	return pr, nil
}

// GetTimeline is the *build* timeline GET keyed by the pipeline-run id
// (…/_apis/build/builds/{runId}/timeline, §2.5 -- a different API family than
// pipelines/runs).
func (c adoClient) GetTimeline(ctx context.Context, id int64) ([]timelineRecord, error) {
	url := fmt.Sprintf("%s/%s/%s/_apis/build/builds/%d/timeline?api-version=7.1", c.baseUrl, c.organization, c.project, id)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("building get-timeline request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	c.setAuthHeader(req)

	body, err := c.do(req, fmt.Sprintf("Failed to get Azure DevOps pipeline timeline for run %d", id))
	if err != nil {
		return nil, err
	}

	var tr timelineResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return nil, fmt.Errorf("parsing timeline response: %w", err)
	}
	return tr.Records, nil
}
