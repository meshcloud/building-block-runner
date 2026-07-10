package tf

import (
	"encoding/base64"
	"encoding/json"
	"time"

	meshapi "github.com/meshcloud/building-block-runner/internal/meshapi"
)

// runDTOToInternal converts a RunDetailsDTO to an internal Run.
// The injected Decryptor decrypts sensitive inputs and the SSH key: certDecryptor in polling
// mode, NoopDecryptor in single-run mode (values already decrypted by the controller).
func runDTOToInternal(dto *meshapi.RunDetailsDTO, dec Decryptor) (*Run, error) {
	behavior, err := DetermineBehavior(dto.Spec.Behavior)
	if err != nil {
		return nil, err
	}

	var impl meshapi.TerraformImplementation
	if err := json.Unmarshal(dto.Spec.Definition.Spec.Implementation, &impl); err != nil {
		return nil, err
	}

	source := terraformImplToGitSource(&impl, dec)

	// Its not easy to provide this input at another position. As the whole run does not
	// exist when the inputs are setted. This would still require a two-stage process at
	// other places too and would still be hacky because a run is not doable when an input is
	// missing but the run itself should be an input -> circular dependency.
	jsonBytes, err := json.Marshal(dto)
	if err != nil {
		return nil, err
	}
	encoded := base64.StdEncoding.EncodeToString(jsonBytes)

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
		RunJsonBase64:          encoded,
		Source:                 source,
		UseMeshBackendFallback: impl.UseMeshHttpBackendFallback,
		PreRunScript:           impl.PreRunScript,
		PlanArtifactUrl:        dto.Links.PlanArtifact.Href,
	}, nil
}

// ToInternalWithoutDecryption converts a RunDetailsDTO to an internal Run without decrypting sensitive values.
// This is used when the run JSON has already been decrypted at the controller level.
func ToInternalWithoutDecryption(dto *meshapi.RunDetailsDTO, dec Decryptor) (*Run, error) {
	behavior, err := DetermineBehavior(dto.Spec.Behavior)
	if err != nil {
		return nil, err
	}

	var impl meshapi.TerraformImplementation
	if err := json.Unmarshal(dto.Spec.Definition.Spec.Implementation, &impl); err != nil {
		return nil, err
	}

	source := terraformImplToGitSource(&impl, dec)

	// Encode the run JSON for potential use
	jsonBytes, err := json.Marshal(dto)
	if err != nil {
		return nil, err
	}
	encoded := base64.StdEncoding.EncodeToString(jsonBytes)

	// Convert inputs to variables without marking them as sensitive (already decrypted)
	vars := make(map[string]*Variable)
	for _, input := range dto.Spec.BuildingBlock.Spec.Inputs {
		vars[input.Key] = &Variable{
			value:       input.Value,
			env:         input.Env,
			Type:        DataType(input.Type),
			isSensitive: false, // Mark as not sensitive since decryption already happened
		}
	}

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
		Vars:                   vars,
		RunJsonBase64:          encoded,
		Source:                 source,
		UseMeshBackendFallback: impl.UseMeshHttpBackendFallback,
		PreRunScript:           impl.PreRunScript,
		PlanArtifactUrl:        dto.Links.PlanArtifact.Href,
	}, nil
}

func toInternalVariableMap(m []meshapi.BuildingBlockInputSpecDTO) map[string]*Variable {
	r := make(map[string]*Variable)
	for _, v := range m {
		r[v.Key] = &Variable{value: v.Value, env: v.Env, isSensitive: v.IsSensitive, Type: DataType(v.Type)}
	}
	return r
}

// with this behavior the update won't update steps if the current status' step is nil.
func (status RunStatus) toExternal() meshapi.RunStatusUpdateDTO {

	// status
	runStatus := status.Status.str()

	// steps
	var steps []meshapi.StepStatusUpdateDTO = nil
	if len(status.Steps) > 0 {
		steps = make([]meshapi.StepStatusUpdateDTO, len(status.Steps))
		for i, statusStep := range status.Steps {
			output := make(map[string]meshapi.OutputDTO)
			for k, v := range statusStep.Outputs {
				output[k] = meshapi.OutputDTO{
					Value:     v.Value,
					Type:      string(v.Type),
					Sensitive: v.Sensitive,
				}
			}

			stepStatus := statusStep.Status.str()
			steps[i] = meshapi.StepStatusUpdateDTO{
				Id:            statusStep.Name,
				DisplayName:   statusStep.DisplayName,
				Status:        &stepStatus,
				UserMessage:   statusStep.UserMessage,
				SystemMessage: statusStep.SystemMessage,
				Outputs:       output,
			}
		}
	}

	// artifact: encode binary plan as base64 if present
	artifact := ""
	if len(status.Artifact) > 0 {
		artifact = base64.StdEncoding.EncodeToString(status.Artifact)
	}

	return meshapi.RunStatusUpdateDTO{
		BlockRunId: status.RunId,
		Source:     AppConfig.RunnerUuid,
		Type:       meshapi.RunTypeTerraform,
		Status:     &runStatus,
		CreatedOn:  time.Now(),
		Summary:    status.Summary,
		Steps:      steps,
		Artifact:   artifact,
	}
}

func terraformImplAuthMethod(impl *meshapi.TerraformImplementation, dec Decryptor) auth {
	if impl.SshPrivateKey == nil {
		return &NoAuth{}
	}
	return &SshAuth{
		certStr:        *impl.SshPrivateKey,
		knownHostEntry: knownHostsToInternal(impl.KnownHost),
		dec:            dec,
	}
}

func terraformImplToGitSource(impl *meshapi.TerraformImplementation, dec Decryptor) *GitSource {
	auth := terraformImplAuthMethod(impl, dec)

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
