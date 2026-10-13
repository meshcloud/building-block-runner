package dispatcher

import (
	"context"
	"errors"
	"fmt"
	"uuid"

	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/http/meshapi"
)

// Run is a claimed run with its secrets decrypted.
type Run = meshapi.MeshRunnerRun

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
var Current func(ctx context.Context, cfg config.Config) (Dispatcher, error)

type WorkloadIdentityProvider interface {
	// WorkloadIdentity returns nil when the runs have no workload identity.
	WorkloadIdentity() *meshapi.MeshRunnerRegistrationWif
}

// RunReader is implemented by a dispatcher whose runs outlive the process that started them.
type RunReader interface {
	ReadRun(ctx context.Context, runUuid uuid.UUID) (Run, error)
}

// ErrFailureReported marks an error after which the run already has its FAILED status.
var ErrFailureReported = errors.New("the run reported its failure")

var ErrRunTooLarge = errors.New("the run is too large, reduce the size of the building block inputs")

var runnerTypes = map[meshapi.MeshRunnerImplementationType]config.RunnerType{
	meshapi.MeshRunnerImplementationTypeTerraform:      config.RunnerTypeTf,
	meshapi.MeshRunnerImplementationTypeManual:         config.RunnerTypeManual,
	meshapi.MeshRunnerImplementationTypeGithubWorkflow: config.RunnerTypeGitHub,
	meshapi.MeshRunnerImplementationTypeGitlabCiCd:     config.RunnerTypeGitLab,
	meshapi.MeshRunnerImplementationTypeAzureDevops:    config.RunnerTypeAzureDevOps,
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
