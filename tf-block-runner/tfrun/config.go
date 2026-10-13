package tfrun

import "uuid"

var AppConfig TfRunnerConfig

type TfRunnerConfig struct {
	TfCommandTimeoutMins  int
	TfParentWorkingDir    string
	TfInstallDir          string
	SkipHostKeyValidation bool
	WsTimeoutMins         int
	InitTimeoutMins       int
	RunnerUuid            uuid.UUID
	ApiUrl                string
}
