package tf

import (
	"cmp"
	"context"
	_ "embed"
	"io/fs"
	"maps"
	"math/big"
	"os"
	"path"
	"regexp"
	"slices"
	"strings"
	"testing"
	"uuid"

	"github.com/meshcloud/meshstack-cli/client"
	"github.com/sebdah/goldie/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/internal/executor"
)

//go:embed testdata/custom_meshStack_run_vars.tf
var customMeshStackRunVarsTf []byte

func assertContainsHCL(t *testing.T, content, expected string) {
	t.Helper()
	escaped := regexp.QuoteMeta(expected)
	pattern := strings.ReplaceAll(escaped, ` = `, `\s*=\s*`)
	assert.Regexp(t, pattern, content, "Expected to find pattern: %s", expected)
}

func Test_saveInputFiles_savedUnencryptedTextFileViaDataUrl(t *testing.T) {
	uut := makeTestGenericCmd(t)

	addInput(uut, client.MeshRunnerRunInput{
		Key: "testfile", Value: "data:text/plain;base64,aGVsbG8gd29ybGQK",
		Type: client.MeshBuildingBlockIOTypeFile,
	})

	savedFiles, err := uut.saveInputFiles()

	assert.Equal(t, 1, savedFiles)
	require.NoError(t, err)

	filePath := path.Join(uut.moduleDir, "testfile")
	data, err := os.ReadFile(filePath)

	require.NoError(t, err)

	fileContent := string(data)

	assert.Equal(t, "hello world\n", fileContent)
}

func Test_saveInputFiles_overwriteIfFileAlreadyExists(t *testing.T) {
	uut := makeTestGenericCmd(t)

	addInput(uut, client.MeshRunnerRunInput{
		Key: "testfile", Value: "data:text/plain;base64,aGVsbG8gd29ybGQK",
		Type: client.MeshBuildingBlockIOTypeFile,
	})
	filePath := path.Join(uut.moduleDir, "testfile")

	err := os.WriteFile(filePath, []byte("test"), 0o600)
	require.NoError(t, err)

	savedFiles, err := uut.saveInputFiles()

	assert.Equal(t, 1, savedFiles)
	assert.NoError(t, err)
}

func Test_saveInputFiles_IgnoresNonFileVariables(t *testing.T) {
	uut := makeTestGenericCmd(t)

	addInput(uut, client.MeshRunnerRunInput{
		Key: "testfile", Value: "data:text/plain;base64,aGVsbG8gd29ybGQK",
		Type: client.MeshBuildingBlockIOTypeString,
	})

	savedFiles, err := uut.saveInputFiles()

	assert.Equal(t, 0, savedFiles)
	assert.NoError(t, err)
}

func Test_saveInputFiles_ErrorsOnFilesAsEnvironments(t *testing.T) {
	uut := makeTestGenericCmd(t)

	addInput(uut, client.MeshRunnerRunInput{
		Key: "testfile", Value: "data:text/plain;base64,aGVsbG8gd29ybGQK",
		Env:  true,
		Type: client.MeshBuildingBlockIOTypeFile,
	})

	savedFiles, err := uut.saveInputFiles()
	require.ErrorContains(t, err, "variable 'testfile' with type FILE cannot be marked as environment va")
	assert.Equal(t, 0, savedFiles)
}

func Test_vars_ConsidersRegularVarsHigherThenThosePassedFromEnv(t *testing.T) {
	uut := makeTestGenericCmd(t)

	addInput(uut, client.MeshRunnerRunInput{Key: "v", Value: "value1"})
	addInput(uut, client.MeshRunnerRunInput{Key: "TF_VAR_v", Value: "value2", Env: true})

	err := uut.vars()
	require.NoError(t, err)

	tfvarsPath := path.Join(uut.moduleDir, "aaaaaa_meshstack-e48f8924-a6c0-4ff0-9528-ff3c1f6f94d8.auto.tfvars")
	content, err := os.ReadFile(tfvarsPath)
	require.NoError(t, err)
	assertContainsHCL(t, string(content), `v = "value1"`)
}

