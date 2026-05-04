package meshapi

import (
	"encoding/json"
	"fmt"
	"time"
)

// RunDetailsDTO represents the full structure of a building block run returned by the meshfed API.
type RunDetailsDTO struct {
	ApiVersion string     `json:"apiVersion"`
	Kind       string     `json:"kind"`
	Metadata   RunMetaDTO `json:"metadata"`
	Spec       RunSpecDTO `json:"spec"`
	Status     string     `json:"status"`
	Links      LinksDTO   `json:"_links"`
}

type LinksDTO struct {
	Self             LinkDTO `json:"self"`
	RegisterSource   LinkDTO `json:"registerSource"`
	UpdateSource     LinkDTO `json:"updateSource"`
	MeshstackBaseUrl LinkDTO `json:"meshstackBaseUrl"`
}

type LinkDTO struct {
	Href      string `json:"href"`
	Templated bool   `json:"templated,omitempty"`
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
	ProjectIdentifier      string                      `json:"projectIdentifier,omitempty"`
	FullPlatformIdentifier string                      `json:"fullPlatformIdentifier,omitempty"`
	Inputs                 []BuildingBlockInputSpecDTO `json:"inputs"`
	ParentBuildingBlocks   []ParentBuildingBlockDTO    `json:"parentBuildingBlocks"`
}

type ParentBuildingBlockDTO struct {
	BuildingBlockUuid string `json:"buildingBlockUuid"`
	DefinitionUuid    string `json:"definitionUuid"`
}

type BuildingBlockInputSpecDTO struct {
	Key         string      `json:"key"`
	Value       interface{} `json:"value"`
	Type        string      `json:"type"`
	IsSensitive bool        `json:"isSensitive"`
	Env         bool        `json:"isEnvironment"`
}

type DefinitionSpecDTO struct {
	Uuid string                   `json:"uuid"`
	Spec DefinitionDetailsSpecDTO `json:"spec"`
}

type DefinitionDetailsSpecDTO struct {
	WorkspaceIdentifier string          `json:"workspaceIdentifier"`
	Version             int             `json:"version"`
	Implementation      json.RawMessage `json:"implementation"`
}

// ImplementationType represents the implementation type from the JSON API response.
type ImplementationType string

const (
	ImplTypeManual         ImplementationType = "MANUAL"
	ImplTypeTerraform      ImplementationType = "TERRAFORM"
	ImplTypeGitHubWorkflow ImplementationType = "GITHUB_WORKFLOW"
	ImplTypeGitLabCICD     ImplementationType = "GITLAB_CICD"
	ImplTypeAzureDevOps    ImplementationType = "AZURE_DEVOPS"
)

// implementationTypeJSON is used to extract the type field from JSON without full parsing.
type implementationTypeJSON struct {
	Type string `json:"type"`
}

// GetImplementationType returns the implementation type from the raw JSON without full parsing.
func (d *DefinitionDetailsSpecDTO) GetImplementationType() (ImplementationType, error) {
	var t implementationTypeJSON
	if err := json.Unmarshal(d.Implementation, &t); err != nil {
		return "", fmt.Errorf("failed to extract implementation type: %w", err)
	}
	return ImplementationType(t.Type), nil
}

// TerraformImplementation holds the Terraform runner implementation spec.
type TerraformImplementation struct {
	Type                       string        `json:"type"`
	TerraformVersion           string        `json:"terraformVersion"`
	RepositoryUrl              string        `json:"repositoryUrl"`
	RepositoryPath             *string       `json:"repositoryPath,omitempty"`
	RefName                    *string       `json:"refName,omitempty"`
	SshPrivateKey              *string       `json:"sshPrivateKey,omitempty"`
	KnownHost                  *KnownHostDTO `json:"knownHost,omitempty"`
	Async                      bool          `json:"async"`
	UseMeshHttpBackendFallback bool          `json:"useMeshHttpBackendFallback"`
	PreRunScript               *string       `json:"preRunScript,omitempty"`
}

type KnownHostDTO struct {
	Host     string `json:"host"`
	KeyType  string `json:"keyType"`
	KeyValue string `json:"keyValue"`
}

