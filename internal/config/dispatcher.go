package config

import (
	"github.com/meshcloud/meshstack-cli/client/types/xurl"

	"github.com/meshcloud/building-block-runner/v2/internal/config/xtime"
)

type Dispatcher struct {
	ForTypes          RunnerTypes                  `yaml:"forTypes" env:"FOR_TYPES"`
	Register          *Register                    `yaml:"register" envPrefix:"REGISTER_"`
	PollingInterval   xtime.Duration[xtime.Second] `yaml:"pollingInterval" env:"POLLING_INTERVAL"`
	MaxConcurrentJobs int                          `yaml:"maxConcurrentJobs" env:"MAX_CONCURRENT_JOBS"`
	Timeout           xtime.Duration[xtime.Minute] `yaml:"timeout" env:"TIMEOUT"`
	KillGrace         xtime.Duration[xtime.Second] `yaml:"killGrace" env:"KILL_GRACE"`
	KeycloakWif       *KeycloakWif                 `yaml:"keycloakWif" env:",init" envPrefix:"KEYCLOAK_WIF_"`
}

type KeycloakWif struct {
	Url           xurl.URL                     `yaml:"url" env:"URL"`
	Realm         string                       `yaml:"realm" env:"REALM"`
	ClientId      string                       `yaml:"clientId" env:"CLIENT_ID"`
	ClientSecret  string                       `yaml:"clientSecret" env:"CLIENT_SECRET"`
	TokenLifetime xtime.Duration[xtime.Minute] `yaml:"tokenLifetime" env:"TOKEN_LIFETIME"`
}

type Register struct {
	DisplayName string `yaml:"displayName" env:"DISPLAY_NAME"`
}

const defaultKeycloakWifTokenLifetimeMinutes = 90

func defaultDispatcher() Dispatcher {
	return Dispatcher{
		PollingInterval:   xtime.Seconds(10),
		MaxConcurrentJobs: 20,
		Timeout:           xtime.Minutes(60),
		KillGrace:         xtime.Seconds(120),
	}
}

func (d Dispatcher) MaxConcurrentRuns() (limit int, limited bool) {
	return d.MaxConcurrentJobs, d.MaxConcurrentJobs >= 0
}
