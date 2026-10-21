package poller

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"time"

	"github.com/meshcloud/meshstack-cli/client"
	"github.com/meshcloud/meshstack-cli/pkg/auth"
	"github.com/meshcloud/meshstack-cli/pkg/setting"

	"github.com/meshcloud/building-block-runner/internal/build"
)

const (
	meshfedStartTimeout       = 10 * time.Minute
	meshfedStartRetryInterval = 10 * time.Second
)

func newClientOnceMeshfedAnswers(ctx context.Context, settings setting.Sources, retryInterval time.Duration) (client.Client, error) {
	ctx, cancel := context.WithTimeoutCause(ctx, meshfedStartTimeout, fmt.Errorf("gave up after %s", meshfedStartTimeout))
	defer cancel()
	for {
		meshStack, err := newClient(ctx, settings)
		if err == nil || !isUnanswered(err) {
			return meshStack, err
		}
		slog.WarnContext(ctx, "meshStack does not answer, retrying", "retryIn", retryInterval, "error", err)
		select {
		case <-ctx.Done():
			return client.Client{}, fmt.Errorf("build the meshStack client (%w): %w", context.Cause(ctx), err)
		case <-time.After(retryInterval):
		}
	}
}

func isUnanswered(err error) bool {
	if httpErr, ok := errors.AsType[client.HttpError](err); ok {
		return slices.Contains([]int{http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout}, httpErr.StatusCode)
	}
	_, isTransportErr := errors.AsType[*url.Error](err)
	return isTransportErr
}

func newClient(ctx context.Context, settings setting.Sources) (client.Client, error) {
	return auth.ResolveClient(ctx, clientOptions(settings))
}

func clientOptions(settings setting.Sources) auth.ResolveClientOptions {
	return auth.ResolveClientOptions{
		Version:        build.Version,
		GitHubRepo:     "meshcloud/building-block-runner",
		SettingSources: settings,
	}
}
