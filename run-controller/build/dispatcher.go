package build

import (
	"context"

	"github.com/meshcloud/building-block-runner/go-meshapi-client/meshapi"
)

type Dispatcher interface {
	Dispatch(ctx context.Context, run *meshapi.RunDetailsDTO, decryptedRunJsonBase64 string, runnerType string) error
	CountActive() (int, error)
	// Wait blocks until every run dispatched to this process has finished.
	Wait()
}

var Dispatchers = map[string]func() (Dispatcher, error){}
