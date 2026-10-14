package config

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"time"
	"uuid"

	"github.com/meshcloud/building-block-runner/internal/config/internal/legacy"
	"github.com/meshcloud/building-block-runner/internal/config/xtime"
	"github.com/meshcloud/building-block-runner/internal/crypto"
)

func (cfg *Config) applyLegacy(ctx context.Context, l legacy.Config) (err error) {
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
	auth.Key.ClientId = cmp.Or(l.Api.ClientId, blockRunner.Auth.ApiKey.ClientId)
	auth.Key.ClientSecret = cmp.Or(l.Api.ClientSecret, blockRunner.Auth.ApiKey.ClientSecret)

	for _, privateKey := range []crypto.PrivateKey{l.Crypto.PrivateKey, l.PrivateKey, blockRunner.PrivateKey} {
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
		// The legacy tf runner ran one run at a time.
		cfg.Dispatcher.MaxConcurrentJobs = cmp.Or(l.MaxConcurrentJobs, 1)
		cfg.Tf.CacheDir = cmp.Or(l.TfInstallDir, cfg.Tf.CacheDir)
		cfg.Tf.SkipHostKeyValidation = l.InsecureHostKeys
		cfg.Tf.InitTimeout = cmp.Or(xtime.Minutes(l.InitTimeoutMins), cfg.Tf.InitTimeout)
		if l.WsTimeoutMins != 0 {
			cfg.Tf.InitTimeout.Duration = max(cfg.Tf.InitTimeout.Duration, time.Duration(l.WsTimeoutMins)*time.Minute)
			slog.WarnContext(ctx, "tf wsTimeoutMins is merged into initTimeout, which now covers every preparation step", "wsTimeoutMins", l.WsTimeoutMins, "initTimeout", cfg.Tf.InitTimeout.String())
		}
	}

	for implementationType := range l.Implementations {
		if _, err = ParseRunnerType(implementationType); err != nil {
			return fmt.Errorf("legacy implementation: %w", err)
		}
	}
	if len(l.Implementations) > 0 {
		cfg.Dispatcher.ForTypes = append(cfg.Dispatcher.ForTypes, RunnerTypeAll)
	}

	return cfg.applyLegacyK8s(l.RunController)
}
