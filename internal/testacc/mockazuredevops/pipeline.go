package mockazuredevops

import (
	"crypto/rand"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/meshcloud/meshstack-cli/client"

	"github.com/meshcloud/building-block-runner/v2/internal/testacc/mockpipeline"
)

const (
	organization = "meshcloud-testacc"
	project      = "building-block"
	pipelineId   = "42"
	Stage        = "Deploy"
	SkippedStage = "Cleanup"
)

type Pipeline struct {
	*mockpipeline.Pipeline

	Pat string

	baseUrl string
}

func New(t *testing.T, meshStack client.MeshRunnerClient, options mockpipeline.Options) Pipeline {
	t.Helper()
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	p := Pipeline{Pipeline: mockpipeline.New(t, meshStack, options), Pat: rand.Text(), baseUrl: server.URL}
	mux.HandleFunc("POST /{organization}/{project}/_apis/pipelines/{pipeline}/runs", p.authorized(p.runPipeline))
	mux.HandleFunc("GET /{organization}/{project}/_apis/pipelines/{pipeline}/runs/{run}", p.authorized(p.getRun))
	mux.HandleFunc("GET /{organization}/{project}/_apis/build/builds/{run}/timeline", p.authorized(p.timeline))
	mux.HandleFunc("PATCH /{organization}/{project}/_apis/build/builds/{run}", p.authorized(p.updateBuild))
	return p
}

func (p Pipeline) Vars(t *testing.T) map[string]any {
	t.Helper()
	return map[string]any{
		"base_url":     p.baseUrl,
		"organization": organization,
		"project":      project,
		"pipeline_id":  pipelineId,
		"pat":          p.Pat,
		"async":        p.Async,
	}
}

func (p Pipeline) RunsUrl(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("%s/%s/%s/_apis/pipelines/%s/runs?api-version=7.1", p.baseUrl, organization, project, pipelineId)
}

func (p Pipeline) authorized(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, pat, ok := r.BasicAuth(); !ok || pat != p.Pat {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		requestedPipeline := r.PathValue("pipeline")
		if r.PathValue("organization") != organization || r.PathValue("project") != project || (requestedPipeline != "" && requestedPipeline != pipelineId) {
			mockpipeline.WriteJson(w, http.StatusNotFound, map[string]any{"message": "The pipeline does not exist."})
			return
		}
		if r.URL.Query().Get("api-version") == "" {
			mockpipeline.WriteJson(w, http.StatusBadRequest, map[string]any{"message": "No api-version was supplied for the request."})
			return
		}
		next(w, r)
	}
}

func runId(r *http.Request) int64 {
	id, _ := strconv.ParseInt(r.PathValue("run"), 10, 64)
	return id
}
