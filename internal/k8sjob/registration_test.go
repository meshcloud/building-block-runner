package k8sjob

import (
	"testing"

	meshapi "github.com/meshcloud/building-block-runner/internal/meshapi"
)

func testRegistrationInfo() RegistrationInfo {
	return RegistrationInfo{
		Uuid:             "test-controller-uuid",
		OwnedByWorkspace: "test-workspace",
		DisplayName:      "Test Controller",
		PublicKey:        "test-public-key",
		Namespace:        "test-namespace",
	}
}

func TestBuildRunnerRegistrationDTO_ImplementationTypeIsAll(t *testing.T) {
	dto := BuildRunnerRegistrationDTO(testRegistrationInfo())

	if dto.Spec.ImplementationType != string(meshapi.RunnerTypeAll) {
		t.Errorf("expected implementationType %q, got %q", meshapi.RunnerTypeAll, dto.Spec.ImplementationType)
	}
}

func TestBuildRunnerRegistrationDTO_UsesUuid(t *testing.T) {
	dto := BuildRunnerRegistrationDTO(testRegistrationInfo())

	if dto.Metadata.Uuid != "test-controller-uuid" {
		t.Errorf("expected UUID %q, got %q", "test-controller-uuid", dto.Metadata.Uuid)
	}
}

func TestBuildRunnerRegistrationDTO_UsesPublicKey(t *testing.T) {
	dto := BuildRunnerRegistrationDTO(testRegistrationInfo())

	if dto.Spec.PublicKey != "test-public-key" {
		t.Errorf("expected publicKey %q, got %q", "test-public-key", dto.Spec.PublicKey)
	}
}

func TestBuildRunnerRegistrationDTO_OwnedByWorkspace(t *testing.T) {
	dto := BuildRunnerRegistrationDTO(testRegistrationInfo())

	if dto.Metadata.OwnedByWorkspace != "test-workspace" {
		t.Errorf("expected OwnedByWorkspace %q, got %q", "test-workspace", dto.Metadata.OwnedByWorkspace)
	}
}

func TestBuildRunnerRegistrationDTO_DisplayName(t *testing.T) {
	dto := BuildRunnerRegistrationDTO(testRegistrationInfo())

	if dto.Spec.DisplayName != "Test Controller" {
		t.Errorf("expected DisplayName %q, got %q", "Test Controller", dto.Spec.DisplayName)
	}
}

func TestBuildRunnerRegistrationDTO_WIFConfiguredWhenOIDCIssuerSet(t *testing.T) {
	info := testRegistrationInfo()
	info.OidcIssuer = "https://oidc.example.com"

	dto := BuildRunnerRegistrationDTO(info)

	if dto.Spec.WorkloadIdentityFederation == nil {
		t.Fatal("expected WorkloadIdentityFederation to be configured when oidcIssuer is set")
	}
	if dto.Spec.WorkloadIdentityFederation.Issuer != "https://oidc.example.com" {
		t.Errorf("expected issuer %q, got %q", "https://oidc.example.com", dto.Spec.WorkloadIdentityFederation.Issuer)
	}
	if dto.Spec.WorkloadIdentityFederation.Gcp == nil || dto.Spec.WorkloadIdentityFederation.Aws == nil || dto.Spec.WorkloadIdentityFederation.Azure == nil {
		t.Error("expected all three WIF cloud blocks to be populated")
	}
}

func TestBuildRunnerRegistrationDTO_WIFNotConfiguredWhenOIDCIssuerEmpty(t *testing.T) {
	dto := BuildRunnerRegistrationDTO(testRegistrationInfo())

	if dto.Spec.WorkloadIdentityFederation != nil {
		t.Error("expected WorkloadIdentityFederation to be nil when oidcIssuer is empty")
	}
}
