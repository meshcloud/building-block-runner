package tf

import (
	_ "embed"
	"encoding/json"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-exec/tfexec"
	"github.com/sebdah/goldie/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

//go:embed testdata/custom_meshStack_run_vars.tf
var customMeshStackRunVarsTf []byte

// assertContainsHCL is a helper that converts an expected "key = value" string into a regex
// pattern that allows flexible whitespace (to handle HCL's automatic alignment of equals signs),
// then asserts the pattern exists in the content.
func assertContainsHCL(t *testing.T, content, expected string) {
	t.Helper()
	// Escape special regex characters in the expected string
	escaped := regexp.QuoteMeta(expected)
	// Replace spaces around the equals sign with flexible whitespace pattern
	pattern := strings.ReplaceAll(escaped, ` = `, `\s*=\s*`)
	assert.Regexp(t, pattern, content, "Expected to find pattern: %s", expected)
}

func Test_saveInputFiles_savedUnencryptedTextFileViaDataUrl(t *testing.T) {
	uut := makeTestGenericTfCmd(t)

	uut.params.vars["testfile"] = &Variable{
		value: "data:text/plain;base64,aGVsbG8gd29ybGQK",
		env:   false,
		Type:  DATA_TYPE_FILE,
	}

	savedFiles, err := uut.saveInputFiles()

	assert.Equal(t, 1, savedFiles)
	require.NoError(t, err)

	filePath := path.Join(uut.runContextInfo.workingDirectory, "testfile")
	data, err := os.ReadFile(filePath)

	require.NoError(t, err)

	fileContent := string(data)

	assert.Equal(t, "hello world\n", fileContent)
}

func Test_saveInputFiles_overwriteIfFileAlreadyExists(t *testing.T) {
	uut := makeTestGenericTfCmd(t)

	uut.params.vars["testfile"] = &Variable{
		value: "data:text/plain;base64,aGVsbG8gd29ybGQK",
		env:   false,
		Type:  DATA_TYPE_FILE,
	}
	filePath := path.Join(uut.runContextInfo.workingDirectory, "testfile")

	err := os.WriteFile(filePath, []byte("test"), 0600)
	require.NoError(t, err)

	savedFiles, err := uut.saveInputFiles()

	assert.Equal(t, 1, savedFiles)
	require.NoError(t, err)
}

func Test_saveInputFiles_IgnoresNonFileVariables(t *testing.T) {
	uut := makeTestGenericTfCmd(t)

	uut.params.vars["testfile"] = &Variable{
		value: "data:text/plain;base64,aGVsbG8gd29ybGQK",
		env:   false,
		Type:  DATA_TYPE_STRING,
	}

	savedFiles, err := uut.saveInputFiles()

	assert.Equal(t, 0, savedFiles)
	require.NoError(t, err)
}

func Test_saveInputFiles_ErrorsOnFilesAsEnvironments(t *testing.T) {
	uut := makeTestGenericTfCmd(t)

	uut.params.vars["testfile"] = &Variable{
		value: "data:text/plain;base64,aGVsbG8gd29ybGQK",
		env:   true,
		Type:  DATA_TYPE_FILE,
	}

	savedFiles, err := uut.saveInputFiles()
	require.ErrorContains(t, err, "variable 'testfile' with type FILE cannot be marked as environment va")
	assert.Equal(t, 0, savedFiles)
}

func Test_vars_ConsidersRegularVarsHigherThenThosePassedFromEnv(t *testing.T) {
	uut := makeTestGenericTfCmd(t)

	uut.params.vars["v"] = &Variable{value: "value1", env: false}
	uut.params.vars["TF_VAR_v"] = &Variable{value: "value2", env: true}

	err := uut.vars()
	require.NoError(t, err)

	// Verify the meshstack-{uuid}.auto.tfvars file was created with correct content
	tfvarsPath := path.Join(uut.runContextInfo.workingDirectory, "aaaaaa_meshstack-e48f8924-a6c0-4ff0-9528-ff3c1f6f94d8.auto.tfvars")
	content, err := os.ReadFile(tfvarsPath)
	require.NoError(t, err)
	assertContainsHCL(t, string(content), `v = "value1"`)
}

func Test_vars_UsesVarsFromEnv(t *testing.T) {
	uut := makeTestGenericTfCmd(t)

	uut.params.vars["v1"] = &Variable{value: "value1", env: false}
	uut.params.vars["TF_VAR_v2"] = &Variable{value: "value2", env: true}

	err := uut.vars()
	require.NoError(t, err)

	// Verify the meshstack-{uuid}.auto.tfvars file was created with correct content
	tfvarsPath := path.Join(uut.runContextInfo.workingDirectory, "aaaaaa_meshstack-e48f8924-a6c0-4ff0-9528-ff3c1f6f94d8.auto.tfvars")
	content, err := os.ReadFile(tfvarsPath)
	require.NoError(t, err)
	assertContainsHCL(t, string(content), `v1 = "value1"`)
	assertContainsHCL(t, string(content), `v2 = "value2"`)
}

func Test_vars_correctlyEncodesNoMatterTheType(t *testing.T) {
	uut := makeTestGenericTfCmd(t)

	uut.params.vars["v1"] = &Variable{value: "[{\"a\": \"b\"},{\"a\": \"c\"}]", Type: DATA_TYPE_LIST}
	uut.params.vars["v2"] = &Variable{value: "[{\"a\": \"d\"},{\"a\": \"e\"}]", Type: DATA_TYPE_CODE}
	uut.params.vars["v3"] = &Variable{value: "[{\"a\": \"d\"}]", Type: DATA_TYPE_CODE}
	uut.params.vars["v4"] = &Variable{value: true, Type: DATA_TYPE_BOOLEAN}
	uut.params.vars["v5"] = &Variable{value: 10, Type: DATA_TYPE_INTEGER}
	uut.params.vars["v6"] = &Variable{value: "this-is-not-double-encoded", Type: DATA_TYPE_STRING}
	uut.params.vars["v7"] = &Variable{value: "some: key\nother: yaml", Type: DATA_TYPE_CODE}
	uut.params.vars["v8"] = &Variable{value: "single-select-not-double-encoded", Type: DATA_TYPE_SINGLESELECT}

	// Make some variables with explicit type
	variableTf, err := os.Create(path.Join(uut.runContextInfo.workingDirectory, "variable.tf"))
	require.NoError(t, err)
	defer func() {
		require.NoError(t, variableTf.Close())
	}()
	_, err = variableTf.Write([]byte(`variable "v1" { type = any }` + "\n"))
	require.NoError(t, err)
	_, err = variableTf.Write([]byte(`variable "v3" { type = string }` + "\n"))
	require.NoError(t, err)
	_, err = variableTf.Write([]byte(`variable "v6" { type = string }` + "\n"))
	require.NoError(t, err)
	_, err = variableTf.Write([]byte(`variable "v7" { type = string }` + "\n"))
	require.NoError(t, err)
	_, err = variableTf.Write([]byte(`variable "v8" { type = string }` + "\n"))
	require.NoError(t, err)

	// Now run the test
	err = uut.vars()
	require.NoError(t, err)

	// Verify the meshstack-{uuid}.auto.tfvars file was created with correct content
	tfvarsPath := path.Join(uut.runContextInfo.workingDirectory, "aaaaaa_meshstack-e48f8924-a6c0-4ff0-9528-ff3c1f6f94d8.auto.tfvars")
	content, err := os.ReadFile(tfvarsPath)
	require.NoError(t, err)
	assert.Contains(t, string(content), "v1 = [{\n  a = \"b\"\n  }, {\n  a = \"c\"\n}]")
	assert.Contains(t, string(content), "v2 = [{\n  a = \"d\"\n  }, {\n  a = \"e\"\n}]")
	assertContainsHCL(t, string(content), `v3 = "[{\"a\":\"d\"}]"`) // this a correctly encoded JSON as a string
	assertContainsHCL(t, string(content), "v4 = true")
	assertContainsHCL(t, string(content), "v5 = 10")
	assertContainsHCL(t, string(content), `v6 = "this-is-not-double-encoded"`)
	assertContainsHCL(t, string(content), `v7 = "some: key\nother: yaml"`)
	assertContainsHCL(t, string(content), `v8 = "single-select-not-double-encoded"`)

}

func Test_collectOutput(t *testing.T) {
	uut := makeTestGenericTfCmd(t)

	jsonRawFrom := func(v any) json.RawMessage {
		b, err := json.Marshal(v)
		require.NoError(t, err)
		return b
	}

	out := make(map[string]tfexec.OutputMeta, 3)
	out["number"] = tfexec.OutputMeta{Type: jsonRawFrom("number"), Value: jsonRawFrom(5)}
	out["list"] = tfexec.OutputMeta{Type: jsonRawFrom("array"), Value: jsonRawFrom([]string{"foo", "bar"})}
	out["object"] = tfexec.OutputMeta{Type: jsonRawFrom("object"), Value: jsonRawFrom(
		struct {
			Test1 string `json:"test1"`
			Test2 bool   `json:"test2"`
			Test3 int    `json:"test3"`
		}{
			Test1: "foo",
			Test2: true,
			Test3: 42,
		},
	),
	}

	uut.collectOutput(out)
	collectedOutput := uut.runContextInfo.runStatus.currentStepStatus().Outputs
	assert.Len(t, collectedOutput, 3)

	assert.Equal(t, DataType(DATA_TYPE_INTEGER), collectedOutput["number"].Type)
	assert.Equal(t, jsonRawFrom(5), collectedOutput["number"].Value)

	assert.Equal(t, DataType(DATA_TYPE_CODE), collectedOutput["list"].Type)
	assert.Equal(t, "[\n  \"foo\",\n  \"bar\"\n]", collectedOutput["list"].Value)

	assert.Equal(t, DataType(DATA_TYPE_CODE), collectedOutput["object"].Type)
	objectValue, ok := collectedOutput["object"].Value.(string)
	require.True(t, ok)
	assert.JSONEq(t, "{\n  \"test1\": \"foo\",\n  \"test2\": true,\n  \"test3\": 42\n}", objectValue)
}

// Creates a new *GenericTfCmd and provides config with a working directory
// that is automatically created in /tmp.
// Second return value is a function that should be called in case the UUT
// is no longer needed, best in a defer statement directly after this function
// invocation. e.g.:
// uut, cleanUpFunc := makeTestGenericTfCmd()
// defer cleanUpFunc()
// This function returns nil as first argument, in case something fails.
func makeTestGenericTfCmd(t *testing.T) *GenericTfCmd {
	t.Helper()
	wd := t.TempDir()
	lw, err := NewLogWrap(slog.New(slog.NewTextHandler(io.Discard, nil)), "/dev/null")
	require.NoError(t, err)
	return &GenericTfCmd{
		params: &TfCmdParams{
			vars: make(map[string]*Variable),
			dec:  certDecryptor{crypto: testCrypto(t)},
		},
		runContextInfo: &RunContextInfo{
			workingDirectory: wd,
			logwrap:          lw,
			// Provide default test values for meshStack variables
			runJsonBase64: "dGVzdC1ydW4tanNvbi1iYXNlNjQtZGVmYXVsdA==",
			bbId:          "test-building-block-id",
			runId:         "test-run-id",
			runStatus: &RunStatus{
				Steps: []StepStatus{
					{Name: "Test"},
				},
				CurrentStepIndex: 0,
			},
		},
	}
}

// Test that both FILE type variables and regular variables are written correctly
// and don't conflict with each other.
func Test_FileAndRegularVariablesWorkTogether(t *testing.T) {
	uut := makeTestGenericTfCmd(t)

	// Add FILE type variable
	uut.params.vars["config.json"] = &Variable{
		value: "data:text/plain;base64,eyJ0ZXN0IjogInZhbHVlIn0K", // {"test": "value"}
		env:   false,
		Type:  DATA_TYPE_FILE,
	}

	// Add regular variables
	uut.params.vars["project_id"] = &Variable{value: "proj-123", env: false, Type: DATA_TYPE_STRING}
	uut.params.vars["users"] = &Variable{
		value: `[{"id":"user1","name":"John"},{"id":"user2","name":"Jane"}]`,
		env:   false,
		Type:  DATA_TYPE_LIST,
	}

	// Save FILE type variables
	savedFiles, err := uut.saveInputFiles()
	require.NoError(t, err)
	assert.Equal(t, 1, savedFiles)

	// Process regular variables
	err = uut.vars()
	require.NoError(t, err)

	// Verify FILE type variable was written to its own file
	configFilePath := path.Join(uut.runContextInfo.workingDirectory, "config.json")
	configContent, err := os.ReadFile(configFilePath)
	require.NoError(t, err)
	assert.Contains(t, string(configContent), `{"test": "value"}`)

	// Verify regular variables were written to meshstack-{uuid}.auto.tfvars
	tfvarsPath := path.Join(uut.runContextInfo.workingDirectory, "aaaaaa_meshstack-e48f8924-a6c0-4ff0-9528-ff3c1f6f94d8.auto.tfvars")
	tfvarsContent, err := os.ReadFile(tfvarsPath)
	require.NoError(t, err)
	assert.Contains(t, string(tfvarsContent), "project_id = \"proj-123\"")
	assert.Contains(t, string(tfvarsContent), "users = [{\n  id   = \"user1\"\n  name = \"John\"\n  }, {\n  id   = \"user2\"\n  name = \"Jane\"\n}]")

	// Verify FILE type variable is NOT in meshstack.auto.tfvars
	assert.NotContains(t, string(tfvarsContent), "config.json")

	// Verify both files exist and are separate
	configInfo, err := os.Stat(configFilePath)
	require.NoError(t, err)
	assert.False(t, configInfo.IsDir())

	tfvarsInfo, err := os.Stat(tfvarsPath)
	require.NoError(t, err)
	assert.False(t, tfvarsInfo.IsDir())
}

// Test_vars_WithSpecialCharacters tests that variables with special characters
// are correctly written to the tfvars file.
func Test_vars_WithSpecialCharacters(t *testing.T) {
	uut := makeTestGenericTfCmd(t)

	uut.params.vars["path"] = &Variable{
		value: `C:\Program Files\App`,
		Type:  DATA_TYPE_STRING,
	}
	uut.params.vars["json_string"] = &Variable{
		value: `{"key": "value with \"quotes\""}`,
		Type:  DATA_TYPE_STRING,
	}
	uut.params.vars["description"] = &Variable{
		value: `This is a "quoted" string with \backslashes\`,
		Type:  DATA_TYPE_STRING,
	}

	err := uut.vars()
	require.NoError(t, err)

	// Read the generated tfvars file
	tfvarsPath := path.Join(uut.runContextInfo.workingDirectory, "aaaaaa_meshstack-e48f8924-a6c0-4ff0-9528-ff3c1f6f94d8.auto.tfvars")
	content, err := os.ReadFile(tfvarsPath)
	require.NoError(t, err)

	contentStr := string(content)

	// Verify proper escaping in the file content
	assertContainsHCL(t, contentStr, `path = "C:\\Program Files\\App"`)
	assertContainsHCL(t, contentStr, `json_string = "{\"key\": \"value with \\\"quotes\\\"\"}"`)
	assertContainsHCL(t, contentStr, `description = "This is a \"quoted\" string with \\backslashes\\"`)
}

// Test_vars_WithPrettyPrintedJSONObjects tests that CODE/LIST values containing
// pretty-printed JSON objects (with newlines) are written as-is, not quoted.
func Test_vars_WithPrettyPrintedJSONObjects(t *testing.T) {
	uut := makeTestGenericTfCmd(t)

	// Simulate the creator object that was causing the bug - pretty-printed JSON
	prettyCreator := `{
  "type" : "User",
  "identifier" : "c4b2d768-1507-4e4f-9c07-73069869aae4",
  "displayName" : "Max Mustermann (max.mustermann@example.com)",
  "username" : "max.mustermann@example.com",
  "email" : "max.mustermann@example.com",
  "euid" : "max.mustermann@example.com"
}`

	uut.params.vars["creator"] = &Variable{
		value: prettyCreator,
		Type:  DATA_TYPE_CODE,
	}

	// Also test with compact JSON for comparison
	uut.params.vars["creator_compact"] = &Variable{
		value: `{"type":"User","identifier":"123"}`,
		Type:  DATA_TYPE_CODE,
	}

	uut.params.vars["env-var"] = &Variable{
		value: `This value should be ignored.`,
		env:   true,
	}

	uut.runContextInfo.runId = "some-run-id-12345"
	uut.runContextInfo.bbId = "some-bbd-id-123"
	uut.runContextInfo.runJsonBase64 = "c29tZS1kYXRhYXNkYTIzMjEzMjN3YWRzZGFzZGEzMjEzMgo="

	err := uut.vars()
	require.NoError(t, err)

	// Read the generated tfvars file
	tfvarsPath := path.Join(uut.runContextInfo.workingDirectory, "aaaaaa_meshstack-e48f8924-a6c0-4ff0-9528-ff3c1f6f94d8.auto.tfvars")
	content, err := os.ReadFile(tfvarsPath)
	require.NoError(t, err)

	g := goldie.New(t, goldie.WithNameSuffix(".golden.tfvars"))
	g.Assert(t, "expected-pretty-json", content)

	// Verify that env-only variables are not included in the tfvars file
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

// Test_setEnvWith_CreatesMeshStackRunVarsFileAndSetsTfVarEnv verifies that:
//  1. The meshStack_run_vars.tf file is created when it doesn't exist.
//  2. All three meshStack variables are declared: the building block id as required, and the
//     deprecated run-scoped variables (run id / run b64) as optional (nullable, default null) so
//     they can be omitted on dry-runs and saved-plan replays — see vars().
//
// Note: meshStack variables are provided via *.auto.tfvars, not environment variables.
func Test_setEnvWith_CreatesMeshStackRunVarsFileAndSetsTfVarEnv(t *testing.T) {
	uut := makeTestGenericTfCmd(t)

	// Set up test data for meshstack variables
	uut.runContextInfo.runJsonBase64 = "dGVzdC1ydW4tanNvbi1iYXNlNjQ="
	uut.runContextInfo.bbId = "test-bb-id"
	uut.runContextInfo.runId = "test-run-id"

	// Call buildTfEnv
	capturedEnv, err := uut.buildTfEnv()
	require.NoError(t, err)

	// Call vars() which creates the meshStack_run_vars.tf file
	err = uut.vars()
	require.NoError(t, err)

	// Verify the file declares the building block id (required) and the deprecated run-scoped
	// variables as optional (nullable, default null).
	meshStackVarsPath := path.Join(uut.runContextInfo.workingDirectory, "meshStack_run_vars.tf")
	content, err := os.ReadFile(meshStackVarsPath)
	require.NoError(t, err)
	contentStr := string(content)
	assert.Contains(t, contentStr, "variable \"meshstack_building_block_id\"")
	assert.Contains(t, contentStr, "variable \"meshstack_building_block_run_b64\"")
	assert.Contains(t, contentStr, "variable \"meshstack_building_block_run_id\"")
	assert.Contains(t, contentStr, "default")
	assert.Contains(t, contentStr, "null")

	// Verify the meshStack variables are NOT in environment (they go to *.auto.tfvars instead)
	require.NotNil(t, capturedEnv, "Environment should be set")
	assert.NotContains(t, capturedEnv, "TF_VAR_meshstack_building_block_run_b64", "meshStack variables should not be in env")
	assert.NotContains(t, capturedEnv, "TF_VAR_meshstack_building_block_id", "meshStack variables should not be in env")
	assert.NotContains(t, capturedEnv, "TF_VAR_meshstack_building_block_run_id", "meshStack variables should not be in env")
}

func readGeneratedTfvars(t *testing.T, uut *GenericTfCmd) string {
	t.Helper()
	tfvarsPath := path.Join(uut.runContextInfo.workingDirectory, "aaaaaa_meshstack-e48f8924-a6c0-4ff0-9528-ff3c1f6f94d8.auto.tfvars")
	content, err := os.ReadFile(tfvarsPath)
	require.NoError(t, err)
	return string(content)
}

// Test_vars_OmitsRunScopedVarValuesOnDetectAndSavedPlanReplay verifies the saved-plan invariant:
// the deprecated run-scoped variables (run id / run b64) get a value written into auto.tfvars only
// for a fresh apply/destroy. On a DETECT plan (so nothing run-scoped is baked into the plan) and on
// an APPLY replaying a predecessor plan (planArtifactUrl set, so no "Mismatch between input and plan
// variable value") their value is omitted. The building block id is always written, and all three
// remain declared regardless so building blocks referencing them still parse.
func Test_vars_OmitsRunScopedVarValuesOnDetectAndSavedPlanReplay(t *testing.T) {
	cases := []struct {
		name            string
		runMode         string
		planArtifactUrl string
		wantRunScoped   bool
	}{
		{name: "fresh apply writes run-scoped values", runMode: "APPLY", wantRunScoped: true},
		{name: "destroy writes run-scoped values", runMode: "DESTROY", wantRunScoped: true},
		{name: "detect omits run-scoped values", runMode: "DETECT", wantRunScoped: false},
		{name: "apply replaying predecessor plan omits run-scoped values", runMode: "APPLY", planArtifactUrl: "https://example/artifact", wantRunScoped: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			uut := makeTestGenericTfCmd(t)
			uut.params.runMode = tc.runMode
			uut.params.planArtifactUrl = tc.planArtifactUrl
			uut.runContextInfo.bbId = "some-bbd-id-123"
			uut.runContextInfo.runId = "some-run-id-12345"
			uut.runContextInfo.runJsonBase64 = "c29tZS1ydW4tanNvbg=="

			require.NoError(t, uut.vars())

			tfvars := readGeneratedTfvars(t, uut)
			// The building block id is stable across runs and always written.
			assert.Regexp(t, `meshstack_building_block_id\s+= "some-bbd-id-123"`, tfvars)

			if tc.wantRunScoped {
				assert.Regexp(t, `meshstack_building_block_run_id\s+= "some-run-id-12345"`, tfvars)
				assert.Regexp(t, `meshstack_building_block_run_b64\s+= "c29tZS1ydW4tanNvbg=="`, tfvars)
			} else {
				assert.NotContains(t, tfvars, "meshstack_building_block_run_id")
				assert.NotContains(t, tfvars, "meshstack_building_block_run_b64")
			}

			// Regardless of run mode, all three variables must be declared so modules that reference
			// them keep parsing. The run-scoped ones are optional (default null).
			declPath := path.Join(uut.runContextInfo.workingDirectory, "meshStack_run_vars.tf")
			decl, err := os.ReadFile(declPath)
			require.NoError(t, err)
			assert.Contains(t, string(decl), `variable "meshstack_building_block_id"`)
			assert.Contains(t, string(decl), `variable "meshstack_building_block_run_id"`)
			assert.Contains(t, string(decl), `variable "meshstack_building_block_run_b64"`)
		})
	}
}

// Test_vars_SkipsDeclaringVariablesAlreadyDeclaredByBuildingBlock verifies that when a building
// block already declares a meshStack variable in its own configuration, the runner does NOT emit a
// duplicate declaration in the generated meshStack_run_vars.tf (a duplicate would make terraform
// fail with "Duplicate variable declaration"). The variable's value is still written to auto.tfvars.
// This restores coverage of the existingVariableInputs skip branch that the pre-existing variables.tf
// version of Test_setEnvWith_CreatesMeshStackRunVarsFileAndSetsTfVarEnv used to exercise.
func Test_vars_SkipsDeclaringVariablesAlreadyDeclaredByBuildingBlock(t *testing.T) {
	uut := makeTestGenericTfCmd(t)
	uut.runContextInfo.bbId = "test-bb-id"
	uut.runContextInfo.runId = "test-run-id"
	uut.runContextInfo.runJsonBase64 = "dGVzdC1ydW4tanNvbi1iYXNlNjQ="

	// The building block declares meshstack_building_block_id itself (see the embedded fixture).
	err := os.WriteFile(path.Join(uut.runContextInfo.workingDirectory, "variables.tf"), customMeshStackRunVarsTf, 0644)
	require.NoError(t, err)

	require.NoError(t, uut.vars())

	// The runner must not re-declare the already-declared variable, but must still declare the others.
	declPath := path.Join(uut.runContextInfo.workingDirectory, "meshStack_run_vars.tf")
	decl, err := os.ReadFile(declPath)
	require.NoError(t, err)
	declStr := string(decl)
	assert.NotContains(t, declStr, `variable "meshstack_building_block_id"`, "must not duplicate a building-block-declared variable")
	assert.Contains(t, declStr, `variable "meshstack_building_block_run_id"`)
	assert.Contains(t, declStr, `variable "meshstack_building_block_run_b64"`)

	// The value is still provided via auto.tfvars regardless of who declares the variable.
	assert.Regexp(t, `meshstack_building_block_id\s+= "test-bb-id"`, readGeneratedTfvars(t, uut))
}

// Test_setEnvWith_DoesNotOverwriteExistingMeshStackRunVarsFile tests that
// if the meshStack_run_vars.tf file already exists, it is not overwritten.
func Test_setEnvWith_DoesNotOverwriteExistingMeshStackRunVarsFile(t *testing.T) {
	uut := makeTestGenericTfCmd(t)
	uut.params.vars["ENV_FROM_VAR"] = &Variable{
		value: "should-be-there",
		env:   true,
	}
	uut.params.vars["TF_VAR_blub"] = &Variable{
		value: "should-NOT-be-there",
		env:   true,
	}
	uut.params.vars["non-env-input"] = &Variable{
		value: "should-NOT-be-there",
		env:   false,
	}

	// Create an existing meshStack_run_vars.tf file with custom content
	meshStackVarsPath := path.Join(uut.runContextInfo.workingDirectory, "meshStack_run_vars.tf")
	err := os.WriteFile(meshStackVarsPath, customMeshStackRunVarsTf, 0644)
	require.NoError(t, err)

	// Call buildTfEnv
	capturedEnv, err := uut.buildTfEnv()
	require.NoError(t, err)

	// Call vars() which would create the meshStack_run_vars.tf file
	err = uut.vars()
	require.NoError(t, err)

	// meshStack variables are no longer set as environment variables
	assert.Equal(t, "should-be-there", capturedEnv["ENV_FROM_VAR"])
	assert.NotContains(t, capturedEnv, "TF_VAR_blub")
	assert.NotContains(t, capturedEnv, "non-env-input")

	// Verify the file still contains the custom content (not overwritten)
	content, err := os.ReadFile(meshStackVarsPath)
	require.NoError(t, err)
	assert.Equal(t, string(customMeshStackRunVarsTf), string(content), "Existing file should not be overwritten")
}

// Test_buildTfEnv_WithMeshBackendFallbackAndRunToken_SetsTfHttpBasicAuthEnv verifies that when the
// meshStack HTTP backend is in play, buildTfEnv supplies the run token via TF_HTTP_USERNAME/PASSWORD
// (Basic auth) rather than baking it into the backend config, so OpenTofu re-reads a live token at
// every plan/apply instead of embedding a since-revoked one into a saved plan.
func Test_buildTfEnv_WithMeshBackendFallbackAndRunToken_SetsTfHttpBasicAuthEnv(t *testing.T) {
	uut := makeTestGenericTfCmd(t)
	uut.runContextInfo.useMeshBackendFallback = true
	uut.runContextInfo.runToken = "ephemeral-run-token"

	capturedEnv, err := uut.buildTfEnv()
	require.NoError(t, err)

	assert.Equal(t, MeshStackRunTokenBasicUser, capturedEnv["TF_HTTP_USERNAME"])
	assert.Equal(t, "ephemeral-run-token", capturedEnv["TF_HTTP_PASSWORD"])
}

// Test_buildTfEnv_WithoutMeshBackendFallback_DoesNotSetTfHttpBasicAuthEnv verifies that
// buildTfEnv leaves TF_HTTP_* unset when the meshStack backend is not in play (e.g. a building
// block brings its own backend), and also when no runToken is available.
func Test_buildTfEnv_WithoutMeshBackendFallback_DoesNotSetTfHttpBasicAuthEnv(t *testing.T) {
	uut := makeTestGenericTfCmd(t)
	uut.runContextInfo.useMeshBackendFallback = false
	uut.runContextInfo.runToken = "ephemeral-run-token"

	capturedEnv, err := uut.buildTfEnv()
	require.NoError(t, err)

	assert.NotContains(t, capturedEnv, "TF_HTTP_USERNAME")
	assert.NotContains(t, capturedEnv, "TF_HTTP_PASSWORD")

	uut.runContextInfo.useMeshBackendFallback = true
	uut.runContextInfo.runToken = ""

	capturedEnv, err = uut.buildTfEnv()
	require.NoError(t, err)

	assert.NotContains(t, capturedEnv, "TF_HTTP_USERNAME")
	assert.NotContains(t, capturedEnv, "TF_HTTP_PASSWORD")
}

func Test_createMeshStackHttpBackendFile_MissingRunToken_ReturnsError(t *testing.T) {
	originalConfig := AppConfig
	AppConfig = TfRunnerConfig{
		RunApiBackend: RunApiConfig{
			Url:      "http://localhost:8080",
			User:     "some-user",
			Password: "top-secret",
		},
	}
	t.Cleanup(func() { AppConfig = originalConfig })

	uut := makeTestGenericTfCmd(t)
	uut.runContextInfo.bbId = "test-bb-id"
	uut.runContextInfo.workspaceIdentifier = "test-workspace"
	// runToken deliberately left empty — simulates missing token from server

	err := uut.createMeshStackHttpBackendFile()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "runToken is empty")

	// No backend file should have been written
	var filesWritten []string
	_ = fs.WalkDir(os.DirFS(uut.runContextInfo.workingDirectory), ".", func(p string, d fs.DirEntry, err error) error {
		if strings.HasPrefix(p, "meshStack_httpbackend") {
			filesWritten = append(filesWritten, p)
		}
		return nil
	})
	assert.Empty(t, filesWritten, "no backend file should be written when runToken is missing")
}

// Test_createMeshStackHttpBackendFile_WithRunToken verifies that the generated backend config
// contains only the state endpoint address and no auth (no headers/Authorization/Bearer) — the
// runToken is supplied to Terraform via TF_HTTP_USERNAME/TF_HTTP_PASSWORD env vars instead (see
// buildTfEnv), so nothing secret is baked into a saved plan.
// It also verifies that meshstackBaseUrl from the run links is used as the backend base URL.
func Test_createMeshStackHttpBackendFile_WithRunToken(t *testing.T) {
	originalConfig := AppConfig
	// No credentials configured – simulates Kubernetes mode where none are injected.
	AppConfig = TfRunnerConfig{
		RunApiBackend: RunApiConfig{
			Url: "http://fallback-url-should-not-be-used:8080",
		},
	}
	t.Cleanup(func() { AppConfig = originalConfig })

	uut := makeTestGenericTfCmd(t)
	uut.runContextInfo.bbId = "test-bb-id"
	uut.runContextInfo.workspaceIdentifier = "test-workspace"
	uut.runContextInfo.runToken = "ephemeral-run-token"
	uut.runContextInfo.meshstackBaseUrl = "https://meshstack.example.com"

	err := uut.createMeshStackHttpBackendFile()
	require.NoError(t, err)

	g := goldie.New(t, goldie.WithNameSuffix(".golden.tf"))
	asserted := false
	require.NoError(t, fs.WalkDir(os.DirFS(uut.runContextInfo.workingDirectory), ".", func(p string, d fs.DirEntry, err error) error {
		if strings.HasPrefix(p, "meshStack_httpbackend") {
			content, err := os.ReadFile(path.Join(uut.runContextInfo.workingDirectory, p))
			if err != nil {
				return err
			}
			asserted = true
			g.Assert(t, "backend", content)
			assert.Contains(t, string(content), "address")
			assert.NotContains(t, string(content), "headers")
			assert.NotContains(t, string(content), "Authorization")
			assert.NotContains(t, string(content), "Bearer")
		}
		return nil
	}))
	assert.True(t, asserted)
}
