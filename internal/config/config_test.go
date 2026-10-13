package config

import (
	"embed"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/internal/config/xtime"
	"github.com/meshcloud/building-block-runner/internal/config/xurl"
	"github.com/meshcloud/building-block-runner/internal/crypto"
)

//go:embed testdata/*.yml
var testdata embed.FS

//go:embed testdata/private-key.pem
var privateKeyPEM string

func TestLoad(t *testing.T) {
	t.Run("runner-config-old-springboot.yml", func(t *testing.T) {
		unsetRunnerEnv(t)
		assert.Equal(t, Config{
			Uuid:   uuid.MustParse("a9786b14-ecfe-44dd-b04c-2bcfd326aa23"),
			Api:    newApi("http://localhost:8304", "", ""),
			Crypto: crypto.Crypto{PrivateKeys: crypto.PrivateKeys{parsedPrivateKey(t)}},
			Dispatcher: Dispatcher{
				PollingInterval:   xtime.Seconds(10),
				MaxConcurrentJobs: 20,
				Timeout:           xtime.Minutes(60),
			},
			Tf: defaultTf(),
		}, load(t, "runner-config-old-springboot.yml"))
	})

	t.Run("runner-config-old-springboot.yml with env", func(t *testing.T) {
		unsetRunnerEnv(t)
		t.Setenv("RUNNER_UUID", "0b6d1b5e-3c0f-4a51-9d0e-6f1f4b7c2a10")
		t.Setenv("RUNNER_API_URL", "https://meshstack.example.com")
		t.Setenv("RUNNER_API_USERNAME", "other-user")
		t.Setenv("RUNNER_API_PASSWORD", "other-password")
		t.Setenv("RUNNER_API_CLIENT_ID", "other-client-id")
		t.Setenv("RUNNER_API_CLIENT_SECRET", "other-client-secret")

		expected := Config{
			Uuid:   uuid.MustParse("0b6d1b5e-3c0f-4a51-9d0e-6f1f4b7c2a10"),
			Crypto: crypto.Crypto{PrivateKeys: crypto.PrivateKeys{parsedPrivateKey(t)}},
			Dispatcher: Dispatcher{
				PollingInterval:   xtime.Seconds(10),
				MaxConcurrentJobs: 20,
				Timeout:           xtime.Minutes(60),
			},
			Tf: defaultTf(),
		}
		expected.Api.Url = xurl.MustParse("https://meshstack.example.com")
		expected.Api.Auth.Basic.Username = "other-user"
		expected.Api.Auth.Basic.Password = "other-password"
		expected.Api.Auth.Key.ClientId = "other-client-id"
		expected.Api.Auth.Key.ClientSecret = "other-client-secret"
		assert.Equal(t, expected, load(t, "runner-config-old-springboot.yml"))
	})

	t.Run("runner-config-old-tf.yml with env", func(t *testing.T) {
		unsetRunnerEnv(t)
		t.Setenv("RUNNER_UUID", "0b6d1b5e-3c0f-4a51-9d0e-6f1f4b7c2a10")
		t.Setenv("RUNNER_API_URL", "https://meshstack.example.com")
		t.Setenv("RUNNER_API_USERNAME", "other-user")
		t.Setenv("RUNNER_API_PASSWORD", "other-pa$$word")
		t.Setenv("RUNNER_API_CLIENT_ID", "other-client-id")
		t.Setenv("RUNNER_API_CLIENT_SECRET", "other-client-secret")
		t.Setenv("RUNNER_PRIVATE_KEY", strings.ReplaceAll(privateKeyPEM, "\n", ""))
		t.Setenv("RUNNER_PRIVATE_KEY_FILE", "./testdata/private-key.pem,./testdata/private-key.pem")
		t.Setenv("RUNNER_DISPATCHER_MAX_CONCURRENT_JOBS", "5")
		t.Setenv("RUNNER_TF_WORKING_DIR", "/other/wd")
		t.Setenv("RUNNER_TF_WS_TIMEOUT", "7")

		cfg, logs := loadWithLogs(t, "runner-config-old-tf.yml")

		assert.Equal(t, uuid.MustParse("0b6d1b5e-3c0f-4a51-9d0e-6f1f4b7c2a10"), cfg.Uuid)
		expectedApi := newApi("https://meshstack.example.com", "other-client-id", "other-client-secret")
		expectedApi.Auth.Basic.Username = "other-user"
		expectedApi.Auth.Basic.Password = "other-pa$$word"
		assert.Equal(t, expectedApi, cfg.Api)
		privateKeyFile := crypto.PrivateKeyFile{Path: "./testdata/private-key.pem", PrivateKey: parsedPrivateKey(t)}
		assert.Equal(t, crypto.Crypto{
			PrivateKeys:     crypto.PrivateKeys{parsedPrivateKey(t)},
			PrivateKeyFiles: crypto.PrivateKeyFiles{privateKeyFile, privateKeyFile},
		}, cfg.Crypto)
		assert.Equal(t, 5, cfg.Dispatcher.MaxConcurrentJobs)
		assert.Equal(t, "/other/wd", cfg.Tf.TfParentWorkingDir)
		assert.Equal(t, xtime.Minutes(7), cfg.Tf.WsTimeout)
		assert.Equal(t, []string{
			"Using RUNNER_UUID from environment",
			"Using RUNNER_API_URL from environment",
			"Using RUNNER_API_USERNAME from environment for RUNNER_API_AUTH_BASIC_USERNAME",
			"Using RUNNER_API_PASSWORD from environment for RUNNER_API_AUTH_BASIC_PASSWORD",
			"Using RUNNER_API_CLIENT_ID from environment for RUNNER_API_AUTH_KEY_CLIENT_ID",
			"Using RUNNER_API_CLIENT_SECRET from environment for RUNNER_API_AUTH_KEY_CLIENT_SECRET",
			"Using RUNNER_PRIVATE_KEY from environment for RUNNER_CRYPTO_PRIVATE_KEYS",
			"Using RUNNER_PRIVATE_KEY_FILE from environment for RUNNER_CRYPTO_PRIVATE_KEY_FILES",
			"Using RUNNER_DISPATCHER_MAX_CONCURRENT_JOBS from environment",
			"Using RUNNER_TF_WORKING_DIR from environment",
			"Using RUNNER_TF_WS_TIMEOUT from environment",
		}, logs)
	})

	t.Run("runner-config-old-tf.yml with env names that win over their fallback", func(t *testing.T) {
		unsetRunnerEnv(t)
		t.Setenv("RUNNER_CRYPTO_PRIVATE_KEY_FILES", "./testdata/private-key.pem")
		t.Setenv("RUNNER_PRIVATE_KEY_FILE", "./testdata/private-key.pem,./testdata/private-key.pem")

		cfg, logs := loadWithLogs(t, "runner-config-old-tf.yml")

		assert.Len(t, cfg.Crypto.PrivateKeyFiles, 1)
		assert.Equal(t, []string{"Using RUNNER_CRYPTO_PRIVATE_KEY_FILES from environment"}, logs)
	})

	t.Run("runner-config-old-tf.yml", func(t *testing.T) {
		unsetRunnerEnv(t)
		assert.Equal(t, Config{
			Uuid: uuid.MustParse("79090ca8-94f8-4e56-b382-6d7db52b7e59"),
			Api:  newApi("http://localhost:8300", "", ""),
			Crypto: crypto.Crypto{
				PrivateKeys:     crypto.PrivateKeys{parsedPrivateKey(t)},
				PrivateKeyFiles: crypto.PrivateKeyFiles{{Path: "./testdata/private-key.pem", PrivateKey: parsedPrivateKey(t)}},
			},
			Dispatcher: Dispatcher{
				ForTypes:          RunnerTypes{RunnerTypeTf},
				PollingInterval:   xtime.Seconds(10),
				MaxConcurrentJobs: 1,
				Timeout:           xtime.Minutes(60),
			},
			Tf: Tf{
				TfParentWorkingDir: "/tmp/runner/wd",
				TfInstallDir:       "/tmp/runner/tfbin",
				WsTimeout:          xtime.Minutes(5),
				InitTimeout:        xtime.Minutes(3),
			},
		}, load(t, "runner-config-old-tf.yml"))
	})
}

