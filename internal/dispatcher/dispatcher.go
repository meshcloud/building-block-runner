package dispatcher

import (
	"context"
	"errors"
	"fmt"
	"uuid"

	"github.com/meshcloud/meshstack-cli/client"

	"github.com/meshcloud/building-block-runner/internal/config"
)

// Run is a claimed run with its secrets decrypted.
type Run = client.MeshRunnerRun

type RunStatus struct {
	RunUuid uuid.UUID
	Ended   bool
	// Err is set only when Ended.
	Err error
}

type Dispatcher interface {
	// Start returns once the run is handed over.
	Start(ctx context.Context, run Run) error
	// Status is closed once the context given to Current has ended.
	Status() <-chan RunStatus
}

// Current is set by the init of the dispatcher package that the build tags select.
var Current func(ctx context.Context, cfg config.Config, api client.MeshRunnerClient) (Dispatcher, error)

// DefaultForTypes fills an empty dispatcher.forTypes when the selected dispatcher package sets it.
var DefaultForTypes func() config.RunnerTypes

type WorkloadIdentityProvider interface {
	// WorkloadIdentity returns nil when the runs have no workload identity.
	WorkloadIdentity() *client.MeshRunnerWorkloadIdentityFed
}

// FailureReportConfirmer is implemented by a dispatcher that must not report a failure again once meshStack accepted it.
type FailureReportConfirmer interface {
	FailureReported(ctx context.Context, runUuid uuid.UUID)
}

// RunReader is implemented by a dispatcher whose runs outlive the process that started them.
type RunReader interface {
	ReadRun(ctx context.Context, runUuid uuid.UUID) (Run, error)
}

// ErrFailureReported marks an error after which the run already has its FAILED status.
var ErrFailureReported = errors.New("the run reported its failure")

var ErrRunTooLarge = errors.New("the run is too large, reduce the size of the building block inputs")

var runnerTypes = map[client.MeshRunnerImplementationType]config.RunnerType{
	client.MeshRunnerImplementationTypeTerraform:      config.RunnerTypeTf,
	client.MeshRunnerImplementationTypeManual:         config.RunnerTypeManual,
	client.MeshRunnerImplementationTypeGithubWorkflow: config.RunnerTypeGitHub,
	client.MeshRunnerImplementationTypeGitlabCiCd:     config.RunnerTypeGitLab,
	client.MeshRunnerImplementationTypeAzureDevops:    config.RunnerTypeAzureDevOps,
}

func RunnerTypeOf(run Run) (config.RunnerType, error) {
	implementationType, err := run.Spec.Definition.Spec.ImplementationType()
	if err != nil {
		return "", err
	}
	runnerType, ok := runnerTypes[implementationType]
	if !ok {
		return "", fmt.Errorf("implementation type %s is unknown", implementationType)
	}
	return runnerType, nil
}
