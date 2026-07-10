package tfrun

// CP9 (PLAN_DETAIL_01_tf_characterization_tests.md §9): ReadConfig black-box, NewAuthProvider,
// the DTO->internal conversions (incl. ToInternalWithoutDecryption / runDTOToInternal error
// branches, knownHostsToInternal), and the small enum/status types' error branches. These pin the
// config-precedence and DTO contracts (D9 pin 16a: the k8s single-run run JSON shape) at unit level;
// B7/B12 already pinned in bug_inventory_test.go are not duplicated here.

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"testing"

	meshapi "github.com/meshcloud/building-block-runner/go-meshapi-client/meshapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withSavedAppConfig snapshots the package-level AppConfig (a phase-2 D4 injection target) and the
// single-run env vars ReadConfig/validateAuthConfig read, restoring them after the test so ReadConfig
// tests can freely mutate the global without leaking into sibling suites.
func withSavedAppConfig(t *testing.T) {
	t.Helper()
	previous := AppConfig
	t.Cleanup(func() { AppConfig = previous })
	AppConfig = TfRunnerConfig{}
	// Neutralize any ambient single-run signal so validateAuthConfig takes the polling branch unless a
	// subtest opts in explicitly.
	t.Setenv(envExecutionMode, "")
	t.Setenv(envRunJsonFilePath, "")
}

func writeConfigFile(t *testing.T, yaml string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "runner-config.yml")
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatalf("writeConfigFile: %v", err)
	}
	return path
}

func discardLogger() *log.Logger { return log.New(os.NewFile(0, os.DevNull), "", 0) }

func Test_ReadConfig_FileOnly(t *testing.T) {
	withSavedAppConfig(t)
	path := writeConfigFile(t, `
timeoutMins: 42
workingDir: /tmp/wd
tfInstallDir: /tmp/tf
runnerUuid: file-uuid
api:
  url: https://api.example.com
  user: file-user
  password: file-pass
`)
	t.Setenv(envConfigFile, path)

	require.NoError(t, ReadConfig(discardLogger()))
	assert.Equal(t, 42, AppConfig.TfCommandTimeoutMins)
	assert.Equal(t, "file-uuid", AppConfig.RunnerUuid)
	assert.Equal(t, "https://api.example.com", AppConfig.RunApiBackend.Url)
	assert.Equal(t, "file-user", AppConfig.RunApiBackend.User)
}

func Test_ReadConfig_EnvOverridesFile(t *testing.T) {
	withSavedAppConfig(t)
	path := writeConfigFile(t, `
runnerUuid: file-uuid
api:
  url: https://file.example.com
  user: file-user
  password: file-pass
`)
	t.Setenv(envConfigFile, path)
	t.Setenv(envRunnerUuid, "env-uuid")
	t.Setenv(envApiUrl, "https://env.example.com")

	require.NoError(t, ReadConfig(discardLogger()))
	assert.Equal(t, "env-uuid", AppConfig.RunnerUuid, "RUNNER_UUID env must override the file")
	assert.Equal(t, "https://env.example.com", AppConfig.RunApiBackend.Url)
}

func Test_ReadConfig_MissingFileUsesDefaultsAndEnv(t *testing.T) {
	withSavedAppConfig(t)
	t.Setenv(envConfigFile, filepath.Join(t.TempDir(), "does-not-exist.yml"))
	t.Setenv(envRunnerUuid, "env-uuid")
	t.Setenv(envAuthClientId, "cid")
	t.Setenv(envAuthClientSecret, "csecret")

	require.NoError(t, ReadConfig(discardLogger()), "a missing config file must fall back to defaults+env")
	assert.Equal(t, "env-uuid", AppConfig.RunnerUuid)
	assert.Equal(t, "cid", AppConfig.RunApiBackend.ClientId)
	// applyEnvVars defaults PrivateKeyFile to the well-known name when neither file nor env set it.
	assert.Equal(t, defaultPrivateKeyFile, AppConfig.PrivateKeyFile)
}

