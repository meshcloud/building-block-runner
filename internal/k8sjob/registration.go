package k8sjob

import (
	"fmt"

	meshapi "github.com/meshcloud/building-block-runner/internal/meshapi"
)

// RegistrationInfo carries the fields BuildRunnerRegistrationDTO needs -- the dissolution
// target of the former internal/controller/dtos.go (PLAN_DETAIL_05 §5): the WIF block is
// tightly coupled to the Job/ServiceAccount volume mounts this package creates (see
// createVolumes' comment), so it stays k8sjob-owned rather than moving to meshapi. Callers
// (cmd/bbrunner, D11: only main wires) supply these from the persona config instead of a
// package-level AppConfig global.
type RegistrationInfo struct {
	Uuid             string
	OwnedByWorkspace string
	DisplayName      string
	PublicKey        string
	Namespace        string
	OidcIssuer       string
}

// BuildRunnerRegistrationDTO creates the MeshBuildingBlockRunnerDTO for the run-controller.
// WIF configuration is auto-constructed from info.OidcIssuer + info.Namespace when an
// issuer was discovered. The implementation type is set to ALL to signal that this
// controller handles all run types.
func BuildRunnerRegistrationDTO(info RegistrationInfo) *meshapi.MeshBuildingBlockRunnerDTO {
	dto := &meshapi.MeshBuildingBlockRunnerDTO{
		ApiVersion: "v1-preview",
		Kind:       "meshBuildingBlockRunner",
		Metadata: meshapi.MeshBuildingBlockRunnerMetaDTO{
			Uuid:             info.Uuid,
			OwnedByWorkspace: info.OwnedByWorkspace,
		},
		Spec: meshapi.MeshBuildingBlockRunnerSpecDTO{
			DisplayName:        info.DisplayName,
			PublicKey:          info.PublicKey,
			ImplementationType: string(meshapi.RunnerTypeAll),
		},
	}

	if info.OidcIssuer != "" {
		// Subject pattern for WIF validation on the API side.
		// At runtime, actual service accounts are created with format:
		// system:serviceaccount:<namespace>:workspace.<bbd-workspace>.buildingblockdefinition.<bbd-uuid>
		// See kubernetes.go createRunnerJob() for the actual service account creation.
		subjectPattern := fmt.Sprintf("system:serviceaccount:%s:workspace.<bbd-workspace>.buildingblockdefinition.<bbd-uuid>", info.Namespace)
		dto.Spec.WorkloadIdentityFederation = &meshapi.WifDTO{
			Issuer:  info.OidcIssuer,
			Subject: subjectPattern,
			Gcp: &meshapi.GcpWifDTO{
				Audience:  fmt.Sprintf("gcp-workload-identity-provider:%s", info.Namespace),
				TokenPath: "/var/run/secrets/workload-identity/gcp/token",
			},
			Aws: &meshapi.AwsWifDTO{
				Audience:  fmt.Sprintf("aws-workload-identity-provider:%s", info.Namespace),
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
