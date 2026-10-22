package github

import (
	"cmp"
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/meshcloud/meshstack-cli/client/http"
	"github.com/meshcloud/meshstack-cli/client/types/xurl"

	"github.com/meshcloud/building-block-runner/internal/executor"
)

type api struct {
	http    http.Client
	baseUrl xurl.URL
	headers []http.RequestOption
}

func newApi(baseUrl string) (api, error) {
	var parsed xurl.URL
	if err := parsed.UnmarshalText([]byte(strings.TrimSuffix(strings.TrimSpace(baseUrl), "/"))); err != nil {
		return api{}, fmt.Errorf("GitHub base URL %q: %w", baseUrl, err)
	}
	return api{http: executor.NewPipelineClient(), baseUrl: parsed}, nil
}

func (a api) withToken(token string) api {
	a.headers = []http.RequestOption{
		http.WithHeader("Accept", "application/vnd.github+json"),
		http.WithHeader("X-Github-Api-Version", "2022-11-28"),
		http.WithHeader("Authorization", "Bearer "+token),
	}
	return a
}

func (a api) do[R any](ctx context.Context, method string, target *url.URL, options ...http.RequestOption) (R, error) {
	return a.http.DoRequest[R](ctx, method, target, append(options, a.headers...)...)
}

func (a api) url(segments ...string) *url.URL {
	return a.baseUrl.JoinPath(segments...)
}

func (a api) repoUrl(owner, repository string, segments ...string) *url.URL {
	return a.url(append([]string{"repos", owner, repository}, segments...)...)
}

func installationApi(ctx context.Context, a api, appJwt, owner, repository string) (api, error) {
	app := a.withToken(appJwt)
	installation, err := app.do[struct {
		Id int64 `json:"id"`
	}](ctx, http.MethodGet, app.repoUrl(owner, repository, "installation"))
	if err != nil {
		return api{}, fmt.Errorf("find the GitHub App installation in %s/%s: %w", owner, repository, err)
	}
	token, err := app.do[struct {
		Token       string            `json:"token"`
		Permissions map[string]string `json:"permissions"`
	}](ctx, http.MethodPost, app.url("app", "installations", strconv.FormatInt(installation.Id, 10), "access_tokens"))
	if err != nil {
		return api{}, fmt.Errorf("create an installation token of the GitHub App: %w", err)
	}
	if token.Permissions["actions"] != "write" {
		return api{}, fmt.Errorf("the GitHub App needs the permission actions:write, but its installation has actions:%s", cmp.Or(token.Permissions["actions"], "none"))
	}
	return a.withToken(token.Token), nil
}
