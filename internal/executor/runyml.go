package executor

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"os"

	"github.com/goccy/go-yaml"
	"github.com/meshcloud/meshstack-cli/client"
)

// DecodeRunYaml skips the YAML conversion for JSON, as it turns an integer beyond 64 bits into a string.
func DecodeRunYaml(data []byte) (run client.MeshRunnerRun, err error) {
	runJson := data
	if !jsontext.Value(data).IsValid() {
		if runJson, err = yaml.YAMLToJSON(data); err != nil {
			return
		}
	}
	err = json.Unmarshal(runJson, &run)
	return
}

func ReadRunYaml(path string) (client.MeshRunnerRun, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return client.MeshRunnerRun{}, err
	}
	run, err := DecodeRunYaml(data)
	if err != nil {
		return client.MeshRunnerRun{}, fmt.Errorf("run file %s: %w", path, err)
	}
	return run, nil
}

func encodeWithoutImplementation(run client.MeshRunnerRun) ([]byte, error) {
	// The implementation holds the runner's secrets, such as the app key or the trigger token.
	run.Spec.Definition.Spec.Implementation = client.MeshRunnerRunImplementation{Type: run.Spec.Definition.Spec.Implementation.Type}
	return json.Marshal(run)
}
