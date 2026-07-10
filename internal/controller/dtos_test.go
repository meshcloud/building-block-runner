package controller

import (
	"testing"

	meshapi "github.com/meshcloud/building-block-runner/internal/meshapi"
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

	dto := BuildRunnerRegistrationDTO("test-namespace", "")

	if dto.Spec.ImplementationType != string(meshapi.RunnerTypeAll) {
		t.Errorf("expected implementationType %q, got %q", meshapi.RunnerTypeAll, dto.Spec.ImplementationType)
	}
}

func TestBuildRunnerRegistrationDTO_UsesControllerUUID(t *testing.T) {
	cleanup := setupAppConfigForDTOTests()
	defer cleanup()

	dto := BuildRunnerRegistrationDTO("test-namespace", "")

	if dto.Metadata.Uuid != "test-controller-uuid" {
		t.Errorf("expected UUID %q, got %q", "test-controller-uuid", dto.Metadata.Uuid)
	}
}

func TestBuildRunnerRegistrationDTO_UsesPublicKey(t *testing.T) {
	cleanup := setupAppConfigForDTOTests()
	defer cleanup()

	dto := BuildRunnerRegistrationDTO("test-namespace", "")

	if dto.Spec.PublicKey != "test-public-key" {
		t.Errorf("expected publicKey %q, got %q", "test-public-key", dto.Spec.PublicKey)
	}
}

func TestBuildRunnerRegistrationDTO_OwnedByWorkspace(t *testing.T) {
	cleanup := setupAppConfigForDTOTests()
	defer cleanup()

	dto := BuildRunnerRegistrationDTO("test-namespace", "")

	if dto.Metadata.OwnedByWorkspace != "test-workspace" {
		t.Errorf("expected OwnedByWorkspace %q, got %q", "test-workspace", dto.Metadata.OwnedByWorkspace)
	}
}

func TestBuildRunnerRegistrationDTO_DisplayName(t *testing.T) {
	cleanup := setupAppConfigForDTOTests()
	defer cleanup()

	dto := BuildRunnerRegistrationDTO("test-namespace", "")

	if dto.Spec.DisplayName != "Test Controller" {
		t.Errorf("expected DisplayName %q, got %q", "Test Controller", dto.Spec.DisplayName)
	}
}

func TestBuildRunnerRegistrationDTO_WIFConfiguredWhenOIDCIssuerSet(t *testing.T) {
	cleanup := setupAppConfigForDTOTests()
	defer cleanup()

	dto := BuildRunnerRegistrationDTO("test-namespace", "https://oidc.example.com")

	if dto.Spec.WorkloadIdentityFederation == nil {
		t.Fatal("expected WorkloadIdentityFederation to be configured when oidcIssuer is set")
	}
	if dto.Spec.WorkloadIdentityFederation.Issuer != "https://oidc.example.com" {
		t.Errorf("expected issuer %q, got %q", "https://oidc.example.com", dto.Spec.WorkloadIdentityFederation.Issuer)
	}
}

func TestBuildRunnerRegistrationDTO_WIFNotConfiguredWhenOIDCIssuerEmpty(t *testing.T) {
	cleanup := setupAppConfigForDTOTests()
	defer cleanup()

	dto := BuildRunnerRegistrationDTO("test-namespace", "")

	if dto.Spec.WorkloadIdentityFederation != nil {
		t.Error("expected WorkloadIdentityFederation to be nil when oidcIssuer is empty")
	}
}
