package process

import (
	"fmt"
	"os"
	"slices"

	"github.com/meshcloud/meshstack-cli/pkg/setting"

	"github.com/meshcloud/building-block-runner/v2/internal/config"
	"github.com/meshcloud/building-block-runner/v2/internal/dispatcher"
	"github.com/meshcloud/building-block-runner/v2/internal/executor"
)

var systemEnv = slices.Concat(executor.InheritedEnv, []string{"RUNNER_LOG_LEVEL", "RUNNER_LOG_FORMAT", "GOCOVERDIR"})

func (d *Dispatcher) childEnv(run dispatcher.Run, runnerType config.RunnerType) ([]string, error) {
	executorConfig, err := d.cfg.ExecutorConfig(runnerType)
	if err != nil {
		return nil, fmt.Errorf("encode the %s config: %w", runnerType, err)
	}
	env := []string{
		executor.EnvConfig + "=" + executorConfig,
		executor.EnvRunnerUuid + "=" + d.cfg.Uuid.String(),
		setting.Endpoint.EnvKey() + "=" + dispatcher.MeshStackEndpoint(d.cfg, run),
		setting.ApiToken.EnvKey() + "=" + run.Spec.RunToken,
	}
	for _, key := range systemEnv {
		if value, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+value)
		}
	}
	return env, nil
}