func TestLoadWithoutFile(t *testing.T) {
	unsetRunnerEnv(t)
	t.Setenv("RUNNER_UUID", "0b6d1b5e-3c0f-4a51-9d0e-6f1f4b7c2a10")

	cfg, err := Load(nil, log.New(io.Discard, "", 0))

	require.NoError(t, err)
	assert.Equal(t, Config{
		Uuid:       uuid.MustParse("0b6d1b5e-3c0f-4a51-9d0e-6f1f4b7c2a10"),
		Dispatcher: Dispatcher{PollingInterval: xtime.Seconds(10), MaxConcurrentJobs: 20, Timeout: xtime.Minutes(60)},
		Tf: Tf{
			TfParentWorkingDir: "/tmp/runner/wd",
			TfInstallDir:       "/tmp/runner/tfbin",
			WsTimeout:          xtime.Minutes(5),
			InitTimeout:        xtime.Minutes(3),
		},
	}, cfg)
}

func TestLoadTfDefaults(t *testing.T) {
	unsetRunnerEnv(t)
	t.Setenv("RUNNER_TF_INIT_TIMEOUT", "1")

	cfg, err := Load([]byte("tf:\n  wsTimeout: 7\n"), log.New(io.Discard, "", 0))

	require.NoError(t, err)
	assert.Equal(t, Tf{
		TfParentWorkingDir: "/tmp/runner/wd",
		TfInstallDir:       "/tmp/runner/tfbin",
		WsTimeout:          xtime.Minutes(7),
		InitTimeout:        xtime.Minutes(1),
	}, cfg.Tf)
}

