package tfrun

// bug_inventory_test.go pins the D13 bug inventory
// (PLAN_DETAIL_01_tf_characterization_tests.md §6): every FIXME(bug) test below asserts
// *current*, buggy behavior verbatim. Do not "fix" these assertions here — phase 2b flips each
// one to assert correct behavior together with the matching production fix (D13). This file is
// deliberately self-contained (its own TfFacade test double, its own tfcmd/execute harness) so it
// stays disjoint from the other phase-1 checkpoint test files.
//
// Not pinned here, by design:
//   - B6 (manager.go shutdownCalled) and B10 (runcontextinfo.go reportStatus shallow copy) are
//     genuine data races. D13 exempts them from "pin verbatim, fix in 2b": they are fixed
//     structurally in phase 2 (atomic.Bool / deep-copy), so no FIXME(bug) test asserts the race
//     itself here (A5: `-race` is intentionally not enabled until phase 2).
//   - B8 (tfbinaries.go installTofuBinaries uses context.Background()) lives in a file excluded
//     from the coverage gate (§7) and needs a live network download to reach; inventory-only.
//   - B9 (gitsource.go nil-deref logging `*g.path` when it is nil) requires the cloned tmp
//     directory to vanish between clone and stat — not reachable without a contrived facade;
//     inventory-only per the detail plan.
//   - B11 (main.go single-run failure only logged, process exits 0) lives in `package main`,
//     outside the `tfrun` gate; revisited with the persona main in phase 2/4.

