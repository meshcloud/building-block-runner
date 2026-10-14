package controller

import (
	"encoding/json"
	"testing"

	meshapi "github.com/meshcloud/building-block-runner/go-meshapi-client/meshapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupAppConfigForDTOTests() func() {
	prev := AppConfig
	AppConfig = &ControllerConfig{
		Uuid:             "test-controller-uuid",
		OwnedByWorkspace: "test-workspace",
		DisplayName:      "Test Controller",
		Namespace:        "test-namespace",
		Api: ApiConfig{
			Url:      "http://localhost:8080",
			Username: "user",
			Password: "pass",
		},
		Crypto: CryptoConfig{
			PublicKey:  "test-public-key",
			PrivateKey: "test-private-key",
		},
		Implementations: map[string]JobSpecTemplate{
			"TERRAFORM": {Image: "tf-runner:latest"},
		},
	}
	return func() { AppConfig = prev }
}

func TestBuildRunnerRegistrationDTO_ImplementationTypeIsAll(t *testing.T) {
	cleanup := setupAppConfigForDTOTests()
	defer cleanup()

	dto := BuildRunnerRegistrationDTO(kubernetesWif("test-namespace", ""))

	if dto.Spec.ImplementationType != string(meshapi.RunnerTypeAll) {
		t.Errorf("expected implementationType %q, got %q", meshapi.RunnerTypeAll, dto.Spec.ImplementationType)
	}
}

func TestBuildRunnerRegistrationDTO_UsesControllerUUID(t *testing.T) {
	cleanup := setupAppConfigForDTOTests()
	defer cleanup()

	dto := BuildRunnerRegistrationDTO(kubernetesWif("test-namespace", ""))

	if dto.Metadata.Uuid != "test-controller-uuid" {
		t.Errorf("expected UUID %q, got %q", "test-controller-uuid", dto.Metadata.Uuid)
	}
}

func TestBuildRunnerRegistrationDTO_UsesPublicKey(t *testing.T) {
	cleanup := setupAppConfigForDTOTests()
	defer cleanup()

	dto := BuildRunnerRegistrationDTO(kubernetesWif("test-namespace", ""))

	if dto.Spec.PublicKey != "test-public-key" {
		t.Errorf("expected publicKey %q, got %q", "test-public-key", dto.Spec.PublicKey)
	}
}

func TestBuildRunnerRegistrationDTO_OwnedByWorkspace(t *testing.T) {
	cleanup := setupAppConfigForDTOTests()
	defer cleanup()

	dto := BuildRunnerRegistrationDTO(kubernetesWif("test-namespace", ""))

	if dto.Metadata.OwnedByWorkspace != "test-workspace" {
		t.Errorf("expected OwnedByWorkspace %q, got %q", "test-workspace", dto.Metadata.OwnedByWorkspace)
	}
}

func TestBuildRunnerRegistrationDTO_DisplayName(t *testing.T) {
	cleanup := setupAppConfigForDTOTests()
	defer cleanup()

	dto := BuildRunnerRegistrationDTO(kubernetesWif("test-namespace", ""))

	if dto.Spec.DisplayName != "Test Controller" {
		t.Errorf("expected DisplayName %q, got %q", "Test Controller", dto.Spec.DisplayName)
	}
}

func TestBuildRunnerRegistrationDTO_WIFConfiguredWhenOIDCIssuerSet(t *testing.T) {
	cleanup := setupAppConfigForDTOTests()
	defer cleanup()

	dto := BuildRunnerRegistrationDTO(kubernetesWif("test-namespace", "https://oidc.example.com"))

	if dto.Spec.WorkloadIdentityFederation == nil {
		t.Fatal("expected WorkloadIdentityFederation to be configured when oidcIssuer is set")
	}
	if dto.Spec.WorkloadIdentityFederation.Issuer != "https://oidc.example.com" {
		t.Errorf("expected issuer %q, got %q", "https://oidc.example.com", dto.Spec.WorkloadIdentityFederation.Issuer)
	}
}

