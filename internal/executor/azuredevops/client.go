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

	"github.com/meshcloud/building-block-runner/internal/executor"
)

const apiVersion = "7.1"

type azureDevOps struct {
	http    http.Client
	runs    *url.URL
	builds  *url.URL
	options []http.RequestOption
}

func newAzureDevOps(implementation client.MeshBuildingBlockRunAzureDevopsImplementation) (azureDevOps, error) {
	var baseUrl xurl.URL
	if err := baseUrl.UnmarshalText([]byte(implementation.AzureDevOpsBaseUrl)); err != nil {
		return azureDevOps{}, fmt.Errorf("the Azure DevOps base URL %q: %w", implementation.AzureDevOpsBaseUrl, err)
	}
	project := baseUrl.JoinPath(url.PathEscape(implementation.Organization), url.PathEscape(implementation.Project))
	return azureDevOps{
		http:   executor.NewHttpClient(),
		runs:   project.JoinPath("_apis", "pipelines", url.PathEscape(implementation.PipelineId), "runs"),
		builds: project.JoinPath("_apis", "build", "builds"),
		options: []http.RequestOption{
			http.WithoutRedirects(),
			http.WithUrlQuery(map[string]string{"api-version": apiVersion}),
			http.WithHeader("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(":"+implementation.PersonalAccessToken))),
			http.WithHeader("Accept", "application/json"),
			http.WithContentType("application/json"),
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
	return http.UnmarshalJsonBody[pipelineRun](a.http.DoRequest(ctx, http.MethodPost, a.runs, append(a.options, http.WithJsonBody(request))...))
}

func (a azureDevOps) run(ctx context.Context, runId int64) (pipelineRun, error) {
	return http.UnmarshalJsonBody[pipelineRun](a.http.DoRequest(ctx, http.MethodGet, a.runs.JoinPath(strconv.FormatInt(runId, 10)), a.options...))
}

func (a azureDevOps) timeline(ctx context.Context, runId int64) ([]timelineRecord, error) {
	timeline, err := http.UnmarshalJsonBody[struct {
		Records []timelineRecord `json:"records"`
	}](a.http.DoRequest(ctx, http.MethodGet, a.builds.JoinPath(strconv.FormatInt(runId, 10), "timeline"), a.options...))
	if err != nil {
		return nil, err
	}
	return timeline.Records, nil
}

func (a azureDevOps) cancel(ctx context.Context, runId int64) error {
	return http.IgnoreResponse(a.http.DoRequest(ctx, http.MethodPatch, a.builds.JoinPath(strconv.FormatInt(runId, 10)),
		append(a.options, http.WithJsonBody(map[string]string{"status": "cancelling"}))...))
}
