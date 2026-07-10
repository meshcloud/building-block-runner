package tf

// CP13 (PLAN_DETAIL_01_tf_characterization_tests.md §9): the measured stragglers found via coverage
// once CP1-CP12 landed — pure helpers with real decision surface (output-type matching, data-URL
// decoding, HCL variable parsing, script exit-code/error handling) plus the not-otherwise-reached
// failure branches of the plan/destroy execute paths and the observer's final-update-error handling.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/terraform-exec/tfexec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- matchOutputType: every terraform output type maps to the right DataType ------------------

func Test_MatchOutputType(t *testing.T) {
	cases := []struct {
		name     string
		typeJSON string
		want     DataType
	}{
		{"number", `"number"`, DATA_TYPE_INTEGER},
		{"bool", `"bool"`, DATA_TYPE_BOOLEAN},
		{"string", `"string"`, DATA_TYPE_STRING},
		{"object falls back to code", `"object"`, DATA_TYPE_CODE},
		{"complex array type is code", `["object",{"a":"string"}]`, DATA_TYPE_CODE},
		{"empty array is code", `[]`, DATA_TYPE_CODE},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := matchOutputType(tfexec.OutputMeta{Type: json.RawMessage(tc.typeJSON)})
			assert.Equal(t, tc.want, got)
		})
	}
}

// --- extractContentFromDataUrl ---------------------------------------------------------------

func Test_ExtractContentFromDataUrl(t *testing.T) {
	content, err := extractContentFromDataUrl("data:text/plain;base64,aGVsbG8=")
	require.NoError(t, err)
	assert.Equal(t, "hello", string(content))

	_, err = extractContentFromDataUrl("not-a-data-url")
	require.Error(t, err, "a string without the data:...;base64, prefix must error")

	_, err = extractContentFromDataUrl("data:text/plain;base64,!!!not-base64!!!")
	assert.Error(t, err, "invalid base64 payload must error")
}

// --- tfconfig_parse: HCL variable extraction + diagnostic branches ---------------------------

func Test_ParseVariableInputs_HappyPath(t *testing.T) {
	fsys := fstest.MapFS{
		"main.tf":     {Data: []byte("variable \"region\" {\n  type = string\n}\nvariable \"count\" {\n  type = number\n}\n")},
		"untyped.tf":  {Data: []byte("variable \"anything\" {}\n")},
		"ignore.txt":  {Data: []byte("not terraform")},
		"subdir/x.tf": {Data: []byte("variable \"nested\" { type = bool }")},
	}
	vars, diags := ParseVariableInputs(fsys)
	require.False(t, diags.HasErrors(), "valid HCL must not produce error diagnostics: %s", diags)
	assert.Equal(t, "string", vars["region"].Type)
	assert.Equal(t, "number", vars["count"].Type)
	assert.Equal(t, "any", vars["anything"].Type, "a variable without a type argument defaults to any")
}

func Test_ParseVariableInputs_SyntaxErrorProducesDiagnostics(t *testing.T) {
	fsys := fstest.MapFS{"broken.tf": {Data: []byte("variable \"x\" { type = ")}}
	_, diags := ParseVariableInputs(fsys)
	assert.True(t, diags.HasErrors(), "malformed HCL must surface error diagnostics")
}

// errOpenFS.Open always fails, so fs.ReadDir(fsys, ".") errors — the ParseTerraformConfig
// read-directory diagnostic branch (tfconfig_parse.go:22-29).
type errOpenFS struct{}

func (errOpenFS) Open(string) (fs.File, error) { return nil, errors.New("cannot open") }

func Test_ParseTerraformConfig_ReadDirErrorYieldsDiagnostic(t *testing.T) {
	var got hcl.Diagnostics
	for _, diags := range ParseTerraformConfig(errOpenFS{}) {
		got = diags
	}
	require.NotNil(t, got)
	assert.True(t, got.HasErrors())
	assert.Equal(t, "Failed to read entries in Terraform config dir", got[0].Summary)
}

func Test_ParseTerraformConfig_ReadFileErrorYieldsDiagnostic(t *testing.T) {
	// A dangling symlink is listed by ReadDir as a regular file, but fs.ReadFile follows it and
	// fails — exercising the per-file read-error diagnostic (tfconfig_parse.go:36-42).
	dir := t.TempDir()
	require.NoError(t, os.Symlink(filepath.Join(dir, "nonexistent-target"), filepath.Join(dir, "broken.tf")))

	sawReadError := false
	for _, diags := range ParseTerraformConfig(os.DirFS(dir)) {
		if diags.HasErrors() {
			sawReadError = true
		}
	}
	assert.True(t, sawReadError, "a file that cannot be read must yield an error diagnostic")
}

