package executor

import (
	"github.com/meshcloud/meshstack-cli/client/http"

	"github.com/meshcloud/building-block-runner/internal/build"
)

func NewPipelineClient() http.Client {
	return http.NewClient(http.UserAgent{GitHubRepo: "meshcloud/building-block-runner", Version: build.Version},
		http.WithoutRedirects(),
		// A trigger retries only on 429 and 503, which the service answered without acting on it.
		http.WithRetry(func(method string, _ bool) http.RetryOn {
			if method == http.MethodGet {
				return http.RetryOnTransient
			}
			return http.RetryOnBusy
		}))
}
