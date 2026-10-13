package config

import (
	"io"
	"log"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/internal/config/xtime"
	"github.com/meshcloud/building-block-runner/internal/config/xurl"
)

func TestExpandPlaceholders(t *testing.T) {
	t.Setenv("SET", "value")
	t.Setenv("EMPTY", "")
	t.Setenv("RUNNER_APIURL", "relaxed")

	for input, expected := range map[string]string{
		"plain":                   "plain",
		"${SET}":                  "value",
		"a-${SET}-b":              "a-value-b",
		"${SET}${SET}":            "valuevalue",
		"${UNSET:default}":        "default",
		"${EMPTY:default}":        "default",
		"${UNSET:-1}":             "-1",
		"${SET:default}":          "value",
		"${UNSET:}":               "",
		"${UNSET:http://x:8080}":  "http://x:8080",
		"${UNSET:${SET}}":         "value",
		"${UNSET:${UNSET2:deep}}": "deep",
		"${runner.api-url}":       "relaxed",
		`\${SET}`:                 "${SET}",
		"pa$$word $SET $(K8S)":    "pa$$word $SET $(K8S)",
	} {
		actual, err := expandPlaceholders(input)
		if assert.NoError(t, err, input) {
			assert.Equal(t, expected, actual, input)
		}
	}

	for input, expectedErr := range map[string]string{
		"${UNSET}":        "environment variable UNSET of placeholder ${UNSET} is not set or empty",
		"${EMPTY}":        "environment variable EMPTY of placeholder ${EMPTY} is not set or empty",
		"${UNSET:${NO}}":  "environment variable NO of placeholder ${NO} is not set or empty",
		"${SET":           "placeholder ${SET has no closing brace",
		"${:default}":     "placeholder ${:default} has no name",
		"a ${UNSET:${X}b": "has no closing brace",
	} {
		_, err := expandPlaceholders(input)
		assert.ErrorContains(t, err, expectedErr, input)
	}
}

func TestLoadSubstitutesEnv(t *testing.T) {
	unsetRunnerEnv(t)
	t.Setenv("JOBS", "7")
	t.Setenv("INSECURE", "true")
	t.Setenv("KEY_FILE", "./testdata/private-key.pem")

	cfg, err := Load([]byte(`
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
  workingDir: "${WD:/tmp/wd}"
  installDir: &dir '${DIR:/tmp/dir # with: "yaml" syntax}'
api:
  url: ${URL:http://localhost:8080}
  auth:
    basic:
      username: !!str ${API_USER:007}
      password: |
        ${PASSWORD:secret}
`), log.New(io.Discard, "", 0))

	require.NoError(t, err)
	assert.Equal(t, "runner ${NOT_RESOLVED}", cfg.Dispatcher.Register.DisplayName)
	assert.Equal(t, 7, cfg.Dispatcher.MaxConcurrentJobs)
	assert.Equal(t, xtime.Seconds(30), cfg.Dispatcher.PollingInterval)
	assert.Equal(t, "./testdata/private-key.pem", cfg.Crypto.PrivateKeyFiles[0].Path)
	assert.True(t, cfg.Tf.SkipHostKeyValidation)
	assert.Equal(t, "/tmp/wd", cfg.Tf.TfParentWorkingDir)
	assert.Equal(t, `/tmp/dir # with: "yaml" syntax`, cfg.Tf.TfInstallDir)
	assert.Equal(t, xurl.MustParse("http://localhost:8080"), cfg.Api.Url)
	assert.Equal(t, "007", cfg.Api.Auth.Basic.Username)
	assert.Equal(t, "secret\n", cfg.Api.Auth.Basic.Password)
}

func TestLoadFailsOnUnresolvedPlaceholders(t *testing.T) {
	unsetRunnerEnv(t)
	_, err := Load([]byte("workspace: ${WORKSPACE_UNSET}\ndisplayName: ${DISPLAY_UNSET}\n"), log.New(io.Discard, "", 0))
	assert.ErrorContains(t, err, "line 1: environment variable WORKSPACE_UNSET")
	assert.ErrorContains(t, err, "line 2: environment variable DISPLAY_UNSET")
}
