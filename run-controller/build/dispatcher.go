package build

import "github.com/meshcloud/building-block-runner/go-meshapi-client/meshapi"

type Dispatcher interface {
	Dispatch(run *meshapi.RunDetailsDTO, decryptedRunJsonBase64 string, runnerType string) error
	CountActive() (int, error)
}

var Dispatchers = map[string]func() (Dispatcher, error){}
