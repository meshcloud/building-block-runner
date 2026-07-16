package azdevops

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/internal/meshapitest"
)

// Test_Scenario_SyncPoll_AlreadyCompleted verifies an already-COMPLETED trigger response
// sends the final update immediately, zero poll GETs.
func Test_Scenario_SyncPoll_AlreadyCompleted(t *testing.T) {
	ado := newSeqADO(t)
	ado.triggerResp = adoResp{status: 200, body: `{"id":5,"state":"completed","result":"succeeded","createdDate":"now"}`}
	srv := meshapitest.NewServer(t)

	run := buildRun(t, ado.URL, "tok", implFixture{PersonalAccessToken: "pat", Async: false}, nil)
	h := newTestHandler(factoryFor(srv), newFakeClock())
	require.NoError(t, execute(t, h, run))

	require.Len(t, ado.Requests(), 1, "only the trigger POST -- zero GET run/timeline calls")

	patches := srv.Patches()
	require.Len(t, patches, 2, "trigger-success update, then the final update")
	final := decodePatch(t, patches[1].Body)
	require.Equal(t, "SUCCEEDED", final.Status)
	require.Equal(t, "SUCCEEDED", final.Steps[0].Status)
	require.Contains(t, final.Steps[0].SystemMessage, "state: COMPLETED, result: SUCCEEDED", "final message uses enum-NAME rendering, not wire values")
}

// Test_Scenario_SyncPoll_Happy covers one iteration inProgress->completed with
// stages -- asserts stage steps, trigger-step re-emission (different message than the
// initial trigger update), one-way dedup + COMPLETED re-send, and the final trigger-only
// update with enum-NAME rendering.
func Test_Scenario_SyncPoll_Happy(t *testing.T) {
	ado := newSeqADO(t)
	srv := meshapitest.NewServer(t)

	// Iteration 1: run still in progress, two stages (build completed, deploy in progress).
	ado.SeedRun(200, `{"id":1,"state":"inProgress","result":null,"createdDate":"now"}`)
	ado.SeedTimeline(200, `{"records":[
		{"id":"build","name":"Build","type":"Stage","order":0,"state":"completed","result":"succeeded"},
		{"id":"deploy","name":"Deploy","type":"Stage","order":1,"state":"inProgress"}
	]}`)
	// Iteration 2: run completed; "build" is COMPLETED again (re-sent), "deploy" now
	// COMPLETED+succeeded (new terminal state).
	ado.SeedRun(200, `{"id":1,"state":"completed","result":"succeeded","createdDate":"now","url":"https://ado/run/1"}`)
	ado.SeedTimeline(200, `{"records":[
		{"id":"build","name":"Build","type":"Stage","order":0,"state":"completed","result":"succeeded"},
		{"id":"deploy","name":"Deploy","type":"Stage","order":1,"state":"completed","result":"succeeded"}
	]}`)

	run := buildRun(t, ado.URL, "tok", implFixture{PersonalAccessToken: "pat", Async: false}, nil)
	h := newTestHandler(factoryFor(srv), newFakeClock())
	require.NoError(t, execute(t, h, run))

	patches := srv.Patches()
	require.Len(t, patches, 4, "trigger-success, 2 stage-batch updates, final")

	trigger := decodePatch(t, patches[0].Body)
	require.Contains(t, trigger.Steps[0].UserMessage, "Polling for completion status...")

	batch1 := decodePatch(t, patches[1].Body)
	require.Equal(t, "IN_PROGRESS", batch1.Status)
	require.Len(t, batch1.Steps, 3, "trigger step + 2 stages")
	require.Equal(t, StepId, batch1.Steps[0].Id)
	require.Equal(t, "SUCCEEDED", batch1.Steps[0].Status)
	require.Equal(t, "Triggered Azure DevOps Pipeline", batch1.Steps[0].UserMessage, "re-emission uses the no-suffix message (U-P4), distinct from the initial trigger update")
	require.Equal(t, "ado-stage-build", batch1.Steps[1].Id)
	require.Equal(t, "Stage: Build", batch1.Steps[1].DisplayName)
	require.Equal(t, "SUCCEEDED", batch1.Steps[1].Status)
	require.Equal(t, "ado-stage-deploy", batch1.Steps[2].Id)
	require.Equal(t, "IN_PROGRESS", batch1.Steps[2].Status)

	batch2 := decodePatch(t, patches[2].Body)
	require.Len(t, batch2.Steps, 3, "COMPLETED 'build' is re-sent every subsequent poll (U-P5 one-way dedup)")
	require.Equal(t, "ado-stage-build", batch2.Steps[1].Id)
	require.Equal(t, "SUCCEEDED", batch2.Steps[1].Status)
	require.Equal(t, "ado-stage-deploy", batch2.Steps[2].Id)
	require.Equal(t, "SUCCEEDED", batch2.Steps[2].Status, "deploy newly COMPLETED -- included")

	final := decodePatch(t, patches[3].Body)
	require.Equal(t, "SUCCEEDED", final.Status)
	require.Len(t, final.Steps, 1, "final update carries only the trigger step, no stage steps")
	require.Contains(t, final.Steps[0].SystemMessage, "Pipeline run 1 completed with state: COMPLETED, result: SUCCEEDED. View run: https://ado/run/1")
}

