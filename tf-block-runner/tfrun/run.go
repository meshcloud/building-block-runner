package tfrun

import (
	"fmt"
	"time"

	meshcrypto "github.com/meshcloud/building-block-runner/go-meshapi-client/crypto"
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

// add more "decryptable" types here, once we support them.
func (variable Variable) decryptIfSensitive(crypto *meshcrypto.MeshCertBasedCrypto) (result any, err error) {
	result = variable.value
	if variable.isSensitive {
		switch variable.Type {
		case DATA_TYPE_CODE:
			result, err = crypto.DecryptMeshCertBased(fmt.Sprintf("%v", variable.value))
		case DATA_TYPE_STRING:
			result, err = crypto.DecryptMeshCertBased(fmt.Sprintf("%v", variable.value))
		case DATA_TYPE_FILE:
			result, err = crypto.DecryptMeshCertBased(fmt.Sprintf("%v", variable.value))
		}
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
