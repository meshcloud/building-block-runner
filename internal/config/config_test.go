package config

import (
	"embed"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"uuid"

	"github.com/meshcloud/meshstack-cli/client/types/xurl"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/internal/config/tf"
	"github.com/meshcloud/building-block-runner/internal/config/xtime"
	"github.com/meshcloud/building-block-runner/internal/crypto"
)

//go:embed testdata/*.yml
var testdata embed.FS

//go:embed testdata/private-key.pem
var privateKeyPEM string

var envUuid = uuid.MustParse("0b6d1b5e-3c0f-4a51-9d0e-6f1f4b7c2a10")

func Test_Load_ReadsTheNewAndTheLegacyLayouts(t *testing.T) {
	unsetRunnerEnv(t)

	t.Run("without a file", func(t *testing.T) {
		t.Setenv("RUNNER_UUID", envUuid.String())
		cfg, err := LoadFile(t.Context(), filepath.Join(t.TempDir(), "missing.yml"))

		require.NoError(t, err)
		assert.Equal(t, Config{
			Uuid:       envUuid,
			Dispatcher: Dispatcher{PollingInterval: xtime.Seconds(10), MaxConcurrentJobs: 20, Timeout: xtime.Minutes(60), KillGrace: xtime.Seconds(120)},
			Tf:         defaultTf(envUuid),
		}, cfg)
	})

	t.Run("env wins over the file and the file over the defaults", func(t *testing.T) {
		t.Setenv("RUNNER_TF_INIT_TIMEOUT", "1")
		t.Setenv("RUNNER_DISPATCHER_FOR_TYPES", "tf,MANUAL,all")

		cfg := load(t, []byte("tf:\n  wsTimeout: 7\ndispatcher:\n  forTypes: [github]\n"))

		assert.Equal(t, tf.Config{CacheDir: filepath.Join(cfg.RunnerDir(), "tfcache"), WsTimeout: xtime.Minutes(7), InitTimeout: xtime.Minutes(1), RegistryRetries: 3, StopGrace: xtime.Seconds(60)}, cfg.Tf)
		assert.Equal(t, RunnerTypes{RunnerTypeTf, RunnerTypeManual, RunnerTypeAll}, cfg.Dispatcher.ForTypes)
	})

	t.Run("legacy tf layout", func(t *testing.T) {
		cfg := loadFile(t, "runner-config-old-tf.yml")

		assert.Equal(t, Config{
			Uuid: uuid.MustParse("79090ca8-94f8-4e56-b382-6d7db52b7e59"),
			Api:  newApi("http://localhost:8300", "", ""),
			Crypto: crypto.Crypto{
				PrivateKeys:     crypto.PrivateKeys{parsedPrivateKey(t)},
				PrivateKeyFiles: crypto.PrivateKeyFiles{{Path: "./testdata/private-key.pem", PrivateKey: parsedPrivateKey(t)}},
			},
			Dispatcher: Dispatcher{ForTypes: RunnerTypes{RunnerTypeTf}, PollingInterval: xtime.Seconds(10), MaxConcurrentJobs: 1, Timeout: xtime.Minutes(60), KillGrace: xtime.Seconds(120)},
			Tf:         tf.Config{CacheDir: "/tmp/runner/tfbin", WsTimeout: xtime.Minutes(5), InitTimeout: xtime.Minutes(3), RegistryRetries: 3, StopGrace: xtime.Seconds(60)},
		}, cfg)
	})

	t.Run("legacy tf layout with env", func(t *testing.T) {
		t.Setenv("RUNNER_UUID", envUuid.String())
		t.Setenv("RUNNER_API_URL", "https://meshstack.example.com")
		t.Setenv("RUNNER_API_USERNAME", "other-user")
		t.Setenv("RUNNER_API_PASSWORD", "other-pa$$word")
		t.Setenv("RUNNER_API_CLIENT_ID", "other-client-id")
		t.Setenv("RUNNER_API_CLIENT_SECRET", "other-client-secret")
		t.Setenv("RUNNER_PRIVATE_KEY", strings.ReplaceAll(privateKeyPEM, "\n", ""))
		t.Setenv("RUNNER_PRIVATE_KEY_FILE", "./testdata/private-key.pem,./testdata/private-key.pem")
		t.Setenv("RUNNER_DISPATCHER_MAX_CONCURRENT_JOBS", "5")
		t.Setenv("RUNNER_TF_CACHE_DIR", "/other/cache")
		t.Setenv("RUNNER_TF_WS_TIMEOUT", "7")

		cfg := loadFile(t, "runner-config-old-tf.yml")

		assert.Equal(t, envUuid, cfg.Uuid)
		expectedApi := newApi("https://meshstack.example.com", "other-client-id", "other-client-secret")
		expectedApi.Auth.Basic = BasicAuth{Username: "other-user", Password: "other-pa$$word"}
		assert.Equal(t, expectedApi, cfg.Api)
		privateKeyFile := crypto.PrivateKeyFile{Path: "./testdata/private-key.pem", PrivateKey: parsedPrivateKey(t)}
		assert.Equal(t, crypto.Crypto{
			PrivateKeys:     crypto.PrivateKeys{parsedPrivateKey(t)},
			PrivateKeyFiles: crypto.PrivateKeyFiles{privateKeyFile, privateKeyFile},
		}, cfg.Crypto)
		assert.Equal(t, 5, cfg.Dispatcher.MaxConcurrentJobs)
		assert.Equal(t, "/other/cache", cfg.Tf.CacheDir)
		assert.Equal(t, xtime.Minutes(7), cfg.Tf.WsTimeout)
	})

	t.Run("an env name wins over its fallback", func(t *testing.T) {
		t.Setenv("RUNNER_CRYPTO_PRIVATE_KEY_FILES", "./testdata/private-key.pem")
		t.Setenv("RUNNER_PRIVATE_KEY_FILE", "./testdata/private-key.pem,./testdata/private-key.pem")

		cfg := loadFile(t, "runner-config-old-tf.yml")

		assert.Len(t, cfg.Crypto.PrivateKeyFiles, 1)
	})

	t.Run("legacy tf timeoutMins is the dispatcher timeout", func(t *testing.T) {
		cfg := load(t, []byte("runnerUuid: 79090ca8-94f8-4e56-b382-6d7db52b7e59\ntimeoutMins: 30\n"))

		assert.Equal(t, xtime.Minutes(30), cfg.Dispatcher.Timeout)
		assert.Equal(t, RunnerTypes{RunnerTypeTf}, cfg.Dispatcher.ForTypes)
	})

	t.Run("legacy Spring layout", func(t *testing.T) {
		cfg := loadFile(t, "runner-config-old-springboot.yml")

		assert.Equal(t, Config{
			Uuid:       uuid.MustParse("a9786b14-ecfe-44dd-b04c-2bcfd326aa23"),
			Api:        newApi("http://localhost:8304", "", ""),
			Crypto:     crypto.Crypto{PrivateKeys: crypto.PrivateKeys{parsedPrivateKey(t)}},
			Dispatcher: defaultDispatcher(),
			Tf:         defaultTf(uuid.MustParse("a9786b14-ecfe-44dd-b04c-2bcfd326aa23")),
		}, cfg)
	})

	t.Run("legacy Spring layout accepts the other keys of a Spring config", func(t *testing.T) {
		load(t, []byte("server:\n  port: 8080\nlogging:\n  level:\n    ROOT: INFO\nblockrunner:\n  uuid: 79090ca8-94f8-4e56-b382-6d7db52b7e59\n"))
	})

	t.Run("implicit private key file", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, crypto.ImplicitPrivateKeyFile), []byte(privateKeyPEM), 0o600))
		t.Chdir(dir)

		cfg := load(t, nil)

		assert.Equal(t, crypto.PrivateKeyFiles{{Path: crypto.ImplicitPrivateKeyFile, PrivateKey: parsedPrivateKey(t)}}, cfg.Crypto.PrivateKeyFiles)
	})

	t.Run("file from env", func(t *testing.T) {
		t.Setenv("RUNNER_CONFIG_FILE", "")
		t.Setenv("RUNCONTROLLER_CONFIG_FILE", "")
		assert.Equal(t, "runner-config.yml", FileFromEnv())

		t.Setenv("RUNCONTROLLER_CONFIG_FILE", "controller.yml")
		assert.Equal(t, "controller.yml", FileFromEnv())

		t.Setenv("RUNNER_CONFIG_FILE", "runner.yml")
		assert.Equal(t, "runner.yml", FileFromEnv())
	})
}

