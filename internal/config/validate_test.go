package config

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/meshcloud/building-block-runner/internal/crypto"
)

func Test_Validate_ReportsEveryMissingSetting(t *testing.T) {
	valid := func() Config {
		cfg := defaultConfig()
		cfg.Uuid = envUuid
		cfg.Api = newApi("http://localhost:8080", "", "")
		cfg.Crypto = crypto.Crypto{PrivateKeys: crypto.PrivateKeys{parsedPrivateKey(t)}}
		cfg.Dispatcher.ForTypes = RunnerTypes{RunnerTypeTf}
		return cfg
	}

	t.Run("accepts a complete config", func(t *testing.T) {
		assert.NoError(t, valid().Validate())
		assert.NoError(t, valid().ValidateDispatcher())
		assert.NoError(t, valid().ValidateTf())
	})

	t.Run("reports every error of an empty config", func(t *testing.T) {
		assert.EqualError(t, Config{}.Validate(), "uuid is required\ndispatcher.forTypes needs exactly one runner type")
		assert.EqualError(t, Config{}.ValidateDispatcher(), `no private key configured
dispatcher.pollingInterval must be positive
dispatcher.timeout must be positive
dispatcher.maxConcurrentJobs must not be 0, a negative value means unlimited`)
		assert.EqualError(t, Config{}.ValidateTf(), `tf.workingDir is required
tf.installDir is required
tf.wsTimeout must be positive
tf.initTimeout must be positive`)
	})

	t.Run("fails on two runner types and an incomplete registration", func(t *testing.T) {
		cfg := valid()
		cfg.Dispatcher.ForTypes = RunnerTypes{RunnerTypeTf, RunnerTypeManual}
		cfg.Dispatcher.Register = &Register{}
		assert.EqualError(t, cfg.Validate(), "dispatcher.forTypes needs exactly one runner type")
		assert.EqualError(t, cfg.ValidateDispatcher(), "dispatcher.register.displayName is required")
	})
}
