package executor

import (
	"errors"
	gohttp "net/http"

	"github.com/meshcloud/meshstack-cli/client/http"
)

func NewPipelineClient() http.Client {
	return http.NewClient(userAgent,
		http.WithoutRedirects(),
		// A trigger retries only on 429 and 503, which the service answered without acting on it.
		http.WithRetry(func(method string, _ bool) http.RetryOn {
			if method == http.MethodGet {
				return http.RetryOnTransient
			}
			return http.RetryOnBusy
		}))
}

func IsPermanent(err error) bool {
	httpErr, ok := errors.AsType[http.Error](err)
	return ok && httpErr.IsClientError() &&
		httpErr.StatusCode != gohttp.StatusRequestTimeout && httpErr.StatusCode != gohttp.StatusTooManyRequests
}
