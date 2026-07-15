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
	value any
	env   bool
	Type  DataType
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