func TestLoadApiTokenFromEnv(t *testing.T) {
	unsetRunnerEnv(t)
	t.Setenv("MESHSTACK_API_TOKEN", "run-token")

	var logs strings.Builder
	cfg, err := Load([]byte("api:\n  url: http://localhost:8080\n"), log.New(&logs, "", 0))

	require.NoError(t, err)
	assert.Equal(t, "run-token", cfg.Api.Auth.Token)
	assert.Equal(t, "Using MESHSTACK_API_TOKEN from environment for RUNNER_API_AUTH_TOKEN\n", logs.String())
}

func TestLoadDispatcherForTypesFromEnv(t *testing.T) {
	unsetRunnerEnv(t)
	t.Setenv("RUNNER_DISPATCHER_FOR_TYPES", "tf,MANUAL,all")

	cfg, err := Load([]byte("dispatcher:\n  forTypes: [github]\n"), log.New(io.Discard, "", 0))

	require.NoError(t, err)
	assert.Equal(t, RunnerTypes{RunnerTypeTf, RunnerTypeManual, RunnerTypeAll}, cfg.Dispatcher.ForTypes)
}

func TestLoadLayouts(t *testing.T) {
	load := func(yml string) error {
		unsetRunnerEnv(t)
		_, err := Load([]byte(yml), log.New(io.Discard, "", 0))
		return err
	}

	t.Run("fails on an unknown field", func(t *testing.T) {
		err := load("dispatcher:\n  pollingIntervall: 5s\n")
		assert.ErrorContains(t, err, `new layout: [2:3] unknown field "pollingIntervall"`)
		assert.ErrorContains(t, err, `legacy layout: [1:1] unknown field "dispatcher"`)
	})

	t.Run("fails on a legacy field in the new layout", func(t *testing.T) {
		assert.ErrorContains(t, load("dispatcher:\n  maxConcurrentJobs: 5\nrunnerUuid: 79090ca8-94f8-4e56-b382-6d7db52b7e59\n"), `unknown field "runnerUuid"`)
	})

	t.Run("fails on a new field in a legacy layout", func(t *testing.T) {
		err := load("api:\n  url: http://localhost:8080\n  user: bb-api\n  auth:\n    profile: local-dev\n")
		assert.ErrorContains(t, err, `new layout: [3:3] unknown field "user"`)
		assert.ErrorContains(t, err, `legacy layout: [4:3] unknown field "auth"`)
	})

	t.Run("fails on an unknown field below blockrunner", func(t *testing.T) {
		assert.ErrorContains(t, load("blockrunner:\n  uuid: 79090ca8-94f8-4e56-b382-6d7db52b7e59\n  unknown-key: x\n"), `unknown field "unknownkey"`)
	})

	t.Run("maps the legacy tf timeoutMins to the dispatcher timeout", func(t *testing.T) {
		unsetRunnerEnv(t)
		cfg, err := Load([]byte("runnerUuid: 79090ca8-94f8-4e56-b382-6d7db52b7e59\ntimeoutMins: 30\n"), log.New(io.Discard, "", 0))
		require.NoError(t, err)
		assert.Equal(t, xtime.Minutes(30), cfg.Dispatcher.Timeout)
		assert.Equal(t, RunnerTypes{RunnerTypeTf}, cfg.Dispatcher.ForTypes)
	})

	t.Run("accepts the other keys of a Spring config", func(t *testing.T) {
		assert.NoError(t, load("server:\n  port: 8080\nlogging:\n  level:\n    ROOT: INFO\nblockrunner:\n  uuid: 79090ca8-94f8-4e56-b382-6d7db52b7e59\n"))
	})
}

