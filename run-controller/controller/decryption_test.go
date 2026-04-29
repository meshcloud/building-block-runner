package controller

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

func TestGetImplementationType_ValidTypes(t *testing.T) {
	tests := []struct {
		name     string
		implType ImplementationType
	}{
		{"Terraform", ImplTypeTerraform},
		{"GitHub Workflow", ImplTypeGitHubWorkflow},
		{"GitLab CICD", ImplTypeGitLabCICD},
		{"Azure DevOps", ImplTypeAzureDevOps},
		{"Manual", ImplTypeManual},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			impl := map[string]interface{}{
				"type": string(tt.implType),
			}
			implJson, _ := json.Marshal(impl)

			dto := &DefinitionDetailsSpecDTO{
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
	dto := &DefinitionDetailsSpecDTO{
		Version:        1,
		Implementation: []byte("invalid json"),
	}

	if _, err := dto.GetImplementationType(); err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestDecryptRunDetails_UnsupportedImplementationType(t *testing.T) {
	runDetails := RunDetailsDTO{
		ApiVersion: "v1",
		Kind:       "meshBuildingBlockRun",
		Metadata:   RunMetaDTO{Uuid: "test-uuid"},
		Spec: RunSpecDTO{
			RunNumber: 1,
			Behavior:  "APPLY",
			BuildingBlock: BuildingBlockSpecDTO{
				Uuid: "bb-uuid",
				Spec: BuildingBlockDetailsSpecDTO{
					DisplayName:         "Test BB",
					WorkspaceIdentifier: "test-workspace",
					Inputs:              []BuildingBlockInputSpecDTO{},
				},
			},
			Definition: DefinitionSpecDTO{
				Uuid: "def-uuid",
				Spec: DefinitionDetailsSpecDTO{
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
	runDetails := RunDetailsDTO{
		ApiVersion: "v1",
		Kind:       "meshBuildingBlockRun",
		Metadata:   RunMetaDTO{Uuid: "test-uuid"},
		Spec: RunSpecDTO{
			RunNumber: 1,
			Behavior:  "APPLY",
			BuildingBlock: BuildingBlockSpecDTO{
				Uuid: "bb-uuid",
				Spec: BuildingBlockDetailsSpecDTO{
					DisplayName:         "Test BB",
					WorkspaceIdentifier: "test-workspace",
					Inputs:              []BuildingBlockInputSpecDTO{},
				},
			},
			Definition: DefinitionSpecDTO{
				Uuid: "def-uuid",
				Spec: DefinitionDetailsSpecDTO{
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

	var decodedRun RunDetailsDTO
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
		implType   ImplementationType
		runnerType RunnerImplementationType
	}{
		{"Terraform maps to Terraform", ImplTypeTerraform, RunnerTypeTerraform},
		{"GitHub Workflow maps to GitHub Workflow", ImplTypeGitHubWorkflow, RunnerTypeGitHubWorkflow},
		{"GitLab CICD maps to GitLab Pipeline", ImplTypeGitLabCICD, RunnerTypeGitLabPipeline},
		{"Azure DevOps maps to Azure DevOps Pipeline", ImplTypeAzureDevOps, RunnerTypeAzureDevOpsPipeline},
		{"Manual maps to Manual", ImplTypeManual, RunnerTypeManual},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.implType.ToRunnerType()
			if result != tt.runnerType {
				t.Errorf("got %v, want %v", result, tt.runnerType)
			}
		})
	}
}
