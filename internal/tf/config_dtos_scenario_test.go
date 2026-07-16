package tf

// ReadConfig black-box, NewAuthProvider,
// the meshapi.Run interpretation functions (incl. BehaviorFor/ParseTerraformImplementation error
// branches, knownHostsToInternal), and the small enum/status types' error branches. These pin the
// config-precedence and DTO contracts (the k8s single-run run JSON shape) at unit level;
// the bug pins already covered in bug_inventory_test.go are not duplicated here.

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/internal/config"
	meshapi "github.com/meshcloud/building-block-runner/internal/meshapi"
	"github.com/meshcloud/building-block-runner/internal/report"
)

func writeConfigFile(t *testing.T, yaml string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "runner-config.yml")
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatalf("writeConfigFile: %v", err)
	}
	return path
}

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func Test_ReadConfig_FileOnly(t *testing.T) {
	path := writeConfigFile(t, `
timeoutMins: 42
workingDir: /tmp/wd
tfInstallDir: /tmp/tf
uuid: file-uuid
api:
  url: https://api.example.com
  username: file-user
  password: file-pass
`)
	t.Setenv(envConfigFile, path)

	cfg, err := ReadConfig(discardLogger())
	require.NoError(t, err)
	assert.Equal(t, 42, cfg.TfCommandTimeoutMins)
	assert.Equal(t, "file-uuid", cfg.Uuid)
	assert.Equal(t, "https://api.example.com", cfg.Api.Url)
	assert.Equal(t, "file-user", cfg.Api.Username)
}

func Test_ReadConfig_EnvOverridesFile(t *testing.T) {
	path := writeConfigFile(t, `
uuid: file-uuid
api:
  url: https://file.example.com
  username: file-user
  password: file-pass
`)
	t.Setenv(envConfigFile, path)
	t.Setenv("RUNNER_UUID", "env-uuid")
	t.Setenv("RUNNER_API_URL", "https://env.example.com")

	cfg, err := ReadConfig(discardLogger())
	require.NoError(t, err)
	assert.Equal(t, "env-uuid", cfg.Uuid, "RUNNER_UUID env must override the file")
	assert.Equal(t, "https://env.example.com", cfg.Api.Url)
}

func Test_ReadConfig_MissingFileUsesDefaultsAndEnv(t *testing.T) {
	t.Setenv(envConfigFile, filepath.Join(t.TempDir(), "does-not-exist.yml"))
	t.Setenv("RUNNER_UUID", "env-uuid")
	t.Setenv("RUNNER_API_CLIENT_ID", "cid")
	t.Setenv("RUNNER_API_CLIENT_SECRET", "csecret")

	cfg, err := ReadConfig(discardLogger())
	require.NoError(t, err, "a missing config file must fall back to defaults+env")
	assert.Equal(t, "env-uuid", cfg.Uuid)
	assert.Equal(t, "cid", cfg.Api.ClientId)
	// ReadConfig defaults PrivateKeyFile to the well-known name when neither file nor env set it.
	assert.Equal(t, defaultPrivateKeyFile, cfg.PrivateKeyFile)
}

func Test_ReadConfig_InvalidYamlReturnsError(t *testing.T) {
	path := writeConfigFile(t, "this-is-not-a-mapping")
	t.Setenv(envConfigFile, path)

	_, err := ReadConfig(discardLogger())
	assert.Error(t, err)
}

// Test_ReadConfig_MissingUuidDefaultsToDevUuid pins the convergence behavior change: like the four
// HTTP types, tf now seeds the shared compiled-in dev uuid (config.DefaultRunnerUuid), so a file
// with no uuid: no longer fails the uuid-required check -- it boots against the local-dev runner
// identity (real deployments override via RUNNER_UUID). validateRunnerUuid still guards the
// (now unreachable via defaults) empty case.
func Test_ReadConfig_MissingUuidDefaultsToDevUuid(t *testing.T) {
	path := writeConfigFile(t, `
api:
  username: u
  password: p
`)
	t.Setenv(envConfigFile, path)

	cfg, err := ReadConfig(discardLogger())
	require.NoError(t, err)
	assert.Equal(t, config.DefaultRunnerUuid, cfg.Uuid)
}

// Test_ReadConfig_LegacyRunnerUuidDropped pins that the pre-consolidation `runnerUuid:` yaml
// spelling is no longer honored (dropped in the pre-release consolidation, docs/DEPRECATIONS.md):
// only `uuid:` populates the runner uuid, so a file carrying only `runnerUuid:` falls back to the
// shared dev-default uuid rather than picking up the legacy value.
func Test_ReadConfig_LegacyRunnerUuidDropped(t *testing.T) {
	path := writeConfigFile(t, `
runnerUuid: legacy-uuid
api:
  username: u
  password: p
`)
	t.Setenv(envConfigFile, path)

	cfg, err := ReadConfig(discardLogger())
	require.NoError(t, err)
	assert.Equal(t, config.DefaultRunnerUuid, cfg.Uuid, "the dropped runnerUuid: alias must not populate the uuid")
	assert.NotEqual(t, "legacy-uuid", cfg.Uuid)
}

