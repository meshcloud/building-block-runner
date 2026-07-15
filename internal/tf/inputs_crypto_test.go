package tf

// Inputs / crypto / artifact-cap pins.
//
// Pins covered here:
//   - a plaintext (already-decrypted) STRING/FILE input lands verbatim in the generated tfvars
//     file / written file (tfcmd.go:513-561 saveInputFiles, tfcmd.go:565-706 vars). Decryption
//     itself -- the genuine encrypt/decrypt round-trip, key-mismatch guidance, and the
//     sensitive-non-decryptable-type guard -- now happens once at the claim boundary and is
//     pinned there (internal/meshapi's DecryptRunDetails tests, wired via internal/rundecrypt),
//     not in this package.
//   - encodeVarValueForEnv (tfcmd.go:708-723): MULTISELECT JSON-encoding and its error path.
//   - buildTfEnv (tfcmd.go:455-511): the cleanSystemEnv whitelist (tfcmd.go:401-446) does not
//     leak ambient ("poisoned") process env vars.
//   - the stale-plan apply-error message (tfapply.go:238-244) at scenario level.
//
// The 128MiB artifact-download cap pin lives in
// go-meshapi-client/meshapi/artifact_cap_test.go, since the cap is enforced in that package.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path"
	"testing"

	"github.com/hashicorp/terraform-exec/tfexec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	meshapi "github.com/meshcloud/building-block-runner/internal/meshapi"
	"github.com/meshcloud/building-block-runner/internal/report"
)

const tfvarsFileName = "aaaaaa_meshstack-e48f8924-a6c0-4ff0-9528-ff3c1f6f94d8.auto.tfvars"

// Test_ApplySucceeded_PlaintextStringAndFileInputsLandInWorkingDir pins that a plaintext STRING
// input lands verbatim in the generated tfvars file, and a plaintext FILE input's data-URL
// content is written verbatim to its target file. Every claimed run's inputs arrive plaintext
// (decryption happens once at the claim boundary), so these are no longer "sensitive" inputs
// from this package's point of view.
func (suite *WorkerTestSuite) Test_ApplySucceeded_PlaintextStringAndFileInputsLandInWorkingDir() {
	const secretValue = "s3cr3t-database-password"

	const fileContent = "secret-file-content\n"
	fileDataUrl := "data:text/plain;base64," + base64.StdEncoding.EncodeToString([]byte(fileContent))

	suite.calls.fetch = runDetailsFetchCall(
		withBehavior(APPLY.str()),
		withRepo(suite.repo.Path, suite.repoPath),
		withInputs(
			buildingBlockInput("secret_var", secretValue, DATA_TYPE_STRING, sensitiveInput()),
			buildingBlockInput("secret_file", fileDataUrl, DATA_TYPE_FILE, sensitiveInput()),
		),
	)

	var tfvarsContent, writtenFileContent []byte
	suite.tfMock.applyFunc = func(ctx context.Context, opts ...tfexec.ApplyOption) error {
		var err error
		tfvarsContent, err = os.ReadFile(path.Join(suite.tfMock.workingDir, tfvarsFileName))
		suite.Require().NoError(err, "reading generated tfvars file")
		writtenFileContent, err = os.ReadFile(path.Join(suite.tfMock.workingDir, "secret_file"))
		suite.Require().NoError(err, "reading written secret_file")
		return nil
	}

	updateCalls := make([]http.Request, 0)
	suite.calls.update = func(req *http.Request) *http.Response {
		updateCalls = append(updateCalls, *req)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewBuffer([]byte("{}"))), Header: make(http.Header)}
	}

	suite.runWorker()

	suite.Contains(string(tfvarsContent), secretValue, "plaintext value must land in the tfvars file")
	suite.Equal(fileContent, string(writtenFileContent), "the data-URL content must be written verbatim")

	suite.Require().GreaterOrEqual(len(updateCalls), 1)
	lastUpdate := updateCalls[len(updateCalls)-1]
	data, err := io.ReadAll(lastUpdate.Body)
	suite.Require().NoError(err)
	var update meshapi.RunStatusUpdateDTO
	suite.Require().NoError(json.Unmarshal(data, &update))
	suite.Equal(report.SUCCEEDED.String(), *update.Status)
}

