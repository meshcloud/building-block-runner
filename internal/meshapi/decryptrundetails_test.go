package meshapi

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

// These tests migrated with DecryptRunDetails from internal/k8sjob. The nil-Decryptor cases
// never reach Decrypt (MANUAL has no secrets; unsupported/invalid error before any decrypt),
// so nil is a valid stand-in there.

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

	if _, err := DecryptRunDetails(runJsonBase64, nil); err == nil {
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

	result, err := DecryptRunDetails(runJsonBase64, nil)
	if err != nil {
		t.Errorf("expected no error for MANUAL type, got: %v", err)
	}
	if result == "" {
		t.Error("expected non-empty result")
	}

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
			if _, err := DecryptRunDetails(tt.input, nil); err == nil {
				t.Errorf("expected error for %s", tt.name)
			}
		})
	}
}
