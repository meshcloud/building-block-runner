//go:build tf || !manual

package inprocess

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/meshcloud/meshstack-cli/client"

	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/dispatcher"
	"github.com/meshcloud/building-block-runner/internal/logctx"
	"github.com/meshcloud/building-block-runner/tf-block-runner/tfrun"
)

func init() { executorFactories[config.RunnerTypeTf] = newTfExecutor }

func newTfExecutor(cfg config.Config) (executeFunc, error) {
	if err := cfg.ValidateTf(); err != nil {
		return nil, err
	}
	tfBinaries, err := tfrun.NewTfBin(cfg.Tf.TfInstallDir, slog.NewLogLogger(slog.Default().Handler(), slog.LevelInfo).Writer())
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, run dispatcher.Run, api client.MeshRunnerRunClient) error {
		logger := slog.NewLogLogger(logctx.Bind(ctx, slog.Default().Handler()), slog.LevelInfo)
		err := tfrun.Execute(ctx, logger, cfg, run, api, tfBinaries)
		if errors.Is(err, tfrun.ErrFailureReported) {
			return fmt.Errorf("%w: %w", err, dispatcher.ErrFailureReported)
		}
		return err
	}, nil
}
