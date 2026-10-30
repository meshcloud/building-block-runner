package mockazuredevops

import (
	"fmt"
	"net/http"
	"time"

	"github.com/meshcloud/building-block-runner/v2/internal/testacc/mockpipeline"
)

var (
	runStates    = []string{"inProgress", "inProgress", "completed"}
	recordStates = []string{"pending", "inProgress", "completed"}
)

func (p Pipeline) runPipeline(w http.ResponseWriter, r *http.Request) {
	body, ok := mockpipeline.DecodeBody[struct {
		TemplateParameters map[string]string `json:"templateParameters"`
		Resources          struct {
			Repositories struct {
				Self struct {
					RefName string `json:"refName"`
				} `json:"self"`
			} `json:"repositories"`
		} `json:"resources"`
	}](w, r, "The request body is no pipeline run.")
	if !ok {
		return
	}
	params := body.TemplateParameters
	run, runErr := mockpipeline.RunAt(params["MESHSTACK_RUN_URL"], params["MESHSTACK_RUN_TOKEN"])
	started := p.Start(r.Context(), mockpipeline.Trigger{Ref: body.Resources.Repositories.Self.RefName, Params: params}, run, runErr)
	mockpipeline.WriteJson(w, http.StatusOK, p.runJson(started))
}

func (p Pipeline) getRun(w http.ResponseWriter, r *http.Request) {
	run, found := p.Poll(runId(r))
	if !found {
		mockpipeline.WriteJson(w, http.StatusNotFound, map[string]any{"message": "The pipeline run does not exist."})
		return
	}
	mockpipeline.WriteJson(w, http.StatusOK, p.runJson(run))
}

func (p Pipeline) updateBuild(w http.ResponseWriter, r *http.Request) {
	const onlyCancel = "The mock only cancels a build."
	body, ok := mockpipeline.DecodeBody[struct {
		Status string `json:"status"`
	}](w, r, onlyCancel)
	if !ok {
		return
	}
	if body.Status != "cancelling" {
		mockpipeline.WriteJson(w, http.StatusBadRequest, map[string]any{"message": onlyCancel})
		return
	}
	run, found := p.Cancel(runId(r))
	if !found {
		mockpipeline.WriteJson(w, http.StatusNotFound, map[string]any{"message": "The build does not exist."})
		return
	}
	mockpipeline.WriteJson(w, http.StatusOK, map[string]any{
		"id":          run.Id,
		"buildNumber": fmt.Sprintf("#%d", run.Id),
		"status":      body.Status,
		"queueTime":   run.Created.Format(time.RFC3339),
	})
}

func (p Pipeline) runJson(run mockpipeline.Run) map[string]any {
	state, completed := run.Progress(runStates)
	webUrl := fmt.Sprintf("%s/%s/%s/_build/results?buildId=%d", p.baseUrl, organization, project, run.Id)
	return map[string]any{
		"id":           run.Id,
		"name":         fmt.Sprintf("#%d", run.Id),
		"state":        state,
		"result":       result(run, completed),
		"createdDate":  run.Created.Format(time.RFC3339),
		"finishedDate": finished(run, completed),
		"url":          fmt.Sprintf("%s/%s/%s/_apis/pipelines/%s/runs/%d", p.baseUrl, organization, project, pipelineId, run.Id),
		"_links":       map[string]any{"web": map[string]string{"href": webUrl}},
	}
}

func (p Pipeline) timeline(w http.ResponseWriter, r *http.Request) {
	run, found := p.Peek(runId(r))
	if !found {
		mockpipeline.WriteJson(w, http.StatusNotFound, map[string]any{"message": "The build does not exist."})
		return
	}
	state, completed := run.Progress(recordStates)
	stageId := fmt.Sprintf("stage-%d", run.Id)
	record := func(id, name, recordType string, parentId any) map[string]any {
		return map[string]any{
			"id": id, "name": name, "type": recordType, "parentId": parentId, "order": 1,
			"state": state, "result": result(run, completed),
			"startTime": run.Created.Format(time.RFC3339), "finishTime": finished(run, completed),
		}
	}
	mockpipeline.WriteJson(w, http.StatusOK, map[string]any{"records": []map[string]any{
		record(stageId, Stage, "Stage", nil),
		record(fmt.Sprintf("job-%d", run.Id), Stage+" job", "Job", stageId),
		{
			"id": fmt.Sprintf("skipped-stage-%d", run.Id), "name": SkippedStage, "type": "Stage", "parentId": nil, "order": 2,
			"state": "completed", "result": "skipped",
		},
	}})
}

func result(run mockpipeline.Run, completed bool) any {
	switch {
	case !completed:
		return nil
	case run.EndsCanceled():
		return "canceled"
	case run.Succeeds():
		return "succeeded"
	}
	return "failed"
}

func finished(run mockpipeline.Run, completed bool) any {
	if !completed {
		return nil
	}
	return run.Created.Add(time.Minute).Format(time.RFC3339)
}
