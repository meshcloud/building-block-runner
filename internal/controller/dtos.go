package controller

import (
	"fmt"

	meshapi "github.com/meshcloud/building-block-runner/internal/meshapi"
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
		// Subject pattern for WIF validation on the API side.
		// At runtime, actual service accounts are created with format:
		// system:serviceaccount:<namespace>:workspace.<bbd-workspace>.buildingblockdefinition.<bbd-uuid>
		// See kubernetes.go CreateRunnerJob() for the actual service account creation.
		subjectPattern := fmt.Sprintf("system:serviceaccount:%s:workspace.<bbd-workspace>.buildingblockdefinition.<bbd-uuid>", namespace)
		dto.Spec.WorkloadIdentityFederation = &meshapi.WifDTO{
			Issuer:  oidcIssuer,
			Subject: subjectPattern,
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
