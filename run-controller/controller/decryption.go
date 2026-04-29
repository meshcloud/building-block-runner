package controller

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/meshcloud/meshfed-release/buildingblocks/run-controller/crypto"
)

// decryptRunDetails decrypts all sensitive fields in the run details
func decryptRunDetails(runJsonBase64 string, cryptoInstance *crypto.MeshCertBasedCrypto) (string, error) {
	// Decode base64
	runJsonBytes, err := base64.StdEncoding.DecodeString(runJsonBase64)
	if err != nil {
		return "", fmt.Errorf("failed to decode base64 run JSON: %w", err)
	}

	// Parse JSON into DTO
	runDetails, err := parseRunDetails(runJsonBytes)
	if err != nil {
		return "", fmt.Errorf("failed to parse run JSON: %w", err)
	}

	// Decrypt sensitive inputs
	for i := range runDetails.Spec.BuildingBlock.Spec.Inputs {
		input := &runDetails.Spec.BuildingBlock.Spec.Inputs[i]
		if input.IsSensitive && input.Value != nil {
			// Only decrypt string-like types
			if strValue, ok := input.Value.(string); ok && strValue != "" {
				decryptedValue, err := cryptoInstance.DecryptMeshCertBased(strValue)
				if err != nil {
					return "", fmt.Errorf("failed to decrypt sensitive input '%s': %w", input.Key, err)
				}
				input.Value = decryptedValue
			}
		}
	}

	// Decrypt sensitive fields based on implementation type
	implType, err := runDetails.Spec.Definition.Spec.GetImplementationType()
	if err != nil {
		return "", fmt.Errorf("failed to get implementation type: %w", err)
	}
	implRaw := runDetails.Spec.Definition.Spec.Implementation

	switch implType {
	case ImplTypeTerraform:
		var impl TerraformImplementation
		if err := json.Unmarshal(implRaw, &impl); err != nil {
			return "", fmt.Errorf("failed to parse Terraform implementation: %w", err)
		}
		if impl.SshPrivateKey != nil && *impl.SshPrivateKey != "" {
			decryptedKey, err := cryptoInstance.DecryptMeshCertBased(*impl.SshPrivateKey)
			if err != nil {
				return "", fmt.Errorf("failed to decrypt SSH private key: %w", err)
			}
			impl.SshPrivateKey = &decryptedKey
		}
		if runDetails.Spec.Definition.Spec.Implementation, err = json.Marshal(impl); err != nil {
			return "", fmt.Errorf("failed to marshal Terraform implementation: %w", err)
		}

	case ImplTypeGitHubWorkflow:
		var impl GithubImplementation
		if err := json.Unmarshal(implRaw, &impl); err != nil {
			return "", fmt.Errorf("failed to parse GitHub implementation: %w", err)
		}
		if impl.AppPem != "" {
			decryptedPem, err := cryptoInstance.DecryptMeshCertBased(impl.AppPem)
			if err != nil {
				return "", fmt.Errorf("failed to decrypt GitHub appPem: %w", err)
			}
			impl.AppPem = decryptedPem
		}
		if runDetails.Spec.Definition.Spec.Implementation, err = json.Marshal(impl); err != nil {
			return "", fmt.Errorf("failed to marshal GitHub implementation: %w", err)
		}

	case ImplTypeGitLabCICD:
		var impl GitlabImplementation
		if err := json.Unmarshal(implRaw, &impl); err != nil {
			return "", fmt.Errorf("failed to parse GitLab implementation: %w", err)
		}
		if impl.PipelineTriggerToken != "" {
			decryptedToken, err := cryptoInstance.DecryptMeshCertBased(impl.PipelineTriggerToken)
			if err != nil {
				return "", fmt.Errorf("failed to decrypt GitLab pipeline trigger token: %w", err)
			}
			impl.PipelineTriggerToken = decryptedToken
		}
		if runDetails.Spec.Definition.Spec.Implementation, err = json.Marshal(impl); err != nil {
			return "", fmt.Errorf("failed to marshal GitLab implementation: %w", err)
		}

	case ImplTypeAzureDevOps:
		var impl AzureDevOpsImplementation
		if err := json.Unmarshal(implRaw, &impl); err != nil {
			return "", fmt.Errorf("failed to parse Azure DevOps implementation: %w", err)
		}
		if impl.PersonalAccessToken != "" {
			decryptedPat, err := cryptoInstance.DecryptMeshCertBased(impl.PersonalAccessToken)
			if err != nil {
				return "", fmt.Errorf("failed to decrypt Azure DevOps personal access token: %w", err)
			}
			impl.PersonalAccessToken = decryptedPat
		}
		if runDetails.Spec.Definition.Spec.Implementation, err = json.Marshal(impl); err != nil {
			return "", fmt.Errorf("failed to marshal Azure DevOps implementation: %w", err)
		}

	case ImplTypeManual:
		// Manual implementations have no secrets to decrypt
		// Nothing to do here

	default:
		return "", fmt.Errorf("unsupported implementation type: %s", implType)
	}

	// Re-encode to JSON
	decryptedJsonBytes, err := json.Marshal(runDetails)
	if err != nil {
		return "", fmt.Errorf("failed to marshal decrypted run JSON: %w", err)
	}

	// Re-encode to base64
	return base64.StdEncoding.EncodeToString(decryptedJsonBytes), nil
}