func Test_vars_UsesVarsFromEnv(t *testing.T) {
	uut := makeTestGenericCmd(t)

	addInput(uut, client.MeshRunnerRunInput{Key: "v1", Value: "value1"})
	addInput(uut, client.MeshRunnerRunInput{Key: "TF_VAR_v2", Value: "value2", Env: true})

	err := uut.vars()
	require.NoError(t, err)

	tfvarsPath := path.Join(uut.moduleDir, "aaaaaa_meshstack-e48f8924-a6c0-4ff0-9528-ff3c1f6f94d8.auto.tfvars")
	content, err := os.ReadFile(tfvarsPath)
	require.NoError(t, err)
	assertContainsHCL(t, string(content), `v1 = "value1"`)
	assertContainsHCL(t, string(content), `v2 = "value2"`)
}

func Test_vars_correctlyEncodesNoMatterTheType(t *testing.T) {
	uut := makeTestGenericCmd(t)

	addInput(uut, client.MeshRunnerRunInput{Key: "v1", Value: "[{\"a\": \"b\"},{\"a\": \"c\"}]", Type: client.MeshBuildingBlockIOTypeList})
	addInput(uut, client.MeshRunnerRunInput{Key: "v2", Value: "[{\"a\": \"d\"},{\"a\": \"e\"}]", Type: client.MeshBuildingBlockIOTypeCode})
	addInput(uut, client.MeshRunnerRunInput{Key: "v3", Value: "[{\"a\": \"d\"}]", Type: client.MeshBuildingBlockIOTypeCode})
	addInput(uut, client.MeshRunnerRunInput{Key: "v4", Value: true, Type: client.MeshBuildingBlockIOTypeBoolean})
	addInput(uut, client.MeshRunnerRunInput{Key: "v5", Value: big.NewInt(10), Type: client.MeshBuildingBlockIOTypeInteger})
	addInput(uut, client.MeshRunnerRunInput{Key: "v6", Value: "this-is-not-double-encoded", Type: client.MeshBuildingBlockIOTypeString})
	addInput(uut, client.MeshRunnerRunInput{Key: "v7", Value: "some: key\nother: yaml", Type: client.MeshBuildingBlockIOTypeCode})
	addInput(uut, client.MeshRunnerRunInput{Key: "v8", Value: "single-select-not-double-encoded", Type: client.MeshBuildingBlockIOTypeSingleSelect})
	addInput(uut, client.MeshRunnerRunInput{Key: "v9", Value: codeInputJsonWithTemplateSequences, Type: client.MeshBuildingBlockIOTypeCode})

	variableTf, err := os.Create(path.Join(uut.moduleDir, "variable.tf"))
	require.NoError(t, err)
	defer func() {
		require.NoError(t, variableTf.Close())
	}()
	_, err = variableTf.WriteString(`variable "v1" { type = any }` + "\n")
	require.NoError(t, err)
	_, err = variableTf.WriteString(`variable "v3" { type = string }` + "\n")
	require.NoError(t, err)
	_, err = variableTf.WriteString(`variable "v6" { type = string }` + "\n")
	require.NoError(t, err)
	_, err = variableTf.WriteString(`variable "v7" { type = string }` + "\n")
	require.NoError(t, err)
	_, err = variableTf.WriteString(`variable "v8" { type = string }` + "\n")
	require.NoError(t, err)

	err = uut.vars()
	require.NoError(t, err)

	tfvarsPath := path.Join(uut.moduleDir, "aaaaaa_meshstack-e48f8924-a6c0-4ff0-9528-ff3c1f6f94d8.auto.tfvars")
	content, err := os.ReadFile(tfvarsPath)
	require.NoError(t, err)
	assert.Contains(t, string(content), "v1 = [{\n  a = \"b\"\n  }, {\n  a = \"c\"\n}]")
	assert.Contains(t, string(content), "v2 = [{\n  a = \"d\"\n  }, {\n  a = \"e\"\n}]")
	assertContainsHCL(t, string(content), `v3 = "[{\"a\":\"d\"}]"`)
	assertContainsHCL(t, string(content), "v4 = true")
	assertContainsHCL(t, string(content), "v5 = 10")
	assertContainsHCL(t, string(content), `v6 = "this-is-not-double-encoded"`)
	assertContainsHCL(t, string(content), `v7 = "some: key\nother: yaml"`)
	assertContainsHCL(t, string(content), `v8 = "single-select-not-double-encoded"`)
	assert.Contains(t, string(content), "v9 = {\n  computed = \"$${1 + 1}\"\n  password = \"pa$${ss}wo%%{rd}\"\n}")
}

