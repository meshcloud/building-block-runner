package tf

// CP8 (PLAN_DETAIL_01_tf_characterization_tests.md §9): pre-run script + failure UX at use-case
// level. The pre-run script contract (D9): the script runs with a $MESHSTACK_USER_MESSAGE file whose
// trimmed content becomes the step UserMessage, receives the run JSON on stdin and the run mode as
// $1, and on failure the user message overrides the generic error. failWithUserMsg additionally
// rewrites "exit status N" and surfaces timeout/cancel context errors (tfcmd.go:108-122).

import (
	"context"
	"time"

	"github.com/hashicorp/terraform-exec/tfexec"
)

func (suite *WorkerTestSuite) Test_PreRunScript_Success_SetsUserMessage() {
	script := `echo "provisioning complete for the user" >> "$MESHSTACK_USER_MESSAGE"`
	suite.calls.fetch = runDetailsFetchCall(withRepo(suite.repo.Path, suite.repoPath), withPreRunScript(script))
	updateCalls := suite.collectUpdatesWorker()

	suite.runWorker()

	final := decodeUpdate(suite.T(), (*updateCalls)[len(*updateCalls)-1])
	suite.Equal(SUCCEEDED.str(), *final.Status)
	step := findStep(suite.T(), final, StepPreRunScript)
	suite.Equal(SUCCEEDED.str(), *step.Status)
	suite.Require().NotNil(step.UserMessage)
	suite.Equal("provisioning complete for the user", *step.UserMessage)
}

func (suite *WorkerTestSuite) Test_PreRunScript_Failure_UserMessageOverrides() {
	script := `echo "something the platform team wants you to see" >> "$MESHSTACK_USER_MESSAGE"
echo "internal detail on stderr" 1>&2
exit 3`
	suite.calls.fetch = runDetailsFetchCall(withRepo(suite.repo.Path, suite.repoPath), withPreRunScript(script))
	updateCalls := suite.collectUpdatesWorker()

	suite.runWorker()

	final := decodeUpdate(suite.T(), (*updateCalls)[len(*updateCalls)-1])
	suite.Equal(FAILED.str(), *final.Status)
	step := findStep(suite.T(), final, StepPreRunScript)
	suite.Equal(FAILED.str(), *step.Status)
	suite.Require().NotNil(step.UserMessage)
	suite.Equal("something the platform team wants you to see", *step.UserMessage)
	suite.Require().NotNil(step.SystemMessage)
	suite.Contains(*step.SystemMessage, "pre-run script exited with code 3")
}

func (suite *WorkerTestSuite) Test_PreRunScript_ReceivesRunJsonOnStdin() {
	// The script copies its stdin (the run JSON) into the user message file, proving the run JSON
	// round-trips (scriptcmd.go decodeRunJSON -> stdin).
	script := `cat >> "$MESHSTACK_USER_MESSAGE"`
	suite.calls.fetch = runDetailsFetchCall(withRepo(suite.repo.Path, suite.repoPath), withPreRunScript(script))
	updateCalls := suite.collectUpdatesWorker()

	suite.runWorker()

	final := decodeUpdate(suite.T(), (*updateCalls)[len(*updateCalls)-1])
	suite.Equal(SUCCEEDED.str(), *final.Status)
	step := findStep(suite.T(), final, StepPreRunScript)
	suite.Require().NotNil(step.UserMessage)
	suite.Contains(*step.UserMessage, "MeshBuildingBlockRun", "the run JSON must reach the script on stdin")
}

func (suite *WorkerTestSuite) Test_Timeout_ProducesTimeoutUserMessage() {
	suite.w.timeout = time.Second // far below the 10s status ticker so only the timeout fires
	// Apply blocks until the run context deadline elapses, then returns that error.
	suite.tfMock.applyFunc = func(ctx context.Context, opts ...tfexec.ApplyOption) error {
		<-ctx.Done()
		return ctx.Err()
	}

	suite.calls.fetch = runDetailsFetchCall(withRepo(suite.repo.Path, suite.repoPath))
	updateCalls := suite.collectUpdatesWorker()

	suite.runWorker()

	final := decodeUpdate(suite.T(), (*updateCalls)[len(*updateCalls)-1])
	suite.Equal(FAILED.str(), *final.Status)
	step := findStep(suite.T(), final, StepExecuteTf)
	suite.Require().NotNil(step.UserMessage)
	suite.Contains(*step.UserMessage, "exceeded the configured timeout")
}

func (suite *WorkerTestSuite) Test_ExitStatusError_RewrittenToActionableMessage() {
	suite.tfMock.applyFunc = func(ctx context.Context, opts ...tfexec.ApplyOption) error {
		return exitStatusError{}
	}

	suite.calls.fetch = runDetailsFetchCall(withRepo(suite.repo.Path, suite.repoPath))
	updateCalls := suite.collectUpdatesWorker()

	suite.runWorker()

	final := decodeUpdate(suite.T(), (*updateCalls)[len(*updateCalls)-1])
	suite.Equal(FAILED.str(), *final.Status)
	step := findStep(suite.T(), final, StepExecuteTf)
	suite.Require().NotNil(step.UserMessage)
	suite.Contains(*step.UserMessage, "command failed (exit status 1)")
}

// exitStatusError reproduces the "exit status N" text terraform-exec surfaces from a failed tofu
// process, so failWithUserMsg's rewrite branch (tfcmd.go:111-113) is exercised without a real
// subprocess.
type exitStatusError struct{}

func (exitStatusError) Error() string { return "exit status 1" }
