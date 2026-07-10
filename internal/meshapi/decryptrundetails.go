package meshapi

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	meshcrypto "github.com/meshcloud/building-block-runner/internal/crypto"
)

// DecryptRunDetails decrypts every sensitive field of a claimed run JSON -- the sensitive
// building-block inputs plus the per-implementation-type secret (Terraform SSH key, GitHub
// appPem, GitLab pipeline-trigger-token, Azure DevOps PAT) -- and returns the re-encoded,
// base64-wrapped run JSON. It is the k8s Secret-handover decryption the run-controller runs
// before mounting a run into a Job pod, and is the home PLAN_DETAIL_03 step 8 intended for
// it (the shared meshapi package, next to the Decryptor seam and DecryptInputs). It moved
// here verbatim from the former internal/controller/decryption.go via internal/k8sjob: the
// five implementation-type branches, the "only decrypt non-empty string-like values" guard
// and the unsupported-type error are unchanged.
//
// Unlike DecryptInputs (which serves outbound payloads and therefore NEVER touches the
// implementation object, umbrella §7.6), DecryptRunDetails decrypts everything the Job pod
// needs and every sensitive string input regardless of its declared type -- the two rules
// coexist by design (plan 06B §16.8). dec decouples this from the concrete cert crypto: the
// controller passes NewCertDecryptorFromCrypto(itsValidatedKeypair); the empty-value guards
// below mean the Decryptor is only ever asked to decrypt a non-empty ciphertext.
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

	// Decrypt sensitive inputs
	for i := range runDetails.Spec.BuildingBlock.Spec.Inputs {
		input := &runDetails.Spec.BuildingBlock.Spec.Inputs[i]
		if input.IsSensitive && input.Value != nil {
			// Only decrypt string-like types
			if strValue, ok := input.Value.(string); ok && strValue != "" {
				decryptedValue, err := dec.Decrypt(strValue)
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

// NewCertDecryptorFromCrypto adapts an already-constructed cert crypto instance (e.g. the
// run-controller's validated key pair from meshcrypto.NewCertBasedDecryptorWithValidation)
// into a Decryptor, so a caller that owns the crypto instance can hand it to
// DecryptRunDetails without meshapi re-loading the key. The empty-string guard is CertDecryptor's
// (Kotlin decrypt("") == ""); DecryptRunDetails never passes an empty value anyway.
func NewCertDecryptorFromCrypto(c *meshcrypto.MeshCertBasedCrypto) Decryptor {
	return CertDecryptor{crypto: c}
}
