package tfrun

// CP4 (PLAN_DETAIL_01_tf_characterization_tests.md §9): inputs / crypto / artifact-cap pins.
//
// Pins covered here (see plan §5 D9 pin map, §6 bug inventory):
//   - genuine encrypt/decrypt round-trip for sensitive STRING and FILE inputs landing in the
//     generated tfvars file / written file (run.go:45-56 decryptIfSensitive, tfcmd.go:513-561
//     saveInputFiles, tfcmd.go:565-706 vars).
//   - decrypt failure surfaces the key-mismatch guidance text (run.go:58-63).
//   - FIXME(bug) B5: a sensitive BOOLEAN input is not decrypted (decryptIfSensitive's switch only
//     handles CODE/STRING/FILE) and its ciphertext is written verbatim.
//   - encodeVarValueForEnv (tfcmd.go:708-723): MULTISELECT JSON-encoding and its error path.
//   - buildTfEnv (tfcmd.go:455-511): env-var decrypt-failure path, and the cleanSystemEnv
//     whitelist (tfcmd.go:401-446) does not leak ambient ("poisoned") process env vars.
//   - the stale-plan apply-error message (tfapply.go:238-244) at scenario level.
//
// The 128MiB artifact-download cap pin (D9 pin 8) lives in
// go-meshapi-client/meshapi/artifact_cap_test.go, since the cap is enforced in that package.

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"testing"
	"time"

	"github.com/hashicorp/terraform-exec/tfexec"
	meshcrypto "github.com/meshcloud/building-block-runner/go-meshapi-client/crypto"
	meshapi "github.com/meshcloud/building-block-runner/go-meshapi-client/meshapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const tfvarsFileName = "aaaaaa_meshstack-e48f8924-a6c0-4ff0-9528-ff3c1f6f94d8.auto.tfvars"

// generateMismatchedTestCrypto builds a genuine (self-signed, 4096-bit — matching the checked-in
// resources/test.pem key size so a size-driven early error can't stand in for a real OAEP decrypt
// failure) RSA key pair independent of resources/test.pem+test.key, to prove decryption fails when
// the runner's configured private key does not match the key an input was encrypted with — the
// same "wrong runner / stale definition version" scenario run.go:58-63's guidance text addresses.
func generateMismatchedTestCrypto(t *testing.T) *meshcrypto.MeshCertBasedCrypto {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 4096)
	require.NoError(t, err, "generateMismatchedTestCrypto: GenerateKey")

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "cp4-mismatched-test-cert"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err, "generateMismatchedTestCrypto: CreateCertificate")
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})

	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	keyPath := filepath.Join(t.TempDir(), "mismatched.key")
	require.NoError(t, os.WriteFile(keyPath, keyPEM, 0600), "generateMismatchedTestCrypto: WriteFile")

	crypto, pubKeyErr, privateKeyErr := meshcrypto.NewCertBasedCrypto(keyPath, certPEM)
	require.NoError(t, pubKeyErr, "generateMismatchedTestCrypto: NewCertBasedCrypto pubKeyErr")
	require.NoError(t, privateKeyErr, "generateMismatchedTestCrypto: NewCertBasedCrypto privateKeyErr")

	return crypto
}

// Test_ApplySucceeded_DecryptsSensitiveStringAndFileInputsIntoWorkingDir pins that a sensitive
// STRING input is decrypted into plaintext in the generated tfvars file, and a sensitive FILE
// input is decrypted and written verbatim to its target file — the ciphertext itself must not
// leak into either. Uses genuine encryption/decryption with the checked-in test key pair
// (resources/test.pem/test.key), not ciphertext-shaped assertions.
func (suite *WorkerTestSuite) Test_ApplySucceeded_DecryptsSensitiveStringAndFileInputsIntoWorkingDir() {
	crypto := installTestCrypto(suite.T())

	const secretValue = "s3cr3t-database-password"
	secretCiphertext := encryptForTest(suite.T(), crypto, secretValue)

	const fileContent = "secret-file-content\n"
	fileDataUrl := "data:text/plain;base64," + base64.StdEncoding.EncodeToString([]byte(fileContent))
	fileCiphertext := encryptForTest(suite.T(), crypto, fileDataUrl)

	suite.calls.fetch = runDetailsFetchCall(
		withBehavior(APPLY.str()),
		withRepo(suite.repo.Path, suite.repoPath),
		withInputs(
			buildingBlockInput("secret_var", secretCiphertext, DATA_TYPE_STRING, sensitiveInput()),
			buildingBlockInput("secret_file", fileCiphertext, DATA_TYPE_FILE, sensitiveInput()),
		),
	)

	var tfvarsContent, writtenFileContent []byte
	suite.tfMock.applyFunc = func(ctx context.Context, opts ...tfexec.ApplyOption) error {
		rci := ctx.Value(runInfoContextKey).(*RunContextInfo)
		var err error
		tfvarsContent, err = os.ReadFile(path.Join(rci.workingDirectory, tfvarsFileName))
		suite.Require().NoError(err, "reading generated tfvars file")
		writtenFileContent, err = os.ReadFile(path.Join(rci.workingDirectory, "secret_file"))
		suite.Require().NoError(err, "reading decrypted secret_file")
		return nil
	}

	updateCalls := make([]http.Request, 0)
	suite.calls.update = func(req *http.Request) *http.Response {
		updateCalls = append(updateCalls, *req)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewBuffer([]byte("{}"))), Header: make(http.Header)}
	}

	suite.runWorker()

	suite.Contains(string(tfvarsContent), secretValue, "decrypted plaintext must land in the tfvars file")
	suite.NotContains(string(tfvarsContent), secretCiphertext, "ciphertext must never leak into the tfvars file")
	suite.Equal(fileContent, string(writtenFileContent), "the decrypted data-URL content must be written verbatim")

	suite.Require().GreaterOrEqual(len(updateCalls), 1)
	lastUpdate := updateCalls[len(updateCalls)-1]
	data, err := io.ReadAll(lastUpdate.Body)
	suite.Require().NoError(err)
	var update meshapi.RunStatusUpdateDTO
	suite.Require().NoError(json.Unmarshal(data, &update))
	suite.Equal(SUCCEEDED.str(), *update.Status)
}

