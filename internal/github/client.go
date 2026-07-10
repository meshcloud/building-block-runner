package github

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
)

// githubClient is the unexported GitHub REST wire adapter (§4.3, D11 sibling-split avoided:
// ~5 calls, no real seam). Headers per §2.3 (Accept, X-GitHub-Api-Version, Bearer);
// redirects are disabled on the injected *http.Client (§2.3). Run/job DTOs are
// package-local (umbrella §10.11); the workflow-run/job status strings are validated
// against the three known values, an unknown one erroring into the poll-retry path (§2.5.2).
type githubClient struct {
	baseURL string
	http    *http.Client
}

// newGithubClient builds the adapter for one sanitized per-run base URL. The *http.Client is
// the external-API seam (fakeable; redirects disabled by the caller / wiring, §4.1).
func newGithubClient(baseURL string, hc *http.Client) *githubClient {
	if hc == nil {
		hc = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	return &githubClient{baseURL: baseURL, http: hc}
}

// ---- run/job DTOs (package-local, umbrella §10.11) ----

// workflowRunStatus is the validated workflow-run lifecycle string (§2.3): only
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
	//nolint:staticcheck // ST1005: frozen Kotlin message, ported byte-identically (§7.11).
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
	//nolint:staticcheck // ST1005: frozen Kotlin message, ported byte-identically (§7.11).
	return "", fmt.Errorf("Unknown workflow job status: %s", v)
}

// workflowRun mirrors the GitHub run fields the poller reads (§2.3). Status is validated
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

// workflowJob mirrors the GitHub job fields reported as steps (§2.5.3).
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
// number). A non-2xx raises an *externalCallError (the MeshHttpException twin, §2.6).
func (c *githubClient) installationId(appToken, owner, repo string) (string, error) {
	u := c.baseURL + "/repos/" + owner + "/" + repo + "/installation"
	body, err := c.doGet(u, appToken, "Failed to obtain GitHub installation ID")
	if err != nil {
		return "", err
	}
	var resp struct {
		Id json.Number `json:"id"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("parsing installation response: %w", err)
	}
	return resp.Id.String(), nil
}

// installationToken performs POST /app/installations/{id}/access_tokens ⇒ the installation
// token, enforcing the actions=write permission gate (§2.3): a token lacking it fails the
// run down the generic path with the frozen "missing write permissions" message. A non-2xx
// raises an *externalCallError (§2.6).
func (c *githubClient) installationToken(appToken, installationID string) (string, error) {
	u := c.baseURL + "/app/installations/" + installationID + "/access_tokens"
	req, err := http.NewRequest(http.MethodPost, u, nil)
	if err != nil {
		return "", err
	}
	c.setCommonHeaders(req, appToken)
	body, status, err := c.do(req)
	if err != nil {
		return "", err
	}
	if !is2xx(status) {
		return "", newExternalCallError("Failed to obtain GitHub installation token", u, status, body)
	}
	var resp struct {
		Token       string            `json:"token"`
		Permissions map[string]string `json:"permissions"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("parsing installation token response: %w", err)
	}
	if resp.Permissions["actions"] != "write" {
		//nolint:staticcheck // ST1005: frozen Kotlin MeshException message, ported byte-identically (§7.11).
		return "", fmt.Errorf(
			"Your installed GitHub App is missing write permissions for actions. "+
				"Required permissions: actions=write. Actual permissions: %s",
			renderPermissions(resp.Permissions))
	}
	return resp.Token, nil
}

// renderPermissions deterministically renders the permission map for the gate message.
// Kotlin embeds a JVM Map.toString() ("{actions=read, …}" in JSON order); Go maps have no
// stable order, so keys are sorted — one flagged byte-level delta in one system message
// (§16.5).
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

