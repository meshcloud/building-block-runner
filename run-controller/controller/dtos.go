package controller

import (
	"encoding/json"
	"fmt"
)

// RunDetailsDTO represents the full structure of a building block run
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

// RunnerImplementationType represents the runner type used in configuration (application.yml)
// This is used to identify which runner should execute a building block
type RunnerImplementationType string

const (
	RunnerTypeManual              RunnerImplementationType = "MANUAL"
	RunnerTypeTerraform           RunnerImplementationType = "TERRAFORM"
	RunnerTypeGitHubWorkflow      RunnerImplementationType = "GITHUB_WORKFLOW"
	RunnerTypeGitLabPipeline      RunnerImplementationType = "GITLAB_PIPELINE"
	RunnerTypeAzureDevOpsPipeline RunnerImplementationType = "AZURE_DEVOPS_PIPELINE"
)

// ImplementationType represents the implementation type from the JSON API response
// This comes from the backend and uses different names for some types (e.g., GITLAB_CICD vs GITLAB_PIPELINE)
type ImplementationType string

const (
	ImplTypeManual         ImplementationType = "MANUAL"
	ImplTypeTerraform      ImplementationType = "TERRAFORM"
	ImplTypeGitHubWorkflow ImplementationType = "GITHUB_WORKFLOW"
	ImplTypeGitLabCICD     ImplementationType = "GITLAB_CICD"
	ImplTypeAzureDevOps    ImplementationType = "AZURE_DEVOPS"
)

// ToRunnerType maps an ImplementationType from JSON to the corresponding RunnerImplementationType
func (t ImplementationType) ToRunnerType() RunnerImplementationType {
	switch t {
	case ImplTypeGitLabCICD:
		return RunnerTypeGitLabPipeline
	case ImplTypeAzureDevOps:
		return RunnerTypeAzureDevOpsPipeline
	default:
		// For MANUAL, TERRAFORM, and GITHUB_WORKFLOW, the string values are identical between
		// ImplementationType and RunnerImplementationType, so this conversion is type-safe.
		// Note: the types are different, and this only works because the string values match exactly.
		return RunnerImplementationType(t)
	}
}

// implementationTypeJSON is used internally to extract the type field from JSON
type implementationTypeJSON struct {
	Type string `json:"type"`
}

// GetImplementationType returns the implementation type without fully parsing
func (d *DefinitionDetailsSpecDTO) GetImplementationType() (ImplementationType, error) {
	var t implementationTypeJSON
	if err := json.Unmarshal(d.Implementation, &t); err != nil {
		return "", err
	}
	return ImplementationType(t.Type), nil
}

// Terraform implementation
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

// GitHub implementation
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

// GitLab implementation
type GitlabImplementation struct {
	Type                 string `json:"type"`
	GitlabBaseUrl        string `json:"gitlabBaseUrl"`
	ProjectId            string `json:"projectId"`
	RefName              string `json:"refName"`
	PipelineTriggerToken string `json:"pipelineTriggerToken"`
}

// Azure DevOps implementation
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

// RunInfo contains key information extracted from run details for job creation and logging
type RunInfo struct {
	Uuid                             string
	WorkspaceIdentifier              string
	BuildingBlockDefinitionUuid      string
	BuildingBlockDefinitionWorkspace string
}

// GetRunInfo extracts key information from the run details
func (dto *RunDetailsDTO) GetRunInfo() RunInfo {
	return RunInfo{
		Uuid:                             dto.Metadata.Uuid,
		WorkspaceIdentifier:              dto.Spec.BuildingBlock.Spec.WorkspaceIdentifier,
		BuildingBlockDefinitionUuid:      dto.Spec.Definition.Uuid,
		BuildingBlockDefinitionWorkspace: dto.Spec.Definition.Spec.WorkspaceIdentifier,
	}
}

// parseRunDetails parses run JSON bytes into RunDetailsDTO
func parseRunDetails(data []byte) (*RunDetailsDTO, error) {
	var runDetails RunDetailsDTO
	if err := json.Unmarshal(data, &runDetails); err != nil {
		return nil, err
	}
	return &runDetails, nil
}

// ==================== Status Reporting DTOs ====================
// These DTOs are used for reporting run errors back to the meshfed API

// SourceRegistrationDTO is sent via POST to register the run-controller as a status source for a run
type SourceRegistrationDTO struct {
	Source SourceDTO             `json:"source"`
	Steps  []StepRegistrationDTO `json:"steps"`
}

// SourceDTO identifies the source reporting status
type SourceDTO struct {
	Id string `json:"id"`
}

// StepRegistrationDTO registers a step as part of source registration
type StepRegistrationDTO struct {
	Id          string  `json:"id"`
	DisplayName string  `json:"displayName"`
	Status      *string `json:"status"`
}

// StatusUpdateDTO is sent via PATCH to update the status of a run
type StatusUpdateDTO struct {
	Status  *string         `json:"status"`
	Summary *string         `json:"summary"`
	Steps   []StepUpdateDTO `json:"steps"`
}

// StepUpdateDTO updates the status of a single step
type StepUpdateDTO struct {
	Id            string  `json:"id"`
	DisplayName   string  `json:"displayName"`
	Status        *string `json:"status"`
	UserMessage   *string `json:"userMessage"`
	SystemMessage *string `json:"systemMessage"`
}

