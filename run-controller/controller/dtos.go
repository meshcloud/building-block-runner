package controller

import (
	"fmt"
	"strings"

	meshapi "github.com/meshcloud/building-block-runner/go-meshapi-client/meshapi"
	"github.com/meshcloud/building-block-runner/run-controller/keycloakwif"
)

// BuildRunnerRegistrationDTO creates the MeshBuildingBlockRunnerDTO from the global AppConfig and the given workload
// identity federation, which can be nil.
// The implementation type is set to ALL to signal that this controller handles all run types.
func BuildRunnerRegistrationDTO(wif *meshapi.WifDTO) *meshapi.MeshBuildingBlockRunnerDTO {
	dto := &meshapi.MeshBuildingBlockRunnerDTO{
		ApiVersion: "v1-preview",
		Kind:       "meshBuildingBlockRunner",
		Metadata: meshapi.MeshBuildingBlockRunnerMetaDTO{
			Uuid:             AppConfig.Uuid,
			OwnedByWorkspace: AppConfig.OwnedByWorkspace,
		},
		Spec: meshapi.MeshBuildingBlockRunnerSpecDTO{
			DisplayName:                AppConfig.DisplayName,
			PublicKey:                  AppConfig.Crypto.PublicKey,
			ImplementationType:         string(meshapi.RunnerTypeAll),
			WorkloadIdentityFederation: wif,
		},
	}
	return dto
}

// meshStack fills in the subject template per building block definition. The subject is scoped to the
// building block definition UUID and its owning workspace.
const (
	// Kubernetes puts the service account name into the sub claim.
	serviceAccountNameTemplate = "workspace.{{ workspaceIdentifier }}.buildingblockdefinition.{{ buildingBlockDefinitionUuid }}"
	keycloakSubjectTemplate    = "bbd:{{ workspaceIdentifier }}:{{ buildingBlockDefinitionUuid }}"
)

func renderSubjectTemplate(template string, runInfo meshapi.RunInfo) string {
	return strings.NewReplacer(
		"{{ workspaceIdentifier }}", runInfo.BuildingBlockDefinitionWorkspace,
		"{{ buildingBlockDefinitionUuid }}", runInfo.BuildingBlockDefinitionUuid,
	).Replace(template)
}

func registeredWif(config WorkloadIdentityFederationConfig, namespace, discoveredIssuer string) *meshapi.WifDTO {
	switch config.issuer() {
	case WifIssuerKubernetes:
		return kubernetesWif(namespace, discoveredIssuer)
	case WifIssuerKeycloak:
		return keycloakWif(config.Keycloak.issuer())
	default:
		return nil
	}
}

func kubernetesWif(namespace, oidcIssuer string) *meshapi.WifDTO {
	if oidcIssuer == "" {
		// Register no workload identity when the run-controller could not discover the cluster's OIDC issuer.
		return nil
	}
	return &meshapi.WifDTO{
		Issuer:          oidcIssuer,
		SubjectTemplate: "system:serviceaccount:" + namespace + ":" + serviceAccountNameTemplate,
		Gcp: &meshapi.GcpWifDTO{
			Audience:  fmt.Sprintf("gcp-workload-identity-provider:%s", namespace),
			TokenPath: "/var/run/secrets/workload-identity/gcp/token",
		},
		Aws: &meshapi.AwsWifDTO{
			Audience:  fmt.Sprintf("aws-workload-identity-provider:%s", namespace),
			TokenPath: "/var/run/secrets/workload-identity/aws/token",
		},
		Azure: &meshapi.AzureWifDTO{
			Audience:  "api://AzureADTokenExchange",
			TokenPath: "/var/run/secrets/workload-identity/azure/token",
		},
	}
}

// keycloakWif registers the same token file and audience for every cloud, because the run-controller mints one
// Keycloak token per run.
func keycloakWif(issuer string) *meshapi.WifDTO {
	return &meshapi.WifDTO{
		Issuer:          issuer,
		SubjectTemplate: keycloakSubjectTemplate,
		Gcp:             &meshapi.GcpWifDTO{Audience: keycloakwif.Audience, TokenPath: wifTokenPath},
		Aws:             &meshapi.AwsWifDTO{Audience: keycloakwif.Audience, TokenPath: wifTokenPath},
		Azure:           &meshapi.AzureWifDTO{Audience: keycloakwif.Audience, TokenPath: wifTokenPath},
	}
}
