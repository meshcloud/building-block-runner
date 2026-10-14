package dispatcher

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"os"

	"github.com/goccy/go-yaml"

	"github.com/meshcloud/building-block-runner/internal/crypto"
)

// DecodeRunYaml skips the YAML conversion for JSON, as it turns an integer beyond 64 bits into a string.
func DecodeRunYaml(data []byte) (run Run, err error) {
	if !jsontext.Value(data).IsValid() {
		if data, err = yaml.YAMLToJSON(data); err != nil {
			return
		}
	}
	err = json.Unmarshal(data, &run)
	return
}

func ReadRunYaml(path string) (Run, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Run{}, err
	}
	run, err := DecodeRunYaml(data)
	if err != nil {
		return Run{}, fmt.Errorf("run file %s: %w", path, err)
	}
	return run, nil
}

func RedactedRunYaml(run Run) string {
	run, err := crypto.RedactRun(run)
	if err != nil {
		return fmt.Sprintf("<cannot redact the run: %v>", err)
	}
	encoded, err := json.Marshal(run, jsontext.Multiline(true))
	if err != nil {
		return fmt.Sprintf("<cannot encode the run: %v>", err)
	}
	return string(encoded)
}
