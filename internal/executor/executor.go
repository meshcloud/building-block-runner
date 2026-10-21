package executor

import (
	"context"
	"encoding/json/v2"
	"fmt"

	"github.com/meshcloud/meshstack-cli/client"
	"github.com/meshcloud/meshstack-cli/client/http"
	"github.com/meshcloud/meshstack-cli/client/types/enum"
	"github.com/meshcloud/meshstack-cli/pkg/auth"
	"github.com/meshcloud/meshstack-cli/pkg/setting"

	"github.com/meshcloud/building-block-runner/internal/build"
	"github.com/meshcloud/building-block-runner/internal/config"
)

const (
	EnvRunJson    = "RUNNER_RUN_JSON"
	EnvConfig     = "RUNNER_CONFIG"
	EnvRunnerUuid = "RUNNER_UUID"
)

var userAgent = http.UserAgent{GitHubRepo: "meshcloud/building-block-runner", Version: build.Version}

func NewHttpClient() http.Client {
	return http.NewClient(userAgent)
}

func ResolveClient(ctx context.Context, sources setting.Sources) (client.Client, error) {
	return auth.ResolveClient(ctx, auth.ResolveClientOptions{SettingSources: sources, UserAgent: userAgent})
}

type Run struct {
	client.MeshBuildingBlockRun

	Json     []byte
	Api      client.MeshBuildingBlockRunClient
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

var runnerTypes = map[enum.Entry[client.MeshBuildingBlockRunImplementationType]]config.RunnerType{
	client.MeshBuildingBlockRunImplementationTypeTerraform:      config.RunnerTypeTf,
	client.MeshBuildingBlockRunImplementationTypeManual:         config.RunnerTypeManual,
	client.MeshBuildingBlockRunImplementationTypeGithubWorkflow: config.RunnerTypeGitHub,
	client.MeshBuildingBlockRunImplementationTypeGitlabCiCd:     config.RunnerTypeGitLab,
	client.MeshBuildingBlockRunImplementationTypeAzureDevops:    config.RunnerTypeAzureDevOps,
}

func RunnerTypeOf(run client.MeshBuildingBlockRun) (config.RunnerType, error) {
	implementationType := run.Spec.Definition.Spec.Implementation.Type
	runnerType, ok := runnerTypes[implementationType]
	if !ok {
		return "", fmt.Errorf("implementation type %s is unknown", implementationType)
	}
	return runnerType, nil
}
