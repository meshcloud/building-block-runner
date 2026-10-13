package config

import (
	"cmp"
	"fmt"
	"slices"
	"uuid"

	"github.com/meshcloud/building-block-runner/internal/config/internal/legacy"
	"github.com/meshcloud/building-block-runner/internal/config/xtime"
	"github.com/meshcloud/building-block-runner/internal/crypto"
)

func (cfg *Config) applyLegacy(l legacy.Config) (err error) {
	blockRunner := cmp.Or(l.BlockRunner, &legacy.BlockRunner{})

	if id := cmp.Or(l.Uuid, l.RunnerUuid, blockRunner.Uuid); id != "" {
		if cfg.Uuid, err = uuid.Parse(id); err != nil {
			return fmt.Errorf("legacy uuid: %w", err)
		}
	}
	cfg.Api.Auth.Workspace = l.OwnedByWorkspace
	if l.DisplayName != "" {
		cfg.Dispatcher.Register = &Register{DisplayName: l.DisplayName}
	}

	if url := cmp.Or(l.Api.Url, blockRunner.Api.Url); url != "" {
		if err = cfg.Api.Url.UnmarshalText([]byte(url)); err != nil {
			return fmt.Errorf("legacy api url: %w", err)
		}
	}
	auth := &cfg.Api.Auth
	auth.Basic.Username = cmp.Or(l.Api.Username, l.Api.User, blockRunner.Auth.Username)
	auth.Basic.Password = cmp.Or(l.Api.Password, blockRunner.Auth.Password)
	auth.Key.ClientId = cmp.Or(l.Api.ClientId, blockRunner.Auth.ApiKey.ClientId)
	auth.Key.ClientSecret = cmp.Or(l.Api.ClientSecret, blockRunner.Auth.ApiKey.ClientSecret)

	for _, privateKey := range []crypto.PrivateKey{l.Crypto.PrivateKey, l.TfRunner.PrivateKey, blockRunner.PrivateKey} {
		if privateKey.PrivateKey != nil {
			cfg.Crypto.PrivateKeys = append(cfg.Crypto.PrivateKeys, privateKey)
		}
	}
	for _, privateKeyFile := range []crypto.PrivateKeyFile{l.PrivateKeyFile, blockRunner.PrivateKeyFile} {
		if privateKeyFile.Path != "" {
			cfg.Crypto.PrivateKeyFiles = append(cfg.Crypto.PrivateKeyFiles, privateKeyFile)
		}
	}

	if l.PollingIntervalSeconds != 0 {
		cfg.Dispatcher.PollingInterval = xtime.Seconds(l.PollingIntervalSeconds)
	}
	cfg.Dispatcher.MaxConcurrentJobs = cmp.Or(l.MaxConcurrentJobs, cfg.Dispatcher.MaxConcurrentJobs)
	cfg.Dispatcher.Timeout = cmp.Or(xtime.Minutes(l.TimeoutMins), cfg.Dispatcher.Timeout)

	if l.TfRunner != (legacy.TfRunner{}) {
		cfg.Dispatcher.ForTypes = append(cfg.Dispatcher.ForTypes, RunnerTypeTf)
		// The legacy tf runner ran one run at a time.
		cfg.Dispatcher.MaxConcurrentJobs = cmp.Or(l.MaxConcurrentJobs, 1)
		cfg.Tf.TfParentWorkingDir = cmp.Or(l.WorkingDir, cfg.Tf.TfParentWorkingDir)
		cfg.Tf.TfInstallDir = cmp.Or(l.TfInstallDir, cfg.Tf.TfInstallDir)
		cfg.Tf.SkipHostKeyValidation = l.InsecureHostKeys
		cfg.Tf.WsTimeout = cmp.Or(xtime.Minutes(l.WsTimeoutMins), cfg.Tf.WsTimeout)
		cfg.Tf.InitTimeout = cmp.Or(xtime.Minutes(l.InitTimeoutMins), cfg.Tf.InitTimeout)
	}

	implementationTypes := RunnerTypes{}
	for implementationType := range l.Implementations {
		runnerType, err := ParseRunnerType(implementationType)
		if err != nil {
			return fmt.Errorf("legacy implementation: %w", err)
		}
		implementationTypes = append(implementationTypes, runnerType)
	}
	for _, runnerType := range allRunnerTypes {
		if slices.Contains(implementationTypes, runnerType) {
			cfg.Dispatcher.ForTypes = append(cfg.Dispatcher.ForTypes, runnerType)
		}
	}

	legacyK8s, err := k8sFromLegacy(l.RunController)
	if err != nil {
		return err
	}
	cfg.K8s = cmp.Or(legacyK8s, cfg.K8s)
	return nil
}