func Test_ReadConfig_InvalidYamlReturnsError(t *testing.T) {
	withSavedAppConfig(t)
	// A bare scalar where a mapping is expected makes yaml.Unmarshal into TfRunnerConfig fail.
	path := writeConfigFile(t, "this-is-not-a-mapping")
	t.Setenv(envConfigFile, path)

	assert.Error(t, ReadConfig(discardLogger()))
}

func Test_ReadConfig_MissingAuthInPollingModeFails(t *testing.T) {
	withSavedAppConfig(t)
	path := writeConfigFile(t, "runnerUuid: some-uuid\n")
	t.Setenv(envConfigFile, path)

	err := ReadConfig(discardLogger())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "authentication required in polling mode")
}

func Test_ReadConfig_MissingRunnerUuidFails(t *testing.T) {
	withSavedAppConfig(t)
	path := writeConfigFile(t, `
api:
  user: u
  password: p
`)
	t.Setenv(envConfigFile, path)

	err := ReadConfig(discardLogger())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "runnerUuid is required")
}

func Test_ReadConfig_LoadsPrivateKeyFile(t *testing.T) {
	withSavedAppConfig(t)
	keyPath := filepath.Join(t.TempDir(), "key.pem")
	require.NoError(t, os.WriteFile(keyPath, []byte("PRIVATE-KEY-CONTENTS"), 0o600))

	path := writeConfigFile(t, `
runnerUuid: uuid
api:
  user: u
  password: p
`)
	t.Setenv(envConfigFile, path)
	t.Setenv(envPrivateKeyFile, keyPath)

	require.NoError(t, ReadConfig(discardLogger()))
	assert.Equal(t, "PRIVATE-KEY-CONTENTS", AppConfig.PrivateKey, "private key file contents must load into PrivateKey")
}

func Test_ReadConfig_SingleRunModeSkipsAuthRequirement(t *testing.T) {
	withSavedAppConfig(t)
	path := writeConfigFile(t, "runnerUuid: uuid\n")
	t.Setenv(envConfigFile, path)
	t.Setenv(envExecutionMode, "single-run")
	t.Setenv(envRunJsonFilePath, "/var/run/secrets/meshstack/run.json")

	require.NoError(t, ReadConfig(discardLogger()), "single-run mode with RUN_JSON_FILE_PATH must not require auth")
}

func Test_ReadConfig_BothAuthMethodsConfigured(t *testing.T) {
	withSavedAppConfig(t)
	path := writeConfigFile(t, `
runnerUuid: uuid
api:
  user: u
  password: p
  clientId: cid
  clientSecret: csecret
`)
	t.Setenv(envConfigFile, path)

	// Both auth methods fully configured is a valid configuration (API key wins) — ReadConfig logs
	// the precedence note and succeeds.
	require.NoError(t, ReadConfig(discardLogger()))
}

func Test_ReadConfig_DefaultConfigPathWhenEnvUnset(t *testing.T) {
	withSavedAppConfig(t)
	t.Setenv(envConfigFile, "") // empty => ReadConfig falls back to the default "runner-config.yml"
	t.Setenv(envRunnerUuid, "env-uuid")
	t.Setenv(envAuthUsername, "u")
	t.Setenv(envAuthPassword, "p")

	// The default file does not exist in the package test dir, so ReadConfig uses defaults + env.
	require.NoError(t, ReadConfig(discardLogger()))
	assert.Equal(t, "env-uuid", AppConfig.RunnerUuid)
}

func Test_ReadConfig_InsecureHostKeysLogged(t *testing.T) {
	withSavedAppConfig(t)
	path := writeConfigFile(t, `
runnerUuid: uuid
insecureHostKeys: true
api:
  user: u
  password: p
`)
	t.Setenv(envConfigFile, path)

	require.NoError(t, ReadConfig(discardLogger()))
	assert.True(t, AppConfig.SkipHostKeyValidation)
}

