package tf

// The remaining execute() failure
// branches that APPLY already exercises but DETECT/DESTROY carry their own copies of, plus the FILE
// input and malformed-HCL branches of saveInputFiles/vars, and a direct buildTfEnv SSH pin.

import (
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	meshapi "github.com/meshcloud/building-block-runner/internal/meshapi"
	"github.com/meshcloud/building-block-runner/internal/report"
)

func (suite *WorkerTestSuite) fileInputRun(behavior string, input meshapi.BuildingBlockInputSpecDTO) *[]http.Request {
	suite.calls.fetch = runDetailsFetchCall(withBehavior(behavior), withRepo(suite.repo.Path, suite.repoPath), withInputs(input))
	return suite.collectUpdatesWorker()
}

func (suite *WorkerTestSuite) Test_Apply_InvalidFileInputDataUrl_FailsRun() {
	updateCalls := suite.fileInputRun(APPLY.str(), buildingBlockInput("badfile", "not-a-data-url", DATA_TYPE_FILE))
	suite.runWorker()
	final := decodeUpdate(suite.T(), (*updateCalls)[len(*updateCalls)-1])
	suite.Equal(report.FAILED.String(), *final.Status)
	step := findStep(suite.T(), final, StepInput)
	suite.Equal(report.FAILED.String(), *step.Status)
}

func (suite *WorkerTestSuite) Test_Destroy_InvalidFileInputDataUrl_FailsRun() {
	updateCalls := suite.fileInputRun(DESTROY.str(), buildingBlockInput("badfile", "not-a-data-url", DATA_TYPE_FILE))
	suite.runWorker()
	final := decodeUpdate(suite.T(), (*updateCalls)[len(*updateCalls)-1])
	suite.Equal(report.FAILED.String(), *final.Status)
}

func (suite *WorkerTestSuite) Test_Detect_FileInputMarkedEnv_FailsRun() {
	// A FILE input cannot also be an environment variable (tfcmd.go:520-524).
	updateCalls := suite.fileInputRun(DETECT.str(), buildingBlockInput("f", "data:text/plain;base64,aGk=", DATA_TYPE_FILE, envInput()))
	suite.runWorker()
	final := decodeUpdate(suite.T(), (*updateCalls)[len(*updateCalls)-1])
	suite.Equal(report.FAILED.String(), *final.Status)
}

func (suite *WorkerTestSuite) Test_Apply_MalformedTerraformConfig_StillSucceeds() {
	// A malformed .tf makes ParseVariableInputs return error diagnostics; vars() logs them and
	// proceeds with an empty variable set rather than failing the run (tfcmd.go:569-576).
	repo := makeLocalGitRepo(suite.T(), map[string]string{"main.tf": "variable \"x\" { type = "})
	suite.calls.fetch = runDetailsFetchCall(withRepo(repo.Path, ""))
	updateCalls := suite.collectUpdatesWorker()

	suite.runWorker()

	final := decodeUpdate(suite.T(), (*updateCalls)[len(*updateCalls)-1])
	suite.Equal(report.SUCCEEDED.String(), *final.Status, "a malformed config only skips type-mismatch detection, it does not fail the run")
}

// Test_BuildTfEnv_SshSourceSetsGitSshCommand pins that an SSH-authenticated source injects
// GIT_SSH_COMMAND into the terraform subprocess env, honoring SkipHostKeyValidation
// (tfcmd.go:460-467).
func Test_BuildTfEnv_SshSourceSetsGitSshCommand(t *testing.T) {
	wd := t.TempDir()
	lw, err := NewLogWrap(slog.New(slog.NewTextHandler(io.Discard, nil)), filepath.Join(wd, "log.txt"))
	require.NoError(t, err)
	rci := &RunContextInfo{workingDirectory: wd, logwrap: lw}
	tfcmd := &GenericTfCmd{
		runContextInfo: rci,
		params:         &TfCmdParams{source: &GitSource{auth: &SshAuth{}}, vars: map[string]*Variable{}},
	}

	tfcmd.params.skipHostKeyValidation = false
	env, err := tfcmd.buildTfEnv()
	require.NoError(t, err)
	require.Contains(t, env, "GIT_SSH_COMMAND")
	assert.Contains(t, env["GIT_SSH_COMMAND"], TMP_FILE_SSH_CERT)
	assert.NotContains(t, env["GIT_SSH_COMMAND"], "StrictHostKeyChecking=no")

	tfcmd.params.skipHostKeyValidation = true
	env, err = tfcmd.buildTfEnv()
	require.NoError(t, err)
	assert.Contains(t, env["GIT_SSH_COMMAND"], "StrictHostKeyChecking=no", "insecure host-key mode appends the ssh option")
}