func Test_Load_SubstitutesEnvIntoTypedValues(t *testing.T) {
	unsetRunnerEnv(t)
	t.Setenv("JOBS", "7")
	t.Setenv("INSECURE", "true")
	t.Setenv("KEY_FILE", "./testdata/private-key.pem")

	cfg, err := Load(t.Context(), []byte(`
dispatcher:
  register:
    displayName: !no-envsubst runner ${NOT_RESOLVED}
  maxConcurrentJobs: ${JOBS}
  pollingInterval: ${POLLING:30}
crypto:
  privateKeyFiles:
    - ${KEY_FILE}
tf:
  insecureHostKeys: ${INSECURE:false}
  cacheDir: &dir '${DIR:/tmp/dir # with: "yaml" syntax}'
api:
  url: ${URL:http://localhost:8080}
  auth:
    basic:
      username: !!str ${API_USER:007}
      password: |
        ${PASSWORD:secret}
`))

	require.NoError(t, err)
	assert.Equal(t, "runner ${NOT_RESOLVED}", cfg.Dispatcher.Register.DisplayName)
	assert.Equal(t, 7, cfg.Dispatcher.MaxConcurrentJobs)
	assert.Equal(t, xtime.Seconds(30), cfg.Dispatcher.PollingInterval)
	assert.Equal(t, "./testdata/private-key.pem", cfg.Crypto.PrivateKeyFiles[0].Path)
	assert.True(t, cfg.Tf.SkipHostKeyValidation)
	assert.Equal(t, `/tmp/dir # with: "yaml" syntax`, cfg.Tf.CacheDir)
	assert.Equal(t, xurl.MustParsef("http://localhost:8080"), cfg.Api.Url)
	assert.Equal(t, "007", cfg.Api.Auth.Basic.Username)
	assert.Equal(t, "secret\n", cfg.Api.Auth.Basic.Password)
}

