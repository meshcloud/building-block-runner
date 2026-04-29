package tfrun

import (
	"encoding/base64"
	"encoding/json"
	"time"
)

type RunDetailsDTO struct {
	ApiVersion string     `json:"apiVersion"`
	Kind       string     `json:"kind"`
	Metadata   RunMetaDTO `json:"metadata"`
	Spec       RunSpecDTO `json:"spec"`
	Links      LinksDTO   `json:"_links"`
}

type LinksDTO struct {
	RegisterSourceUrl LinkDTO `json:"registerSource"`
	UpdateSourceUrl   LinkDTO `json:"updateSource"`
	MeshstackBaseUrl  LinkDTO `json:"meshstackBaseUrl"`
}

type LinkDTO struct {
	Href string `json:"href"`
}

type RunMetaDTO struct {
	Uuid string `json:"uuid"`
}

type RunSpecDTO struct {
	RunNumber     int                  `json:"runNumber"`
	Behavior      string               `json:"behavior"`
	BuildingBlock BuildingBlockSpecDTO `json:"buildingBlock"`
	Definition    DefinitionSpecDTO    `json:"buildingBlockDefinition"`
	RunToken      string               `json:"runToken"`
}

type BuildingBlockSpecDTO struct {
	Uuid string                      `json:"uuid"`
	Spec BuildingBlockDetailsSpecDTO `json:"spec"`
}

type BuildingBlockDetailsSpecDTO struct {
	DisplayName            string                      `json:"displayName"`
	WorkspaceIdentifier    string                      `json:"workspaceIdentifier"`
	ProjectIdentifier      string                      `json:"projectIdentifier"`
	FullPlatformIdentifier string                      `json:"fullPlatformIdentifier"`
	Inputs                 []BuildingBlockInputSpecDTO `json:"inputs"`
	ParentBuildingBlocks   []ParentBuildingBlockDTO    `json:"parentBuildingBlocks"`
}

type ParentBuildingBlockDTO struct {
	BuildingBlockUuid string `json:"buildingBlockUuid"`
	DefinitionUuid    string `json:"definitionUuid"`
}

type BuildingBlockInputSpecDTO struct {
	Key         string   `json:"key"`
	Value       any      `json:"value"`
	Type        DataType `json:"type"`
	IsSensitive bool     `json:"isSensitive"`
	Env         bool     `json:"isEnvironment"`
}

type DefinitionSpecDTO struct {
	Uuid string                   `json:"uuid"`
	Spec DefinitionDetailsSpecDTO `json:"spec"`
}

type DefinitionDetailsSpecDTO struct {
	Version        int               `json:"version"`
	Implementation ImplementationDTO `json:"implementation"`
}

type ImplementationDTO struct {
	TerraformVersion           string        `json:"terraformVersion"`
	RepositoryUrl              string        `json:"repositoryUrl"`
	RepositoryPath             *string       `json:"repositoryPath"`
	RefName                    *string       `json:"refName"`
	PrivateKey                 *string       `json:"sshPrivateKey"`
	KnownHost                  *KnownHostDTO `json:"knownHost"`
	Async                      bool          `json:"async"`
	UseMeshHttpBackendFallback bool          `json:"useMeshHttpBackendFallback"`
	PreRunScript               *string       `json:"preRunScript"`
}

type KnownHostDTO struct {
	Host     string `json:"host"`
	KeyType  string `json:"keyType"`
	KeyValue string `json:"keyValue"`
}

type RunStatusUpdateDTO struct {
	BlockRunId string          `json:"blockRunId"`
	Source     string          `json:"source"`
	Type       RunType         `json:"type"`
	Status     *string         `json:"status"`
	CreatedOn  time.Time       `json:"createdOn"`
	Summary    *string         `json:"summary"`
	Steps      []StepUpdateDTO `json:"steps"`
}

type StepUpdateDTO struct {
	Id            string               `json:"id"`
	DisplayName   string               `json:"displayName"`
	Status        *string              `json:"status"`
	UserMessage   *string              `json:"userMessage"`
	SystemMessage *string              `json:"systemMessage"`
	Outputs       map[string]OutputDTO `json:"outputs"`
}

type OutputDTO struct {
	Value     any      `json:"value"`
	Type      DataType `json:"type"`
	Sensitive bool     `json:"isSensitive"`
}

type RegistrationDTO struct {
	Source SourceRegistrationDTO  `json:"source"`
	Steps  []StepsRegistrationDTO `json:"steps"`
}

type SourceRegistrationDTO struct {
	Id          string  `json:"id"`
	ExternalId  *string `json:"externalId"`
	ExternalUrl *string `json:"externalUrl"`
}

type StepsRegistrationDTO struct {
	Id          string  `json:"id"`
	DisplayName string  `json:"displayName"`
	Status      *string `json:"status"`
}

type RunUpdateResponseDTO struct {
	Abort bool `json:"runAborted"`
}

