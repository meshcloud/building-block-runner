package k8sjob

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"

	meshapi "github.com/meshcloud/building-block-runner/internal/meshapi"
)

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

// TestDecryptFailure_IsSilentDispatchFailure pins the marker used by KubernetesJobDispatcher
// to signal Loop's decrypt-failure quirk (§10.2/§16.8): the wrapped error must still satisfy
// the standard error contract (Error/Unwrap) as well as the marker method.
func TestDecryptFailure_IsSilentDispatchFailure(t *testing.T) {
	errBoom := errors.New("boom")
	inner := &decryptFailure{err: errBoom}
	if inner.Error() != errBoom.Error() {
		t.Errorf("expected Error() to delegate to the wrapped error, got %q", inner.Error())
	}
	if !errors.Is(inner.Unwrap(), errBoom) {
		t.Error("expected Unwrap() to return the wrapped error")
	}
	inner.SilentDispatchFailure() // must not panic; marker method only
}
