package mockgithub

import (
	"encoding/base64"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/meshcloud/meshstack-cli/client"

	"github.com/meshcloud/building-block-runner/v2/internal/testacc/mockpipeline"
)

// statuses holds waiting, which GitHub sends while a job waits for a runner or an approval.
var statuses = []string{"queued", "waiting", "in_progress", "completed"}

func (a App) installation(w http.ResponseWriter, _ *http.Request) {
	mockpipeline.WriteJson(w, http.StatusOK, map[string]any{
		"id": installationId, "app_id": appId, "client_id": "Iv1.testacc", "target_type": "Organization",
	})
}

func (a App) accessToken(w http.ResponseWriter, r *http.Request) {
	if r.PathValue("installation") != strconv.Itoa(installationId) {
		notFound(w)
		return
	}
	token := a.InstallationToken
	if a.options.TokenLifetime > 0 {
		token = a.issued.issue(a.options.TokenLifetime)
	}
	actions := "write"
	if a.options.WithoutActionsWrite {
		actions = "read"
	}
	mockpipeline.WriteJson(w, http.StatusCreated, map[string]any{
		"token":                token,
		"expires_at":           time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		"permissions":          map[string]string{"actions": actions, "metadata": "read"},
		"repository_selection": "selected",
	})
}

func (a App) dispatch(w http.ResponseWriter, r *http.Request) {
	workflow := r.PathValue("workflow")
	if workflow != workflowFile && workflow != DestroyWorkflow {
		notFound(w)
		return
	}
	body, ok := mockpipeline.DecodeBody[struct {
		Ref              string            `json:"ref"`
		Inputs           map[string]string `json:"inputs"`
		ReturnRunDetails bool              `json:"return_run_details"`
	}](w, r, "Problems parsing JSON")
	if !ok {
		return
	}
	if unexpected := a.unexpectedInputs(body.Inputs); len(unexpected) > 0 {
		names, _ := json.Marshal(unexpected)
		mockpipeline.WriteJson(w, http.StatusUnprocessableEntity, map[string]any{"message": "Unexpected inputs provided: " + string(names)})
		return
	}
	run, runErr := runOf(body.Inputs)
	started := a.Start(r.Context(), mockpipeline.Trigger{Workflow: workflow, Ref: body.Ref, Params: body.Inputs}, run, runErr)
	if !body.ReturnRunDetails || a.options.WithoutRunDetails {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	mockpipeline.WriteJson(w, http.StatusOK, map[string]any{
		"workflow_run_id": started.Id,
		"run_url":         fmt.Sprintf("%s/repos/%s/%s/actions/runs/%d", a.baseUrl, owner, repository, started.Id),
		"html_url":        a.htmlUrl(started),
	})
}

func (a App) unexpectedInputs(inputs map[string]string) []string {
	var unexpected []string
	for name := range inputs {
		if a.options.DeclaredInputs != nil && !slices.Contains(a.options.DeclaredInputs, name) {
			unexpected = append(unexpected, name)
		}
	}
	slices.Sort(unexpected)
	return unexpected
}

func runOf(inputs map[string]string) (client.MeshRunnerRun, error) {
	encoded, withRunObject := inputs["buildingBlockRun"]
	if !withRunObject {
		return mockpipeline.RunAt(inputs["buildingBlockRunUrl"], inputs["MESHSTACK_RUN_TOKEN"])
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return client.MeshRunnerRun{}, err
	}
	return mockpipeline.DecodeRun(decoded)
}

func (a App) listRuns(w http.ResponseWriter, r *http.Request) {
	if r.PathValue("workflow") != workflowFile {
		notFound(w)
		return
	}
	var runs []map[string]any
	for run := range a.NewestRuns {
		runs = append(runs, a.runJson(run))
	}
	mockpipeline.WriteJson(w, http.StatusOK, map[string]any{"total_count": len(runs), "workflow_runs": runs})
}

func (a App) getRun(w http.ResponseWriter, r *http.Request) {
	run, found := a.Poll(runId(r))
	if !found || a.options.RunNotFound {
		notFound(w)
		return
	}
	mockpipeline.WriteJson(w, http.StatusOK, a.runJson(run))
}

func (a App) listJobs(w http.ResponseWriter, r *http.Request) {
	run, found := a.Peek(runId(r))
	if !found {
		notFound(w)
		return
	}
	job := a.runJson(run)
	job["run_id"] = run.Id
	job["name"] = Job
	job["started_at"] = run.Created.Format(time.RFC3339)
	job["completed_at"] = nil
	if job["conclusion"] != nil {
		job["completed_at"] = job["updated_at"]
	}
	mockpipeline.WriteJson(w, http.StatusOK, map[string]any{"total_count": 1, "jobs": []map[string]any{job}})
}

func (a App) cancelRun(w http.ResponseWriter, r *http.Request) {
	run, found := a.Peek(runId(r))
	if !found {
		notFound(w)
		return
	}
	if _, completed := run.Progress(statuses); completed {
		mockpipeline.WriteJson(w, http.StatusConflict, map[string]any{"message": "Cannot cancel a workflow run that is completed."})
		return
	}
	a.Cancel(run.Id)
	mockpipeline.WriteJson(w, http.StatusAccepted, map[string]any{})
}

func runId(r *http.Request) int64 {
	id, _ := strconv.ParseInt(r.PathValue("run"), 10, 64)
	return id
}

func (a App) runJson(run mockpipeline.Run) map[string]any {
	status, completed := run.Progress(statuses)
	var conclusion any
	switch {
	case !completed:
	case run.EndsCanceled():
		conclusion = "cancelled"
	case run.Succeeds():
		conclusion = "success"
	default:
		conclusion = "failure"
	}
	return map[string]any{
		"id":         run.Id,
		"status":     status,
		"conclusion": conclusion,
		"created_at": run.Created.Format(time.RFC3339),
		"updated_at": run.Created.Add(time.Duration(run.Polls) * time.Second).Format(time.RFC3339),
		"html_url":   a.htmlUrl(run),
	}
}

func (a App) htmlUrl(run mockpipeline.Run) string {
	return fmt.Sprintf("%s/%s/%s/actions/runs/%d", a.baseUrl, owner, repository, run.Id)
}
