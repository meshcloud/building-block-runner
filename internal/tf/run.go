package tf

import (
	"fmt"
	"time"
)

const (
	DEFAULT_TF_VER = "1.4.4"
)

type Run struct {
	Id                     string
	RequestedAt            time.Time
	TerraformVersion       string
	Behavior               Behavior
	WorkspaceIdentifier    *string
	ProjectIdentifier      *string
	FullPlatformIdentifier *string
	BuildingBlockId        string
	BuildingBlockName      string
	IsAsync                bool
	Vars                   map[string]*Variable
	RunJsonBase64          string
	Source                 *GitSource
	UseMeshBackendFallback bool
	PreRunScript           *string
	RunToken               string
	MeshstackBaseUrl       string
	// PlanArtifactUrl is set (from the runner-facing _links.planArtifact.href) only when this
	// APPLY run must apply a predecessor DETECT run's saved terraform plan. Empty => plain apply.
	PlanArtifactUrl string
}

type Variable struct {
	value       any
	env         bool
	Type        DataType
	isSensitive bool
}

// A nil dec means "no decryptor configured" and passes the value through unchanged, matching
// the former meshcrypto.Crypto == nil single-run behavior.
//
// Every sensitive value is decrypted regardless of DataType (B5 fix, phase 2b): a prior
// type-switch only decrypted CODE/STRING/FILE, silently leaving ciphertext as the value for any
// other sensitive type (e.g. BOOLEAN, INTEGER). The encrypted wire representation is always a
// string produced by the sender's encryption regardless of the input's logical DataType, so
// decrypting unconditionally is correct for every type, not just the three previously listed.
func (variable Variable) decryptIfSensitive(dec Decryptor) (result any, err error) {
	result = variable.value
	if variable.isSensitive && dec != nil {
		result, err = dec.Decrypt(fmt.Sprintf("%v", variable.value))
		if err != nil {
			// Wrap the error with helpful context
			return nil, fmt.Errorf("failed to decrypt secret input: %w. "+
				"This typically indicates a key mismatch - the private key provided to this building block block runner does not match the public key used to encrypt the input. "+
				"Please verify: "+
				"1) The correct building block block runner is configured for this building block definition, "+
				"2) The private key configured in the runner matches the public key for the runner configured in meshStack, or "+
				"3) Create and publish a new building block definition version and re-upload the plaintext for secret inputs so they are re-encrypted with the correct key", err)
		}
	}
	return result, err
}

type TfOutput struct {
	Value     any
	Type      DataType
	Sensitive bool
}

func (run Run) toWorkspaceStr() string {
	workspaceIdentifier := "_"
	if run.WorkspaceIdentifier != nil {
		workspaceIdentifier = *run.WorkspaceIdentifier
	}

	projectIdentifier := "_"
	if run.ProjectIdentifier != nil {
		projectIdentifier = *run.ProjectIdentifier
	}

	fullPlatformIdentifier := "_"
	if run.FullPlatformIdentifier != nil {
		fullPlatformIdentifier = *run.FullPlatformIdentifier
	}

	return fmt.Sprintf(
		"%s.%s.%s:%s",
		workspaceIdentifier,
		projectIdentifier,
		fullPlatformIdentifier,
		run.BuildingBlockId,
	)
}