// Test_Scenario_SyncPoll_EmptyStages covers an empty stage list falling back to the
// run-state-only update, sent unconditionally (no dedup on this path -- only the
// timeline-*failure* fallback dedups).
func Test_Scenario_SyncPoll_EmptyStages(t *testing.T) {
	ado := newSeqADO(t)
	srv := meshapitest.NewServer(t)
	ado.SeedRun(200, `{"id":1,"state":"completed","result":"succeeded","createdDate":"now"}`)
	ado.SeedTimeline(200, `{"records":[]}`)

	run := buildRun(t, ado.URL, "tok", implFixture{PersonalAccessToken: "pat", Async: false}, nil)
	h := newTestHandler(factoryFor(srv), newFakeClock())
	require.NoError(t, execute(t, h, run))

	patches := srv.Patches()
	require.Len(t, patches, 3, "trigger-success, the unconditional empty-stages state-only update, then final")
	stateOnly := decodePatch(t, patches[1].Body)
	require.Equal(t, "IN_PROGRESS", stateOnly.Status)
	require.Contains(t, stateOnly.Steps[0].SystemMessage, "Pipeline run 1 state: completed")
}

// Test_Scenario_SyncPoll_Timeout verifies a fake Clock already past the 30-min budget
// reports exactly one failed update with the pinned timeout message, no GET, no wait.
func Test_Scenario_SyncPoll_Timeout(t *testing.T) {
	ado := newSeqADO(t)
	srv := meshapitest.NewServer(t)

	run := buildRun(t, ado.URL, "tok", implFixture{PersonalAccessToken: "pat", Async: false}, nil)
	clock := newFakeClock()
	clock.bumpAfterFirstCall = 31 * time.Minute
	h := newTestHandler(factoryFor(srv), clock)
	require.NoError(t, execute(t, h, run))

	require.Empty(t, ado.Requests()[1:], "no GET run/timeline calls after the trigger") // only the trigger POST

	patches := srv.Patches()
	require.Len(t, patches, 2, "trigger-success, then the failed timeout update")
	failed := decodePatch(t, patches[1].Body)
	require.Equal(t, "FAILED", failed.Status)
	require.Equal(t, "Could not trigger the Azure DevOps Pipeline", failed.Steps[0].UserMessage)
	require.Contains(t, failed.Steps[0].SystemMessage, "internal error")
	require.Contains(t, failed.Steps[0].SystemMessage, "Pipeline polling timeout after 30 minutes")
}