// GithubImplementation holds the GitHub Actions runner implementation spec.
type GithubImplementation struct {
	Type               string  `json:"type"`
	GithubBaseUrl      string  `json:"githubBaseUrl"`
	Owner              string  `json:"owner"`
	AppId              string  `json:"appId"`
	AppPem             string  `json:"appPem"`
	Repository         string  `json:"repository"`
	Branch             string  `json:"branch"`
	ApplyWorkflow      string  `json:"applyWorkflow"`
	DestroyWorkflow    *string `json:"destroyWorkflow,omitempty"`
	Async              bool    `json:"async"`
	OmitRunObjectInput bool    `json:"omitRunObjectInput"`
}

// GitlabImplementation holds the GitLab CI/CD runner implementation spec.
type GitlabImplementation struct {
	Type                 string `json:"type"`
	GitlabBaseUrl        string `json:"gitlabBaseUrl"`
	ProjectId            string `json:"projectId"`
	RefName              string `json:"refName"`
	PipelineTriggerToken string `json:"pipelineTriggerToken"`
}

// AzureDevOpsImplementation holds the Azure DevOps runner implementation spec.
type AzureDevOpsImplementation struct {
	Type                string  `json:"type"`
	AzureDevOpsBaseUrl  string  `json:"azureDevOpsBaseUrl"`
	Organization        string  `json:"organization"`
	Project             string  `json:"project"`
	PipelineId          string  `json:"pipelineId"`
	PersonalAccessToken string  `json:"personalAccessToken"`
	Async               bool    `json:"async"`
	RefName             *string `json:"refName,omitempty"`
}

// RunInfo contains key information extracted from a RunDetailsDTO.
type RunInfo struct {
	Uuid                             string
	WorkspaceIdentifier              string
	BuildingBlockDefinitionUuid      string
	BuildingBlockDefinitionWorkspace string
}

// GetRunInfo extracts key information from the run details.
func (dto *RunDetailsDTO) GetRunInfo() RunInfo {
	return RunInfo{
		Uuid:                             dto.Metadata.Uuid,
		WorkspaceIdentifier:              dto.Spec.BuildingBlock.Spec.WorkspaceIdentifier,
		BuildingBlockDefinitionUuid:      dto.Spec.Definition.Uuid,
		BuildingBlockDefinitionWorkspace: dto.Spec.Definition.Spec.WorkspaceIdentifier,
	}
}

// ParseRunDetails parses run JSON bytes into RunDetailsDTO.
func ParseRunDetails(data []byte) (*RunDetailsDTO, error) {
	var runDetails RunDetailsDTO
	if err := json.Unmarshal(data, &runDetails); err != nil {
		return nil, err
	}
	return &runDetails, nil
}

// RunUpdateResponseDTO is returned by the status PATCH endpoint and signals whether the run was aborted.
type RunUpdateResponseDTO struct {
	Abort bool `json:"runAborted"`
}

// StepStatusUpdateDTO updates the status of a single step in a PATCH status update.
// Outputs is optional — only runners that produce step outputs (e.g. Terraform) populate it.
type StepStatusUpdateDTO struct {
	Id            string               `json:"id"`
	DisplayName   string               `json:"displayName"`
	Status        *string              `json:"status,omitempty"`
	UserMessage   *string              `json:"userMessage,omitempty"`
	SystemMessage *string              `json:"systemMessage,omitempty"`
	Outputs       map[string]OutputDTO `json:"outputs,omitempty"`
}

// OutputDTO represents the value and metadata of a step output.
// Type is a plain string so the shared library stays agnostic of runner-specific type enums.
type OutputDTO struct {
	Value     any    `json:"value"`
	Type      string `json:"type"`
	Sensitive bool   `json:"isSensitive"`
}

// RegistrationDTO is sent via POST to register a source for a building block run.
type RegistrationDTO struct {
	Source SourceDTO             `json:"source"`
	Steps  []StepRegistrationDTO `json:"steps"`
}

// SourceDTO identifies the source reporting status.
type SourceDTO struct {
	Id          string  `json:"id"`
	ExternalId  *string `json:"externalId,omitempty"`
	ExternalUrl *string `json:"externalUrl,omitempty"`
}

// StepRegistrationDTO registers a step as part of source registration.
type StepRegistrationDTO struct {
	Id          string  `json:"id"`
	DisplayName string  `json:"displayName"`
	Status      *string `json:"status"`
}

// ==================== Status Update DTOs ====================

// RunType identifies which runner type produced a status update.
type RunType string

