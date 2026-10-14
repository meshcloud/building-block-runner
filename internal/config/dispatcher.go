package config

import (
	"github.com/meshcloud/building-block-runner/internal/config/xtime"
)

type Dispatcher struct {
	ForTypes RunnerTypes `yaml:"forTypes" env:"FOR_TYPES"`
	// Register makes the runner update its registration in meshStack when it starts.
	Register          *Register                    `yaml:"register" envPrefix:"REGISTER_"`
	PollingInterval   xtime.Duration[xtime.Second] `yaml:"pollingInterval" env:"POLLING_INTERVAL"`
	MaxConcurrentJobs int                          `yaml:"maxConcurrentJobs" env:"MAX_CONCURRENT_JOBS"`
	// Timeout is the deadline of a run, which a k8s job gets as activeDeadlineSeconds.
	Timeout xtime.Duration[xtime.Minute] `yaml:"timeout" env:"TIMEOUT"`
	// KillGrace is how long a run may take to stop before it is killed, which a k8s job gets as terminationGracePeriodSeconds.
	KillGrace xtime.Duration[xtime.Second] `yaml:"killGrace" env:"KILL_GRACE"`
}

type Register struct {
	DisplayName string `yaml:"displayName" env:"DISPLAY_NAME"`
}

func defaultDispatcher() Dispatcher {
	return Dispatcher{
		PollingInterval:   xtime.Seconds(10),
		MaxConcurrentJobs: 20,
		Timeout:           xtime.Minutes(60),
		KillGrace:         xtime.Seconds(120),
	}
}

// MaxConcurrentRuns is false when maxConcurrentJobs is negative, which means unlimited.
func (d Dispatcher) MaxConcurrentRuns() (limit int, limited bool) {
	return d.MaxConcurrentJobs, d.MaxConcurrentJobs >= 0
}
