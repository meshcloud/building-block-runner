package legacy

import "github.com/meshcloud/building-block-runner/internal/crypto"

type TfRunner struct {
	RunnerUuid       string                `yaml:"runnerUuid"`
	TimeoutMins      int                   `yaml:"timeoutMins"`
	WsTimeoutMins    int                   `yaml:"wsTimeoutMins"`
	InitTimeoutMins  int                   `yaml:"initTimeoutMins"`
	WorkingDir       string                `yaml:"workingDir"`
	TfInstallDir     string                `yaml:"tfInstallDir"`
	InsecureHostKeys bool                  `yaml:"insecureHostKeys"`
	PrivateKey       crypto.PrivateKey     `yaml:"privateKey"`
	PrivateKeyFile   crypto.PrivateKeyFile `yaml:"privateKeyFile"`
}
