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
}

func (tf *MockedTfFacade) initMockFuncs() {
	tf.initFunc = func(ctx context.Context, opts ...tfexec.InitOption) error { return nil }
	tf.applyFunc = func(ctx context.Context, opts ...tfexec.ApplyOption) error { return nil }
	tf.planFunc = func(ctx context.Context, opts ...tfexec.PlanOption) (bool, error) { return true, nil }
	tf.destroyFunc = func(ctx context.Context, opts ...tfexec.DestroyOption) error { return nil }
}

func (tf *MockedTfFacade) SetEnv(env map[string]string) error {
	return nil
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

func (tf *MockedTfFacade) WorkspaceList(ctx context.Context) ([]string, string, error) {
	return []string{}, "", nil
}

func (tf *MockedTfFacade) WorkspaceNew(ctx context.Context, workspace string, opts ...tfexec.WorkspaceNewCmdOption) error {
	return nil
}

func (tf *MockedTfFacade) WorkspaceSelect(ctx context.Context, workspace string) error {
	return nil
}

func (tf *MockedTfFacade) WorkspaceDelete(ctx context.Context, workspace string, opts ...tfexec.WorkspaceDeleteCmdOption) error {
	return nil
}

func (tf *MockedTfFacade) SetStdout(w io.Writer) {
	tf.stdOut = w
}

func (tf *MockedTfFacade) SetStderr(w io.Writer) {
	tf.stdErr = w
}
