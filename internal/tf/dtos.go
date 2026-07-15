package tf

import (
	"encoding/json"
	"time"

	meshapi "github.com/meshcloud/building-block-runner/internal/meshapi"
)

// RunDTOToInternal converts a RunDetailsDTO to an internal Run. Values arrive plaintext: both
// the polling path (rundecrypt.Wrap) and the controller-fed single-run path decrypt at the
// claim boundary before this mapper ever sees the DTO. It does NOT populate RunJsonBase64 --
// the run JSON handed to the pre-run script is the decrypted raw meshfed object, set by the
// caller (Handler.Execute) from cr.RawJson, so the script sees the object 1:1 rather than a
// lossy re-serialization of this typed DTO.
func RunDTOToInternal(dto *meshapi.RunDetailsDTO) (*Run, error) {
	behavior, err := DetermineBehavior(dto.Spec.Behavior)
	if err != nil {
		return nil, err
	}

	var impl meshapi.TerraformImplementation
	if err := json.Unmarshal(dto.Spec.Definition.Spec.Implementation, &impl); err != nil {
		return nil, err
	}

	source := terraformImplToGitSource(&impl)

	return &Run{
		Id:                     dto.Metadata.Uuid,
		TerraformVersion:       impl.TerraformVersion,
		RequestedAt:            time.Now(),
		RunToken:               dto.Spec.RunToken,
		MeshstackBaseUrl:       dto.Links.MeshstackBaseUrl.Href,
		WorkspaceIdentifier:    &dto.Spec.BuildingBlock.Spec.WorkspaceIdentifier,
		ProjectIdentifier:      &dto.Spec.BuildingBlock.Spec.ProjectIdentifier,
		FullPlatformIdentifier: &dto.Spec.BuildingBlock.Spec.FullPlatformIdentifier,
		Behavior:               behavior,
		BuildingBlockId:        dto.Spec.BuildingBlock.Uuid,
		BuildingBlockName:      dto.Spec.BuildingBlock.Spec.DisplayName,
		IsAsync:                impl.Async,
		Vars:                   toInternalVariableMap(dto.Spec.BuildingBlock.Spec.Inputs),
		Source:                 source,
		UseMeshBackendFallback: impl.UseMeshHttpBackendFallback,
		PreRunScript:           impl.PreRunScript,
		PlanArtifactUrl:        dto.Links.PlanArtifact.Href,
	}, nil
}

func toInternalVariableMap(m []meshapi.BuildingBlockInputSpecDTO) map[string]*Variable {
	r := make(map[string]*Variable)
	for _, v := range m {
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