import (
	"context"
	"errors"
	"io"
	"log"
	"os"
	"path"
	"testing"
	"time"

	"github.com/hashicorp/terraform-exec/tfexec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- shared test double: a TfFacade with configurable workspace behavior -----------------------
//
// This is a bug-inventory-local double, not an extension of the shared MockedTfFacade
// (mockedtffacade.go): selectWorkspace/deleteWorkspaceIfNeeded/plainInit are exercised directly
// (no Worker/execute() involved for the workspace pins), so a small local double keeps this
// slice's production-file footprint at zero.
type workspaceFacade struct {
	workspaceListFunc    func(ctx context.Context) ([]string, string, error)
	workspaceSelectFunc  func(ctx context.Context, workspace string) error
	workspaceSelectCalls []string
	workspaceNewCalls    []string
	workspaceDeleteCalls []string
}

var _ TfFacade = &workspaceFacade{}

func (f *workspaceFacade) Init(ctx context.Context, opts ...tfexec.InitOption) error   { return nil }
func (f *workspaceFacade) Apply(ctx context.Context, opts ...tfexec.ApplyOption) error { return nil }
func (f *workspaceFacade) Plan(ctx context.Context, opts ...tfexec.PlanOption) (bool, error) {
	return true, nil
}
func (f *workspaceFacade) Destroy(ctx context.Context, opts ...tfexec.DestroyOption) error {
	return nil
}
func (f *workspaceFacade) Output(ctx context.Context, opts ...tfexec.OutputOption) (map[string]tfexec.OutputMeta, error) {
	return map[string]tfexec.OutputMeta{}, nil
}
func (f *workspaceFacade) SetEnv(env map[string]string) error { return nil }
func (f *workspaceFacade) SetStdout(w io.Writer)              {}
func (f *workspaceFacade) SetStderr(w io.Writer)              {}

func (f *workspaceFacade) WorkspaceList(ctx context.Context, opts ...tfexec.WorkspaceListOption) ([]string, string, error) {
	return f.workspaceListFunc(ctx)
}

func (f *workspaceFacade) WorkspaceNew(ctx context.Context, workspace string, opts ...tfexec.WorkspaceNewCmdOption) error {
	f.workspaceNewCalls = append(f.workspaceNewCalls, workspace)
	return nil
}

func (f *workspaceFacade) WorkspaceSelect(ctx context.Context, workspace string, opts ...tfexec.WorkspaceSelectOption) error {
	f.workspaceSelectCalls = append(f.workspaceSelectCalls, workspace)
	return f.workspaceSelectFunc(ctx, workspace)
}

func (f *workspaceFacade) WorkspaceDelete(ctx context.Context, workspace string, opts ...tfexec.WorkspaceDeleteCmdOption) error {
	f.workspaceDeleteCalls = append(f.workspaceDeleteCalls, workspace)
	return nil
}

// makeBugInventoryTfCmd builds a *GenericTfCmd with just enough state to drive
// selectWorkspace/useWorkspaceIfNeeded/deleteWorkspaceIfNeeded/plainInit directly (no Worker, no
// execute()). AppConfig.WsTimeoutMins/InitTimeoutMins are global config read by those methods;
// saved and restored so this test cannot leak state into others (same pattern as the CP1
// encrypted-input helpers' meshcrypto.Crypto save/restore).
func makeBugInventoryTfCmd(t *testing.T, buildingBlockId, suggestedWorkspace string) *GenericTfCmd {
	t.Helper()

	previousWs, previousInit := AppConfig.WsTimeoutMins, AppConfig.InitTimeoutMins
	AppConfig.WsTimeoutMins = 1
	AppConfig.InitTimeoutMins = 1
	t.Cleanup(func() {
		AppConfig.WsTimeoutMins = previousWs
		AppConfig.InitTimeoutMins = previousInit
	})

	return &GenericTfCmd{
		ctx: context.Background(),
		params: &TfCmdParams{
			buildingBlockId:    buildingBlockId,
			suggestedWorkspace: suggestedWorkspace,
			useWorkspaces:      true,
		},
		runContextInfo: &RunContextInfo{
			logwrap: NewLogWrap(log.New(io.Discard, "[bug-inventory] ", log.LstdFlags), "/dev/null"),
		},
	}
}

// --- B1: selectWorkspace swallows the WorkspaceSelect error -------------------------------------

// Test_BugInventory_B1_WorkspaceSelectErrorSwallowed pins tfcmd.go:231-234 (selectWorkspace):
// when the workspace WorkspaceList *did* find a match but the subsequent WorkspaceSelect call on
// it errors, selectWorkspace swallows that error (`return "", nil`) instead of propagating it.
// The caller, useWorkspaceIfNeeded, then reads the empty workspace name as "no existing
// workspace" and creates a brand-new one, silently splitting the workspace tf actually has on
// disk from the one meshStack now believes is active.
func Test_BugInventory_B1_WorkspaceSelectErrorSwallowed(t *testing.T) {
	// FIXME(bug): B1 — correct behavior (2b) propagates the WorkspaceSelect error out of
	// selectWorkspace instead of returning ("", nil); useWorkspaceIfNeeded must then fail the
	// run rather than creating a new workspace.
	const realWorkspaceName = "org.proj.plat:bb-1"
	uut := makeBugInventoryTfCmd(t, "bb-1", realWorkspaceName)

	facade := &workspaceFacade{
		workspaceListFunc: func(ctx context.Context) ([]string, string, error) {
			return []string{realWorkspaceName}, "unrelated-workspace", nil
		},
		workspaceSelectFunc: func(ctx context.Context, workspace string) error {
			return errors.New("boom: workspace select failed")
		},
	}

	err := uut.useWorkspaceIfNeeded(facade)

	require.NoError(t, err, "bug: the swallowed WorkspaceSelect error never surfaces")
	assert.Equal(t, []string{realWorkspaceName}, facade.workspaceSelectCalls,
		"the existing workspace was found and a select was attempted (and failed)")
	assert.Equal(t, []string{realWorkspaceName}, facade.workspaceNewCalls,
		"bug: a NEW workspace is created even though a matching one already exists on disk")
}

// --- B2: selectWorkspace returns the wrong (bare buildingBlockId) name ---------------------------

// Test_BugInventory_B2_WorkspaceSelectReturnsWrongName pins tfcmd.go:236: on the "found in the
// available list" branch, selectWorkspace returns params.buildingBlockId instead of the actual
// matched workspace name `ws` — contrast the "already on the expected workspace" branch two lines
// above (tfcmd.go:222-225), which correctly returns the full `current` name.
func Test_BugInventory_B2_WorkspaceSelectReturnsWrongName(t *testing.T) {
	// FIXME(bug): B2 — correct behavior (2b) returns `ws` (the real matched workspace name), not
	// tfcmd.params.buildingBlockId.
	const realWorkspaceName = "org.proj.plat:bb-2"
	uut := makeBugInventoryTfCmd(t, "bb-2", realWorkspaceName)

	facade := &workspaceFacade{
		workspaceListFunc: func(ctx context.Context) ([]string, string, error) {
			return []string{realWorkspaceName}, "default", nil
		},
		workspaceSelectFunc: func(ctx context.Context, workspace string) error { return nil },
	}

	workspace, err := uut.selectWorkspace(facade)

	require.NoError(t, err)
	assert.Equal(t, "bb-2", workspace,
		"bug: returns the bare buildingBlockId, not the matched workspace name %q", realWorkspaceName)
	assert.NotEqual(t, realWorkspaceName, workspace)
}

// Test_BugInventory_B2_DeleteWorkspaceIfNeeded_DeletesWrongName shows B2's downstream consequence:
// deleteWorkspaceIfNeeded (tfcmd.go:244-270) deletes whatever selectWorkspace returned, so it
// inherits the wrong (bare buildingBlockId) name — a name that normally does not exist as an
// actual tf workspace — and a DESTROY run's real workspace is left behind, with only a log line
// about it (tfcmd.go:266-268).
func Test_BugInventory_B2_DeleteWorkspaceIfNeeded_DeletesWrongName(t *testing.T) {
	// FIXME(bug): B2 (continued) — once B2 is fixed, this must assert WorkspaceDelete is called
	// with realWorkspaceName, not "bb-2".
	const realWorkspaceName = "org.proj.plat:bb-2"
	uut := makeBugInventoryTfCmd(t, "bb-2", realWorkspaceName)

	facade := &workspaceFacade{
		workspaceListFunc: func(ctx context.Context) ([]string, string, error) {
			return []string{realWorkspaceName}, "default", nil
		},
		workspaceSelectFunc: func(ctx context.Context, workspace string) error { return nil },
	}

	uut.deleteWorkspaceIfNeeded(facade)

	require.NotEmpty(t, facade.workspaceDeleteCalls)
	assert.Equal(t, "bb-2", facade.workspaceDeleteCalls[len(facade.workspaceDeleteCalls)-1],
		"bug: deletes the bare buildingBlockId, not the real workspace %q — DESTROY leaves it behind on disk",
		realWorkspaceName)
}

// --- B3: deleteWorkspaceIfNeeded continues after a selectWorkspace error -------------------------

// Test_BugInventory_B3_DeleteWorkspaceIfNeeded_ContinuesAfterSelectError pins tfcmd.go:253-259:
// when selectWorkspace itself fails (e.g. the underlying `tofu workspace list` errors),
// deleteWorkspaceIfNeeded only logs "won't attempt deletion again" but does not return — it falls
// through and still attempts WorkspaceSelect("default") followed by WorkspaceDelete("") with the
// zero-value workspace name.
func Test_BugInventory_B3_DeleteWorkspaceIfNeeded_ContinuesAfterSelectError(t *testing.T) {
	// FIXME(bug): B3 — correct behavior (2b) returns immediately after the selectWorkspace error
	// instead of falling through to WorkspaceSelect("default")/WorkspaceDelete("").
	uut := makeBugInventoryTfCmd(t, "bb-3", "org.proj.plat:bb-3")

	facade := &workspaceFacade{
		workspaceListFunc: func(ctx context.Context) ([]string, string, error) {
			return nil, "", errors.New("boom: workspace list failed")
		},
		workspaceSelectFunc: func(ctx context.Context, workspace string) error { return nil },
	}

	uut.deleteWorkspaceIfNeeded(facade)

	assert.Contains(t, facade.workspaceSelectCalls, "default",
		"bug: still attempts WorkspaceSelect(\"default\") after the earlier WorkspaceList error")
	require.NotEmpty(t, facade.workspaceDeleteCalls)
	assert.Empty(t, facade.workspaceDeleteCalls[len(facade.workspaceDeleteCalls)-1],
		"bug: attempts WorkspaceDelete(\"\") — the zero-value workspace name")
}

// --- B4: plainInit's retry pause is nanoseconds, not the promised second -----------------------

// Test_BugInventory_B4_PlainInitRetrySleepIsNanosecondsNotSeconds pins tfcmd.go:171-185
// (plainInit): the comment says "Wait one second and retry", but `time.Sleep(1000)` sleeps 1000
// *nanoseconds* (time.Duration's base unit is ns), not time.Second. The retry-once behavior
// itself is correct and pinned alongside it; only the pause duration is wrong.
func Test_BugInventory_B4_PlainInitRetrySleepIsNanosecondsNotSeconds(t *testing.T) {
	// FIXME(bug): B4 — correct behavior (2b) sleeps time.Second (or uses an injected clock);
	// this elapsed-time assertion must then be updated (or replaced with a fake clock) instead of
	// silently starting to fail.
	uut := makeBugInventoryTfCmd(t, "bb-4", "org.proj.plat:bb-4")

	calls := 0
	facade := &MockedTfFacade{}
	facade.initMockFuncs()
	facade.initFunc = func(ctx context.Context, opts ...tfexec.InitOption) error {
		calls++
		if calls == 1 {
			return errors.New("transient init failure")
		}
		return nil
	}

	start := time.Now()
	err := uut.plainInit(context.Background(), facade, false)
	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.Equal(t, 2, calls, "init is retried exactly once on failure")
	assert.Less(t, elapsed, 500*time.Millisecond,
		"bug: the retry pause is ~1000ns, not the ~1s the log message promises — "+
			"a correct fix would make this elapsed time close to 1s, failing this assertion")
}

// --- B5: a sensitive input of an undecryptable type keeps its ciphertext ------------------------

// Test_BugInventory_B5_SensitiveNonDecryptableTypeKeepsCiphertext pins run.go:45-56
// (Variable.decryptIfSensitive): the decrypt switch only handles CODE/STRING/FILE. A sensitive
// input of any other type (INTEGER, BOOLEAN, SINGLE_SELECT, MULTI_SELECT, LIST) falls through the
// switch untouched, so the ciphertext itself — not the decrypted plaintext — silently becomes the
// variable's value that ends up in the generated tfvars/env.
func Test_BugInventory_B5_SensitiveNonDecryptableTypeKeepsCiphertext(t *testing.T) {
	// FIXME(bug): B5 — correct behavior (2b) either decrypts every sensitive value (extending the
	// switch) or fails fast for unsupported sensitive types; this test must then assert the
	// decrypted plaintext ("true") instead of the ciphertext.
	crypto := testCrypto(t)
	ciphertext := encryptForTest(t, crypto, "true")

	v := Variable{value: ciphertext, isSensitive: true, Type: DATA_TYPE_BOOLEAN}

	result, err := v.decryptIfSensitive(certDecryptor{crypto: crypto})

	require.NoError(t, err)
	assert.Equal(t, ciphertext, result,
		"bug: the raw ciphertext is passed through unchanged for a sensitive BOOLEAN input")
	assert.NotEqual(t, "true", result)
}

// --- B7: NewLogWrap returns a bare nil on file-open failure --------------------------------------

// Test_BugInventory_B7_NewLogWrapReturnsNilOnOpenError pins logwrapper.go:16-19: NewLogWrap
// returns nil (no error) when the log file cannot be opened. Its only production caller,
// initRunContextInfo (runcontextinfo.go:56), never checks for nil, so the first log write after a
// bad path (missing parent dir, permission error, …) nil-derefs and panics.
func Test_BugInventory_B7_NewLogWrapReturnsNilOnOpenError(t *testing.T) {
	// FIXME(bug): B7 — correct behavior (2b) returns (nil, error) so the caller can fail the run
	// cleanly instead of nil-dereffing on first write.
	lw := NewLogWrap(log.New(io.Discard, "", log.LstdFlags), "/nonexistent-dir/does-not-exist/x.log")

	assert.Nil(t, lw, "bug: nil is silently returned instead of an error on file-open failure")
}

// --- B12: Behavior.str()'s default branch calls log.Fatalf ---------------------------------------

// Test_BugInventory_B12_DetermineBehaviorUnknownStringNeverReachesFatalStringer pins behavior.go's
// Behavior.str(): its default branch calls log.Fatalf, which os.Exit(1)s the whole process for an
// unmapped Behavior value — not something a test can invoke in-process without killing the test
// binary. The reachable half of this pin is DetermineBehavior, the only production parser of an
// external (run JSON) string into a Behavior: it correctly returns UNKNOWN_BEHAVIOR + an error
// instead of ever constructing a Behavior value that would later hit the fatal branch.
func Test_BugInventory_B12_DetermineBehaviorUnknownStringNeverReachesFatalStringer(t *testing.T) {
	// FIXME(bug): B12 — correct behavior (2b) makes Behavior.str() return ("UNKNOWN", error) (or
	// similar) instead of log.Fatalf; DetermineBehavior's contract pinned below is expected to be
	// unaffected by that fix.
	behavior, err := DetermineBehavior("bogus")

	require.Error(t, err)
	assert.Equal(t, UNKNOWN_BEHAVIOR, behavior)
}

// --- B13: HINT_INIT_FAILED is printed for DETECT/DESTROY but not APPLY --------------------------

// makeBugInventoryRun builds a minimal, real (hermetic, local-filesystem) Run whose GitSource
// clones the given local repo — reusing the CP1 fixtures (makeLocalGitRepo) rather than
// reimplementing git plumbing — with an empty Vars map and no pre-run script, so execute() reaches
// (and fails at) tf.Init with nothing else able to fail first.
func makeBugInventoryRun(repo *localGitRepo, behavior Behavior) *Run {
	return &Run{
		Id:                  "bug-inv-b13-" + behavior.str(),
		BuildingBlockId:     "bb-13",
		TerraformVersion:    DEFAULT_TF_VER,
		Behavior:            behavior,
		WorkspaceIdentifier: p("_"),
		Vars:                map[string]*Variable{},
		Source: &GitSource{
			url:  repo.Path,
			auth: &NoAuth{},
			// gitFacade is the real adapter (not a mock): cloning a local filesystem path is
			// hermetic (no network), the same technique CP1 uses for the scenario suites.
			gitFacade: &Git{},
		},
	}
}

// runToInitFailure drives a single TfCmd (APPLY/DETECT/DESTROY) through initRunSteps()+execute()
// against a TfFacade whose Init always fails, using the same wiring worker.go/singlerunworker.go
// use in production (initRunContextInfo + GitSource.setLog + TfCmdParams), and returns the
// SystemMessage of the step execute() failed on.
func runToInitFailure(t *testing.T, behavior Behavior) string {
	t.Helper()

	previousInit := AppConfig.InitTimeoutMins
	AppConfig.InitTimeoutMins = 1
	t.Cleanup(func() { AppConfig.InitTimeoutMins = previousInit })

	repo := makeLocalGitRepo(t, map[string]string{"main.tf": "# no variable blocks\n"})
	run := makeBugInventoryRun(repo, behavior)

	wd := t.TempDir()
	// initRunContextInfo (runcontextinfo.go:56) opens "<wd>/logs/logs-<runId>.txt" via
	// NewLogWrap; worker.go/singlerunworker.go always os.Mkdir the "logs" subdir first
	// (worker.go:103) — mirrored here so this harness matches production wiring.
	require.NoError(t, os.Mkdir(path.Join(wd, "logs"), 0700))
	runContextInfo := initRunContextInfo(run, "[bug-inventory] ", io.Discard, wd)
	run.Source.setLog(runContextInfo.logwrap)
	ctx := context.Background()

	params := &TfCmdParams{
		dir:                wd,
		buildingBlockId:    run.BuildingBlockId,
		tfVersion:          run.TerraformVersion,
		useWorkspaces:      false, // init fails before workspace logic is ever reached
		suggestedWorkspace: run.toWorkspaceStr(),
		vars:               run.Vars,
		source:             run.Source,
		runMode:            run.Behavior.str(),
	}

	mock := &MockedTfFacade{}
	mock.initMockFuncs()
	mock.initFunc = func(ctx context.Context, opts ...tfexec.InitOption) error {
		return errors.New("boom: tf init failed")
	}
	tfbin, err := ForTestNewTfBin(t.TempDir(), io.Discard, mock)
	require.NoError(t, err)

	var tfCmd TfCmd
	switch behavior {
	case APPLY:
		tfCmd = ApplyCmd(ctx, runContextInfo, params, tfbin, nil)
	case DETECT:
		tfCmd = PlanCmd(ctx, runContextInfo, params, tfbin)
	case DESTROY:
		tfCmd = DestroyCmd(ctx, runContextInfo, params, tfbin)
	default:
		t.Fatalf("unsupported behavior %v", behavior)
	}

	tfCmd.initRunSteps()
	tfCmd.execute()

	currentStep := runContextInfo.runStatus.currentStepStatus()
	require.NotNil(t, currentStep)
	require.NotNil(t, currentStep.SystemMessage, "execute() must have failed and captured a SystemMessage")
	return *currentStep.SystemMessage
}

// Test_BugInventory_B13_HintInitFailedAsymmetry pins the asymmetry between tfplan.go:135-138 /
// tfdestroy.go:140-143 (both print HINT_INIT_FAILED to the logs right before failing on an init
// error) and tfapply.go:149-152 (which fails on the very same init error without ever printing
// the hint) — an APPLY user gets no "Check provider and / or backend config" guidance that a
// DETECT/DESTROY user would get for an identical failure.
func Test_BugInventory_B13_HintInitFailedAsymmetry(t *testing.T) {
	// FIXME(bug): B13 — correct behavior (2b) is consistent: either all three behaviors print
	// HINT_INIT_FAILED on init failure, or none do. This test must be updated to assert that
	// consistent behavior once fixed.
	t.Run("DETECT prints the hint", func(t *testing.T) {
		msg := runToInitFailure(t, DETECT)
		assert.Contains(t, msg, HINT_INIT_FAILED)
	})

	t.Run("DESTROY prints the hint", func(t *testing.T) {
		msg := runToInitFailure(t, DESTROY)
		assert.Contains(t, msg, HINT_INIT_FAILED)
	})

	t.Run("bug: APPLY does not print the hint", func(t *testing.T) {
		msg := runToInitFailure(t, APPLY)
		assert.NotContains(t, msg, HINT_INIT_FAILED)
	})
}