func Test_ReadConfig_LoadsPrivateKeyFile(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "key.pem")
	require.NoError(t, os.WriteFile(keyPath, []byte("PRIVATE-KEY-CONTENTS"), 0o600))

	path := writeConfigFile(t, `
uuid: uuid
api:
  username: u
  password: p
`)
	t.Setenv(envConfigFile, path)
	t.Setenv(envPrivateKeyFile, keyPath)

	cfg, err := ReadConfig(discardLogger())
	require.NoError(t, err)
	assert.Equal(t, "PRIVATE-KEY-CONTENTS", cfg.PrivateKey, "private key file contents must load into PrivateKey")
}

func Test_ReadConfig_NoAuthSucceedsForFileSource(t *testing.T) {
	path := writeConfigFile(t, "uuid: uuid\n")
	t.Setenv(envConfigFile, path)

	// A config with no api: block loads fine -- tf no longer fails fast without standing
	// credentials, so a file-source (single-run) bootstrap needs none.
	_, err := ReadConfig(discardLogger())
	require.NoError(t, err, "ReadConfig must not require auth; a no-auth config loads with the dev defaults")
}

// Test_ReadConfig_AppliesDevApiDefaults pins that, with the api: block omitted, ReadConfig applies
// the shared compiled-in dev-local API defaults (config.Default*) -- the same values the other four
// fit types bake into their config.Api -- so a zero-config tf poll dispatcher boots against the
// local meshfed-API instead of failing fast for missing standing credentials.
func Test_ReadConfig_AppliesDevApiDefaults(t *testing.T) {
	path := writeConfigFile(t, "uuid: uuid\n")
	t.Setenv(envConfigFile, path)
	// Ensure no ambient RUNNER_API_* env overrides the compiled-in defaults under test.
	t.Setenv("RUNNER_API_URL", "")
	t.Setenv("RUNNER_API_USERNAME", "")
	t.Setenv("RUNNER_API_PASSWORD", "")

	cfg, err := ReadConfig(discardLogger())
	require.NoError(t, err)
	assert.Equal(t, config.DefaultApiUrl, cfg.Api.Url)
	assert.Equal(t, config.DefaultApiUsername, cfg.Api.Username)
	assert.Equal(t, config.DefaultApiPassword, cfg.Api.Password)
}

func Test_ReadConfig_BothAuthMethodsConfigured(t *testing.T) {
	path := writeConfigFile(t, `
uuid: uuid
api:
  username: u
  password: p
  clientId: cid
  clientSecret: csecret
`)
	t.Setenv(envConfigFile, path)

	// Both auth methods fully configured is a valid configuration (API key wins) — ReadConfig logs
	// the precedence note and succeeds.
	_, err := ReadConfig(discardLogger())
	require.NoError(t, err)
}

func Test_ReadConfig_DefaultConfigPathWhenEnvUnset(t *testing.T) {
	t.Setenv(envConfigFile, "") // empty => ReadConfig falls back to the default "runner-config.yml"
	t.Setenv("RUNNER_UUID", "env-uuid")
	t.Setenv("RUNNER_API_USERNAME", "u")
	t.Setenv("RUNNER_API_PASSWORD", "p")

	// The default file does not exist in the package test dir, so ReadConfig uses defaults + env.
	cfg, err := ReadConfig(discardLogger())
	require.NoError(t, err)
	assert.Equal(t, "env-uuid", cfg.Uuid)
}

func Test_ReadConfig_InsecureHostKeysLogged(t *testing.T) {
	path := writeConfigFile(t, `
uuid: uuid
insecureHostKeys: true
api:
  username: u
  password: p
`)
	t.Setenv(envConfigFile, path)

	cfg, err := ReadConfig(discardLogger())
	require.NoError(t, err)
	assert.True(t, cfg.SkipHostKeyValidation)
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
	apiKey := config.Api{Url: "u", ClientId: "cid", ClientSecret: "csecret", Username: "usr", Password: "pw"}
	assert.IsType(t, &meshapi.ApiKeyAuth{}, apiKey.NewAuthProvider(""), "clientId+clientSecret takes precedence over basic auth")

	basic := config.Api{Username: "usr", Password: "pw"}
	assert.IsType(t, meshapi.BasicAuth{}, basic.NewAuthProvider(""))

	none := config.Api{}
	assert.Nil(t, none.NewAuthProvider(""), "no credentials => nil provider (valid in single-run mode)")
}

// --- DTO conversions -------------------------------------------------------------------------

