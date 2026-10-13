package config

import (
	"testing"
	"uuid"

	"github.com/stretchr/testify/assert"

	"github.com/meshcloud/building-block-runner/internal/crypto"
)

func TestValidate(t *testing.T) {
	valid := func() Config {
		cfg := defaultConfig()
		cfg.Uuid = uuid.MustParse("0b6d1b5e-3c0f-4a51-9d0e-6f1f4b7c2a10")
		cfg.Api = newApi("http://localhost:8080", "", "")
		cfg.Crypto = crypto.Crypto{PrivateKeys: crypto.PrivateKeys{parsedPrivateKey(t)}}
		cfg.Dispatcher.ForTypes = RunnerTypes{RunnerTypeTf}
		return cfg
	}
	assert.NoError(t, valid().Validate())
	assert.NoError(t, valid().ValidateDispatcher())

	t.Run("accepts a registration with a profile in place of the workspace", func(t *testing.T) {
		cfg := valid()
		cfg.Dispatcher.Register = &Register{DisplayName: "runner"}
		cfg.Api.Auth.Profile = "local-dev"
		assert.NoError(t, cfg.ValidateDispatcher())
	})

	t.Run("fails on an incomplete registration", func(t *testing.T) {
		cfg := valid()
		cfg.Dispatcher.Register = &Register{}
		assert.EqualError(t, cfg.ValidateDispatcher(), `dispatcher.register.displayName is required
dispatcher.register needs api.auth.workspace or api.auth.profile`)
	})

	t.Run("fails on two runner types", func(t *testing.T) {
		cfg := valid()
		cfg.Dispatcher.ForTypes = RunnerTypes{RunnerTypeTf, RunnerTypeManual}
		assert.EqualError(t, cfg.Validate(), "dispatcher.forTypes needs exactly one runner type")
	})

	t.Run("accepts a profile in place of url and auth", func(t *testing.T) {
		cfg := valid()
		cfg.Api = Api{Auth: Auth{Profile: "local-dev"}}
		assert.NoError(t, cfg.ValidateDispatcher())
	})

	t.Run("accepts a key besides basic auth", func(t *testing.T) {
		cfg := valid()
		cfg.Api.Auth.Key.ClientId, cfg.Api.Auth.Key.ClientSecret = "id", "secret"
		assert.NoError(t, cfg.ValidateDispatcher())
	})

	t.Run("fails on half a key besides basic auth", func(t *testing.T) {
		cfg := valid()
		cfg.Api.Auth.Key.ClientId = "id"
		assert.EqualError(t, cfg.ValidateDispatcher(), "api.auth.key needs both clientId and clientSecret")
	})

	t.Run("fails on half a basic auth", func(t *testing.T) {
		cfg := valid()
		cfg.Api.Auth.Basic.Password = ""
		assert.EqualError(t, cfg.ValidateDispatcher(), "api.auth.basic needs both username and password")
	})

	t.Run("leaves the api to ValidateDispatcher, as a run file brings its run token", func(t *testing.T) {
		cfg := valid()
		cfg.Api = Api{}
		assert.NoError(t, cfg.Validate())
	})

	t.Run("reports all errors of an empty config", func(t *testing.T) {
		assert.EqualError(t, Config{}.Validate(), `uuid is required
dispatcher.forTypes needs exactly one runner type`)
		assert.EqualError(t, Config{}.ValidateDispatcher(), `api.url or api.auth.profile is required
api.auth needs either key, basic or profile
no private key configured
dispatcher.pollingInterval must be positive
dispatcher.timeout must be positive
dispatcher.maxConcurrentJobs must not be 0, a negative value means unlimited`)
	})
}

func TestValidateTf(t *testing.T) {
	assert.NoError(t, defaultConfig().ValidateTf())
	assert.EqualError(t, Config{}.ValidateTf(), `tf.workingDir is required
tf.installDir is required
tf.wsTimeout must be positive
tf.initTimeout must be positive`)
}
