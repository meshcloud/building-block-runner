package meshapi

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

// These round-trip tests migrated with DecryptRunDetails from internal/k8sjob (PLAN_DETAIL_03
// step 8: "tests move"). They reuse this package's testCryptoPair (a real CertDecryptor plus
// a matching encryptor over the checked-in fixture keypair) and encryptForTest, exercising
// the real decrypt path for every implementation-type branch.

// runDetailsWithImpl builds a minimal RunDetailsDTO carrying implRaw as its implementation
// and base64-encodes it, mirroring the shape DecryptRunDetails expects.
func runDetailsWithImpl(t *testing.T, implRaw []byte, inputs []BuildingBlockInputSpecDTO) string {
	t.Helper()
	dto := RunDetailsDTO{
		Metadata: RunMetaDTO{Uuid: "run-uuid"},
		Spec: RunSpecDTO{
			BuildingBlock: BuildingBlockSpecDTO{
				Spec: BuildingBlockDetailsSpecDTO{Inputs: inputs},
			},
			Definition: DefinitionSpecDTO{
				Spec: DefinitionDetailsSpecDTO{Implementation: implRaw},
			},
		},
	}
	raw, err := json.Marshal(dto)
	if err != nil {
		t.Fatalf("failed to marshal run details: %v", err)
	}
	return base64.StdEncoding.EncodeToString(raw)
}

