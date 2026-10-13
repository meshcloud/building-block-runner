package config

import (
	"errors"
	"uuid"
)

// Validate checks what every runner needs, also one that runs a run file with the run token.
func (cfg Config) Validate() error {
	var errs validationErrors
	errs.require(cfg.Uuid != uuid.UUID{}, "uuid is required")
	// meshStack registers a runner for a single runner type, which ALL also is
	errs.require(len(cfg.Dispatcher.ForTypes) == 1, "dispatcher.forTypes needs exactly one runner type")
	return errors.Join(errs...)
}

// ValidateDispatcher checks what claiming and dispatching runs needs on top of Validate.
func (cfg Config) ValidateDispatcher() error {
	var errs validationErrors
	errs.require(cfg.Api.Url.URL != nil || cfg.Api.Auth.Profile != "", "api.url or api.auth.profile is required")
	errs = append(errs, cfg.Api.Auth.validate())
	errs = append(errs, cfg.Crypto.Validate())
	errs.require(cfg.Dispatcher.PollingInterval.Duration > 0, "dispatcher.pollingInterval must be positive")
	errs.require(cfg.Dispatcher.Timeout.Duration > 0, "dispatcher.timeout must be positive")
	errs.require(cfg.Dispatcher.MaxConcurrentJobs != 0, "dispatcher.maxConcurrentJobs must not be 0, a negative value means unlimited")
	if cfg.Dispatcher.Register != nil {
		errs.require(cfg.Dispatcher.Register.DisplayName != "", "dispatcher.register.displayName is required")
		errs.require(cfg.Api.Auth.Workspace != "" || cfg.Api.Auth.Profile != "", "dispatcher.register needs api.auth.workspace or api.auth.profile")
	}
	return errors.Join(errs...)
}

func (auth Auth) validate() error {
	hasKey := auth.Key.ClientId != "" || auth.Key.ClientSecret != ""
	hasBasic := auth.Basic.Username != "" || auth.Basic.Password != ""
	switch {
	case hasKey && (auth.Key.ClientId == "" || auth.Key.ClientSecret == ""):
		return errors.New("api.auth.key needs both clientId and clientSecret")
	case hasKey:
		return nil
	case hasBasic && (auth.Basic.Username == "" || auth.Basic.Password == ""):
		return errors.New("api.auth.basic needs both username and password")
	case hasBasic, auth.Token != "", auth.Profile != "":
		return nil
	}
	return errors.New("api.auth needs either key, basic, profile or a token")
}

// ValidateTf checks what the tf runner needs on top of Validate.
func (cfg Config) ValidateTf() error {
	var errs validationErrors
	errs.require(cfg.Tf.TfParentWorkingDir != "", "tf.workingDir is required")
	errs.require(cfg.Tf.TfInstallDir != "", "tf.installDir is required")
	errs.require(cfg.Tf.WsTimeout.Duration > 0, "tf.wsTimeout must be positive")
	errs.require(cfg.Tf.InitTimeout.Duration > 0, "tf.initTimeout must be positive")
	return errors.Join(errs...)
}

type validationErrors []error

func (errs *validationErrors) require(ok bool, message string) {
	if !ok {
		*errs = append(*errs, errors.New(message))
	}
}