func TestBuildRunnerRegistrationDTO_WIFSubjectTemplateUsesPlaceholders(t *testing.T) {
	cleanup := setupAppConfigForDTOTests()
	defer cleanup()

	dto := BuildRunnerRegistrationDTO(kubernetesWif("test-namespace", "https://oidc.example.com"))

	expected := "system:serviceaccount:test-namespace:workspace.{{ workspaceIdentifier }}.buildingblockdefinition.{{ buildingBlockDefinitionUuid }}"
	if dto.Spec.WorkloadIdentityFederation.SubjectTemplate != expected {
		t.Errorf("expected subjectTemplate %q, got %q", expected, dto.Spec.WorkloadIdentityFederation.SubjectTemplate)
	}
}

func TestBuildRunnerRegistrationDTO_WIFSerializesSubjectTemplate(t *testing.T) {
	cleanup := setupAppConfigForDTOTests()
	defer cleanup()

	dto := BuildRunnerRegistrationDTO(kubernetesWif("test-namespace", "https://oidc.example.com"))

	body, err := json.Marshal(dto)
	if err != nil {
		t.Fatalf("failed to marshal registration DTO: %v", err)
	}

	var payload struct {
		Spec struct {
			Wif map[string]json.RawMessage `json:"workloadIdentityFederation"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("failed to unmarshal registration body: %v", err)
	}

	if _, ok := payload.Spec.Wif["subjectTemplate"]; !ok {
		t.Error("expected subjectTemplate to be sent")
	}
	if _, ok := payload.Spec.Wif["subject"]; ok {
		t.Error("expected the old subject key not to be sent")
	}
}

func TestBuildRunnerRegistrationDTO_WIFNotConfiguredWhenOIDCIssuerEmpty(t *testing.T) {
	cleanup := setupAppConfigForDTOTests()
	defer cleanup()

	dto := BuildRunnerRegistrationDTO(kubernetesWif("test-namespace", ""))

	if dto.Spec.WorkloadIdentityFederation != nil {
		t.Error("expected WorkloadIdentityFederation to be nil when oidcIssuer is empty")
	}
}

func TestRegisteredWif_KeycloakSetForEveryCloud(t *testing.T) {
	config := WorkloadIdentityFederationConfig{
		Issuer:   WifIssuerKeycloak,
		Keycloak: &KeycloakWifConfig{Url: "https://id-dev.meshstack.io", Realm: "meshcloud-dev-wif"},
	}

	wif := registeredWif(config, "test-namespace", "https://container.googleapis.com/v1/projects/p/locations/l/clusters/c")

	require.NotNil(t, wif)
	assert.Equal(t, "https://id-dev.meshstack.io/realms/meshcloud-dev-wif", wif.Issuer)
	assert.Equal(t, "bbd:{{ workspaceIdentifier }}:{{ buildingBlockDefinitionUuid }}", wif.SubjectTemplate)
	for _, cloud := range []struct{ audience, tokenPath string }{
		{wif.Gcp.Audience, wif.Gcp.TokenPath},
		{wif.Aws.Audience, wif.Aws.TokenPath},
		{wif.Azure.Audience, wif.Azure.TokenPath},
	} {
		assert.Equal(t, "meshstack-wif", cloud.audience)
		assert.Equal(t, "/var/run/secrets/workload-identity/meshstack/token", cloud.tokenPath)
	}
}

func TestRegisteredWif_KubernetesIsTheDefault(t *testing.T) {
	wif := registeredWif(WorkloadIdentityFederationConfig{}, "test-namespace", "https://oidc.example.com")

	require.NotNil(t, wif)
	assert.Equal(t, "https://oidc.example.com", wif.Issuer)
}

func TestRegisteredWif_OffRegistersNone(t *testing.T) {
	assert.Nil(t, registeredWif(WorkloadIdentityFederationConfig{Issuer: WifIssuerOff}, "test-namespace", "https://oidc.example.com"))
}
