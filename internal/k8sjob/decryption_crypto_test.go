package k8sjob

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"testing"

	meshcrypto "github.com/meshcloud/building-block-runner/internal/crypto"
	meshapi "github.com/meshcloud/building-block-runner/internal/meshapi"
)

// testCrypto loads the repo's checked-in test key pair (internal/resources/test.pem +
// test.key, the same material internal/crypto/meshcertbasedcrypto_test.go and
// internal/tf/fixtures_test.go's testCrypto prove round-trips) so this suite can encrypt
// fixture secrets and exercise the real decrypt path end to end for every implementation
// type branch, instead of only the nil-crypto MANUAL path (decryption_test.go).
func testCrypto(t *testing.T) *meshcrypto.MeshCertBasedCrypto {
	t.Helper()

	pubKey, err := os.ReadFile("../resources/test.pem")
	if err != nil {
		t.Fatalf("testCrypto: reading test.pem: %v", err)
	}

	crypto, pubKeyErr, privateKeyErr := meshcrypto.NewCertBasedCrypto("../resources/test.key", pubKey)
	if pubKeyErr != nil || privateKeyErr != nil {
		t.Fatalf("testCrypto: NewCertBasedCrypto: pubKeyErr=%v privateKeyErr=%v", pubKeyErr, privateKeyErr)
	}
	return crypto
}

func encryptForTest(t *testing.T, crypto *meshcrypto.MeshCertBasedCrypto, plaintext string) string {
	t.Helper()
	ciphertext, err := crypto.EncryptMeshCertBased(plaintext)
	if err != nil {
		t.Fatalf("encryptForTest: %v", err)
	}
	return ciphertext
}

