package tfrun

import (
	"context"
	"io"

	"github.com/hashicorp/terraform-exec/tfexec"
)

// this is an implementation of TfFacade that does not do anything.
// It can be used in tests.
type MockedTfFacade struct {
	initFunc    func(ctx context.Context, opts ...tfexec.InitOption) error
	applyFunc   func(ctx context.Context, opts ...tfexec.ApplyOption) error
	planFunc    func(ctx context.Context, opts ...tfexec.PlanOption) (bool, error)
	destroyFunc func(ctx context.Context, opts ...tfexec.DestroyOption) error
	stdOut      io.Writer
	stdErr      io.Writer

	// setEnvFunc/workspace*Func are configurable seams (test-infra only, see
	// PLAN_DETAIL_01_tf_characterization_tests.md CP4-CP6) so scenario tests can observe the
	// subprocess env (cleanSystemEnv/TF_HTTP_* pins) and drive the workspace select/create/delete
	// naming logic (incl. the B1-B3 bug pins) without adding a production seam. initMockFuncs
	// gives every field the facade's pre-existing hardcoded behavior as its default, so callers
	// that never touch these fields see no change.
	setEnvFunc          func(env map[string]string) error
	workspaceListFunc   func(ctx context.Context, opts ...tfexec.WorkspaceListOption) ([]string, string, error)
	workspaceNewFunc    func(ctx context.Context, workspace string, opts ...tfexec.WorkspaceNewCmdOption) error
	workspaceSelectFunc func(ctx context.Context, workspace string, opts ...tfexec.WorkspaceSelectOption) error
	workspaceDeleteFunc func(ctx context.Context, workspace string, opts ...tfexec.WorkspaceDeleteCmdOption) error
}

func (tf *MockedTfFacade) initMockFuncs() {
	tf.initFunc = func(ctx context.Context, opts ...tfexec.InitOption) error { return nil }
	tf.applyFunc = func(ctx context.Context, opts ...tfexec.ApplyOption) error { return nil }
	tf.planFunc = func(ctx context.Context, opts ...tfexec.PlanOption) (bool, error) { return true, nil }
	tf.destroyFunc = func(ctx context.Context, opts ...tfexec.DestroyOption) error { return nil }
	tf.setEnvFunc = func(env map[string]string) error { return nil }
	tf.workspaceListFunc = func(ctx context.Context, opts ...tfexec.WorkspaceListOption) ([]string, string, error) {
		return []string{}, "", nil
	}
	tf.workspaceNewFunc = func(ctx context.Context, workspace string, opts ...tfexec.WorkspaceNewCmdOption) error {
		return nil
	}
	tf.workspaceSelectFunc = func(ctx context.Context, workspace string, opts ...tfexec.WorkspaceSelectOption) error {
		return nil
	}
	tf.workspaceDeleteFunc = func(ctx context.Context, workspace string, opts ...tfexec.WorkspaceDeleteCmdOption) error {
		return nil
	}
}

func (tf *MockedTfFacade) SetEnv(env map[string]string) error {
	return tf.setEnvFunc(env)
}

func (tf *MockedTfFacade) Init(ctx context.Context, opts ...tfexec.InitOption) error {
	return tf.initFunc(ctx, opts...)
}

func (tf *MockedTfFacade) Apply(ctx context.Context, opts ...tfexec.ApplyOption) error {
	return tf.applyFunc(ctx, opts...)
}

func (tf *MockedTfFacade) Plan(ctx context.Context, opts ...tfexec.PlanOption) (bool, error) {
	return tf.planFunc(ctx, opts...)
}

func (tf *MockedTfFacade) Destroy(ctx context.Context, opts ...tfexec.DestroyOption) error {
	return tf.destroyFunc(ctx, opts...)
}

func (tf *MockedTfFacade) Output(ctx context.Context, opts ...tfexec.OutputOption) (map[string]tfexec.OutputMeta, error) {
	return map[string]tfexec.OutputMeta{}, nil
}

func (tf *MockedTfFacade) WorkspaceList(ctx context.Context, opts ...tfexec.WorkspaceListOption) ([]string, string, error) {
	return tf.workspaceListFunc(ctx, opts...)
}

func (tf *MockedTfFacade) WorkspaceNew(ctx context.Context, workspace string, opts ...tfexec.WorkspaceNewCmdOption) error {
	return tf.workspaceNewFunc(ctx, workspace, opts...)
}

func (tf *MockedTfFacade) WorkspaceSelect(ctx context.Context, workspace string, opts ...tfexec.WorkspaceSelectOption) error {
	return tf.workspaceSelectFunc(ctx, workspace, opts...)
}

func (tf *MockedTfFacade) WorkspaceDelete(ctx context.Context, workspace string, opts ...tfexec.WorkspaceDeleteCmdOption) error {
	return tf.workspaceDeleteFunc(ctx, workspace, opts...)
}

func (tf *MockedTfFacade) SetStdout(w io.Writer) {
	tf.stdOut = w
}

func (tf *MockedTfFacade) SetStderr(w io.Writer) {
	tf.stdErr = w
}