const (
	RunTypeTerraform      RunType = "TERRAFORM"
	RunTypeGitHubWorkflow RunType = "GITHUB_WORKFLOW"
	RunTypeGitLabCICD     RunType = "GITLAB_CICD"
	RunTypeAzureDevOps    RunType = "AZURE_DEVOPS"
	RunTypeManual         RunType = "MANUAL"
)

// StatusUpdateDTO is sent via PATCH by simple runners (e.g. the run-controller) to update the status of a run.
type StatusUpdateDTO struct {
	Status  *string              `json:"status"`
	Summary *string              `json:"summary"`
	Steps   []StepStatusUpdateDTO `json:"steps"`
}

// RunStatusUpdateDTO is sent via PATCH by runners that produce full step outputs (e.g. the Terraform runner).
type RunStatusUpdateDTO struct {
	BlockRunId string               `json:"blockRunId"`
	Source     string               `json:"source"`
	Type       RunType              `json:"type"`
	Status     *string              `json:"status"`
	CreatedOn  time.Time            `json:"createdOn"`
	Summary    *string              `json:"summary"`
	Steps      []StepStatusUpdateDTO `json:"steps"`
}

// ==================== Runner Implementation Types ====================

// RunnerImplementationType represents the runner type used in configuration (application.yml).
type RunnerImplementationType string

const (
	RunnerTypeManual              RunnerImplementationType = "MANUAL"
	RunnerTypeTerraform           RunnerImplementationType = "TERRAFORM"
	RunnerTypeGitHubWorkflow      RunnerImplementationType = "GITHUB_WORKFLOW"
	RunnerTypeGitLabPipeline      RunnerImplementationType = "GITLAB_PIPELINE"
	RunnerTypeAzureDevOpsPipeline RunnerImplementationType = "AZURE_DEVOPS_PIPELINE"
)

// ToRunnerType maps an ImplementationType to the corresponding RunnerImplementationType.
func ToRunnerType(implType ImplementationType) RunnerImplementationType {
	switch implType {
	case ImplTypeGitLabCICD:
		return RunnerTypeGitLabPipeline
	case ImplTypeAzureDevOps:
		return RunnerTypeAzureDevOpsPipeline
	default:
		// For MANUAL, TERRAFORM, and GITHUB_WORKFLOW the string values match exactly.
		return RunnerImplementationType(implType)
	}
}

// ==================== Runner Registration DTOs ====================

// MeshBuildingBlockRunnerDTO represents the meshObject for a building block runner.
type MeshBuildingBlockRunnerDTO struct {
	ApiVersion string                         `json:"apiVersion"`
	Kind       string                         `json:"kind"`
	Metadata   MeshBuildingBlockRunnerMetaDTO `json:"metadata"`
	Spec       MeshBuildingBlockRunnerSpecDTO `json:"spec"`
}

// MeshBuildingBlockRunnerMetaDTO contains metadata for a runner.
type MeshBuildingBlockRunnerMetaDTO struct {
	Uuid             string `json:"uuid"`
	OwnedByWorkspace string `json:"ownedByWorkspace"`
}

// MeshBuildingBlockRunnerSpecDTO contains the specification for a runner (for PUT/POST).
type MeshBuildingBlockRunnerSpecDTO struct {
	DisplayName                string  `json:"displayName"`
	PublicKey                  string  `json:"publicKey"`
	ImplementationType         string  `json:"implementationType"`
	WorkloadIdentityFederation *WifDTO `json:"workloadIdentityFederation,omitempty"`
}

// WifDTO represents the Workload Identity Federation configuration.
type WifDTO struct {
	Issuer  string       `json:"issuer"`
	Subject string       `json:"subject"`
	Gcp     *GcpWifDTO   `json:"gcp,omitempty"`
	Aws     *AwsWifDTO   `json:"aws,omitempty"`
	Azure   *AzureWifDTO `json:"azure,omitempty"`
}

// GcpWifDTO represents GCP Workload Identity Federation configuration.
type GcpWifDTO struct {
	Audience  string `json:"audience"`
	TokenPath string `json:"tokenPath"`
}

// AwsWifDTO represents AWS Workload Identity Federation configuration.
type AwsWifDTO struct {
	Audience  string `json:"audience"`
	TokenPath string `json:"tokenPath"`
}

// AzureWifDTO represents Azure Workload Identity Federation configuration.
type AzureWifDTO struct {
	Audience  string `json:"audience"`
	TokenPath string `json:"tokenPath"`
}