// Test_ApplyWithPlanArtifact_StalePlanApplyFailure pins the stale-plan apply-error message
// (tfapply.go:238-244): when applying a downloaded predecessor plan fails (e.g. state/config drift
// since the dry-run), the run fails with an actionable message telling the user to re-run the
// dry-run, distinct from the "download failed" message already pinned by
// Test_ApplyWithPlanArtifact_DownloadFailureFailsRun.
func (suite *WorkerTestSuite) Test_ApplyWithPlanArtifact_StalePlanApplyFailure() {
	planArtifactHref := "http://localhost/api/meshobjects/meshbuildingblockruns/run-uuid/plan-artifact"
	suite.calls.fetch = mockApplyRunWithPlanArtifactFetchCall(suite.repo.Path, suite.repoPath, planArtifactHref)

	suite.calls.download = func(req *http.Request) *http.Response {
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(bytes.NewBuffer([]byte("stale-plan-bytes"))),
			Header:     make(http.Header),
		}
	}

	applyOptCount := -1
	suite.tfMock.applyFunc = func(ctx context.Context, opts ...tfexec.ApplyOption) error {
		applyOptCount = len(opts)
		return assert.AnError // stand-in for terraform's real "Saved plan is stale" rejection
	}

	updateCalls := make([]http.Request, 0)
	suite.calls.update = func(req *http.Request) *http.Response {
		updateCalls = append(updateCalls, *req)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewBuffer([]byte("{}"))), Header: make(http.Header)}
	}

	suite.runWorker()

	suite.Equal(1, applyOptCount, "apply must still be called with the saved-plan option")

	suite.Require().GreaterOrEqual(len(updateCalls), 1)
	lastUpdate := updateCalls[len(updateCalls)-1]
	data, err := io.ReadAll(lastUpdate.Body)
	suite.Require().NoError(err)
	var update meshapi.RunStatusUpdateDTO
	suite.Require().NoError(json.Unmarshal(data, &update))

	suite.Equal(report.FAILED.String(), *update.Status)
	executeTf := findStep(suite.T(), update, StepExecuteTf)
	suite.Equal(report.FAILED.String(), *executeTf.Status)
	suite.Require().NotNil(executeTf.UserMessage)
	suite.Contains(*executeTf.UserMessage, "applying the previewed terraform plan failed")
	suite.Contains(*executeTf.UserMessage, "no longer valid")
}

// Test_encodeVarValueForEnv_MultiSelectJSONEncodesValue pins the MULTISELECT branch of
// encodeVarValueForEnv (tfcmd.go:708-723): a MULTI_SELECT value is JSON-encoded, unlike every other
// type which is passed through fmt.Sprintf.
func Test_encodeVarValueForEnv_MultiSelectJSONEncodesValue(t *testing.T) {
	got, err := encodeVarValueForEnv([]string{"a", "b"}, DATA_TYPE_MULTISELECT)
	require.NoError(t, err)
	assert.JSONEq(t, `["a","b"]`, got)
}

// Test_encodeVarValueForEnv_MultiSelectUnmarshalableValue_ReturnsError pins the error branch: a
// value json.Marshal cannot encode (e.g. a channel) surfaces as an error instead of being silently
// stringified.
func Test_encodeVarValueForEnv_MultiSelectUnmarshalableValue_ReturnsError(t *testing.T) {
	_, err := encodeVarValueForEnv(make(chan int), DATA_TYPE_MULTISELECT)
	require.Error(t, err)
}

// Test_buildTfEnv_DoesNotLeakAmbientSecretEnvVars pins cleanSystemEnv's whitelist (tfcmd.go:401-446)
// end to end through buildTfEnv: an ambient process env var that is not on the whitelist (e.g. a
// cloud credential injected at container startup) must not appear in the Terraform subprocess env,
// even though it is present in the test process's real environment.
func Test_buildTfEnv_DoesNotLeakAmbientSecretEnvVars(t *testing.T) {
	uut := makeTestGenericTfCmd(t)
	t.Setenv("AWS_SECRET_ACCESS_KEY", "super-secret-value")

	capturedEnv, err := uut.buildTfEnv()
	require.NoError(t, err)

	assert.NotContains(t, capturedEnv, "AWS_SECRET_ACCESS_KEY")
}