var testRunUuid = uuid.MustParse("6f1c6e8a-3b9e-4c55-9a4f-0d6b7e2c1a90")

func makeTestGenericCmd(t *testing.T) *GenericCmd {
	t.Helper()
	reporter, steps, _ := newTestReporter(t)
	tfRun := run{Json: []byte("test-run-json")}
	tfRun.Metadata.Uuid = testRunUuid
	tfRun.Spec.BuildingBlock.Uuid = "test-building-block-id"
	tfRun.Spec.Definition.Spec.Implementation.Terraform = &client.MeshRunnerTerraformImplementation{}
	return &GenericCmd{reporter: reporter, steps: steps, run: tfRun, moduleDir: t.TempDir()}
}

func addInput(uut *GenericCmd, input client.MeshRunnerRunInput) {
	uut.run.Spec.BuildingBlock.Spec.Inputs = append(uut.run.Spec.BuildingBlock.Spec.Inputs, input)
}

func Test_FileAndRegularVariablesWorkTogether(t *testing.T) {
	uut := makeTestGenericCmd(t)

	addInput(uut, client.MeshRunnerRunInput{
		Key:   "config.json",
		Value: "data:text/plain;base64,eyJ0ZXN0IjogInZhbHVlIn0K",
		Type:  client.MeshBuildingBlockIOTypeFile,
	})

	addInput(uut, client.MeshRunnerRunInput{Key: "project_id", Value: "proj-123", Type: client.MeshBuildingBlockIOTypeString})
	addInput(uut, client.MeshRunnerRunInput{
		Key: "users", Value: `[{"id":"user1","name":"John"},{"id":"user2","name":"Jane"}]`,
		Type: client.MeshBuildingBlockIOTypeList,
	})

	savedFiles, err := uut.saveInputFiles()
	require.NoError(t, err)
	assert.Equal(t, 1, savedFiles)

	err = uut.vars()
	require.NoError(t, err)

	configFilePath := path.Join(uut.moduleDir, "config.json")
	configContent, err := os.ReadFile(configFilePath)
	require.NoError(t, err)
	assert.Contains(t, string(configContent), `{"test": "value"}`)

	tfvarsPath := path.Join(uut.moduleDir, "aaaaaa_meshstack-e48f8924-a6c0-4ff0-9528-ff3c1f6f94d8.auto.tfvars")
	tfvarsContent, err := os.ReadFile(tfvarsPath)
	require.NoError(t, err)
	assert.Contains(t, string(tfvarsContent), "project_id = \"proj-123\"")
	assert.Contains(t, string(tfvarsContent), "users = [{\n  id   = \"user1\"\n  name = \"John\"\n  }, {\n  id   = \"user2\"\n  name = \"Jane\"\n}]")

	assert.NotContains(t, string(tfvarsContent), "config.json")

	configInfo, err := os.Stat(configFilePath)
	require.NoError(t, err)
	assert.False(t, configInfo.IsDir())

	tfvarsInfo, err := os.Stat(tfvarsPath)
	require.NoError(t, err)
	assert.False(t, tfvarsInfo.IsDir())
}

