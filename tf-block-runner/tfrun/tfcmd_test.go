package tfrun

import (
	_ "embed"
	"encoding/json"
	"io"
	"io/fs"
	"log"
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
	// Escape special regex characters in the expected string
	escaped := regexp.QuoteMeta(expected)
	// Replace spaces around the equals sign with flexible whitespace pattern
	pattern := strings.ReplaceAll(escaped, ` = `, `\s*=\s*`)
	assert.Regexp(t, regexp.MustCompile(pattern), content, "Expected to find pattern: %s", expected)
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
	assert.Nil(t, err)

	filePath := path.Join(uut.runContextInfo.workingDirectory, "testfile")
	data, err := os.ReadFile(filePath)

	assert.Nil(t, err)

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
	assert.Nil(t, err)

	savedFiles, err := uut.saveInputFiles()

	assert.Equal(t, 1, savedFiles)
	assert.Nil(t, err)
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
	assert.Nil(t, err)
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
	assert.Equal(t, len(collectedOutput), 3)

	assert.Equal(t, DataType(DATA_TYPE_INTEGER), collectedOutput["number"].Type)
	assert.Equal(t, jsonRawFrom(5), collectedOutput["number"].Value)

	assert.Equal(t, DataType(DATA_TYPE_CODE), collectedOutput["list"].Type)
	assert.Equal(t, "[\n  \"foo\",\n  \"bar\"\n]", collectedOutput["list"].Value)

	assert.Equal(t, DataType(DATA_TYPE_CODE), collectedOutput["object"].Type)
	assert.Equal(t, "{\n  \"test1\": \"foo\",\n  \"test2\": true,\n  \"test3\": 42\n}", collectedOutput["object"].Value)
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
	wd, err := os.MkdirTemp(os.TempDir(), "test-")
	t.Cleanup(func() {
		_ = os.RemoveAll(wd)
	})
	require.NoError(t, err)
	return &GenericTfCmd{
		params: &TfCmdParams{
			vars: make(map[string]*Variable),
		},
		runContextInfo: &RunContextInfo{
			workingDirectory: wd,
			logwrap:          NewLogWrap(log.New(io.Discard, "[tfCmd_test] ", log.LstdFlags), "/dev/null"),
			// Provide default test values for meshStack variables
			runJsonBase64: "dGVzdC1ydW4tanNvbi1iYXNlNjQtZGVmYXVsdA==",
			bbId:          "test-building-block-id",
			runId:         "test-run-id",
			runStatus: &RunStatus{
				Steps: []*StepStatus{
					{Name: "Test"},
				},
				CurrentStepIndex: 0,
			},
		},
	}
}

// Test that both FILE type variables and regular variables are written correctly
// and don't conflict with each other
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
// are correctly written to the tfvars file
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
// pretty-printed JSON objects (with newlines) are written as-is, not quoted
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

// Test_prepareEnv_CreatesMeshStackRunVarsFile tests that:
// 1. The meshStack_run_vars.tf file is created when it doesn't exist
// Note: meshStack variables are now in *.auto.tfvars, not environment variables
// Only variables not already defined will be written!
func Test_setEnvWith_CreatesMeshStackRunVarsFileAndSetsTfVarEnv(t *testing.T) {
	uut := makeTestGenericTfCmd(t)

	// Set up test data for meshstack variables
	uut.runContextInfo.runJsonBase64 = "dGVzdC1ydW4tanNvbi1iYXNlNjQ="
	uut.runContextInfo.bbId = "test-bb-id"
	uut.runContextInfo.runId = "test-run-id"

	// Create an existing variables.tf file with one variable defined
	err := os.WriteFile(path.Join(uut.runContextInfo.workingDirectory, "variables.tf"), customMeshStackRunVarsTf, 0644)
	require.NoError(t, err)

	// Call buildTfEnv
	capturedEnv, err := uut.buildTfEnv()
	require.NoError(t, err)

	// Call vars() which creates the meshStack_run_vars.tf file
	err = uut.vars()
	require.NoError(t, err)

	// Verify the file contains the expected variable declarations
	meshStackVarsPath := path.Join(uut.runContextInfo.workingDirectory, "meshStack_run_vars.tf")
	content, err := os.ReadFile(meshStackVarsPath)
	require.NoError(t, err)
	contentStr := string(content)
	assert.Contains(t, contentStr, "variable \"meshstack_building_block_run_b64\"")
	assert.NotContains(t, contentStr, "variable \"meshstack_building_block_id\"") // defined in custom variables.tf
	assert.Contains(t, contentStr, "variable \"meshstack_building_block_run_id\"")

	// Verify the meshStack variables are NOT in environment (they go to *.auto.tfvars instead)
	require.NotNil(t, capturedEnv, "Environment should be set")
	assert.NotContains(t, capturedEnv, "TF_VAR_meshstack_building_block_run_b64", "meshStack variables should not be in env")
	assert.NotContains(t, capturedEnv, "TF_VAR_meshstack_building_block_id", "meshStack variables should not be in env")
	assert.NotContains(t, capturedEnv, "TF_VAR_meshstack_building_block_run_id", "meshStack variables should not be in env")
}

// Test_setEnvWith_DoesNotOverwriteExistingMeshStackRunVarsFile tests that
// if the meshStack_run_vars.tf file already exists, it is not overwritten
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

// Test_createMeshStackHttpBackendFile_WithRunToken verifies that when a runToken is available
// (Kubernetes / single-run mode), the generated backend config uses a Bearer authorization
// header instead of static username/password credentials.
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
		}
		return nil
	}))
	assert.True(t, asserted)
}
