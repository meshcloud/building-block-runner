package testacc

import (
	"crypto/rand"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"strconv"
	"testing"
	"time"
)

const (
	azureDevOpsOrganization = "meshcloud-testacc"
	azureDevOpsProject      = "building-block"
	azureDevOpsPipelineId   = "42"
	azureDevOpsStage        = "Deploy"
)

type azureDevOpsPipeline struct {
	*pipeline

	baseUrl string
	pat     string
}

func (m *pipelineMock) newAzureDevOpsPipeline(t *testing.T, outcome string, async bool) *azureDevOpsPipeline {
	t.Helper()
	p := &azureDevOpsPipeline{pipeline: m.newPipeline(t, outcome, async), pat: rand.Text()}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /{organization}/{project}/_apis/pipelines/{pipeline}/runs", p.authorized(p.runPipeline))
	mux.HandleFunc("GET /{organization}/{project}/_apis/pipelines/{pipeline}/runs/{run}", p.authorized(p.getRun))
	mux.HandleFunc("GET /{organization}/{project}/_apis/build/builds/{run}/timeline", p.authorized(p.timeline))
	mux.HandleFunc("PATCH /{organization}/{project}/_apis/build/builds/{run}", p.authorized(p.updateBuild))
	p.baseUrl = m.mount(mux)
	return p
}

func (p *azureDevOpsPipeline) vars() map[string]any {
	return map[string]any{
		"base_url":     p.baseUrl,
		"organization": azureDevOpsOrganization,
		"project":      azureDevOpsProject,
		"pipeline_id":  azureDevOpsPipelineId,
		"pat":          p.pat,
		"async":        p.async,
	}
}

func (p *azureDevOpsPipeline) authorized(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, pat, ok := r.BasicAuth(); !ok || pat != p.pat {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		pipelineId := r.PathValue("pipeline")
		if r.PathValue("organization") != azureDevOpsOrganization || r.PathValue("project") != azureDevOpsProject || (pipelineId != "" && pipelineId != azureDevOpsPipelineId) {
			writeJson(w, http.StatusNotFound, map[string]any{"message": "The pipeline does not exist."})
			return
		}
		if r.URL.Query().Get("api-version") == "" {
			writeJson(w, http.StatusBadRequest, map[string]any{"message": "No api-version was supplied for the request."})
			return
		}
		next(w, r)
	}
}

func (p *azureDevOpsPipeline) runPipeline(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TemplateParameters map[string]string `json:"templateParameters"`
		Resources          struct {
			Repositories struct {
				Self struct {
					RefName string `json:"refName"`
				} `json:"self"`
			} `json:"repositories"`
		} `json:"resources"`
	}
	if err := json.UnmarshalRead(r.Body, &body); err != nil {
		writeJson(w, http.StatusBadRequest, map[string]any{"message": err.Error()})
		return
	}
	params := body.TemplateParameters
	run, runErr := runAt(params["MESHSTACK_RUN_URL"], params["MESHSTACK_RUN_TOKEN"])
	started := p.start(r.Context(), pipelineTrigger{ref: body.Resources.Repositories.Self.RefName, params: params}, run, runErr)
	writeJson(w, http.StatusOK, p.runJson(started))
}

func (p *azureDevOpsPipeline) getRun(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("run"), 10, 64)
	run, found := p.poll(id)
	if !found {
		writeJson(w, http.StatusNotFound, map[string]any{"message": "The pipeline run does not exist."})
		return
	}
	writeJson(w, http.StatusOK, p.runJson(run))
}

func (p *azureDevOpsPipeline) updateBuild(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Status string `json:"status"`
	}
	if err := json.UnmarshalRead(r.Body, &body); err != nil || body.Status != "cancelling" {
		writeJson(w, http.StatusBadRequest, map[string]any{"message": "The mock only cancels a build."})
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("run"), 10, 64)
	run, found := p.cancel(id)
	if !found {
		writeJson(w, http.StatusNotFound, map[string]any{"message": "The build does not exist."})
		return
	}
	writeJson(w, http.StatusOK, map[string]any{
		"id":          run.id,
		"buildNumber": fmt.Sprintf("#%d", run.id),
		"status":      body.Status,
		"queueTime":   run.created.Format(time.RFC3339),
	})
}

var (
	azureDevOpsRunStates    = []string{"inProgress", "inProgress", "completed"}
	azureDevOpsRecordStates = []string{"pending", "inProgress", "completed"}
)

func (p *azureDevOpsPipeline) runJson(run pipelineRun) map[string]any {
	state, completed := p.progress(run, azureDevOpsRunStates)
	webUrl := fmt.Sprintf("%s/%s/%s/_build/results?buildId=%d", p.baseUrl, azureDevOpsOrganization, azureDevOpsProject, run.id)
	return map[string]any{
		"id":           run.id,
		"name":         fmt.Sprintf("#%d", run.id),
		"state":        state,
		"result":       p.result(run, completed),
		"createdDate":  run.created.Format(time.RFC3339),
		"finishedDate": p.finished(run, completed),
		"url":          fmt.Sprintf("%s/%s/%s/_apis/pipelines/%s/runs/%d", p.baseUrl, azureDevOpsOrganization, azureDevOpsProject, azureDevOpsPipelineId, run.id),
		"_links":       map[string]any{"web": map[string]string{"href": webUrl}},
	}
}

func (p *azureDevOpsPipeline) timeline(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("run"), 10, 64)
	run, found := p.peek(id)
	if !found {
		writeJson(w, http.StatusNotFound, map[string]any{"message": "The build does not exist."})
		return
	}
	state, completed := p.progress(run, azureDevOpsRecordStates)
	stageId := fmt.Sprintf("stage-%d", run.id)
	record := func(id, name, recordType string, parentId any) map[string]any {
		return map[string]any{
			"id": id, "name": name, "type": recordType, "parentId": parentId, "order": 1,
			"state": state, "result": p.result(run, completed),
			"startTime": run.created.Format(time.RFC3339), "finishTime": p.finished(run, completed),
		}
	}
	writeJson(w, http.StatusOK, map[string]any{"records": []map[string]any{
		record(stageId, azureDevOpsStage, "Stage", nil),
		record(fmt.Sprintf("job-%d", run.id), azureDevOpsStage+" job", "Job", stageId),
	}})
}

func (p *azureDevOpsPipeline) result(run pipelineRun, completed bool) any {
	switch {
	case !completed:
		return nil
	case run.canceled:
		return "canceled"
	case p.succeeds():
		return "succeeded"
	}
	return "failed"
}

func (p *azureDevOpsPipeline) finished(run pipelineRun, completed bool) any {
	if !completed {
		return nil
	}
	return run.created.Add(time.Minute).Format(time.RFC3339)
}
