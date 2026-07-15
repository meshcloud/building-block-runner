package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/meshcloud/building-block-runner/internal/meshapi"
)

// githubClient is the unexported GitHub REST wire adapter (sibling-split avoided:
// ~5 calls, no real seam). Headers are Accept, X-GitHub-Api-Version, Bearer, applied via
// commonOpts; every call routes through the meshapi facade (DoRequest), so redirects,
// retries and Debug body logging are the shared singleton's policy, not this client's. Run/job
// DTOs are package-local; the workflow-run/job status strings are validated against the three
// known values, an unknown one erroring into the poll-retry path.
type githubClient struct {
	baseURL string
	http    *http.Client
	log     meshapi.Logger
}

// newGithubClient builds the adapter for one sanitized per-run base URL. A nil *http.Client
// defaults to the process-wide shared singleton (meshapi.SharedHTTPClient) rather than a
// per-client one, so every GitHub call rides the same bound+retrying+redirect-policy client as
// the rest of the binary.
func newGithubClient(baseURL string, hc *http.Client, log meshapi.Logger) *githubClient {
	if hc == nil {
		hc = meshapi.SharedHTTPClient()
	}
	return &githubClient{baseURL: baseURL, http: hc, log: log}
}

// commonOpts builds the three headers every GitHub call sends.
func commonOpts(token string) []meshapi.RequestOption {
	return []meshapi.RequestOption{
		meshapi.WithHeader("Accept", acceptHeader),
		meshapi.WithHeader(apiVersionHeaderKey, apiVersionValue),
		meshapi.WithHeader("Authorization", "Bearer "+token),
	}
}

// ---- run/job DTOs (package-local) ----

// workflowRunStatus is the validated workflow-run lifecycle string: only
// queued|in_progress|completed are known; anything else errors into the retry path.
type workflowRunStatus string

const (
	runQueued     workflowRunStatus = "queued"
	runInProgress workflowRunStatus = "in_progress"
	runCompleted  workflowRunStatus = "completed"
)

func parseRunStatus(v string) (workflowRunStatus, error) {
	switch workflowRunStatus(v) {
	case runQueued, runInProgress, runCompleted:
		return workflowRunStatus(v), nil
	}
	//nolint:staticcheck // ST1005: frozen Kotlin message, ported byte-identically.
	return "", fmt.Errorf("Unknown workflow run status: %s", v)
}

type workflowJobStatus string

const (
	jobQueued     workflowJobStatus = "queued"
	jobInProgress workflowJobStatus = "in_progress"
	jobCompleted  workflowJobStatus = "completed"
)

func parseJobStatus(v string) (workflowJobStatus, error) {
	switch workflowJobStatus(v) {
	case jobQueued, jobInProgress, jobCompleted:
		return workflowJobStatus(v), nil
	}
	//nolint:staticcheck // ST1005: frozen Kotlin message, ported byte-identically.
	return "", fmt.Errorf("Unknown workflow job status: %s", v)
}

// workflowRun mirrors the GitHub run fields the poller reads. Status is validated
// (unknown ⇒ error into the retry path); conclusion is a free string ("success" etc.).
type workflowRun struct {
	Id         int64
	Status     workflowRunStatus
	Conclusion string
	CreatedAt  string
	HtmlUrl    string
}

type workflowRunJSON struct {
	Id         int64   `json:"id"`
	Status     string  `json:"status"`
	Conclusion *string `json:"conclusion"`
	CreatedAt  string  `json:"created_at"`
	HtmlUrl    string  `json:"html_url"`
}

func (j workflowRunJSON) toRun() (workflowRun, error) {
	st, err := parseRunStatus(j.Status)
	if err != nil {
		return workflowRun{}, err
	}
	return workflowRun{Id: j.Id, Status: st, Conclusion: derefStr(j.Conclusion), CreatedAt: j.CreatedAt, HtmlUrl: j.HtmlUrl}, nil
}

// workflowJob mirrors the GitHub job fields reported as steps.
type workflowJob struct {
	Id          int64
	Name        string
	Status      workflowJobStatus
	Conclusion  string
	StartedAt   string
	CompletedAt string
	HtmlUrl     string
}

type workflowJobJSON struct {
	Id          int64   `json:"id"`
	Name        string  `json:"name"`
	Status      string  `json:"status"`
	Conclusion  *string `json:"conclusion"`
	StartedAt   *string `json:"started_at"`
	CompletedAt *string `json:"completed_at"`
	HtmlUrl     string  `json:"html_url"`
}

