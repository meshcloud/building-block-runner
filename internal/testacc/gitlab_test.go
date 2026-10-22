package testacc

import (
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"testing"
)

const (
	gitlabProjectId      = "4242"
	gitlabMaxRequestBody = 1 << 20
)

type gitlabProject struct {
	*pipeline

	baseUrl      string
	triggerToken string
}

// newGitLabProject plays async pipelines only, as the runner never polls a GitLab pipeline.
func (m *pipelineMock) newGitLabProject(t *testing.T, outcome string) *gitlabProject {
	t.Helper()
	project := &gitlabProject{pipeline: m.newPipeline(t, outcome, true), triggerToken: "glptt-" + rand.Text()}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v4/projects/{project}/trigger/pipeline", project.triggerPipeline)
	project.baseUrl = m.mount(mux)
	return project
}

func (p *gitlabProject) vars() map[string]any {
	return map[string]any{"base_url": p.baseUrl, "project_id": gitlabProjectId, "trigger_token": p.triggerToken}
}

func (p *gitlabProject) triggerPipeline(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(gitlabMaxRequestBody); err != nil && !errors.Is(err, http.ErrNotMultipart) {
		writeJson(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if r.PathValue("project") != gitlabProjectId {
		writeJson(w, http.StatusNotFound, map[string]any{"message": "404 Project Not Found"})
		return
	}
	if r.PostFormValue("token") != p.triggerToken {
		writeJson(w, http.StatusNotFound, map[string]any{"message": "404 Not Found"})
		return
	}
	params := map[string]string{}
	for key, values := range r.PostForm {
		if key != "token" && key != "ref" {
			params[key] = values[0]
		}
	}
	run, runErr := decodeRun([]byte(params["variables[MESHSTACK_RUN]"]))
	started := p.start(r.Context(), pipelineTrigger{ref: r.PostFormValue("ref"), params: params}, run, runErr)
	writeJson(w, http.StatusCreated, map[string]any{
		"id":         started.id,
		"iid":        started.id,
		"project_id": gitlabProjectId,
		"ref":        r.PostFormValue("ref"),
		"status":     "created",
		"source":     "trigger",
		"web_url":    fmt.Sprintf("%s/testacc/-/pipelines/%d", p.baseUrl, started.id),
	})
}
