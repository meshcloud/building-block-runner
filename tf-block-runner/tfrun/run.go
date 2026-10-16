package tfrun

import "fmt"

const (
	DEFAULT_TF_VER = "1.4.4"
)

type Run struct {
	Id                     string
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
	PlanArtifactUrl        string
	ArtifactUploadUrl      string
	// TfStateLockUrl is empty when meshStack cannot lock the state.
	TfStateLockUrl string
}

type Variable struct {
	value any
	env   bool
	Type  DataType
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