// Test_Scenario_SyncPoll_Resilience verifies a GET-run failure is retried (never fatal); a
// timeline failure falls back to a state-only update, sent only on state change.
func Test_Scenario_SyncPoll_Resilience(t *testing.T) {
	ado := newSeqADO(t)
	srv := meshapitest.NewServer(t)

	ado.SeedRun(500, `{"message":"transient"}`) // iteration 1: GET-run fails, retried
	// iteration 2: GET-run ok (still inProgress), timeline fails -> fallback state-only
	ado.SeedRun(200, `{"id":1,"state":"inProgress","result":null,"createdDate":"now"}`)
	ado.SeedTimeline(500, `{"message":"timeline unavailable"}`)
	// iteration 3: same state again (inProgress) -- fallback dedup means no 2nd state-only
	// update this time.
	ado.SeedRun(200, `{"id":1,"state":"inProgress","result":null,"createdDate":"now"}`)
	ado.SeedTimeline(500, `{"message":"still unavailable"}`)
	// iteration 4: timeline succeeds with 0 stages -- the OTHER (unconditional, no-dedup)
	// state-only path, distinct from the fallback's dedup.
	ado.SeedRun(200, `{"id":1,"state":"completed","result":"succeeded","createdDate":"now"}`)
	ado.SeedTimeline(200, `{"records":[]}`)

	run := buildRun(t, ado.URL, "tok", implFixture{PersonalAccessToken: "pat", Async: false}, nil)
	h := newTestHandler(factoryFor(srv), newFakeClock())
	require.NoError(t, execute(t, h, run))

	patches := srv.Patches()
	// trigger-success, 1 fallback state-only (iteration 2; iteration 3's identical state is
	// deduped), the empty-stages state-only (iteration 4, unconditional), final.
	require.Len(t, patches, 4)
	fallback := decodePatch(t, patches[1].Body)
	require.Equal(t, "IN_PROGRESS", fallback.Status)
	require.Contains(t, fallback.Steps[0].SystemMessage, "Pipeline run 1 state: inProgress")

	final := decodePatch(t, patches[3].Body)
	require.Equal(t, "SUCCEEDED", final.Status)
}

// Test_Scenario_SyncPoll_ReportFailureEscalates verifies a PATCH failure sending the final
// update triggers exactly one failed-update attempt; a second PATCH failure propagates out
// of Execute.
func Test_Scenario_SyncPoll_ReportFailureEscalates(t *testing.T) {
	ado := newSeqADO(t)
	ado.triggerResp = adoResp{status: 200, body: `{"id":5,"state":"completed","result":"succeeded","createdDate":"now"}`}
	srv := meshapitest.NewServer(t)
	srv.SeedPatchResponse(meshapitest.PatchResponse{Status: 200}) // trigger-success update: ok
	srv.SeedPatchResponse(meshapitest.PatchResponse{Status: 500}) // final update: fails
	srv.SeedPatchResponse(meshapitest.PatchResponse{Status: 500}) // escalation attempt: also fails

	run := buildRun(t, ado.URL, "tok", implFixture{PersonalAccessToken: "pat", Async: false}, nil)
	h := newTestHandler(factoryFor(srv), newFakeClock())
	require.Error(t, execute(t, h, run), "a second PATCH failure propagates out of Execute")
	require.Len(t, srv.Patches(), 3, "trigger-success + the failed final attempt + the one escalation attempt")
}

