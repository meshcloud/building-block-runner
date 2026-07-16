package tf

import (
	"encoding/json"
	"fmt"

	meshapi "github.com/meshcloud/building-block-runner/internal/meshapi"
)

// BehaviorFor determines the run's Behavior from the raw meshapi.Run spec.
func BehaviorFor(run *meshapi.Run) (Behavior, error) {
	return DetermineBehavior(run.Spec.Behavior)
}

// ParseTerraformImplementation unmarshals the type-specific implementation payload
// carried opaquely on meshapi.Run into its Terraform shape.
func ParseTerraformImplementation(run *meshapi.Run) (meshapi.TerraformImplementation, error) {
	var impl meshapi.TerraformImplementation
	err := json.Unmarshal(run.Spec.Definition.Spec.Implementation, &impl)
	return impl, err
}

// toWorkspaceStr derives the tofu workspace name a run must select/create, straight off the
// wire-shaped meshapi.Run -- no defensive nil-placeholder logic is needed here: unlike the
// deleted tf.Run's pointer fields, WorkspaceIdentifier/ProjectIdentifier/FullPlatformIdentifier
// are plain strings that are simply empty when meshfed omits them (ProjectIdentifier and
// FullPlatformIdentifier are optional), matching what every run actually carried.
func toWorkspaceStr(run *meshapi.Run) string {
	bb := run.Spec.BuildingBlock
	return fmt.Sprintf(
		"%s.%s.%s:%s",
		bb.Spec.WorkspaceIdentifier,
		bb.Spec.ProjectIdentifier,
		bb.Spec.FullPlatformIdentifier,
		bb.Uuid,
	)
}

// VariablesFor projects the building block's input specs into the tf-internal variable map.
func VariablesFor(inputs []meshapi.BuildingBlockInputSpecDTO) map[string]*Variable {
	r := make(map[string]*Variable)
	for _, v := range inputs {
		r[v.Key] = &Variable{value: v.Value, env: v.Env, Type: DataType(v.Type)}
	}
	return r
}

func terraformImplAuthMethod(impl *meshapi.TerraformImplementation) auth {
	if impl.SshPrivateKey == nil {
		return &NoAuth{}
	}
	return &SshAuth{
		certStr:        *impl.SshPrivateKey,
		knownHostEntry: knownHostsToInternal(impl.KnownHost),
	}
}

func terraformImplToGitSource(impl *meshapi.TerraformImplementation) *GitSource {
	auth := terraformImplAuthMethod(impl)

	return &GitSource{
		url:       impl.RepositoryUrl,
		path:      impl.RepositoryPath,
		auth:      auth,
		refName:   impl.RefName,
		gitFacade: &Git{},
	}
}

func knownHostsToInternal(dto *meshapi.KnownHostDTO) *KnownHost {
	if dto == nil {
		return nil
	}

	return &KnownHost{host: dto.Host, key: dto.KeyType, value: dto.KeyValue}
}