// ==================== MeshBuildingBlockRunner DTOs ====================
// These DTOs are used for self-registering runners via the meshObject API

// MeshBuildingBlockRunnerDTO represents the meshObject for a building block runner
type MeshBuildingBlockRunnerDTO struct {
	ApiVersion string                         `json:"apiVersion"`
	Kind       string                         `json:"kind"`
	Metadata   MeshBuildingBlockRunnerMetaDTO `json:"metadata"`
	Spec       MeshBuildingBlockRunnerSpecDTO `json:"spec"`
}

// MeshBuildingBlockRunnerMetaDTO contains metadata for a runner
type MeshBuildingBlockRunnerMetaDTO struct {
	Uuid             string `json:"uuid"`
	OwnedByWorkspace string `json:"ownedByWorkspace"`
}

// MeshBuildingBlockRunnerSpecDTO contains the specification for a runner (for PUT)
type MeshBuildingBlockRunnerSpecDTO struct {
	DisplayName                string  `json:"displayName"`
	PublicKey                  string  `json:"publicKey"`
	ImplementationType         string  `json:"implementationType"`
	WorkloadIdentityFederation *WifDTO `json:"workloadIdentityFederation,omitempty"`
}

// WifDTO represents the Workload Identity Federation configuration
type WifDTO struct {
	Issuer  string       `json:"issuer"`
	Subject string       `json:"subject"`
	Gcp     *GcpWifDTO   `json:"gcp,omitempty"`
	Aws     *AwsWifDTO   `json:"aws,omitempty"`
	Azure   *AzureWifDTO `json:"azure,omitempty"`
}

// GcpWifDTO represents GCP Workload Identity Federation configuration
type GcpWifDTO struct {
	Audience  string `json:"audience"`
	TokenPath string `json:"tokenPath"`
}

// AwsWifDTO represents AWS Workload Identity Federation configuration
type AwsWifDTO struct {
	Audience  string `json:"audience"`
	TokenPath string `json:"tokenPath"`
}

// AzureWifDTO represents Azure Workload Identity Federation configuration
type AzureWifDTO struct {
	Audience  string `json:"audience"`
	TokenPath string `json:"tokenPath"`
}

// BuildRunnerRegistrationDTO creates the MeshBuildingBlockRunnerDTO from RunnerConfig
// WIF configuration is auto-constructed based on the controller's oidcIssuer and namespace
func BuildRunnerRegistrationDTO(runner *RunnerConfig, namespace string, oidcIssuer string) *MeshBuildingBlockRunnerDTO {
	dto := &MeshBuildingBlockRunnerDTO{
		ApiVersion: "v1-preview",
		Kind:       "meshBuildingBlockRunner",
		Metadata: MeshBuildingBlockRunnerMetaDTO{
			Uuid:             runner.Uuid,
			OwnedByWorkspace: runner.OwnedByWorkspace,
		},
		Spec: MeshBuildingBlockRunnerSpecDTO{
			DisplayName:        runner.DisplayName,
			PublicKey:          runner.Crypto.PublicKey,
			ImplementationType: runner.ImplementationType,
		},
	}

	// Auto-construct WIF configuration if OIDC issuer is configured
	// The WIF configuration follows the Kubernetes service account naming conventions:
	// - Subject pattern uses a placeholder <bbd-uuid> that will be matched by the meshObject API
	//   because the actual BBD UUIDs are not known at runner registration time.
	//   The actual service accounts created at runtime (in kubernetes.go) will have real BBD UUIDs.
	// - Token paths are fixed conventions for the cloud providers
	// - Audiences follow standard conventions for each cloud provider
	if oidcIssuer != "" {
		// Subject pattern for WIF validation on the API side.
		// At runtime, actual service accounts are created with format:
		// system:serviceaccount:<namespace>:workspace.<bbd-workspace>.buildingblockdefinition.<bbd-uuid>
		// See kubernetes.go CreateRunnerJob() for the actual service account creation.
		// TODO: Consider splitting into subjectBase (system:serviceaccount:<namespace>:) and computed suffix
		//       to avoid placeholder replacement and make the pattern more explicit.
		subjectPattern := fmt.Sprintf("system:serviceaccount:%s:workspace.<bbd-workspace>.buildingblockdefinition.<bbd-uuid>", namespace)
		dto.Spec.WorkloadIdentityFederation = &WifDTO{
			Issuer:  oidcIssuer,
			Subject: subjectPattern,
			Gcp: &GcpWifDTO{
				Audience:  fmt.Sprintf("gcp-workload-identity-provider:%s", namespace),
				TokenPath: "/var/run/secrets/workload-identity/gcp/token",
			},
			Aws: &AwsWifDTO{
				Audience:  fmt.Sprintf("aws-workload-identity-provider:%s", namespace),
				TokenPath: "/var/run/secrets/workload-identity/aws/token",
			},
			Azure: &AzureWifDTO{
				Audience:  "api://AzureADTokenExchange",
				TokenPath: "/var/run/secrets/workload-identity/azure/token",
			},
		}
	}

	return dto
}