func (j workflowJobJSON) toJob() (workflowJob, error) {
	st, err := parseJobStatus(j.Status)
	if err != nil {
		return workflowJob{}, err
	}
	return workflowJob{
		Id: j.Id, Name: j.Name, Status: st, Conclusion: derefStr(j.Conclusion),
		StartedAt: derefStr(j.StartedAt), CompletedAt: derefStr(j.CompletedAt), HtmlUrl: j.HtmlUrl,
	}, nil
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// ---- calls ----

// installationId performs GET /repos/{owner}/{repo}/installation ⇒ the numeric id rendered
// as a string (Kotlin AppInstallation.installationId is a String; Jackson coerces the JSON
// number). A non-2xx raises an *externalCallError (the MeshHttpException twin).
func (c *githubClient) installationId(appToken, owner, repo string) (string, error) {
	u := c.baseURL + "/repos/" + owner + "/" + repo + "/installation"
	resp, err := doGithubGet[struct {
		Id json.Number `json:"id"`
	}](c, u, appToken, "Failed to obtain GitHub installation ID")
	if err != nil {
		return "", err
	}
	return resp.Id.String(), nil
}

// installationToken performs POST /app/installations/{id}/access_tokens (whitelisted for
// retry: a safe-to-replay token mint) ⇒ the installation token, enforcing the actions=write
// permission gate: a token lacking it fails the run down the generic path with the frozen
// "missing write permissions" message. A non-2xx raises an *externalCallError.
func (c *githubClient) installationToken(appToken, installationID string) (string, error) {
	u := c.baseURL + "/app/installations/" + installationID + "/access_tokens"
	resp, err := meshapi.DoRequest[struct {
		Token       string            `json:"token"`
		Permissions map[string]string `json:"permissions"`
	}](context.Background(), c.http, c.log, http.MethodPost, u, commonOpts(appToken)...)
	if err != nil {
		if he, ok := meshapi.AsHttpError(err); ok {
			return "", newExternalCallError("Failed to obtain GitHub installation token", u, he.StatusCode, he.ResponseBody)
		}
		return "", err
	}
	if resp.Permissions["actions"] != "write" {
		//nolint:staticcheck // ST1005: frozen Kotlin MeshException message, ported byte-identically.
		return "", fmt.Errorf(
			"Your installed GitHub App is missing write permissions for actions. "+
				"Required permissions: actions=write. Actual permissions: %s",
			renderPermissions(resp.Permissions))
	}
	return resp.Token, nil
}

// renderPermissions deterministically renders the permission map for the gate message.
// Kotlin embeds a JVM Map.toString() ("{actions=read, …}" in JSON order); Go maps have no
// stable order, so keys are sorted — one byte-level delta in one system message.
func renderPermissions(perms map[string]string) string {
	keys := make([]string, 0, len(perms))
	for k := range perms {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+perms[k])
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

// dispatchOutcome is the trigger result sum (replacing the Kotlin sealed
// TriggerWorkflowResult — no interface hierarchy).
type dispatchOutcome int

const (
	dispatchSuccess dispatchOutcome = iota
	dispatchUnsupportedInput
	dispatchAPIError
)

type dispatchResult struct {
	outcome dispatchOutcome
	// unsupportedNames is the sorted set of recognized input names GitHub rejected (422).
	unsupportedNames []string
	statusCode       int
	body             string
}

// dispatchWorkflow performs POST …/actions/workflows/{file}/dispatches with the JSON body,
// expecting 204. WithNoRedirect is mandatory here: the body carries MESHSTACK_API_TOKEN /
// MESHSTACK_RUN_TOKEN, and a followed 3xx could silently resend them to whatever the Location
// header names. The path is not in the retry whitelist either, so a transport failure or
// non-2xx is never retried — no risk of double-triggering the workflow.
//
// Response classification: 2xx ⇒ success; 422 whose body contains "Unexpected inputs
// provided" AND a recognized input name ⇒ unsupportedInput(names); any other non-2xx ⇒
// apiError(status, body). recognized is the caller's set of input names to test for.
func (c *githubClient) dispatchWorkflow(token, owner, repo, workflow string, payload dispatchPayload, recognized []string) (dispatchResult, error) {
	u := c.baseURL + "/repos/" + owner + "/" + repo + "/actions/workflows/" + workflow + "/dispatches"
	opts := append(commonOpts(token), meshapi.WithJSONPayload(payload), meshapi.WithNoRedirect())

	_, err := meshapi.DoRequest[json.RawMessage](context.Background(), c.http, c.log, http.MethodPost, u, opts...)
	if err != nil {
		if he, ok := meshapi.AsHttpError(err); ok {
			return classifyDispatchResponse(he.StatusCode, string(he.ResponseBody), recognized), nil
		}
		return dispatchResult{}, err
	}
	return classifyDispatchResponse(http.StatusNoContent, "", recognized), nil
}

// classifyDispatchResponse is the pure 422-heuristic table: status ×
// body → outcome. Pulled out so Test_ClassifyDispatchResponse can pin it directly.
func classifyDispatchResponse(status int, body string, recognized []string) dispatchResult {
	if is2xx(status) {
		return dispatchResult{outcome: dispatchSuccess}
	}
	if status == http.StatusUnprocessableEntity {
		var found []string
		for _, name := range recognized {
			if isUnsupportedInputError(body, name) {
				found = append(found, name)
			}
		}
		if len(found) > 0 {
			sort.Strings(found)
			return dispatchResult{outcome: dispatchUnsupportedInput, unsupportedNames: found, statusCode: status, body: body}
		}
	}
	return dispatchResult{outcome: dispatchAPIError, statusCode: status, body: body}
}

// isUnsupportedInputError mirrors the Kotlin heuristic (:268-271): the body must mention
// BOTH the generic "Unexpected inputs provided" phrase AND the specific input name.
func isUnsupportedInputError(body, inputName string) bool {
	return strings.Contains(body, "Unexpected inputs provided") && strings.Contains(body, inputName)
}

// listWorkflowRuns performs GET …/actions/workflows/{file}/runs?per_page=5 ⇒ workflow_runs
// (the 5 newest, the frozen correlation window).
func (c *githubClient) listWorkflowRuns(token, owner, repo, workflow string) ([]workflowRun, error) {
	u := c.baseURL + "/repos/" + owner + "/" + repo + "/actions/workflows/" + workflow + "/runs?per_page=" + fmt.Sprint(listRunsPerPage)
	resp, err := doGithubGet[struct {
		WorkflowRuns []workflowRunJSON `json:"workflow_runs"`
	}](c, u, token, "Failed to list GitHub workflow runs")
	if err != nil {
		return nil, err
	}
	runs := make([]workflowRun, 0, len(resp.WorkflowRuns))
	for _, r := range resp.WorkflowRuns {
		run, err := r.toRun()
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, nil
}

// workflowRunByID performs GET …/actions/runs/{id}.
func (c *githubClient) workflowRunByID(token, owner, repo string, runID int64) (workflowRun, error) {
	u := c.baseURL + "/repos/" + owner + "/" + repo + "/actions/runs/" + fmt.Sprint(runID)
	j, err := doGithubGet[workflowRunJSON](c, u, token, fmt.Sprintf("Failed to get GitHub workflow run %d", runID))
	if err != nil {
		return workflowRun{}, err
	}
	return j.toRun()
}

// workflowJobs performs GET …/actions/runs/{id}/jobs ⇒ jobs.
func (c *githubClient) workflowJobs(token, owner, repo string, runID int64) ([]workflowJob, error) {
	u := c.baseURL + "/repos/" + owner + "/" + repo + "/actions/runs/" + fmt.Sprint(runID) + "/jobs"
	resp, err := doGithubGet[struct {
		Jobs []workflowJobJSON `json:"jobs"`
	}](c, u, token, fmt.Sprintf("Failed to list GitHub workflow jobs for run %d", runID))
	if err != nil {
		return nil, err
	}
	jobs := make([]workflowJob, 0, len(resp.Jobs))
	for _, j := range resp.Jobs {
		job, err := j.toJob()
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, nil
}

// ---- transport helper ----

// doGithubGet performs one GET with the common headers via the meshapi facade. A non-2xx
// raises an *externalCallError (userMessage carried for parity with the Kotlin
// MeshHttpException userMessage field); any other error (transport failure, ctx, ...) passes
// through unwrapped. A free function, not a method: Go methods cannot carry their own type
// parameters.
func doGithubGet[R any](c *githubClient, rawURL, token, userMessage string) (R, error) {
	resp, err := meshapi.DoRequest[R](context.Background(), c.http, c.log, http.MethodGet, rawURL, commonOpts(token)...)
	if err != nil {
		if he, ok := meshapi.AsHttpError(err); ok {
			var zero R
			return zero, newExternalCallError(userMessage, rawURL, he.StatusCode, he.ResponseBody)
		}
		return resp, err
	}
	return resp, nil
}

func is2xx(status int) bool { return status >= 200 && status < 300 }

// newExternalCallError builds the MeshHttpException twin with the system message
// pre-rendered: "Request: <url>\nGitHub responded with status: <code> and body: <body>".
func newExternalCallError(userMessage, requestURL string, status int, body []byte) *externalCallError {
	return &externalCallError{
		UserMessage:   userMessage,
		SystemMessage: fmt.Sprintf("Request: %s\nGitHub responded with status: %d and body: %s", requestURL, status, string(body)),
		StatusCode:    status,
		RequestUrl:    requestURL,
		ResponseBody:  string(body),
	}
}