func Test_Load_FailsNamingTheProblem(t *testing.T) {
	unsetRunnerEnv(t)
	for name, test := range map[string]struct {
		yml  string
		errs []string
	}{
		"an unknown field": {"dispatcher:\n  pollingIntervall: 5s\n", []string{
			`new layout: [2:3] unknown field "pollingIntervall"`, `legacy layout: [1:1] unknown field "dispatcher"`,
		}},
		"a legacy field in the new layout": {"dispatcher:\n  maxConcurrentJobs: 5\nrunnerUuid: 79090ca8-94f8-4e56-b382-6d7db52b7e59\n", []string{
			`unknown field "runnerUuid"`,
		}},
		"a new field in a legacy layout": {"api:\n  url: http://localhost:8080\n  user: bb-api\n  auth:\n    profile: local-dev\n", []string{
			`new layout: [3:3] unknown field "user"`, `legacy layout: [4:3] unknown field "auth"`,
		}},
		"an unknown field below blockrunner": {"blockrunner:\n  uuid: 79090ca8-94f8-4e56-b382-6d7db52b7e59\n  unknown-key: x\n", []string{
			`unknown field "unknownkey"`,
		}},
		"several documents": {"workspace: a\n---\nworkspace: b\n", []string{"2 yaml documents"}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Load(t.Context(), []byte(test.yml))
			for _, expected := range test.errs {
				assert.ErrorContains(t, err, expected)
			}
		})
	}

	t.Run("names the file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "broken.yml")
		require.NoError(t, os.WriteFile(path, []byte("unknown: 1\n"), 0o600))
		_, err := LoadFile(t.Context(), path)
		assert.ErrorContains(t, err, "config file "+path+": new layout")
	})

	t.Run("names the line of an unset placeholder", func(t *testing.T) {
		_, err := Load(t.Context(), []byte("workspace: ${WORKSPACE_UNSET}\ndisplayName: ${DISPLAY_UNSET}\n"))
		require.ErrorContains(t, err, "line 1: environment variable WORKSPACE_UNSET")
		assert.ErrorContains(t, err, "line 2: environment variable DISPLAY_UNSET")
	})
}

func load(t *testing.T, data []byte) Config {
	t.Helper()
	cfg, err := Load(t.Context(), data)
	require.NoError(t, err)
	return cfg
}

func loadFile(t *testing.T, file string) Config {
	t.Helper()
	data, err := testdata.ReadFile("testdata/" + file)
	require.NoError(t, err)
	return load(t, data)
}

func unsetRunnerEnv(t *testing.T) {
	t.Helper()
	for _, env := range os.Environ() {
		key, _, _ := strings.Cut(env, "=")
		if strings.HasPrefix(key, "RUNNER_") {
			t.Setenv(key, "")
			require.NoError(t, os.Unsetenv(key))
		}
	}
}

func defaultTf(runnerUuid uuid.UUID) tf.Config {
	cfg := tf.Default()
	cfg.CacheDir = filepath.Join(os.TempDir(), "bb-runner-"+runnerUuid.String(), "tfcache")
	return cfg
}

func newApi(apiUrl, clientId, clientSecret string) (api Api) {
	api.Url = xurl.MustParsef("%s", apiUrl)
	api.Auth.Basic = BasicAuth{Username: "bb-api", Password: "guest"}
	api.Auth.Key = ApiKey{ClientId: clientId, ClientSecret: clientSecret}
	return
}

func parsedPrivateKey(t *testing.T) (key crypto.PrivateKey) {
	t.Helper()
	require.NoError(t, key.UnmarshalText([]byte(privateKeyPEM)))
	return
}
