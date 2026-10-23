package executor

import (
	"context"
	"encoding/json/v2"
	"fmt"

	"github.com/meshcloud/meshstack-cli/client"
	"github.com/meshcloud/meshstack-cli/client/types/enum"

	"github.com/meshcloud/building-block-runner/v2/internal/config"
)

const (
	EnvRunYml     = "RUNNER_RUN_YML"
	EnvConfig     = "RUNNER_CONFIG"
	EnvRunnerUuid = "RUNNER_UUID"
)

type Run struct {
	client.MeshRunnerRun

	Json     []byte
	Api      client.MeshRunnerRunClient
	Reporter *Reporter
}

type Executor func(ctx context.Context, run Run) error

type executorFactory func(configJSON string) (Executor, error)

var registry = map[config.RunnerType]executorFactory{}

func Register[C any](runnerType config.RunnerType, newExecutor func(cfg C) (Executor, error)) {
	registry[runnerType] = func(configJSON string) (Executor, error) {
		var cfg C
		if configJSON != "" {
			if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
				return nil, fmt.Errorf("%s: %w", EnvConfig, err)
			}
		}
		return newExecutor(cfg)
	}
}

func Registered(runnerType config.RunnerType) bool {
	_, ok := registry[runnerType]
	return ok
}

func newExecutor(runnerType config.RunnerType, configJSON string) (Executor, error) {
	newExecutor, ok := registry[runnerType]
	if !ok {
		return nil, fmt.Errorf("this runner cannot run %s runs", runnerType)
	}
	execute, err := newExecutor(configJSON)
	if err != nil {
		return nil, fmt.Errorf("%s executor: %w", runnerType, err)
	}
	return execute, nil
}

var runnerTypes = map[enum.Entry[client.MeshRunnerImplementationType]]config.RunnerType{
	client.MeshRunnerImplementationTypeTerraform:      config.RunnerTypeTf,
	client.MeshRunnerImplementationTypeManual:         config.RunnerTypeManual,
	client.MeshRunnerImplementationTypeGithubWorkflow: config.RunnerTypeGitHub,
	client.MeshRunnerImplementationTypeGitlabCiCd:     config.RunnerTypeGitLab,
	client.MeshRunnerImplementationTypeAzureDevops:    config.RunnerTypeAzureDevOps,
}

func RunnerTypeOf(run client.MeshRunnerRun) (config.RunnerType, error) {
	implementationType := run.Spec.Definition.Spec.Implementation.Type
	runnerType, ok := runnerTypes[implementationType]
	if !ok {
		return "", fmt.Errorf("implementation type %s is unknown", implementationType)
	}
	return runnerType, nil
}
