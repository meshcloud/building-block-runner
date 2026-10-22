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
	options []http.RequestOption
}

func newApi(baseUrl string) (api, error) {
	var parsed xurl.URL
	if err := parsed.UnmarshalText([]byte(strings.TrimSuffix(strings.TrimSpace(baseUrl), "/"))); err != nil {
		return api{}, fmt.Errorf("GitHub base URL %q: %w", baseUrl, err)
	}
	return api{http: executor.NewHttpClient(), baseUrl: parsed}, nil
}

func (a api) withToken(token string) api {
	a.options = []http.RequestOption{
		http.WithoutRedirects(),
		http.WithHeader("Accept", "application/vnd.github+json"),
		http.WithHeader("X-Github-Api-Version", "2022-11-28"),
		http.WithHeader("Authorization", "Bearer "+token),
		http.WithContentType("application/json"),
	}
	return a
}

func installationApi(ctx context.Context, a api, appJwt string, repo *url.URL) (api, error) {
	app := a.withToken(appJwt)
	installation, err := http.UnmarshalJsonBody[struct {
		Id int64 `json:"id"`
	}](app.http.DoRequest(ctx, http.MethodGet, repo.JoinPath("installation"), app.options...))
	if err != nil {
		return api{}, fmt.Errorf("find the GitHub App installation in %s: %w", repo, err)
	}
	token, err := http.UnmarshalJsonBody[struct {
		Token       string            `json:"token"`
		Permissions map[string]string `json:"permissions"`
	}](app.http.DoRequest(ctx, http.MethodPost, app.baseUrl.JoinPath("app", "installations", strconv.FormatInt(installation.Id, 10), "access_tokens"), app.options...))
	if err != nil {
		return api{}, fmt.Errorf("create an installation token of the GitHub App: %w", err)
	}
	if token.Permissions["actions"] != "write" {
		return api{}, fmt.Errorf("the GitHub App needs the permission actions:write, but its installation has actions:%s", cmp.Or(token.Permissions["actions"], "none"))
	}
	return a.withToken(token.Token), nil
}
