package controller

import (
	"fmt"

	meshapi "github.com/meshcloud/building-block-runner/go-meshapi-client/meshapi"
)

// BuildRunnerRegistrationDTO creates the MeshBuildingBlockRunnerDTO from the global AppConfig.
// WIF configuration is auto-constructed based on the controller's oidcIssuer and namespace.
// The implementation type is set to ALL to signal that this controller handles all run types.
func BuildRunnerRegistrationDTO(namespace string, oidcIssuer string) *meshapi.MeshBuildingBlockRunnerDTO {
	dto := &meshapi.MeshBuildingBlockRunnerDTO{
		ApiVersion: "v1-preview",
		Kind:       "meshBuildingBlockRunner",
		Metadata: meshapi.MeshBuildingBlockRunnerMetaDTO{
			Uuid:             AppConfig.Uuid,
			OwnedByWorkspace: AppConfig.OwnedByWorkspace,
		},
		Spec: meshapi.MeshBuildingBlockRunnerSpecDTO{
			DisplayName:        AppConfig.DisplayName,
			PublicKey:          AppConfig.Crypto.PublicKey,
			ImplementationType: string(meshapi.RunnerTypeAll),
		},
	}

	if oidcIssuer != "" {
		// meshfed renders the placeholders per building block definition. CreateRunnerJob in kubernetes.go
		// names the service account the same way, so the rendered subject matches the token's sub claim.
		subjectTemplate := fmt.Sprintf("system:serviceaccount:%s:workspace.{{ workspaceIdentifier }}.buildingblockdefinition.{{ buildingBlockDefinitionUuid }}", namespace)
		dto.Spec.WorkloadIdentityFederation = &meshapi.WifDTO{
			Issuer:          oidcIssuer,
			SubjectTemplate: subjectTemplate,
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

	return dto
}