// runDetailsWithImpl builds a minimal RunDetailsDTO carrying implRaw as its implementation
// and base64-encodes it, mirroring the shape decryptRunDetails expects.
func runDetailsWithImpl(t *testing.T, implRaw []byte, inputs []meshapi.BuildingBlockInputSpecDTO) string {
	t.Helper()
	dto := meshapi.RunDetailsDTO{
		Metadata: meshapi.RunMetaDTO{Uuid: "run-uuid"},
		Spec: meshapi.RunSpecDTO{
			BuildingBlock: meshapi.BuildingBlockSpecDTO{
				Spec: meshapi.BuildingBlockDetailsSpecDTO{Inputs: inputs},
			},
			Definition: meshapi.DefinitionSpecDTO{
				Spec: meshapi.DefinitionDetailsSpecDTO{Implementation: implRaw},
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
	crypto := testCrypto(t)
	ciphertext := encryptForTest(t, crypto, "-----BEGIN KEY-----secret-----END KEY-----")

	impl := meshapi.TerraformImplementation{Type: "TERRAFORM", SshPrivateKey: &ciphertext}
	implRaw, err := json.Marshal(impl)
	if err != nil {
		t.Fatalf("failed to marshal implementation: %v", err)
	}

	result, err := decryptRunDetails(runDetailsWithImpl(t, implRaw, nil), crypto)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	decoded := decodeResult(t, result)
	var got meshapi.TerraformImplementation
	if err := json.Unmarshal(decoded.Spec.Definition.Spec.Implementation, &got); err != nil {
		t.Fatalf("failed to decode decrypted implementation: %v", err)
	}
	if got.SshPrivateKey == nil || *got.SshPrivateKey != "-----BEGIN KEY-----secret-----END KEY-----" {
		t.Errorf("expected decrypted ssh private key, got %+v", got.SshPrivateKey)
	}
}

func TestDecryptRunDetails_GithubAppPem(t *testing.T) {
	crypto := testCrypto(t)
	ciphertext := encryptForTest(t, crypto, "app-pem-secret")

	impl := meshapi.GithubImplementation{Type: "GITHUB_WORKFLOW", AppPem: ciphertext}
	implRaw, err := json.Marshal(impl)
	if err != nil {
		t.Fatalf("failed to marshal implementation: %v", err)
	}

	result, err := decryptRunDetails(runDetailsWithImpl(t, implRaw, nil), crypto)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	decoded := decodeResult(t, result)
	var got meshapi.GithubImplementation
	if err := json.Unmarshal(decoded.Spec.Definition.Spec.Implementation, &got); err != nil {
		t.Fatalf("failed to decode decrypted implementation: %v", err)
	}
	if got.AppPem != "app-pem-secret" {
		t.Errorf("expected decrypted appPem, got %q", got.AppPem)
	}
}

func TestDecryptRunDetails_GitlabPipelineTriggerToken(t *testing.T) {
	crypto := testCrypto(t)
	ciphertext := encryptForTest(t, crypto, "trigger-token-secret")

	impl := meshapi.GitlabImplementation{Type: "GITLAB_CICD", PipelineTriggerToken: ciphertext}
	implRaw, err := json.Marshal(impl)
	if err != nil {
		t.Fatalf("failed to marshal implementation: %v", err)
	}

	result, err := decryptRunDetails(runDetailsWithImpl(t, implRaw, nil), crypto)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	decoded := decodeResult(t, result)
	var got meshapi.GitlabImplementation
	if err := json.Unmarshal(decoded.Spec.Definition.Spec.Implementation, &got); err != nil {
		t.Fatalf("failed to decode decrypted implementation: %v", err)
	}
	if got.PipelineTriggerToken != "trigger-token-secret" {
		t.Errorf("expected decrypted pipeline trigger token, got %q", got.PipelineTriggerToken)
	}
}

func TestDecryptRunDetails_AzureDevOpsPersonalAccessToken(t *testing.T) {
	crypto := testCrypto(t)
	ciphertext := encryptForTest(t, crypto, "pat-secret")

	impl := meshapi.AzureDevOpsImplementation{Type: "AZURE_DEVOPS", PersonalAccessToken: ciphertext}
	implRaw, err := json.Marshal(impl)
	if err != nil {
		t.Fatalf("failed to marshal implementation: %v", err)
	}

	result, err := decryptRunDetails(runDetailsWithImpl(t, implRaw, nil), crypto)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	decoded := decodeResult(t, result)
	var got meshapi.AzureDevOpsImplementation
	if err := json.Unmarshal(decoded.Spec.Definition.Spec.Implementation, &got); err != nil {
		t.Fatalf("failed to decode decrypted implementation: %v", err)
	}
	if got.PersonalAccessToken != "pat-secret" {
		t.Errorf("expected decrypted personal access token, got %q", got.PersonalAccessToken)
	}
}

func TestDecryptRunDetails_SensitiveInputs(t *testing.T) {
	crypto := testCrypto(t)
	ciphertext := encryptForTest(t, crypto, "input-secret")

	inputs := []meshapi.BuildingBlockInputSpecDTO{
		{Key: "password", Value: ciphertext, IsSensitive: true},
		{Key: "not-sensitive", Value: "plain", IsSensitive: false},
	}
	impl := meshapi.TerraformImplementation{Type: "TERRAFORM"}
	implRaw, err := json.Marshal(impl)
	if err != nil {
		t.Fatalf("failed to marshal implementation: %v", err)
	}

	result, err := decryptRunDetails(runDetailsWithImpl(t, implRaw, inputs), crypto)
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
	// A ciphertext that isn't validly encrypted for this key pair must surface as an error,
	// not be silently passed through.
	impl := meshapi.TerraformImplementation{Type: "TERRAFORM", SshPrivateKey: strPtr("not-actually-encrypted")}
	implRaw, err := json.Marshal(impl)
	if err != nil {
		t.Fatalf("failed to marshal implementation: %v", err)
	}

	_, err = decryptRunDetails(runDetailsWithImpl(t, implRaw, nil), testCrypto(t))
	if err == nil {
		t.Fatal("expected an error decrypting a non-ciphertext value")
	}
}

func TestDecryptRunDetails_SensitiveInputDecryptFailure_IsAnError(t *testing.T) {
	inputs := []meshapi.BuildingBlockInputSpecDTO{
		{Key: "password", Value: "not-actually-encrypted", IsSensitive: true},
	}
	impl := meshapi.TerraformImplementation{Type: "TERRAFORM"}
	implRaw, err := json.Marshal(impl)
	if err != nil {
		t.Fatalf("failed to marshal implementation: %v", err)
	}

	_, err = decryptRunDetails(runDetailsWithImpl(t, implRaw, inputs), testCrypto(t))
	if err == nil {
		t.Fatal("expected an error decrypting a non-ciphertext sensitive input")
	}
}

func TestDecryptRunDetails_ImplementationTypeParseFailure_IsAnError(t *testing.T) {
	// A bare JSON number is syntactically valid (so it round-trips through the outer
	// RunDetailsDTO's json.RawMessage field untouched) but is the wrong shape for
	// GetImplementationType's {"type": "..."}  extraction, exercising the "failed to get
	// implementation type" branch.
	_, err := decryptRunDetails(runDetailsWithImpl(t, json.RawMessage("123"), nil), testCrypto(t))
	if err == nil {
		t.Fatal("expected an error when the implementation type cannot be parsed")
	}
}

func TestDecryptRunDetails_ImplementationUnmarshalFailure_IsAnErrorPerType(t *testing.T) {
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
			_, err := decryptRunDetails(runDetailsWithImpl(t, json.RawMessage(tt.implRaw), nil), testCrypto(t))
			if err == nil {
				t.Fatalf("expected an unmarshal error for %s implementation", tt.name)
			}
		})
	}
}

func strPtr(s string) *string { return &s }

func decodeResult(t *testing.T, result string) meshapi.RunDetailsDTO {
	t.Helper()
	decoded, err := base64.StdEncoding.DecodeString(result)
	if err != nil {
		t.Fatalf("failed to decode base64 result: %v", err)
	}
	var dto meshapi.RunDetailsDTO
	if err := json.Unmarshal(decoded, &dto); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}
	return dto
}
