package tfrun

import (
	"context"
	"io"

	"github.com/hashicorp/terraform-exec/tfexec"
)

var _ TfFacade = &tfexec.Terraform{}

// TfFacade this is a facade in order to abstract away from functionality of the
// tfexec library and be able to mock tf execution behavior in tests
// tfexec.Terraform implements this interface.
type TfFacade interface {
	Init(ctx context.Context, opts ...tfexec.InitOption) error
	Apply(ctx context.Context, opts ...tfexec.ApplyOption) error
	Plan(ctx context.Context, opts ...tfexec.PlanOption) (bool, error)
	Destroy(ctx context.Context, opts ...tfexec.DestroyOption) error

	Output(ctx context.Context, opts ...tfexec.OutputOption) (map[string]tfexec.OutputMeta, error)

	WorkspaceList(ctx context.Context, opts ...tfexec.WorkspaceListOption) ([]string, string, error)
	WorkspaceNew(ctx context.Context, workspace string, opts ...tfexec.WorkspaceNewCmdOption) error
	WorkspaceSelect(ctx context.Context, workspace string, opts ...tfexec.WorkspaceSelectOption) error
	WorkspaceDelete(ctx context.Context, workspace string, opts ...tfexec.WorkspaceDeleteCmdOption) error

	SetEnv(env map[string]string) error
	SetStdout(w io.Writer)
	SetStderr(w io.Writer)
}
