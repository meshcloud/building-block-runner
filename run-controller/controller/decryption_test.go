package controller

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	meshapi "github.com/meshcloud/building-block-runner/go-meshapi-client/meshapi"
)

func TestGetImplementationType_ValidTypes(t *testing.T) {
	tests := []struct {
		name     string
		implType meshapi.ImplementationType
	}{
		{"Terraform", meshapi.ImplTypeTerraform},
		{"GitHub Workflow", meshapi.ImplTypeGitHubWorkflow},
		{"GitLab CICD", meshapi.ImplTypeGitLabCICD},
		{"Azure DevOps", meshapi.ImplTypeAzureDevOps},
		{"Manual", meshapi.ImplTypeManual},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			impl := map[string]interface{}{
				"type": string(tt.implType),
			}
			implJson, _ := json.Marshal(impl)

			dto := &meshapi.DefinitionDetailsSpecDTO{
				Version:        1,
				Implementation: implJson,
			}

			result, err := dto.GetImplementationType()
			if err != nil {
				t.Errorf("expected no error, got: %v", err)
				return
			}
			if result != tt.implType {
				t.Errorf("got %v, want %v", result, tt.implType)
			}
		})
	}
}

func TestGetImplementationType_InvalidJSON(t *testing.T) {
	dto := &meshapi.DefinitionDetailsSpecDTO{
		Version:        1,
		Implementation: []byte("invalid json"),
	}

	if _, err := dto.GetImplementationType(); err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestDecryptRunDetails_UnsupportedImplementationType(t *testing.T) {
	runDetails := meshapi.RunDetailsDTO{
		ApiVersion: "v1",
		Kind:       "meshBuildingBlockRun",
		Metadata:   meshapi.RunMetaDTO{Uuid: "test-uuid"},
		Spec: meshapi.RunSpecDTO{
			RunNumber: 1,
			Behavior:  "APPLY",
			BuildingBlock: meshapi.BuildingBlockSpecDTO{
				Uuid: "bb-uuid",
				Spec: meshapi.BuildingBlockDetailsSpecDTO{
					DisplayName:         "Test BB",
					WorkspaceIdentifier: "test-workspace",
					Inputs:              []meshapi.BuildingBlockInputSpecDTO{},
				},
			},
			Definition: meshapi.DefinitionSpecDTO{
				Uuid: "def-uuid",
				Spec: meshapi.DefinitionDetailsSpecDTO{
					Version:        1,
					Implementation: json.RawMessage(`{"type": "UNKNOWN_TYPE"}`),
				},
			},
		},
	}

	runJson, _ := json.Marshal(runDetails)
	runJsonBase64 := base64.StdEncoding.EncodeToString(runJson)

	if _, err := decryptRunDetails(runJsonBase64, nil); err == nil {
		t.Error("expected error for unsupported implementation type")
	}
}

func TestDecryptRunDetails_ManualImplementation(t *testing.T) {
	runDetails := meshapi.RunDetailsDTO{
		ApiVersion: "v1",
		Kind:       "meshBuildingBlockRun",
		Metadata:   meshapi.RunMetaDTO{Uuid: "test-uuid"},
		Spec: meshapi.RunSpecDTO{
			RunNumber: 1,
			Behavior:  "APPLY",
			BuildingBlock: meshapi.BuildingBlockSpecDTO{
				Uuid: "bb-uuid",
				Spec: meshapi.BuildingBlockDetailsSpecDTO{
					DisplayName:         "Test BB",
					WorkspaceIdentifier: "test-workspace",
					Inputs:              []meshapi.BuildingBlockInputSpecDTO{},
				},
			},
			Definition: meshapi.DefinitionSpecDTO{
				Uuid: "def-uuid",
				Spec: meshapi.DefinitionDetailsSpecDTO{
					Version:        1,
					Implementation: json.RawMessage(`{"type": "MANUAL"}`),
				},
			},
		},
	}

	runJson, _ := json.Marshal(runDetails)
	runJsonBase64 := base64.StdEncoding.EncodeToString(runJson)

	result, err := decryptRunDetails(runJsonBase64, nil)
	if err != nil {
		t.Errorf("expected no error for MANUAL type, got: %v", err)
	}

	if result == "" {
		t.Error("expected non-empty result")
	}

	// Verify the result can be decoded back
	decoded, err := base64.StdEncoding.DecodeString(result)
	if err != nil {
		t.Fatalf("failed to decode result: %v", err)
	}

	var decodedRun meshapi.RunDetailsDTO
	if err := json.Unmarshal(decoded, &decodedRun); err != nil {
		t.Fatalf("failed to unmarshal decoded result: %v", err)
	}

	if decodedRun.Metadata.Uuid != "test-uuid" {
		t.Errorf("got UUID %v, want test-uuid", decodedRun.Metadata.Uuid)
	}
}

func TestDecryptRunDetails_InvalidInputs(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "invalid base64",
			input: "not-valid-base64!!!",
		},
		{
			name:  "invalid JSON",
			input: base64.StdEncoding.EncodeToString([]byte("not valid json")),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := decryptRunDetails(tt.input, nil); err == nil {
				t.Errorf("expected error for %s", tt.name)
			}
		})
	}
}

func TestImplementationType_ToRunnerType(t *testing.T) {
	tests := []struct {
		name       string
		implType   meshapi.ImplementationType
		runnerType meshapi.RunnerImplementationType
	}{
		{"Terraform maps to Terraform", meshapi.ImplTypeTerraform, meshapi.RunnerTypeTerraform},
		{"GitHub Workflow maps to GitHub Workflow", meshapi.ImplTypeGitHubWorkflow, meshapi.RunnerTypeGitHubWorkflow},
		{"GitLab CICD maps to GitLab Pipeline", meshapi.ImplTypeGitLabCICD, meshapi.RunnerTypeGitLabPipeline},
		{"Azure DevOps maps to Azure DevOps Pipeline", meshapi.ImplTypeAzureDevOps, meshapi.RunnerTypeAzureDevOpsPipeline},
		{"Manual maps to Manual", meshapi.ImplTypeManual, meshapi.RunnerTypeManual},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := meshapi.ToRunnerType(tt.implType)
			if result != tt.runnerType {
				t.Errorf("got %v, want %v", result, tt.runnerType)
			}
		})
	}
}

