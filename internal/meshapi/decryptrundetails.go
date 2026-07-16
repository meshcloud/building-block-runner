package meshapi

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/meshcloud/building-block-runner/internal/secret"
)

// DecryptRunDetails decrypts every sensitive field of a claimed run JSON -- the sensitive
// building-block inputs plus the per-implementation-type secret (Terraform SSH key, GitHub
// appPem, GitLab pipeline-trigger-token, Azure DevOps PAT) -- and returns the re-encoded,
// base64-wrapped run JSON. It is the k8s Secret-handover decryption the run-controller runs
// before mounting a run into a Job pod, and lives in the shared meshapi package, next to
// the Decryptor seam. It moved
// here verbatim from the former internal/controller/decryption.go via internal/k8sjob: the
// five implementation-type branches, the "only decrypt non-empty string-like values" guard
// and the unsupported-type error are unchanged.
//
// Unlike SanitizeRunObjectForHandover (which serves outbound payloads and therefore NEVER touches
// the implementation object), DecryptRunDetails decrypts everything the Job pod needs. Its
// building-block INPUT loop now enforces the shared sensitive-input type policy
// (internal/secret): a sensitive input whose declared type is not STRING/CODE/FILE fails the
// run (secret.UnsupportedSensitiveTypeError) rather than silently decrypting-if-string.
// The per-implementation-secret decryption (SSH key / appPem / trigger token / PAT) below is
// NOT subject to that policy -- those are impl secrets, not building-block inputs. dec
// decouples this from the concrete cert crypto: the controller passes
// NewCertDecryptorFromCrypto(itsValidatedKeypair).
func DecryptRunDetails(runJsonBase64 string, dec Decryptor) (string, error) {
	// Decode base64
	runJsonBytes, err := base64.StdEncoding.DecodeString(runJsonBase64)
	if err != nil {
		return "", fmt.Errorf("failed to decode base64 run JSON: %w", err)
	}

	// Parse JSON into DTO
	runDetails, err := ParseRunDetails(runJsonBytes)
	if err != nil {
		return "", fmt.Errorf("failed to parse run JSON: %w", err)
	}

	// Decrypt sensitive building-block inputs under the shared type policy: a sensitive input
	// whose declared type is not STRING/CODE/FILE fails the run (secret.DecryptValue returns an
	// UnsupportedSensitiveTypeError) instead of the former decrypt-any-non-empty-string rule. A
	// sensitive input with no value at all (nil) is nothing to decrypt and is skipped.
	for i := range runDetails.Spec.BuildingBlock.Spec.Inputs {
		input := &runDetails.Spec.BuildingBlock.Spec.Inputs[i]
		if !input.IsSensitive || input.Value == nil {
			continue
		}
		decryptedValue, err := secret.DecryptValue(dec, input.Key, input.Type, input.Value)
		if err != nil {
			return "", fmt.Errorf("failed to decrypt sensitive input '%s': %w", input.Key, err)
		}
		input.Value = decryptedValue
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
			decryptedKey, err := dec.Decrypt(*impl.SshPrivateKey)
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
			decryptedPem, err := dec.Decrypt(impl.AppPem)
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
			decryptedToken, err := dec.Decrypt(impl.PipelineTriggerToken)
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
			decryptedPat, err := dec.Decrypt(impl.PersonalAccessToken)
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
