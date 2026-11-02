package executor

import (
	"context"
	"errors"
	"fmt"

	"github.com/meshcloud/meshstack-cli/client"
	"github.com/meshcloud/meshstack-cli/client/http"
	"github.com/meshcloud/meshstack-cli/client/types/xurl"

	"github.com/meshcloud/building-block-runner/v2/internal/build"
)

var userAgent = http.UserAgent{GitHubRepo: "meshcloud/building-block-runner", Version: build.Version}

func NewRunClient(ctx context.Context, endpoint, token string) (client.Client, error) {
	endpointUrl, err := parseRunEndpoint(endpoint, token)
	if err != nil {
		return client.Client{}, err
	}
	return client.New(ctx, endpointUrl, http.NewClient(userAgent), http.BearerToken(token)), nil
}

func newRunnerClient(endpoint, token string) (client.MeshRunnerClient, error) {
	endpointUrl, err := parseRunEndpoint(endpoint, token)
	if err != nil {
		return nil, err
	}
	return client.NewRunnerClient(endpointUrl, http.NewClient(userAgent), http.BearerToken(token)), nil
}

func parseRunEndpoint(endpoint, token string) (xurl.URL, error) {
	var endpointUrl xurl.URL
	if token == "" {
		return endpointUrl, errors.New("no API token")
	}
	if err := endpointUrl.UnmarshalText([]byte(endpoint)); err != nil {
		return endpointUrl, fmt.Errorf("endpoint %q: %w", endpoint, err)
	}
	return endpointUrl, nil
}
