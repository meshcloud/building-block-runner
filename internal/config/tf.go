package config

import (
	"github.com/meshcloud/building-block-runner/internal/config/xtime"
)

type Tf struct {
	TfParentWorkingDir    string                       `yaml:"workingDir" env:"WORKING_DIR"`
	TfInstallDir          string                       `yaml:"installDir" env:"INSTALL_DIR"`
	SkipHostKeyValidation bool                         `yaml:"insecureHostKeys" env:"INSECURE_HOST_KEYS"`
	WsTimeout             xtime.Duration[xtime.Minute] `yaml:"wsTimeout" env:"WS_TIMEOUT"`
	InitTimeout           xtime.Duration[xtime.Minute] `yaml:"initTimeout" env:"INIT_TIMEOUT"`
}

func defaultTf() Tf {
	return Tf{
		TfParentWorkingDir: "/tmp/runner/wd",
		TfInstallDir:       "/tmp/runner/tfbin",
		WsTimeout:          xtime.Minutes(5),
		InitTimeout:        xtime.Minutes(3),
	}
}
