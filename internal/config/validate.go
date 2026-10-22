package config

import (
	"errors"
	"fmt"
	"time"
	"uuid"
)

const finalStatusGrace = 30 * time.Second

// Validate checks what every runner needs, also one that runs a run file with the run token.
func (cfg *Config) Validate() error {
	var errs validationErrors
	errs.require(cfg.Uuid != uuid.UUID{}, "uuid is required")
	return errors.Join(errs...)
}

// ValidateDispatcher checks what claiming and dispatching runs needs on top of Validate.
func (cfg *Config) ValidateDispatcher() error {
	var errs validationErrors
	// meshStack registers a runner for a single runner type, which ALL also is.
	errs.require(len(cfg.Dispatcher.ForTypes) == 1, "dispatcher.forTypes needs exactly one runner type")
	errs = append(errs, cfg.Crypto.Validate())
	errs.require(cfg.Dispatcher.PollingInterval.Duration > 0, "dispatcher.pollingInterval must be positive")
	errs.require(cfg.Dispatcher.Timeout.Duration > 0, "dispatcher.timeout must be positive")
	errs.require(cfg.Dispatcher.KillGrace.Duration >= cfg.Tf.StopGrace.Duration+finalStatusGrace,
		fmt.Sprintf("dispatcher.killGrace must be at least tf.stopGrace + %s, so that a stopped run has time to send its final status", finalStatusGrace))
	errs.require(cfg.Dispatcher.MaxConcurrentJobs != 0, "dispatcher.maxConcurrentJobs must not be 0, a negative value means unlimited")
	if cfg.Dispatcher.Register != nil {
		errs.require(cfg.Dispatcher.Register.DisplayName != "", "dispatcher.register.displayName is required")
	}
	if wif := cfg.Dispatcher.KeycloakWif; wif != nil {
		errs.require(wif.Url.URL != nil, "dispatcher.keycloakWif.url is required")
		errs.require(wif.Realm != "", "dispatcher.keycloakWif.realm is required")
		errs.require(wif.ClientId != "", "dispatcher.keycloakWif.clientId is required")
		errs.require(wif.ClientSecret != "", "dispatcher.keycloakWif.clientSecret is required")
		errs.require(wif.TokenLifetime.Duration > 0, "dispatcher.keycloakWif.tokenLifetime must be positive")
	}
	return errors.Join(errs...)
}

type validationErrors []error

func (errs *validationErrors) require(ok bool, message string) {
	if !ok {
		*errs = append(*errs, errors.New(message))
	}
}
