package azuredevops

import (
	"cmp"
	"context"
	"encoding/base64"
	"fmt"
	"net/url"
	"strconv"

	"github.com/meshcloud/meshstack-cli/client"
	"github.com/meshcloud/meshstack-cli/client/http"
	"github.com/meshcloud/meshstack-cli/client/types/xurl"

	"github.com/meshcloud/building-block-runner/v2/internal/executor"
)

const apiVersion = "7.1"

type azureDevOps struct {
	http       http.Client
	project    *url.URL
	pipelineId string
	headers    []http.RequestOption
}

func newAzureDevOps(implementation client.MeshRunnerAzureDevopsImplementation) (azureDevOps, error) {
	var baseUrl xurl.URL
	if err := baseUrl.UnmarshalText([]byte(implementation.AzureDevOpsBaseUrl)); err != nil {
		return azureDevOps{}, fmt.Errorf("the Azure DevOps base URL %q: %w", implementation.AzureDevOpsBaseUrl, err)
	}
	return azureDevOps{
		http:       executor.NewPipelineClient(),
		project:    baseUrl.JoinPath(url.PathEscape(implementation.Organization), url.PathEscape(implementation.Project)),
		pipelineId: implementation.PipelineId,
		headers: []http.RequestOption{
			http.WithHeader("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(":"+implementation.PersonalAccessToken))),
			http.WithHeader("Accept", "application/json"),
		},
	}, nil
}

type pipelineRun struct {
	Id     int64  `json:"id"`
	State  string `json:"state"`
	Result string `json:"result"`
	Url    string `json:"url"`
	Links  struct {
		Web struct {
			Href string `json:"href"`
		} `json:"web"`
	} `json:"_links"`
}

func (r pipelineRun) webUrl() string {
	return cmp.Or(r.Links.Web.Href, r.Url, "N/A")
}

type timelineRecord struct {
	Id         string `json:"id"`
	Name       string `json:"name"`
	Type       string `json:"type"`
	State      string `json:"state"`
	Result     string `json:"result"`
	StartTime  string `json:"startTime"`
	FinishTime string `json:"finishTime"`
	ParentId   string `json:"parentId"`
	Order      int    `json:"order"`
}

type triggerRequest struct {
	TemplateParameters map[string]string `json:"templateParameters"`
	Resources          *resources        `json:"resources,omitzero"`
}

type resources struct {
	Repositories struct {
		Self struct {
			RefName string `json:"refName"`
		} `json:"self"`
	} `json:"repositories"`
}

func (a azureDevOps) trigger(ctx context.Context, request triggerRequest) (pipelineRun, error) {
	return a.do[pipelineRun](ctx, http.MethodPost, a.url("_apis", "pipelines", a.pipelineId, "runs"),
		http.WithJsonPayload(request, "application/json"))
}

func (a azureDevOps) run(ctx context.Context, runId int64) (pipelineRun, error) {
	return a.do[pipelineRun](ctx, http.MethodGet, a.url("_apis", "pipelines", a.pipelineId, "runs", strconv.FormatInt(runId, 10)))
}

func (a azureDevOps) timeline(ctx context.Context, runId int64) ([]timelineRecord, error) {
	timeline, err := a.do[struct {
		Records []timelineRecord `json:"records"`
	}](ctx, http.MethodGet, a.url("_apis", "build", "builds", strconv.FormatInt(runId, 10), "timeline"))
	return timeline.Records, err
}

func (a azureDevOps) cancel(ctx context.Context, runId int64) error {
	_, err := a.do[any](ctx, http.MethodPatch, a.url("_apis", "build", "builds", strconv.FormatInt(runId, 10)),
		http.WithJsonPayload(map[string]string{"status": "cancelling"}, "application/json"))
	return err
}

func (a azureDevOps) do[R any](ctx context.Context, method string, target *url.URL, options ...http.RequestOption) (R, error) {
	return a.http.DoRequest[R](ctx, method, target, append(options, a.headers...)...)
}

func (a azureDevOps) url(segments ...string) *url.URL {
	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
	}
	u := a.project.JoinPath(segments...)
	u.RawQuery = url.Values{"api-version": {apiVersion}}.Encode()
	return u
}
