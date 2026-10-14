package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/internal/config/xtime"
	"github.com/meshcloud/building-block-runner/internal/crypto"
)

func Test_Validate_ReportsEveryMissingSetting(t *testing.T) {
	valid := func() Config {
		cfg := defaultConfig()
		cfg.Uuid = envUuid
		cfg.Api = newApi("http://localhost:8080", "", "")
		cfg.Crypto = crypto.Crypto{PrivateKeys: crypto.PrivateKeys{parsedPrivateKey(t)}}
		cfg.Dispatcher.ForTypes = RunnerTypes{RunnerTypeTf}
		cfg.Tf.CacheDir = "/tmp/tfcache"
		return cfg
	}

	t.Run("accepts a complete config", func(t *testing.T) {
		assert.NoError(t, new(valid()).ValidateDispatcher())
		assert.NoError(t, valid().Tf.Validate())
	})

	t.Run("reports every error of an empty config", func(t *testing.T) {
		require.EqualError(t, new(Config).ValidateDispatcher(), `uuid is required
dispatcher.forTypes needs exactly one runner type
no private key configured
dispatcher.pollingInterval must be positive
dispatcher.timeout must be positive
dispatcher.killGrace must be at least tf.abortGrace + 30s, so that a stopped run has time to send its final status
dispatcher.maxConcurrentJobs must not be 0, a negative value means unlimited`)
		assert.EqualError(t, new(Config).Tf.Validate(), `tf.cacheDir is required
tf.initTimeout must be positive
tf.abortGrace must be positive`)
	})

	t.Run("fails on two runner types and an incomplete registration", func(t *testing.T) {
		cfg := valid()
		cfg.Dispatcher.ForTypes = RunnerTypes{RunnerTypeTf, RunnerTypeManual}
		cfg.Dispatcher.Register = &Register{}
		assert.EqualError(t, cfg.ValidateDispatcher(), "dispatcher.forTypes needs exactly one runner type\ndispatcher.register.displayName is required")
	})

	t.Run("fails on a kill grace that leaves no time for the final status", func(t *testing.T) {
		cfg := valid()
		cfg.Tf.AbortGrace = xtime.Seconds(100)
		cfg.Dispatcher.KillGrace = xtime.Seconds(129)
		require.EqualError(t, cfg.ValidateDispatcher(), "dispatcher.killGrace must be at least tf.abortGrace + 30s, so that a stopped run has time to send its final status")

		cfg.Dispatcher.KillGrace = xtime.Seconds(130)
		assert.NoError(t, cfg.ValidateDispatcher())
	})
}