// Test_Scenario_SyncPoll_CtxCancelReportsTerminal verifies ctx cancellation mid-poll reports
// a TERMINAL status (ABORTED), never leaves the run
// IN_PROGRESS, and pollCompletion returns nil (the run was handled, not an infra failure).
// Driven directly against pollCompletion (rather than through Execute/TriggerPipeline) so the
// test controls exactly when ctx is cancelled without racing the trigger HTTP call, which
// shares the same ctx.
func Test_Scenario_SyncPoll_CtxCancelReportsTerminal(t *testing.T) {
	srv := meshapitest.NewServer(t)
	run := buildRun(t, "http://unused.invalid", "tok", implFixture{PersonalAccessToken: "pat"}, nil)
	reporter := factoryFor(srv)(run)

	clock := newFakeClock()
	clock.afterBlocks = true // force ctx.Done() to be the only ready case, deterministically
	h := newTestHandler(factoryFor(srv), clock)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := h.pollCompletion(ctx, adoClient{}, reporter, testUuid, pipelineRun{Id: 1, State: stateInProgress})
	require.NoError(t, err, "a successfully-reported terminal status is a handled outcome, not an infra failure")

	patches := srv.Patches()
	require.Len(t, patches, 1, "exactly one report: the abort")
	aborted := decodePatch(t, patches[0].Body)
	require.Equal(t, "ABORTED", aborted.Status)
	require.Equal(t, "ABORTED", aborted.Steps[0].Status)
}

// Test_Scenario_SyncPoll_BackendAbort verifies T1: a backend-requested runAborted on the
// in-loop stage-batch PATCH response stops polling promptly -- no further GET run/timeline
// calls -- and reports terminal ABORTED, never SUCCEEDED, with Execute returning nil.
func Test_Scenario_SyncPoll_BackendAbort(t *testing.T) {
	ado := newSeqADO(t)
	srv := meshapitest.NewServer(t)
	srv.SeedPatchResponse(meshapitest.PatchResponse{Status: 200})              // trigger-success update: ok
	srv.SeedPatchResponse(meshapitest.PatchResponse{Status: 200, Abort: true}) // stage-batch update: backend signals abort

	ado.SeedRun(200, `{"id":1,"state":"inProgress","result":null,"createdDate":"now"}`)
	ado.SeedTimeline(200, `{"records":[
		{"id":"build","name":"Build","type":"Stage","order":0,"state":"inProgress"}
	]}`)

	run := buildRun(t, ado.URL, "tok", implFixture{PersonalAccessToken: "pat", Async: false}, nil)
	h := newTestHandler(factoryFor(srv), newFakeClock())
	require.NoError(t, execute(t, h, run))

	require.Len(t, ado.Requests(), 3, "trigger POST + one GET run + one GET timeline -- no further polling")

	patches := srv.Patches()
	require.Len(t, patches, 3, "trigger-success, the stage-batch update carrying the abort signal, then the ABORTED follow-up")
	aborted := decodePatch(t, patches[2].Body)
	require.Equal(t, "ABORTED", aborted.Status)
	require.Equal(t, "ABORTED", aborted.Steps[0].Status)
}

// Test_Scenario_SyncPoll_CtxCancelFallsBackToFailed pins the "never SUCCEEDED, fall back to
// FAILED if ABORTED is rejected" half of the sync-poll cancellation contract.
func Test_Scenario_SyncPoll_CtxCancelFallsBackToFailed(t *testing.T) {
	srv := meshapitest.NewServer(t)
	srv.SeedPatchResponse(meshapitest.PatchResponse{Status: 409}) // ABORTED rejected
	run := buildRun(t, "http://unused.invalid", "tok", implFixture{PersonalAccessToken: "pat"}, nil)
	reporter := factoryFor(srv)(run)

	clock := newFakeClock()
	clock.afterBlocks = true
	h := newTestHandler(factoryFor(srv), clock)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := h.pollCompletion(ctx, adoClient{}, reporter, testUuid, pipelineRun{Id: 1, State: stateInProgress})
	require.NoError(t, err)

	patches := srv.Patches()
	require.Len(t, patches, 2, "the rejected ABORTED attempt, then the FAILED fallback")
	failedFallback := decodePatch(t, patches[1].Body)
	require.Equal(t, "FAILED", failedFallback.Status)
}
