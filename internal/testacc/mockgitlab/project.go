package mockgitlab

import (
	"crypto/rand"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/meshcloud/meshstack-cli/client"

	"github.com/meshcloud/building-block-runner/v2/internal/testacc/mockpipeline"
)

const (
	projectId      = "4242"
	maxRequestBody = 1 << 20
)

// Project plays async pipelines only, as the runner never polls a GitLab pipeline.
type Project struct {
	*mockpipeline.Pipeline

	TriggerToken string

	baseUrl string
}

func New(t *testing.T, meshStack client.MeshRunnerClient, options mockpipeline.Options) Project {
	t.Helper()
	options.Async = true
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	project := Project{Pipeline: mockpipeline.New(t, meshStack, options), TriggerToken: "glptt-" + rand.Text(), baseUrl: server.URL}
	mux.HandleFunc("POST /api/v4/projects/{project}/trigger/pipeline", project.triggerPipeline)
	return project
}

func (p Project) Vars(t *testing.T) map[string]any {
	t.Helper()
	return map[string]any{"base_url": p.baseUrl, "project_id": projectId, "trigger_token": p.TriggerToken}
}

func (p Project) TriggerUrl(t *testing.T) string {
	t.Helper()
	return p.baseUrl + "/api/v4/projects/" + projectId + "/trigger/pipeline"
}

// ParsedInputs are the values that spec:inputs entries get, as GitLab parses a form value as JSON.
func ParsedInputs(t *testing.T, params map[string]string, names ...string) map[string]any {
	t.Helper()
	values := map[string]any{}
	for _, name := range names {
		var value any
		if err := json.Unmarshal([]byte(params["inputs["+name+"]"]), &value); err != nil {
			value = params["inputs["+name+"]"]
		}
		values[name] = value
	}
	return values
}

func (p Project) triggerPipeline(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(maxRequestBody); err != nil && !errors.Is(err, http.ErrNotMultipart) {
		mockpipeline.WriteJson(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if r.PathValue("project") != projectId {
		mockpipeline.WriteJson(w, http.StatusNotFound, map[string]any{"message": "404 Project Not Found"})
		return
	}
	if r.PostFormValue("token") != p.TriggerToken {
		mockpipeline.WriteJson(w, http.StatusNotFound, map[string]any{"message": "404 Not Found"})
		return
	}
	params := map[string]string{}
	for key, values := range r.PostForm {
		if key != "token" && key != "ref" {
			params[key] = values[0]
		}
	}
	run, runErr := runOf(params)
	started := p.Start(r.Context(), mockpipeline.Trigger{Ref: r.PostFormValue("ref"), Params: params}, run, runErr)
	mockpipeline.WriteJson(w, http.StatusCreated, map[string]any{
		"id":         started.Id,
		"iid":        started.Id,
		"project_id": projectId,
		"ref":        r.PostFormValue("ref"),
		"status":     "created",
		"source":     "trigger",
		"web_url":    fmt.Sprintf("%s/testacc/-/pipelines/%d", p.baseUrl, started.Id),
	})
}

// runOf reads the run object the runner sends, or the run URL and token a Terraform module sends.
func runOf(params map[string]string) (client.MeshRunnerRun, error) {
	encoded, withRunObject := params["variables[MESHSTACK_RUN]"]
	if !withRunObject {
		return mockpipeline.RunAt(params["variables[MESHSTACK_RUN_URL]"], params["variables[MESHSTACK_RUN_TOKEN]"])
	}
	return mockpipeline.DecodeRun([]byte(encoded))
}