// dispatchOutcome is the trigger result sum (§4.3, replacing the Kotlin sealed
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
// expecting 204. It carries a single Content-Type: application/json (the stray second HAL
// media-type header of the Kotlin source is dropped — sanctioned delta §16.2). Response
// classification (§2.3): 2xx ⇒ success; 422 whose body contains "Unexpected inputs
// provided" AND a recognized input name ⇒ unsupportedInput(names); any other non-2xx ⇒
// apiError(status, body). recognized is the caller's set of input names to test for.
func (c *githubClient) dispatchWorkflow(token, owner, repo, workflow string, payload dispatchPayload, recognized []string) (dispatchResult, error) {
	u := c.baseURL + "/repos/" + owner + "/" + repo + "/actions/workflows/" + workflow + "/dispatches"
	buf, err := json.Marshal(payload)
	if err != nil {
		return dispatchResult{}, fmt.Errorf("marshaling dispatch payload: %w", err)
	}
	req, err := http.NewRequest(http.MethodPost, u, bytes.NewReader(buf))
	if err != nil {
		return dispatchResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	c.setCommonHeaders(req, token)

	body, status, err := c.do(req)
	if err != nil {
		return dispatchResult{}, err
	}
	return classifyDispatchResponse(status, string(body), recognized), nil
}

// classifyDispatchResponse is the pure 422-heuristic table (§4.3, keep-as-unit): status ×
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
// (the 5 newest, the frozen correlation window §13).
func (c *githubClient) listWorkflowRuns(token, owner, repo, workflow string) ([]workflowRun, error) {
	u := c.baseURL + "/repos/" + owner + "/" + repo + "/actions/workflows/" + workflow + "/runs?per_page=" + fmt.Sprint(listRunsPerPage)
	body, err := c.doGet(u, token, "Failed to list GitHub workflow runs")
	if err != nil {
		return nil, err
	}
	var resp struct {
		WorkflowRuns []workflowRunJSON `json:"workflow_runs"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parsing workflow runs response: %w", err)
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
	body, err := c.doGet(u, token, fmt.Sprintf("Failed to get GitHub workflow run %d", runID))
	if err != nil {
		return workflowRun{}, err
	}
	var j workflowRunJSON
	if err := json.Unmarshal(body, &j); err != nil {
		return workflowRun{}, fmt.Errorf("parsing workflow run response: %w", err)
	}
	return j.toRun()
}

// workflowJobs performs GET …/actions/runs/{id}/jobs ⇒ jobs.
func (c *githubClient) workflowJobs(token, owner, repo string, runID int64) ([]workflowJob, error) {
	u := c.baseURL + "/repos/" + owner + "/" + repo + "/actions/runs/" + fmt.Sprint(runID) + "/jobs"
	body, err := c.doGet(u, token, fmt.Sprintf("Failed to list GitHub workflow jobs for run %d", runID))
	if err != nil {
		return nil, err
	}
	var resp struct {
		Jobs []workflowJobJSON `json:"jobs"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parsing workflow jobs response: %w", err)
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

// ---- transport helpers ----

// doGet issues a GET with the common headers and raises an *externalCallError on a non-2xx
// (userMessage carried for parity with the Kotlin MeshHttpException userMessage field).
func (c *githubClient) doGet(rawURL, token, userMessage string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	c.setCommonHeaders(req, token)
	body, status, err := c.do(req)
	if err != nil {
		return nil, err
	}
	if !is2xx(status) {
		return nil, newExternalCallError(userMessage, rawURL, status, body)
	}
	return body, nil
}

func (c *githubClient) setCommonHeaders(req *http.Request, token string) {
	req.Header.Set("Accept", acceptHeader)
	req.Header.Set(apiVersionHeaderKey, apiVersionValue)
	req.Header.Set("Authorization", "Bearer "+token)
}

func (c *githubClient) do(req *http.Request) ([]byte, int, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return body, resp.StatusCode, nil
}

func is2xx(status int) bool { return status >= 200 && status < 300 }

// newExternalCallError builds the §2.6 MeshHttpException twin with the system message
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