func Test_ReadConfig_SingleRunMissingRunJsonPathFails(t *testing.T) {
	withSavedAppConfig(t)
	path := writeConfigFile(t, "runnerUuid: uuid\n")
	t.Setenv(envConfigFile, path)
	t.Setenv(envExecutionMode, "single-run")
	t.Setenv(envRunJsonFilePath, "") // required in single-run mode

	err := ReadConfig(discardLogger())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "RUN_JSON_FILE_PATH")
}

func Test_ApplyPrivateKeyFile_EmptyPathAndReadError(t *testing.T) {
	cfg := TfRunnerConfig{}
	// Empty path is a no-op.
	applyPrivateKeyFile("", &cfg, discardLogger())
	assert.Empty(t, cfg.PrivateKey)

	// A path pointing at a directory yields a non-ENOENT read error: logged as a warning, PrivateKey
	// left untouched (config.go:177-179).
	applyPrivateKeyFile(t.TempDir(), &cfg, discardLogger())
	assert.Empty(t, cfg.PrivateKey)
}

func Test_RunScript_SucceedsWithEmptyRunJson(t *testing.T) {
	res, err := RunScript(context.Background(), ScriptParams{
		Name:    "pre-run",
		Script:  "echo hello",
		WorkDir: t.TempDir(),
		RunMode: APPLY.str(),
		// empty RunJsonBase64 => decodeRunJSON returns (nil, nil) (scriptcmd.go:156-158)
	})
	require.NoError(t, err)
	assert.Equal(t, 0, res.ExitCode)
	assert.Contains(t, res.SystemMessage, "hello")
}

func Test_NewAuthProvider_Precedence(t *testing.T) {
	apiKey := RunApiConfig{Url: "u", ClientId: "cid", ClientSecret: "csecret", User: "usr", Password: "pw"}
	assert.IsType(t, &meshapi.ApiKeyAuth{}, apiKey.NewAuthProvider(), "clientId+clientSecret takes precedence over basic auth")

	basic := RunApiConfig{User: "usr", Password: "pw"}
	assert.IsType(t, meshapi.BasicAuth{}, basic.NewAuthProvider())

	none := RunApiConfig{}
	assert.Nil(t, none.NewAuthProvider(), "no credentials => nil provider (valid in single-run mode)")
}

// --- DTO conversions -------------------------------------------------------------------------

func Test_ToInternalWithoutDecryption_MapsAllFieldsAndForcesNonSensitive(t *testing.T) {
	dto := runDetailsDTO(
		withBehavior(DETECT.str()),
		withRepo("/repo", "modules/x"),
		withRunToken("rt"),
		withInputs(
			// Even an input flagged sensitive must be treated as already-decrypted plaintext here
			// (dtos.go:87-95): the controller decrypted it before writing the run JSON.
			buildingBlockInput("secret", "already-plain", DATA_TYPE_STRING, sensitiveInput()),
			buildingBlockInput("EXPORTED", "v", DATA_TYPE_STRING, envInput()),
		),
	)

	run, err := ToInternalWithoutDecryption(dto)
	require.NoError(t, err)
	assert.Equal(t, DETECT, run.Behavior)
	assert.Equal(t, "rt", run.RunToken)
	assert.Equal(t, "block-uuid", run.BuildingBlockId)

	secret := run.Vars["secret"]
	require.NotNil(t, secret)
	assert.False(t, secret.isSensitive, "ToInternalWithoutDecryption must force isSensitive=false")
	assert.Equal(t, "already-plain", secret.value)
	assert.True(t, run.Vars["EXPORTED"].env)
}

func Test_ToInternalWithoutDecryption_BadBehaviorReturnsError(t *testing.T) {
	dto := runDetailsDTO()
	dto.Spec.Behavior = "NONSENSE"
	_, err := ToInternalWithoutDecryption(dto)
	assert.Error(t, err)
}

func Test_ToInternalWithoutDecryption_UnparsableImplementationReturnsError(t *testing.T) {
	dto := runDetailsDTO()
	dto.Spec.Definition.Spec.Implementation = json.RawMessage(`{"terraformVersion": 12345}`) // wrong type
	_, err := ToInternalWithoutDecryption(dto)
	assert.Error(t, err)
}