func Test_vars_WithSpecialCharacters(t *testing.T) {
	uut := makeTestGenericCmd(t)

	addInput(uut, client.MeshRunnerRunInput{
		Key: "path", Value: `C:\Program Files\App`,
		Type: client.MeshBuildingBlockIOTypeString,
	})
	addInput(uut, client.MeshRunnerRunInput{
		Key: "json_string", Value: `{"key": "value with \"quotes\""}`,
		Type: client.MeshBuildingBlockIOTypeString,
	})
	addInput(uut, client.MeshRunnerRunInput{
		Key: "description", Value: `This is a "quoted" string with \backslashes\`,
		Type: client.MeshBuildingBlockIOTypeString,
	})

	err := uut.vars()
	require.NoError(t, err)

	tfvarsPath := path.Join(uut.moduleDir, "aaaaaa_meshstack-e48f8924-a6c0-4ff0-9528-ff3c1f6f94d8.auto.tfvars")
	content, err := os.ReadFile(tfvarsPath)
	require.NoError(t, err)

	contentStr := string(content)

	assertContainsHCL(t, contentStr, `path = "C:\\Program Files\\App"`)
	assertContainsHCL(t, contentStr, `json_string = "{\"key\": \"value with \\\"quotes\\\"\"}"`)
	assertContainsHCL(t, contentStr, `description = "This is a \"quoted\" string with \\backslashes\\"`)
}

func Test_vars_CodeInputThatIsNoValidHcl_ValueNotInLog(t *testing.T) {
	uut := makeTestGenericCmd(t)
	var stepLog func() string
	uut.reporter, uut.steps, stepLog = newTestReporter(t)
	addInput(uut, client.MeshRunnerRunInput{
		Key: "secret_code", Value: codeInputPlainText,
		Type: client.MeshBuildingBlockIOTypeCode,
	})

	require.NoError(t, uut.vars())

	updateLog := stepLog()
	assert.Contains(t, updateLog, "While adding variable 'secret_code': Failed to parse input as JSON or HCL")
	assert.NotContains(t, updateLog, "s3cr3t")
	assertContainsHCL(t, readGeneratedTfvars(t, uut), `secret_code = "token: s3cr3t-$${not-hcl}\n"`)
}

func Test_vars_WithPrettyPrintedJSONObjects(t *testing.T) {
	uut := makeTestGenericCmd(t)

	prettyCreator := `{
  "type" : "User",
  "identifier" : "c4b2d768-1507-4e4f-9c07-73069869aae4",
  "displayName" : "Max Mustermann (max.mustermann@example.com)",
  "username" : "max.mustermann@example.com",
  "email" : "max.mustermann@example.com",
  "euid" : "max.mustermann@example.com"
}`

	addInput(uut, client.MeshRunnerRunInput{
		Key: "creator", Value: prettyCreator,
		Type: client.MeshBuildingBlockIOTypeCode,
	})

	addInput(uut, client.MeshRunnerRunInput{
		Key: "creator_compact", Value: `{"type":"User","identifier":"123"}`,
		Type: client.MeshBuildingBlockIOTypeCode,
	})

	addInput(uut, client.MeshRunnerRunInput{
		Key: "env-var", Value: `This value should be ignored.`,
		Env: true,
	})

	uut.run.Metadata.Uuid = testRunUuid
	uut.run.Spec.BuildingBlock.Uuid = "some-bbd-id-123"
	uut.run.Json = []byte("some-run-json")

	err := uut.vars()
	require.NoError(t, err)

	tfvarsPath := path.Join(uut.moduleDir, "aaaaaa_meshstack-e48f8924-a6c0-4ff0-9528-ff3c1f6f94d8.auto.tfvars")
	content, err := os.ReadFile(tfvarsPath)
	require.NoError(t, err)

	g := goldie.New(t, goldie.WithNameSuffix(".golden.tfvars"))
	g.Assert(t, "expected-pretty-json", content)

	assert.NotContains(t, string(content), "env-var")
}

func Test_extractContentFromDataUrl(t *testing.T) {
	tests := []struct {
		name    string
		dataUrl string
		want    []byte
	}{
		{"some data", "data:text/plain;base64,c29tZS1kYXRhCg==", []byte("some-data\n")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := extractContentFromDataUrl(tt.dataUrl)
			require.NoError(t, err)
			assert.Equalf(t, tt.want, got, "extractContentFromDataUrl(%v)", tt.dataUrl)
		})
	}
}

func Test_setEnvWith_CreatesMeshStackRunVarsFileAndSetsTfVarEnv(t *testing.T) {
	uut := makeTestGenericCmd(t)

	uut.run.Json = []byte("some-run-json")
	uut.run.Spec.BuildingBlock.Uuid = "test-bb-id"
	uut.run.Metadata.Uuid = testRunUuid

	capturedEnv, err := uut.buildTfEnv()
	require.NoError(t, err)

	err = uut.vars()
	require.NoError(t, err)

	meshStackVarsPath := path.Join(uut.moduleDir, "meshStack_run_vars.tf")
	content, err := os.ReadFile(meshStackVarsPath)
	require.NoError(t, err)
	contentStr := string(content)
	assert.Contains(t, contentStr, "variable \"meshstack_building_block_id\"")
	assert.Contains(t, contentStr, "variable \"meshstack_building_block_run_b64\"")
	assert.Contains(t, contentStr, "variable \"meshstack_building_block_run_id\"")
	assert.Contains(t, contentStr, "default")
	assert.Contains(t, contentStr, "null")

	require.NotNil(t, capturedEnv, "Environment should be set")
	assert.NotContains(t, capturedEnv, "TF_VAR_meshstack_building_block_run_b64", "meshStack variables should not be in env")
	assert.NotContains(t, capturedEnv, "TF_VAR_meshstack_building_block_id", "meshStack variables should not be in env")
	assert.NotContains(t, capturedEnv, "TF_VAR_meshstack_building_block_run_id", "meshStack variables should not be in env")
}

func readGeneratedTfvars(t *testing.T, uut *GenericCmd) string {
	t.Helper()
	tfvarsPath := path.Join(uut.moduleDir, "aaaaaa_meshstack-e48f8924-a6c0-4ff0-9528-ff3c1f6f94d8.auto.tfvars")
	content, err := os.ReadFile(tfvarsPath)
	require.NoError(t, err)
	return string(content)
}

func Test_vars_OmitsRunScopedVarValuesOnDetectAndSavedPlanReplay(t *testing.T) {
	cases := []struct {
		name          string
		behavior      Behavior
		artifactUrl   string
		wantRunScoped bool
	}{
		{name: "fresh apply writes run-scoped values", behavior: behaviorApply, wantRunScoped: true},
		{name: "destroy writes run-scoped values", behavior: behaviorDestroy, wantRunScoped: true},
		{name: "detect omits run-scoped values", behavior: behaviorDetect, wantRunScoped: false},
		{name: "apply replaying predecessor plan omits run-scoped values", behavior: behaviorApply, artifactUrl: "https://example/artifact", wantRunScoped: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			uut := makeTestGenericCmd(t)
			uut.run.behavior = tc.behavior
			uut.run.Links.Artifact.Href = tc.artifactUrl
			uut.run.Spec.BuildingBlock.Uuid = "some-bbd-id-123"
			uut.run.Metadata.Uuid = testRunUuid
			uut.run.Json = []byte("some-run-json")

			require.NoError(t, uut.vars())

			tfvars := readGeneratedTfvars(t, uut)
			assert.Regexp(t, `meshstack_building_block_id\s+= "some-bbd-id-123"`, tfvars)

			if tc.wantRunScoped {
				assert.Regexp(t, `meshstack_building_block_run_id\s+= "`+testRunUuid.String()+`"`, tfvars)
				assert.Regexp(t, `meshstack_building_block_run_b64\s+= "c29tZS1ydW4tanNvbg=="`, tfvars)
			} else {
				assert.NotContains(t, tfvars, "meshstack_building_block_run_id")
				assert.NotContains(t, tfvars, "meshstack_building_block_run_b64")
			}

			declPath := path.Join(uut.moduleDir, "meshStack_run_vars.tf")
			decl, err := os.ReadFile(declPath)
			require.NoError(t, err)
			assert.Contains(t, string(decl), `variable "meshstack_building_block_id"`)
			assert.Contains(t, string(decl), `variable "meshstack_building_block_run_id"`)
			assert.Contains(t, string(decl), `variable "meshstack_building_block_run_b64"`)
		})
	}
}

func Test_vars_SkipsDeclaringVariablesAlreadyDeclaredByBuildingBlock(t *testing.T) {
	uut := makeTestGenericCmd(t)
	uut.run.Spec.BuildingBlock.Uuid = "test-bb-id"
	uut.run.Metadata.Uuid = testRunUuid
	uut.run.Json = []byte("some-run-json")

	err := os.WriteFile(path.Join(uut.moduleDir, "variables.tf"), customMeshStackRunVarsTf, 0o600)
	require.NoError(t, err)

	require.NoError(t, uut.vars())

	declPath := path.Join(uut.moduleDir, "meshStack_run_vars.tf")
	decl, err := os.ReadFile(declPath)
	require.NoError(t, err)
	declStr := string(decl)
	assert.NotContains(t, declStr, `variable "meshstack_building_block_id"`, "must not duplicate a building-block-declared variable")
	assert.Contains(t, declStr, `variable "meshstack_building_block_run_id"`)
	assert.Contains(t, declStr, `variable "meshstack_building_block_run_b64"`)

	assert.Regexp(t, `meshstack_building_block_id\s+= "test-bb-id"`, readGeneratedTfvars(t, uut))
}

func Test_setEnvWith_DoesNotOverwriteExistingMeshStackRunVarsFile(t *testing.T) {
	uut := makeTestGenericCmd(t)
	addInput(uut, client.MeshRunnerRunInput{
		Key: "ENV_FROM_VAR", Value: "should-be-there",
		Env: true,
	})
	addInput(uut, client.MeshRunnerRunInput{
		Key: "TF_VAR_blub", Value: "should-NOT-be-there",
		Env: true,
	})
	addInput(uut, client.MeshRunnerRunInput{
		Key: "non-env-input", Value: "should-NOT-be-there",
	})

	meshStackVarsPath := path.Join(uut.moduleDir, "meshStack_run_vars.tf")
	err := os.WriteFile(meshStackVarsPath, customMeshStackRunVarsTf, 0o600)
	require.NoError(t, err)

	capturedEnv, err := uut.buildTfEnv()
	require.NoError(t, err)

	err = uut.vars()
	require.NoError(t, err)

	assert.Equal(t, "should-be-there", capturedEnv["ENV_FROM_VAR"])
	assert.NotContains(t, capturedEnv, "TF_VAR_blub")
	assert.NotContains(t, capturedEnv, "non-env-input")

	content, err := os.ReadFile(meshStackVarsPath)
	require.NoError(t, err)
	assert.Equal(t, string(customMeshStackRunVarsTf), string(content), "Existing file should not be overwritten")
}

func Test_buildTfEnv_WithMeshBackendFallbackAndRunToken_SetsTfHttpBasicAuthEnv(t *testing.T) {
	uut := makeTestGenericCmd(t)
	uut.run.terraform().UseMeshHttpBackendFallback = true
	uut.run.Spec.RunToken = "ephemeral-run-token"

	capturedEnv, err := uut.buildTfEnv()
	require.NoError(t, err)

	assert.Equal(t, MeshStackRunTokenBasicUser, capturedEnv["TF_HTTP_USERNAME"])
	assert.Equal(t, "ephemeral-run-token", capturedEnv["TF_HTTP_PASSWORD"])
	assert.Equal(t, "14", capturedEnv["TF_HTTP_RETRY_MAX"])
}

func Test_buildTfEnv_WithoutMeshBackendFallback_DoesNotSetTfHttpBasicAuthEnv(t *testing.T) {
	uut := makeTestGenericCmd(t)
	uut.run.terraform().UseMeshHttpBackendFallback = false
	uut.run.Spec.RunToken = "ephemeral-run-token"

	capturedEnv, err := uut.buildTfEnv()
	require.NoError(t, err)

	assert.NotContains(t, capturedEnv, "TF_HTTP_USERNAME")
	assert.NotContains(t, capturedEnv, "TF_HTTP_PASSWORD")

	uut.run.terraform().UseMeshHttpBackendFallback = true
	uut.run.Spec.RunToken = ""

	capturedEnv, err = uut.buildTfEnv()
	require.NoError(t, err)

	assert.NotContains(t, capturedEnv, "TF_HTTP_USERNAME")
	assert.NotContains(t, capturedEnv, "TF_HTTP_PASSWORD")
}

func Test_createMeshStackHttpBackendFile_MissingRunToken_ReturnsError(t *testing.T) {
	uut := makeTestGenericCmd(t)
	uut.run.Spec.BuildingBlock.Uuid = "test-bb-id"
	uut.run.Spec.BuildingBlock.Spec.WorkspaceIdentifier = "test-workspace"

	err := uut.createMeshStackHttpBackendFile()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "runToken is empty")

	var filesWritten []string
	_ = fs.WalkDir(os.DirFS(uut.moduleDir), ".", func(p string, _ fs.DirEntry, _ error) error {
		if strings.HasPrefix(p, "meshStack_httpbackend") {
			filesWritten = append(filesWritten, p)
		}
		return nil
	})
	assert.Empty(t, filesWritten, "no backend file should be written when runToken is missing")
}

func Test_createMeshStackHttpBackendFile_WithRunToken(t *testing.T) {
	uut := makeTestGenericCmd(t)
	uut.run.Spec.BuildingBlock.Uuid = "test-bb-id"
	uut.run.Spec.BuildingBlock.Spec.WorkspaceIdentifier = "test-workspace"
	uut.run.Spec.RunToken = "ephemeral-run-token"
	uut.run.Links.MeshstackBaseUrl.Href = "https://meshstack.example.com"

	err := uut.createMeshStackHttpBackendFile()
	require.NoError(t, err)

	content := readGeneratedBackendFile(t, uut.moduleDir)
	goldie.New(t, goldie.WithNameSuffix(".golden.tf")).Assert(t, "backend", content)
	assert.NotContains(t, string(content), "headers")
	assert.NotContains(t, string(content), "Authorization")
	assert.NotContains(t, string(content), "Bearer")
}

func Test_createMeshStackHttpBackendFile_WithTfStateLockUrl_LocksViaPostAndDelete(t *testing.T) {
	uut := makeTestGenericCmd(t)
	uut.run.Spec.BuildingBlock.Uuid = "test-bb-id"
	uut.run.Spec.BuildingBlock.Spec.WorkspaceIdentifier = "test-workspace"
	uut.run.Spec.RunToken = "ephemeral-run-token"
	uut.run.Links.MeshstackBaseUrl.Href = "https://meshstack.example.com"
	uut.run.Links.TfStateLock.Href = "https://meshstack.example.com/api/terraform/state/workspace/test-workspace/buildingBlock/test-bb-id/lock"

	require.NoError(t, uut.createMeshStackHttpBackendFile())

	content := readGeneratedBackendFile(t, uut.moduleDir)
	goldie.New(t, goldie.WithNameSuffix(".golden.tf")).Assert(t, "backend-with-lock", content)
}

func readGeneratedBackendFile(t *testing.T, workingDirectory string) []byte {
	t.Helper()
	matches, err := fs.Glob(os.DirFS(workingDirectory), "meshStack_httpbackend-*.tf")
	require.NoError(t, err)
	require.Len(t, matches, 1)
	content, err := os.ReadFile(path.Join(workingDirectory, matches[0]))
	require.NoError(t, err)
	return content
}

func Test_RunnerCredentialsInTheProcessEnvironment_ReachNeitherTofuNorThePreRunScript(t *testing.T) {
	credentials := []string{
		"MESHSTACK_API_KEY", "MESHSTACK_API_SECRET", "MESHSTACK_API_TOKEN", "MESHSTACK_API_USERNAME", "MESHSTACK_API_PASSWORD",
		"MESHSTACK_PROFILE", "MESHSTACK_WORKSPACE", "MESHSTACK_CONFIG_DIR", "RUNNER_API_CLIENT_SECRET", "RUNNER_API_PASSWORD",
	}
	for _, key := range credentials {
		t.Setenv(key, "secret")
	}

	tfEnv, err := makeTestGenericCmd(t).buildTfEnv()
	require.NoError(t, err)
	scriptEnv := buildScriptEnvironmentVariables("/tf/bin", "/user-message.txt", tfEnv)

	for _, key := range credentials {
		assert.NotContains(t, tfEnv, key)
		assert.NotContains(t, scriptEnv, key+"=secret")
	}
}

func Test_buildTfEnv_PassesProxyAndCaSettingsAndNothingUnlisted(t *testing.T) {
	proxyAndCa := []string{
		"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy", "SSL_CERT_DIR", "SSL_CERT_FILE",
	}
	for _, key := range proxyAndCa {
		t.Setenv(key, "value of "+key)
	}
	t.Setenv("UNLISTED", "value")

	tfEnv, err := makeTestGenericCmd(t).buildTfEnv()
	require.NoError(t, err)

	for _, key := range proxyAndCa {
		assert.Equal(t, "value of "+key, tfEnv[key])
	}
	assert.Subset(t, slices.Concat(proxyAndCa, []string{
		"PATH", "HOME", "PWD", "TMPDIR", "TMP", "TEMP", "CUSTOM_CA_CERTS_PATH", "SSH_KNOWN_HOSTS", "NIX_CONFIG",
		"MESHSTACK_ENDPOINT", "MESHSTACK_SKIP_VERSION_CHECK", "TF_LOG", "TF_LOG_CORE", "TF_LOG_PROVIDER", "TF_LOG_PATH",
		"CHECKPOINT_DISABLE", "TF_IN_AUTOMATION",
		"TF_REGISTRY_DISCOVERY_RETRY", "TF_PROVIDER_DOWNLOAD_RETRY", "TF_REGISTRY_CLIENT_TIMEOUT", "TF_HTTP_RETRY_MAX",
	}, slices.Collect(maps.Keys(GitSource{}.gitEnv()))), slices.Collect(maps.Keys(tfEnv)))
}

type statusRecorder struct {
	client.MeshRunnerRunClient

	last client.MeshRunnerRunStatusUpdate
}

func (*statusRecorder) RegisterSource(context.Context, client.MeshRunnerRunSourceRegistration) error {
	return nil
}

func (s *statusRecorder) PatchStatus(_ context.Context, _ uuid.UUID, update client.MeshRunnerRunStatusUpdate) (client.MeshRunnerRunStatusUpdateResult, error) {
	s.last = update
	return client.MeshRunnerRunStatusUpdateResult{}, nil
}

func newTestReporter(t *testing.T) (r *executor.Reporter, steps tfSteps, finishedStepLog func() string) {
	t.Helper()
	api := &statusRecorder{}
	r = executor.NewReporter(api, uuid.New())
	step := r.Step("test", "Test")
	steps = tfSteps{sources: step, input: step, initTf: step, preRunScript: step, execute: step, output: step}
	require.NoError(t, r.Register(t.Context()))
	return r, steps, func() string {
		_, err := r.Finish(t.Context())
		require.NoError(t, err)
		return *cmp.Or(api.last.Steps[0].SystemMessage, new(""))
	}
}
