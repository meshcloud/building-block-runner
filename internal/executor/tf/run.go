package tf

import (
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/meshcloud/meshstack-cli/client"

	"github.com/meshcloud/building-block-runner/internal/executor"
)

type run struct {
	executor.Run

	behavior Behavior
}

func newRun(executorRun executor.Run) (*run, error) {
	if executorRun.Spec.Definition.Spec.Implementation.Terraform == nil {
		return nil, errors.New("the run has no terraform implementation")
	}
	behavior, err := DetermineBehavior(executorRun.Spec.Behavior)
	if err != nil {
		return nil, err
	}
	return &run{Run: executorRun, behavior: behavior}, nil
}

func (r *run) terraform() *client.MeshRunnerTerraformImplementation {
	return r.Spec.Definition.Spec.Implementation.Terraform
}

func (r *run) jsonBase64() string {
	return base64.StdEncoding.EncodeToString(r.Json)
}

func (r *run) inputsByKey() map[string]client.MeshRunnerRunInput {
	inputs := make(map[string]client.MeshRunnerRunInput, len(r.Spec.BuildingBlock.Spec.Inputs))
	for _, input := range r.Spec.BuildingBlock.Spec.Inputs {
		inputs[input.Key] = input
	}
	return inputs
}

func (r *run) workspaceName() string {
	spec := r.Spec.BuildingBlock.Spec
	return fmt.Sprintf("%s.%s.%s:%s", spec.WorkspaceIdentifier, spec.ProjectIdentifier, spec.FullPlatformIdentifier, r.Spec.BuildingBlock.Uuid)
}
