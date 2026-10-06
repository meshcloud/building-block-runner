package manual

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_LoadConfig_ReadsWhatTheKotlinRunnerRead(t *testing.T) {
	writeConfigFile := func(t *testing.T, content string) string {
		path := filepath.Join(t.TempDir(), "runner-config.yml")
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
		return path
	}

	t.Run("without a file, the Kotlin defaults apply", func(t *testing.T) {
		cfg, err := LoadConfig(filepath.Join(t.TempDir(), "runner-config.yml"))
		require.NoError(t, err)

		assert.Equal(t, "d943b032-7836-4fef-a4a0-158817beecf3", cfg.Uuid)
		assert.Equal(t, "http://localhost:8301", cfg.Api.Url)
		assert.Equal(t, "bb-api", cfg.Auth.Username)
		assert.Equal(t, "guest", cfg.Auth.Password)
	})

	t.Run("the install sample of meshStack's panel, with the secret from the environment", func(t *testing.T) {
		path := writeConfigFile(t, `
blockrunner:
  uuid: "5d3c2b1a-0000-4000-8000-000000000001"
  api:
    url: https://meshstack.example.com
  auth:
    api-key:
      client-id: my-client-id
`)
		t.Setenv("RUNNER_API_CLIENT_SECRET", "my-secret")

		cfg, err := LoadConfig(path)
		require.NoError(t, err)

		assert.Equal(t, "5d3c2b1a-0000-4000-8000-000000000001", cfg.Uuid)
		assert.Equal(t, "https://meshstack.example.com", cfg.Api.Url)
		assert.Equal(t, "my-client-id", cfg.Auth.ApiKey.ClientId)
		assert.Equal(t, "my-secret", cfg.Auth.ApiKey.ClientSecret)
	})

	t.Run("any Spring spelling of a key", func(t *testing.T) {
		path := writeConfigFile(t, `
blockrunner:
  auth:
    apiKey:
      clientId: camel-id
      client_secret: snake-secret
`)

		cfg, err := LoadConfig(path)
		require.NoError(t, err)

		assert.Equal(t, "camel-id", cfg.Auth.ApiKey.ClientId)
		assert.Equal(t, "snake-secret", cfg.Auth.ApiKey.ClientSecret)
	})

	t.Run("a copy of the Kotlin runner-config.yml, whose placeholders take the environment or their default", func(t *testing.T) {
		path := writeConfigFile(t, `
blockrunner:
  version: ${VERSION:dev}
  uuid: ${RUNNER_UUID:d943b032-7836-4fef-a4a0-158817beecf3}
  debugMode: false
  api:
    url: ${RUNNER_API_URL:http://localhost:8301}
  auth:
    username: ${RUNNER_API_USERNAME:bb-api}
    password: ${RUNNER_API_PASSWORD:guest}
    api-key:
      client-id: ${RUNNER_API_CLIENT_ID:}
      client-secret: ${RUNNER_API_CLIENT_SECRET:}
`)
		t.Setenv("RUNNER_API_URL", "http://meshstack:8080")

		cfg, err := LoadConfig(path)
		require.NoError(t, err)

		assert.Equal(t, "d943b032-7836-4fef-a4a0-158817beecf3", cfg.Uuid)
		assert.Equal(t, "http://meshstack:8080", cfg.Api.Url)
		assert.Equal(t, "bb-api", cfg.Auth.Username)
		assert.Empty(t, cfg.Auth.ApiKey.ClientId)
	})

	t.Run("the environment wins over the file", func(t *testing.T) {
		path := writeConfigFile(t, `
blockrunner:
  uuid: from-file
  api:
    url: http://from-file
  auth:
    api-key:
      client-id: from-file
`)
		t.Setenv("RUNNER_UUID", "from-env")
		t.Setenv("RUNNER_API_URL", "http://from-env")
		t.Setenv("RUNNER_API_CLIENT_ID", "from-env")

		cfg, err := LoadConfig(path)
		require.NoError(t, err)

		assert.Equal(t, "from-env", cfg.Uuid)
		assert.Equal(t, "http://from-env", cfg.Api.Url)
		assert.Equal(t, "from-env", cfg.Auth.ApiKey.ClientId)
	})

	t.Run("invalid YAML fails", func(t *testing.T) {
		_, err := LoadConfig(writeConfigFile(t, "blockrunner: [unclosed"))
		require.Error(t, err)
	})
}