// Test_ApplyFailed_SensitiveInputDecryptFailure_KeyMismatchGuidance pins the D9 "decrypt-failure
// UX" behavior (run.go:56-64): when a sensitive input was encrypted with a public key that does not
// correspond to the runner's configured private key, the run FAILS and the input step's message
// contains the key-mismatch guidance text — not just a generic decryption error.
func (suite *WorkerTestSuite) Test_ApplyFailed_SensitiveInputDecryptFailure_KeyMismatchGuidance() {
	installTestCrypto(suite.T()) // the runner is configured with resources/test.pem/test.key
	other := generateMismatchedTestCrypto(suite.T())
	ciphertext := encryptForTest(suite.T(), other, "top-secret-value") // encrypted with a different key pair

	suite.calls.fetch = runDetailsFetchCall(
		withBehavior(APPLY.str()),
		withRepo(suite.repo.Path, suite.repoPath),
		withInputs(buildingBlockInput("secret_var", ciphertext, DATA_TYPE_STRING, sensitiveInput())),
	)

	updateCalls := make([]http.Request, 0)
	suite.calls.update = func(req *http.Request) *http.Response {
		updateCalls = append(updateCalls, *req)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewBuffer([]byte("{}"))), Header: make(http.Header)}
	}

	suite.runWorker()

	suite.Require().GreaterOrEqual(len(updateCalls), 1)
	lastUpdate := updateCalls[len(updateCalls)-1]
	data, err := io.ReadAll(lastUpdate.Body)
	suite.Require().NoError(err)
	var update meshapi.RunStatusUpdateDTO
	suite.Require().NoError(json.Unmarshal(data, &update))

	suite.Equal(FAILED.str(), *update.Status)
	inputStep := findStep(suite.T(), update, StepInput)
	suite.Equal(FAILED.str(), *inputStep.Status)
	// The decrypt error (with the key-mismatch guidance) is logged to the update logs
	// (tfcmd.go:602-604 vars()) and surfaces via the step's SystemMessage; the UserMessage set by
	// failWithUserMsg is the generic "input decryption failed for '%s'" wrapper (tfcmd.go:604) — both
	// are asserted here so the pin documents the actual (not idealized) message routing.
	suite.Require().NotNil(inputStep.UserMessage)
	suite.Contains(*inputStep.UserMessage, "input decryption failed for 'secret_var'")
	suite.Require().NotNil(inputStep.SystemMessage)
	suite.Contains(*inputStep.SystemMessage,
		"private key provided to this building block block runner does not match")
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

	suite.Equal(FAILED.str(), *update.Status)
	executeTf := findStep(suite.T(), update, StepExecuteTf)
	suite.Equal(FAILED.str(), *executeTf.Status)
	suite.Require().NotNil(executeTf.UserMessage)
	suite.Contains(*executeTf.UserMessage, "applying the previewed terraform plan failed")
	suite.Contains(*executeTf.UserMessage, "no longer valid")
}

// Test_Vars_SensitiveBooleanInput_PassesThroughCiphertextVerbatim pins
// // FIXME(bug): B5 — decryptIfSensitive's type switch (run.go:45-56) only decrypts sensitive
// CODE/STRING/FILE values; a sensitive BOOLEAN (also: INTEGER/SINGLE_SELECT/MULTI_SELECT/LIST)
// input falls through the switch untouched, so its ciphertext is written into the tfvars file as
// if it were the real value. Correct behavior (phase 2b): decrypt every sensitive value or fail
// fast, per the bug inventory (plan §6, B5).
func Test_Vars_SensitiveBooleanInput_PassesThroughCiphertextVerbatim(t *testing.T) {
	uut := makeTestGenericTfCmd(t)

	const ciphertext = "QUFBQUJCQkJDQ0NDRERERA==-not-a-real-boolean-ciphertext"
	uut.params.vars["flag"] = &Variable{value: ciphertext, isSensitive: true, Type: DATA_TYPE_BOOLEAN}

	err := uut.vars()
	require.NoError(t, err)

	tfvarsPath := path.Join(uut.runContextInfo.workingDirectory, tfvarsFileName)
	content, err := os.ReadFile(tfvarsPath)
	require.NoError(t, err)
	assert.Contains(t, string(content), ciphertext, "B5: ciphertext should (buggily) appear verbatim")
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

// Test_buildTfEnv_EnvVarDecryptFailure_ReturnsError pins buildTfEnv's env-var decrypt-failure path
// (tfcmd.go:481-484): a sensitive env-marked variable whose ciphertext fails to decrypt makes
// buildTfEnv fail the whole call (not just skip that one variable).
func Test_buildTfEnv_EnvVarDecryptFailure_ReturnsError(t *testing.T) {
	installTestCrypto(t)

	uut := makeTestGenericTfCmd(t)
	uut.params.vars["SECRET_ENV"] = &Variable{
		value:       "not-valid-base64!!",
		env:         true,
		isSensitive: true,
		Type:        DATA_TYPE_STRING,
	}

	_, err := uut.buildTfEnv()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "input decryption failed for 'SECRET_ENV'")
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
