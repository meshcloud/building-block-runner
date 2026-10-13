package inprocess

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/dispatcher"
	"github.com/meshcloud/building-block-runner/internal/http/meshapi"
)

type executeFunc func(ctx context.Context, run dispatcher.Run, api meshapi.MeshRunnerRunClient) error

type executors map[config.RunnerType]executeFunc

// Filled by the executor files the build tags select.
var executorFactories = map[config.RunnerType]func(cfg config.Config) (executeFunc, error){}

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

// A binary that links a single executor runs only its runner type.
func defaultForTypes() config.RunnerTypes {
	if len(executorFactories) != 1 {
		return nil
	}
	return slices.Collect(maps.Keys(executorFactories))
}