func (dto RunDetailsDTO) toInternal() (*Run, error) {
	behavior, err := DetermineBehavior(dto.Spec.Behavior)
	if err != nil {
		return nil, err
	}

	source, err := dto.Spec.Definition.Spec.Implementation.toInternal()
	if err != nil {
		return nil, err
	}

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
		TerraformVersion:       dto.Spec.Definition.Spec.Implementation.TerraformVersion,
		RequestedAt:            time.Now(),
		RunToken:               dto.Spec.RunToken,
		MeshstackBaseUrl:       dto.Links.MeshstackBaseUrl.Href,
		WorkspaceIdentifier:    &dto.Spec.BuildingBlock.Spec.WorkspaceIdentifier,
		ProjectIdentifier:      &dto.Spec.BuildingBlock.Spec.ProjectIdentifier,
		FullPlatformIdentifier: &dto.Spec.BuildingBlock.Spec.FullPlatformIdentifier,
		Behavior:               behavior,
		BuildingBlockId:        dto.Spec.BuildingBlock.Uuid,
		BuildingBlockName:      dto.Spec.BuildingBlock.Spec.DisplayName,
		IsAsync:                dto.Spec.Definition.Spec.Implementation.Async,
		Vars:                   toInternalVariableMap(dto.Spec.BuildingBlock.Spec.Inputs),
		RunJsonBase64:          encoded,
		Source:                 source,
		UseMeshBackendFallback: dto.Spec.Definition.Spec.Implementation.UseMeshHttpBackendFallback,
		PreRunScript:           dto.Spec.Definition.Spec.Implementation.PreRunScript,
	}, nil
}

// ToInternalWithoutDecryption converts DTO to internal Run structure without decrypting sensitive values.
// This is used when the run JSON has already been decrypted at the controller level.
func (dto RunDetailsDTO) ToInternalWithoutDecryption() (*Run, error) {
	behavior, err := DetermineBehavior(dto.Spec.Behavior)
	if err != nil {
		return nil, err
	}

	source, err := dto.Spec.Definition.Spec.Implementation.toInternal()
	if err != nil {
		return nil, err
	}

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
			Type:        input.Type,
			isSensitive: false, // Mark as not sensitive since decryption already happened
		}
	}

	return &Run{
		Id:                     dto.Metadata.Uuid,
		TerraformVersion:       dto.Spec.Definition.Spec.Implementation.TerraformVersion,
		RequestedAt:            time.Now(),
		RunToken:               dto.Spec.RunToken,
		MeshstackBaseUrl:       dto.Links.MeshstackBaseUrl.Href,
		WorkspaceIdentifier:    &dto.Spec.BuildingBlock.Spec.WorkspaceIdentifier,
		ProjectIdentifier:      &dto.Spec.BuildingBlock.Spec.ProjectIdentifier,
		FullPlatformIdentifier: &dto.Spec.BuildingBlock.Spec.FullPlatformIdentifier,
		Behavior:               behavior,
		BuildingBlockId:        dto.Spec.BuildingBlock.Uuid,
		BuildingBlockName:      dto.Spec.BuildingBlock.Spec.DisplayName,
		IsAsync:                dto.Spec.Definition.Spec.Implementation.Async,
		Vars:                   vars,
		RunJsonBase64:          encoded,
		Source:                 source,
		UseMeshBackendFallback: dto.Spec.Definition.Spec.Implementation.UseMeshHttpBackendFallback,
		PreRunScript:           dto.Spec.Definition.Spec.Implementation.PreRunScript,
	}, nil
}

func toInternalVariableMap(m []BuildingBlockInputSpecDTO) map[string]*Variable {
	r := make(map[string]*Variable)
	for _, v := range m {
		r[v.Key] = &Variable{value: v.Value, env: v.Env, isSensitive: v.IsSensitive, Type: v.Type}
	}
	return r
}

// with this behavior the update won't update steps if the current status' step is nil
func (status RunStatus) toExternal() (RunStatusUpdateDTO, error) {

	// status
	runStatus := status.Status.str()

	// steps
	var steps []StepUpdateDTO = nil
	if len(status.Steps) > 0 {
		steps = make([]StepUpdateDTO, len(status.Steps))
		for i, statusStep := range status.Steps {
			output := make(map[string]OutputDTO)
			for k, v := range statusStep.Outputs {
				output[k] = OutputDTO{
					Value:     v.Value,
					Type:      v.Type,
					Sensitive: v.Sensitive,
				}
			}

			stepStatus := statusStep.Status.str()
			steps[i] = StepUpdateDTO{
				Id:            statusStep.Name,
				DisplayName:   statusStep.DisplayName,
				Status:        &stepStatus,
				UserMessage:   statusStep.UserMessage,
				SystemMessage: statusStep.SystemMessage,
				Outputs:       output,
			}
		}
	}

	return RunStatusUpdateDTO{
		BlockRunId: status.RunId,
		Source:     AppConfig.RunnerUuid,
		Type:       RUN_TYPE_TF,
		Status:     &runStatus,
		CreatedOn:  time.Now(),
		Summary:    status.Summary,
		Steps:      steps,
	}, nil
}

func (dto ImplementationDTO) authMethod() (auth, error) {

	if dto.PrivateKey == nil {
		return &NoAuth{}, nil
	} else {
		return &SshAuth{
			certStr:        *dto.PrivateKey,
			knownHostEntry: knownHostsToInternal(dto.KnownHost),
		}, nil
	}
}

func (dto ImplementationDTO) toInternal() (*GitSource, error) {
	auth, err := dto.authMethod()
	if err != nil {
		return nil, err
	}

	return &GitSource{
		url:       dto.RepositoryUrl,
		path:      dto.RepositoryPath,
		auth:      auth,
		refName:   dto.RefName,
		gitFacade: &Git{},
	}, nil
}

func knownHostsToInternal(dto *KnownHostDTO) *KnownHost {
	if dto == nil {
		return nil
	}

	return &KnownHost{host: dto.Host, key: dto.KeyType, value: dto.KeyValue}
}

// API Key Authentication DTOs
