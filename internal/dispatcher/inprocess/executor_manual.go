//go:build manual || !tf

package inprocess

import (
	"context"

	"github.com/meshcloud/meshstack-cli/client"

	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/dispatcher"
	"github.com/meshcloud/building-block-runner/manual-block-runner/manual"
)

func init() { executorFactories[config.RunnerTypeManual] = newManualExecutor }

func newManualExecutor(cfg config.Config) (executeFunc, error) {
	return func(ctx context.Context, run dispatcher.Run, api client.MeshRunnerRunClient) error {
		return manual.Execute(ctx, run, api, cfg.Uuid)
	}, nil
}