func TestLoadImplicitPrivateKeyFile(t *testing.T) {
	unsetRunnerEnv(t)
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, crypto.ImplicitPrivateKeyFile), []byte(privateKeyPEM), 0o600))
	t.Chdir(dir)

	var logs strings.Builder
	cfg, err := Load(nil, log.New(&logs, "", 0))

	require.NoError(t, err)
	assert.Equal(t, crypto.PrivateKeyFiles{{Path: crypto.ImplicitPrivateKeyFile, PrivateKey: parsedPrivateKey(t)}}, cfg.Crypto.PrivateKeyFiles)
	assert.Equal(t, "Using private key file runner-private.pem\n", logs.String())
}

func TestLoadFile(t *testing.T) {
	unsetRunnerEnv(t)

	t.Run("loads defaults and environment without a file", func(t *testing.T) {
		var logs strings.Builder
		cfg, err := LoadFile(filepath.Join(t.TempDir(), "missing.yml"), log.New(&logs, "", 0))
		require.NoError(t, err)
		assert.Equal(t, defaultConfig(), cfg)
		assert.Contains(t, logs.String(), "missing.yml does not exist, using defaults and environment")
	})

	t.Run("names the file in errors", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "broken.yml")
		require.NoError(t, os.WriteFile(path, []byte("unknown: 1\n"), 0o600))
		_, err := LoadFile(path, log.New(io.Discard, "", 0))
		assert.ErrorContains(t, err, "config file "+path+": new layout")
	})
}

func TestFileFromEnv(t *testing.T) {
	t.Setenv("RUNNER_CONFIG_FILE", "")
	t.Setenv("RUNCONTROLLER_CONFIG_FILE", "")
	assert.Equal(t, "runner-config.yml", FileFromEnv())

	t.Setenv("RUNCONTROLLER_CONFIG_FILE", "controller.yml")
	assert.Equal(t, "controller.yml", FileFromEnv())

	t.Setenv("RUNNER_CONFIG_FILE", "runner.yml")
	assert.Equal(t, "runner.yml", FileFromEnv())
}

func TestLoadFailsOnMultipleDocuments(t *testing.T) {
	_, err := Load([]byte("workspace: a\n---\nworkspace: b\n"), log.New(io.Discard, "", 0))
	assert.ErrorContains(t, err, "2 yaml documents")
}

func load(t *testing.T, file string) Config {
	t.Helper()
	cfg, _ := loadWithLogs(t, file)
	return cfg
}

func loadWithLogs(t *testing.T, file string) (Config, []string) {
	t.Helper()
	data, err := testdata.ReadFile("testdata/" + file)
	require.NoError(t, err)

	var logs strings.Builder
	cfg, err := Load(data, log.New(&logs, "", 0))
	require.NoError(t, err)
	return cfg, strings.Split(strings.TrimSuffix(logs.String(), "\n"), "\n")
}

func unsetRunnerEnv(t *testing.T) {
	t.Helper()
	for _, env := range os.Environ() {
		key, _, _ := strings.Cut(env, "=")
		if strings.HasPrefix(key, "RUNNER_") || key == "MESHSTACK_API_TOKEN" {
			t.Setenv(key, "")
			require.NoError(t, os.Unsetenv(key))
		}
	}
}

func newApi(apiUrl, clientId, clientSecret string) (api Api) {
	api.Url = xurl.MustParse(apiUrl)
	api.Auth.Basic.Username = "bb-api"
	api.Auth.Basic.Password = "guest"
	api.Auth.Key.ClientId = clientId
	api.Auth.Key.ClientSecret = clientSecret
	return
}

func parsedPrivateKey(t *testing.T) (key crypto.PrivateKey) {
	require.NoError(t, key.UnmarshalText([]byte(privateKeyPEM)))
	return
}
