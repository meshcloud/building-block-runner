package dispatch

import (
	"testing"

	"github.com/stretchr/testify/assert"

	meshapi "github.com/meshcloud/building-block-runner/internal/meshapi"
)

func TestNewK8sJobUnhandledTypeError_MessageIsFrozenByteIdentical(t *testing.T) {
	// D9/D10: this exact string is customer-visible run-status text today
	// (controller.go's processNextRun) and must not change shape now that it is
	// dispatcher-authored.
	err := NewK8sJobUnhandledTypeError(meshapi.RunnerTypeGitHubWorkflow)
	assert.Equal(t, "no implementation handler configured for type 'GITHUB_WORKFLOW'", err.Error())
	assert.Equal(t, meshapi.RunnerTypeGitHubWorkflow, err.Type)

	var target *UnhandledTypeError
	assert.ErrorAs(t, error(err), &target, "must satisfy error and be recoverable via errors.As")
}

func TestNewInProcessUnhandledTypeError_MessageIsActionable(t *testing.T) {
	err := NewInProcessUnhandledTypeError(meshapi.RunnerTypeGitLabPipeline)
	assert.Equal(t, meshapi.RunnerTypeGitLabPipeline, err.Type)
	msg := err.Error()
	assert.Contains(t, msg, "this runner does not handle run type 'GITLAB_PIPELINE'")
	assert.Contains(t, msg, "register the runner with the concrete capability it supports")
	// Must not silently reuse the frozen, vaguer k8sjob wording (§16.4: two messages, one
	// error type, deliberately not unified).
	assert.NotEqual(t, NewK8sJobUnhandledTypeError(meshapi.RunnerTypeGitLabPipeline).Error(), msg)
}

func TestUnhandledTypeError_ImplementsErrorInterface(t *testing.T) {
	var _ error = (*UnhandledTypeError)(nil)
}