// --- scriptcmd error/edge branches -----------------------------------------------------------

func Test_RunScript_WriteScriptFileError(t *testing.T) {
	_, err := RunScript(context.Background(), ScriptParams{
		Name:    "pre-run",
		Script:  "echo hi",
		WorkDir: filepath.Join(t.TempDir(), "does", "not", "exist"),
	})
	assert.Error(t, err, "an unwritable WorkDir must fail before executing anything")
}

func Test_RunScript_InvalidRunJsonBase64(t *testing.T) {
	_, err := RunScript(context.Background(), ScriptParams{
		Name:          "pre-run",
		Script:        "echo hi",
		WorkDir:       t.TempDir(),
		RunJsonBase64: "!!! not valid base64 !!!",
	})
	assert.Error(t, err, "an undecodable run JSON must fail the script setup")
}

func Test_ExtractExitCode_NonExitError(t *testing.T) {
	assert.Equal(t, 0, extractExitCode(nil))
	assert.Equal(t, -1, extractExitCode(errors.New("some non-exit error")))
}

func Test_ReadUserMsgFile_MissingFile(t *testing.T) {
	assert.Empty(t, readUserMsgFile(filepath.Join(t.TempDir(), "nope.txt")))
}

// --- logwrapper write-error branch -----------------------------------------------------------

func Test_LogWrap_WriteAfterCloseErrors(t *testing.T) {
	lw, err := NewLogWrap(log.New(io.Discard, "", 0), filepath.Join(t.TempDir(), "log.txt"))
	require.NoError(t, err)
	lw.Close()
	_, writeErr := lw.Write([]byte("after close"))
	assert.Error(t, writeErr, "writing to a closed update-log file must return the error (logwrapper.go:33-34)")
}

// --- execute() failure branches for DESTROY / DETECT -----------------------------------------
//
// APPLY's equivalent fail-branches are covered by the sync/async success + failure tests; DESTROY
// and DETECT carry their own copies (phase 2 collapses all three into one engine). These pin that a
// failure at SetEnv or workspace resolution aborts the run as FAILED for those two behaviors too.

func (suite *WorkerTestSuite) Test_Destroy_SetEnvFailure_FailsRun() {
	suite.tfMock.setEnvFunc = func(map[string]string) error { return errors.New("setenv boom") }
	suite.calls.fetch = runDetailsFetchCall(withBehavior(DESTROY.str()), withRepo(suite.repo.Path, suite.repoPath))
	updateCalls := suite.collectUpdatesWorker()

	suite.runWorker()

	final := decodeUpdate(suite.T(), (*updateCalls)[len(*updateCalls)-1])
	suite.Equal(FAILED.str(), *final.Status)
}

func (suite *WorkerTestSuite) Test_Detect_WorkspaceListFailure_FailsRun() {
	suite.tfMock.workspaceListFunc = func(context.Context, ...tfexec.WorkspaceListOption) ([]string, string, error) {
		return nil, "", errors.New("workspace list boom")
	}
	suite.calls.fetch = runDetailsFetchCall(withBehavior(DETECT.str()), withRepo(suite.repo.Path, suite.repoPath))
	updateCalls := suite.collectUpdatesWorker()

	suite.runWorker()

	final := decodeUpdate(suite.T(), (*updateCalls)[len(*updateCalls)-1])
	suite.Equal(FAILED.str(), *final.Status)
}

func (suite *WorkerTestSuite) Test_Destroy_MissingRepositoryPath_FailsRun() {
	suite.calls.fetch = runDetailsFetchCall(withBehavior(DESTROY.str()), withRepo(suite.repo.Path, "no/such/path"))
	updateCalls := suite.collectUpdatesWorker()

	suite.runWorker()

	final := decodeUpdate(suite.T(), (*updateCalls)[len(*updateCalls)-1])
	suite.Equal(FAILED.str(), *final.Status)
}

// Test_FinalUpdateError_HandledGracefully drives the observer's final-status UpdateState error
// branch (worker.go:220-222): the coordinator rejects the final PATCH with a 500, and the worker
// logs it without crashing.
func (suite *WorkerTestSuite) Test_FinalUpdateError_HandledGracefully() {
	suite.calls.fetch = runDetailsFetchCall(withRepo(suite.repo.Path, suite.repoPath))
	suite.calls.update = func(*http.Request) *http.Response {
		return &http.Response{StatusCode: 500, Body: io.NopCloser(bytes.NewBufferString("server error")), Header: make(http.Header)}
	}

	suite.NotPanics(func() { suite.runWorker() }, "a failing final status update must not crash the worker")
}
