package tf

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os/exec"
	"time"

	"github.com/hashicorp/terraform-exec/tfexec"
)

var _ Facade = &tfexec.Terraform{}

const planChangedExitCode = 2

type Facade interface {
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

type loggedFacade struct {
	Facade
}

func (tf loggedFacade) Init(ctx context.Context, opts ...tfexec.InitOption) (err error) {
	defer logFinished(ctx, "init", time.Now(), &err)
	return tf.Facade.Init(ctx, opts...)
}

func (tf loggedFacade) Apply(ctx context.Context, opts ...tfexec.ApplyOption) (err error) {
	defer logFinished(ctx, "apply", time.Now(), &err)
	return tf.Facade.Apply(ctx, opts...)
}

func (tf loggedFacade) Plan(ctx context.Context, opts ...tfexec.PlanOption) (changed bool, err error) {
	started := time.Now()
	changed, err = tf.Facade.Plan(ctx, opts...)
	successExitCode := 0
	if changed {
		successExitCode = planChangedExitCode
	}
	slog.InfoContext(ctx, "tf command finished", commandAttrs("plan", started, successExitCode, err)...)
	return changed, err
}

func (tf loggedFacade) Destroy(ctx context.Context, opts ...tfexec.DestroyOption) (err error) {
	defer logFinished(ctx, "destroy", time.Now(), &err)
	return tf.Facade.Destroy(ctx, opts...)
}

func (tf loggedFacade) Output(ctx context.Context, opts ...tfexec.OutputOption) (_ map[string]tfexec.OutputMeta, err error) {
	defer logFinished(ctx, "output", time.Now(), &err)
	return tf.Facade.Output(ctx, opts...)
}

func (tf loggedFacade) WorkspaceList(ctx context.Context, opts ...tfexec.WorkspaceListOption) (_ []string, _ string, err error) {
	defer logFinished(ctx, "workspace list", time.Now(), &err)
	return tf.Facade.WorkspaceList(ctx, opts...)
}

func (tf loggedFacade) WorkspaceNew(ctx context.Context, workspace string, opts ...tfexec.WorkspaceNewCmdOption) (err error) {
	defer logFinished(ctx, "workspace new", time.Now(), &err, "workspace", workspace)
	return tf.Facade.WorkspaceNew(ctx, workspace, opts...)
}

func (tf loggedFacade) WorkspaceSelect(ctx context.Context, workspace string, opts ...tfexec.WorkspaceSelectOption) (err error) {
	defer logFinished(ctx, "workspace select", time.Now(), &err, "workspace", workspace)
	return tf.Facade.WorkspaceSelect(ctx, workspace, opts...)
}

func (tf loggedFacade) WorkspaceDelete(ctx context.Context, workspace string, opts ...tfexec.WorkspaceDeleteCmdOption) (err error) {
	defer logFinished(ctx, "workspace delete", time.Now(), &err, "workspace", workspace)
	return tf.Facade.WorkspaceDelete(ctx, workspace, opts...)
}

func logFinished(ctx context.Context, command string, started time.Time, err *error, attrs ...any) {
	slog.InfoContext(ctx, "tf command finished", append(commandAttrs(command, started, 0, *err), attrs...)...)
}

// The error of a failed command ends with its stderr, the customer's output, which INFO must not carry.
func commandAttrs(command string, started time.Time, successExitCode int, err error) []any {
	attrs := []any{"command", command, "duration", time.Since(started)}
	if err == nil {
		return append(attrs, "exitCode", successExitCode)
	}
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		return append(attrs, "exitCode", exitErr.ExitCode(), "error", exitErr.Error())
	}
	return append(attrs, "error", err)
}