func TestDecryptRunDetails_TerraformSshPrivateKey(t *testing.T) {
	dec, full := testCryptoPair(t)
	ciphertext := encryptForTest(t, full, "-----BEGIN KEY-----secret-----END KEY-----")

	impl := TerraformImplementation{Type: "TERRAFORM", SshPrivateKey: &ciphertext}
	implRaw, err := json.Marshal(impl)
	if err != nil {
		t.Fatalf("failed to marshal implementation: %v", err)
	}

	result, err := DecryptRunDetails(runDetailsWithImpl(t, implRaw, nil), dec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	decoded := decodeResult(t, result)
	var got TerraformImplementation
	if err := json.Unmarshal(decoded.Spec.Definition.Spec.Implementation, &got); err != nil {
		t.Fatalf("failed to decode decrypted implementation: %v", err)
	}
	if got.SshPrivateKey == nil || *got.SshPrivateKey != "-----BEGIN KEY-----secret-----END KEY-----" {
		t.Errorf("expected decrypted ssh private key, got %+v", got.SshPrivateKey)
	}
}

func TestDecryptRunDetails_GithubAppPem(t *testing.T) {
	dec, full := testCryptoPair(t)
	ciphertext := encryptForTest(t, full, "app-pem-secret")

	impl := GithubImplementation{Type: "GITHUB_WORKFLOW", AppPem: ciphertext}
	implRaw, err := json.Marshal(impl)
	if err != nil {
		t.Fatalf("failed to marshal implementation: %v", err)
	}

	result, err := DecryptRunDetails(runDetailsWithImpl(t, implRaw, nil), dec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	decoded := decodeResult(t, result)
	var got GithubImplementation
	if err := json.Unmarshal(decoded.Spec.Definition.Spec.Implementation, &got); err != nil {
		t.Fatalf("failed to decode decrypted implementation: %v", err)
	}
	if got.AppPem != "app-pem-secret" {
		t.Errorf("expected decrypted appPem, got %q", got.AppPem)
	}
}

func TestDecryptRunDetails_GitlabPipelineTriggerToken(t *testing.T) {
	dec, full := testCryptoPair(t)
	ciphertext := encryptForTest(t, full, "trigger-token-secret")

	impl := GitlabImplementation{Type: "GITLAB_CICD", PipelineTriggerToken: ciphertext}
	implRaw, err := json.Marshal(impl)
	if err != nil {
		t.Fatalf("failed to marshal implementation: %v", err)
	}

	result, err := DecryptRunDetails(runDetailsWithImpl(t, implRaw, nil), dec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	decoded := decodeResult(t, result)
	var got GitlabImplementation
	if err := json.Unmarshal(decoded.Spec.Definition.Spec.Implementation, &got); err != nil {
		t.Fatalf("failed to decode decrypted implementation: %v", err)
	}
	if got.PipelineTriggerToken != "trigger-token-secret" {
		t.Errorf("expected decrypted pipeline trigger token, got %q", got.PipelineTriggerToken)
	}
}

func TestDecryptRunDetails_AzureDevOpsPersonalAccessToken(t *testing.T) {
	dec, full := testCryptoPair(t)
	ciphertext := encryptForTest(t, full, "pat-secret")

	impl := AzureDevOpsImplementation{Type: "AZURE_DEVOPS", PersonalAccessToken: ciphertext}
	implRaw, err := json.Marshal(impl)
	if err != nil {
		t.Fatalf("failed to marshal implementation: %v", err)
	}

	result, err := DecryptRunDetails(runDetailsWithImpl(t, implRaw, nil), dec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	decoded := decodeResult(t, result)
	var got AzureDevOpsImplementation
	if err := json.Unmarshal(decoded.Spec.Definition.Spec.Implementation, &got); err != nil {
		t.Fatalf("failed to decode decrypted implementation: %v", err)
	}
	if got.PersonalAccessToken != "pat-secret" {
		t.Errorf("expected decrypted personal access token, got %q", got.PersonalAccessToken)
	}
}

func TestDecryptRunDetails_SensitiveInputs(t *testing.T) {
	dec, full := testCryptoPair(t)
	ciphertext := encryptForTest(t, full, "input-secret")

	inputs := []BuildingBlockInputSpecDTO{
		{Key: "password", Value: ciphertext, IsSensitive: true},
		{Key: "not-sensitive", Value: "plain", IsSensitive: false},
	}
	impl := TerraformImplementation{Type: "TERRAFORM"}
	implRaw, err := json.Marshal(impl)
	if err != nil {
		t.Fatalf("failed to marshal implementation: %v", err)
	}

	result, err := DecryptRunDetails(runDetailsWithImpl(t, implRaw, inputs), dec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	decoded := decodeResult(t, result)
	got := decoded.Spec.BuildingBlock.Spec.Inputs
	if len(got) != 2 {
		t.Fatalf("expected 2 inputs, got %d", len(got))
	}
	if got[0].Value != "input-secret" {
		t.Errorf("expected decrypted sensitive input, got %v", got[0].Value)
	}
	if got[1].Value != "plain" {
		t.Errorf("expected non-sensitive input untouched, got %v", got[1].Value)
	}
}

func TestDecryptRunDetails_DecryptFailure_WrongKeyIsAnError(t *testing.T) {
	dec, _ := testCryptoPair(t)
	// A ciphertext that isn't validly encrypted for this key pair must surface as an error,
	// not be silently passed through.
	impl := TerraformImplementation{Type: "TERRAFORM", SshPrivateKey: strPtr("not-actually-encrypted")}
	implRaw, err := json.Marshal(impl)
	if err != nil {
		t.Fatalf("failed to marshal implementation: %v", err)
	}

	if _, err = DecryptRunDetails(runDetailsWithImpl(t, implRaw, nil), dec); err == nil {
		t.Fatal("expected an error decrypting a non-ciphertext value")
	}
}

func TestDecryptRunDetails_SensitiveInputDecryptFailure_IsAnError(t *testing.T) {
	dec, _ := testCryptoPair(t)
	inputs := []BuildingBlockInputSpecDTO{
		{Key: "password", Value: "not-actually-encrypted", IsSensitive: true},
	}
	impl := TerraformImplementation{Type: "TERRAFORM"}
	implRaw, err := json.Marshal(impl)
	if err != nil {
		t.Fatalf("failed to marshal implementation: %v", err)
	}

	if _, err = DecryptRunDetails(runDetailsWithImpl(t, implRaw, inputs), dec); err == nil {
		t.Fatal("expected an error decrypting a non-ciphertext sensitive input")
	}
}

func TestDecryptRunDetails_ImplementationTypeParseFailure_IsAnError(t *testing.T) {
	dec, _ := testCryptoPair(t)
	// A bare JSON number is syntactically valid (so it round-trips through the outer
	// RunDetailsDTO's json.RawMessage field untouched) but is the wrong shape for
	// GetImplementationType's {"type": "..."}  extraction, exercising the "failed to get
	// implementation type" branch.
	if _, err := DecryptRunDetails(runDetailsWithImpl(t, json.RawMessage("123"), nil), dec); err == nil {
		t.Fatal("expected an error when the implementation type cannot be parsed")
	}
}

func TestDecryptRunDetails_ImplementationUnmarshalFailure_IsAnErrorPerType(t *testing.T) {
	dec, _ := testCryptoPair(t)
	tests := []struct {
		name    string
		implRaw string
	}{
		// terraformVersion expects a string; a number fails json.Unmarshal into
		// TerraformImplementation, exercising the per-type "failed to parse ... implementation" branch.
		{"terraform", `{"type":"TERRAFORM","terraformVersion":123}`},
		{"github", `{"type":"GITHUB_WORKFLOW","appId":123}`},
		{"gitlab", `{"type":"GITLAB_CICD","projectId":123}`},
		{"azuredevops", `{"type":"AZURE_DEVOPS","pipelineId":123}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := DecryptRunDetails(runDetailsWithImpl(t, json.RawMessage(tt.implRaw), nil), dec); err == nil {
				t.Fatalf("expected an unmarshal error for %s implementation", tt.name)
			}
		})
	}
}

func strPtr(s string) *string { return &s }

func decodeResult(t *testing.T, result string) RunDetailsDTO {
	t.Helper()
	decoded, err := base64.StdEncoding.DecodeString(result)
	if err != nil {
		t.Fatalf("failed to decode base64 result: %v", err)
	}
	var dto RunDetailsDTO
	if err := json.Unmarshal(decoded, &dto); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}
	return dto
}