func Test_RunDTOToInternal_BadBehaviorReturnsError(t *testing.T) {
	dto := runDetailsDTO()
	dto.Spec.Behavior = "NONSENSE"
	_, err := runDTOToInternal(dto)
	assert.Error(t, err)
}

func Test_RunDTOToInternal_UnparsableImplementationReturnsError(t *testing.T) {
	dto := runDetailsDTO()
	dto.Spec.Definition.Spec.Implementation = json.RawMessage(`{"async": "not-a-bool"}`)
	_, err := runDTOToInternal(dto)
	assert.Error(t, err)
}

func Test_TerraformImplAuthMethod_SshBranch(t *testing.T) {
	key := "ssh-key-pem"
	impl := &meshapi.TerraformImplementation{
		SshPrivateKey: &key,
		KnownHost:     &meshapi.KnownHostDTO{Host: "h", KeyType: "ssh-rsa", KeyValue: "AAAA"},
	}
	auth, err := terraformImplAuthMethod(impl)
	require.NoError(t, err)
	sshAuth, ok := auth.(*SshAuth)
	require.True(t, ok, "an implementation with an SSH private key must yield *SshAuth")
	assert.Equal(t, "ssh-key-pem", sshAuth.certStr)
	require.NotNil(t, sshAuth.knownHostEntry)
	assert.Equal(t, "h", sshAuth.knownHostEntry.host)

	// No key => NoAuth.
	noAuth, err := terraformImplAuthMethod(&meshapi.TerraformImplementation{})
	require.NoError(t, err)
	assert.IsType(t, &NoAuth{}, noAuth)
}

func Test_KnownHostsToInternal_Nil(t *testing.T) {
	assert.Nil(t, knownHostsToInternal(nil))
	kh := knownHostsToInternal(&meshapi.KnownHostDTO{Host: "h", KeyType: "kt", KeyValue: "kv"})
	require.NotNil(t, kh)
	assert.Equal(t, "h", kh.host)
	assert.Equal(t, "kt", kh.key)
	assert.Equal(t, "kv", kh.value)
}

// --- small enum / status types --------------------------------------------------------------

func Test_ExecutionStatus_Str_PanicsOnUnmapped(t *testing.T) {
	assert.Equal(t, "PENDING", PENDING.str())
	assert.Equal(t, "IN_PROGRESS", IN_PROGRESS.str())
	assert.Equal(t, "SUCCEEDED", SUCCEEDED.str())
	assert.Equal(t, "FAILED", FAILED.str())
	assert.Panics(t, func() { _ = ExecutionStatus(99).str() }, "an unmapped ExecutionStatus must panic")
}

func Test_RunStatus_StepPointerErrorBranches(t *testing.T) {
	empty := &RunStatus{}
	require.Error(t, empty.firstStep(), "firstStep on a stepless run returns an error")
	assert.Nil(t, empty.currentStepStatus(), "currentStepStatus out of range returns nil")

	single := &RunStatus{Steps: []*StepStatus{{Name: "only"}}}
	require.NoError(t, single.firstStep())
	assert.Equal(t, "only", single.currentStepStatus().Name)
	assert.Error(t, single.nextStep(), "nextStep past the last step returns an error")
}

func Test_ToWorkspaceStr_NilIdentifiersBecomePlaceholders(t *testing.T) {
	run := Run{BuildingBlockId: "bb-1"}
	assert.Equal(t, "_._._:bb-1", run.toWorkspaceStr(), "nil identifiers render as _ placeholders")

	ws, proj, plat := "ws", "proj", "plat"
	run2 := Run{WorkspaceIdentifier: &ws, ProjectIdentifier: &proj, FullPlatformIdentifier: &plat, BuildingBlockId: "bb-2"}
	assert.Equal(t, "ws.proj.plat:bb-2", run2.toWorkspaceStr())
}
