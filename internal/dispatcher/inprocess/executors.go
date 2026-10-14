package inprocess

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"log"
	"os"
	"slices"

	"github.com/meshcloud/meshstack-cli/client"

	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/dispatcher"
	"github.com/meshcloud/building-block-runner/manual-block-runner/manual"
	"github.com/meshcloud/building-block-runner/tf-block-runner/tfrun"
)

type executeFunc func(ctx context.Context, run dispatcher.Run, api client.MeshRunnerRunClient) error

type executors map[config.RunnerType]executeFunc

var executorFactories = map[config.RunnerType]func(cfg config.Config) (executeFunc, error){
	config.RunnerTypeTf:     newTfExecutor,
	config.RunnerTypeManual: newManualExecutor,
}

// For ALL, a runner type without executor fails its runs when they start.
func newExecutors(cfg config.Config) (executors, error) {
	forAll := slices.Contains(cfg.Dispatcher.ForTypes, config.RunnerTypeAll)
	executors := executors{}
	for _, runnerType := range cfg.Dispatcher.ForTypes.Expand() {
		newExecutor, ok := executorFactories[runnerType]
		switch {
		case !ok && forAll:
			continue
		case !ok:
			return nil, fmt.Errorf("the in-process dispatcher cannot run %s runs", runnerType)
		}
		execute, err := newExecutor(cfg)
		if err != nil {
			return nil, fmt.Errorf("%s executor: %w", runnerType, err)
		}
		executors[runnerType] = execute
	}
	return executors, nil
}

func newTfExecutor(cfg config.Config) (executeFunc, error) {
	if err := cfg.ValidateTf(); err != nil {
		return nil, err
	}
	tfrun.AppConfig = tfrun.TfRunnerConfig{
		TfCommandTimeoutMins:  int(cfg.Dispatcher.Timeout.Minutes()),
		TfParentWorkingDir:    cfg.Tf.TfParentWorkingDir,
		TfInstallDir:          cfg.Tf.TfInstallDir,
		SkipHostKeyValidation: cfg.Tf.SkipHostKeyValidation,
		WsTimeoutMins:         int(cfg.Tf.WsTimeout.Minutes()),
		InitTimeoutMins:       int(cfg.Tf.InitTimeout.Minutes()),
		RunnerUuid:            cfg.Uuid.String(),
		RunApiBackend:         tfrun.RunApiConfig{Url: cfg.Api.Url.String()},
	}
	tfBinaries, err := tfrun.NewTfBin(tfrun.AppConfig.TfInstallDir, os.Stdout)
	if err != nil {
		return nil, err
	}
	logger := log.New(os.Stdout, "[TF RUNNER] ", log.LstdFlags)
	return func(ctx context.Context, run dispatcher.Run, _ client.MeshRunnerRunClient) error {
		runJson, err := json.Marshal(run)
		if err != nil {
			return err
		}
		err = tfrun.ExecuteDecryptedRun(ctx, logger, runJson, tfBinaries)
		if errors.Is(err, tfrun.ErrFailureReported) {
			return fmt.Errorf("%w: %w", err, dispatcher.ErrFailureReported)
		}
		return err
	}, nil
}

func newManualExecutor(cfg config.Config) (executeFunc, error) {
	var manualConfig manual.Config
	manualConfig.Uuid = cfg.Uuid.String()
	manualConfig.Api.Url = cfg.Api.Url.String()
	return func(ctx context.Context, run dispatcher.Run, _ client.MeshRunnerRunClient) error {
		runJson, err := json.Marshal(run)
		if err != nil {
			return err
		}
		return manual.ExecuteRunJson(ctx, manualConfig, runJson)
	}, nil
}