func Test_RunInterpretation_MapsAllFields(t *testing.T) {
	dto := runDetailsDTO(
		withBehavior(DETECT.str()),
		withRepo("/repo", "modules/x"),
		withRunToken("rt"),
		withInputs(
			// Values arrive plaintext: decryption happens once at the claim boundary
			// (rundecrypt.Wrap / the controller), never in these interpretation functions.
			buildingBlockInput("secret", "already-plain", DATA_TYPE_STRING, sensitiveInput()),
			buildingBlockInput("EXPORTED", "v", DATA_TYPE_STRING, envInput()),
		),
	)

	behavior, err := BehaviorFor(dto)
	require.NoError(t, err)
	assert.Equal(t, DETECT, behavior)
	assert.Equal(t, "rt", dto.Spec.RunToken)
	assert.Equal(t, "block-uuid", dto.Spec.BuildingBlock.Uuid)

	vars := VariablesFor(dto.Spec.BuildingBlock.Spec.Inputs)
	secret := vars["secret"]
	require.NotNil(t, secret)
	assert.Equal(t, "already-plain", secret.value)
	assert.True(t, vars["EXPORTED"].env)
}

func Test_BehaviorFor_BadBehaviorReturnsError(t *testing.T) {
	dto := runDetailsDTO()
	dto.Spec.Behavior = "NONSENSE"
	_, err := BehaviorFor(dto)
	assert.Error(t, err)
}

func Test_ParseTerraformImplementation_UnparsableReturnsError(t *testing.T) {
	dto := runDetailsDTO()
	dto.Spec.Definition.Spec.Implementation = json.RawMessage(`{"async": "not-a-bool"}`)
	_, err := ParseTerraformImplementation(dto)
	assert.Error(t, err)
}

func Test_TerraformImplAuthMethod_SshBranch(t *testing.T) {
	key := "ssh-key-pem"
	impl := &meshapi.TerraformImplementation{
		SshPrivateKey: &key,
		KnownHost:     &meshapi.KnownHostDTO{Host: "h", KeyType: "ssh-rsa", KeyValue: "AAAA"},
	}
	auth := terraformImplAuthMethod(impl)
	sshAuth, ok := auth.(*SshAuth)
	require.True(t, ok, "an implementation with an SSH private key must yield *SshAuth")
	assert.Equal(t, "ssh-key-pem", sshAuth.certStr)
	require.NotNil(t, sshAuth.knownHostEntry)
	assert.Equal(t, "h", sshAuth.knownHostEntry.host)

	// No key => NoAuth.
	noAuth := terraformImplAuthMethod(&meshapi.TerraformImplementation{})
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

func Test_ExecutionStatus_String(t *testing.T) {
	assert.Equal(t, "PENDING", report.PENDING.String())
	assert.Equal(t, "IN_PROGRESS", report.IN_PROGRESS.String())
	assert.Equal(t, "SUCCEEDED", report.SUCCEEDED.String())
	assert.Equal(t, "FAILED", report.FAILED.String())
	// Behavior change: tf adopted report.ExecutionStatus, whose stringer returns "UNKNOWN"
	// for an unmapped value rather than panicking as the deleted tf-local ExecutionStatus.str() did
	// -- this type now crosses package boundaries, so a process-crashing stringer is the wrong
	// failure mode (see report/executionstatus.go).
	assert.NotPanics(t, func() {
		assert.Equal(t, "UNKNOWN", report.ExecutionStatus(99).String())
	})
}

func Test_RunStatus_StepPointerErrorBranches(t *testing.T) {
	empty := &report.RunStatus{}
	require.Error(t, empty.FirstStep(), "firstStep on a stepless run returns an error")
	assert.Nil(t, empty.CurrentStepStatus(), "currentStepStatus out of range returns nil")

	single := &report.RunStatus{Steps: []report.StepStatus{{Name: "only"}}}
	require.NoError(t, single.FirstStep())
	assert.Equal(t, "only", single.CurrentStepStatus().Name)
	assert.Error(t, single.NextStep(), "nextStep past the last step returns an error")
}

func Test_ToWorkspaceStr_EmptyIdentifiers(t *testing.T) {
	// meshapi.Run carries these as plain (never-nil) strings, empty when meshfed omits an
	// optional identifier -- toWorkspaceStr renders that literally, not as a placeholder.
	run := &meshapi.Run{Spec: meshapi.RunSpecDTO{BuildingBlock: meshapi.BuildingBlockSpecDTO{Uuid: "bb-1"}}}
	assert.Equal(t, "..:bb-1", toWorkspaceStr(run))

	run2 := &meshapi.Run{Spec: meshapi.RunSpecDTO{BuildingBlock: meshapi.BuildingBlockSpecDTO{
		Uuid: "bb-2",
		Spec: meshapi.BuildingBlockDetailsSpecDTO{
			WorkspaceIdentifier:    "ws",
			ProjectIdentifier:      "proj",
			FullPlatformIdentifier: "plat",
		},
	}}}
	assert.Equal(t, "ws.proj.plat:bb-2", toWorkspaceStr(run2))
}
