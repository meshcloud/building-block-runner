package dispatcher

import (
	"cmp"
	"context"
	"errors"
	"uuid"

	"github.com/meshcloud/meshstack-cli/client"

	"github.com/meshcloud/building-block-runner/internal/config"
)

// Run is a claimed run with its secrets decrypted.
type Run = client.MeshRunnerRun

type Finished struct {
	RunUuid  uuid.UUID
	ExitCode int
	Failure  string
	// Reported is called once meshStack accepted the failure report of the poller.
	Reported func(context.Context)
}

type InitState struct {
	// Running holds the runs that a previous process started and that have not finished.
	Running              []uuid.UUID
	WorkloadIdentity     *client.MeshRunnerWorkloadIdentityFed
	KeycloakWifTokenPath string
}

type Dispatcher interface {
	Init(ctx context.Context) (InitState, error)
	// Dispatch returns once the run is handed over. A run gets no workload identity token when wifToken is empty.
	Dispatch(ctx context.Context, run Run, wifToken string) error
	// Finished is closed once the context given to Current has ended.
	Finished() <-chan Finished
}

// Current is set by the init of the dispatcher package that the build tags select. It does no network I/O.
var Current func(ctx context.Context, cfg config.Config) (Dispatcher, error)

var ErrRunTooLarge = errors.New("the run is too large, reduce the size of the building block inputs")

func MeshStackEndpoint(cfg config.Config, run Run) string {
	return cmp.Or(run.Links.MeshstackBaseUrl.Href, cfg.Api.Url.String())
}
