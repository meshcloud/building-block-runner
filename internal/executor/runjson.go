package executor

import (
	"encoding/json/v2"
	"fmt"
	"os"

	"github.com/meshcloud/meshstack-cli/client"

	"github.com/meshcloud/building-block-runner/v2/internal/config"
)

func readRunJson(path string) (run client.MeshBuildingBlockRun, _ error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return run, err
	}
	if err = json.Unmarshal(data, &run); err != nil {
		return run, fmt.Errorf("run file %s: %w", path, err)
	}
	return run, nil
}

// encodeRunJson leaves out the implementation for an external pipeline, as it holds the runner's secrets, such as
// the app key or the trigger token. A pre-run script gets it, as it runs next to these secrets anyway.
func encodeRunJson(run client.MeshBuildingBlockRun, runnerType config.RunnerType) ([]byte, error) {
	if runnerType != config.RunnerTypeTf {
		run.Spec.Definition.Spec.Implementation = client.MeshBuildingBlockRunImplementation{Type: run.Spec.Definition.Spec.Implementation.Type}
	}
	return json.Marshal(run)
}
