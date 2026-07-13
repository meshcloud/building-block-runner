package tf

// bug_inventory_test.go pins the D13 bug inventory
// (PLAN_DETAIL_01_tf_characterization_tests.md §6). Phase 2b (PLAN_DETAIL_02 §7) works through
// this inventory: each entry below now asserts *correct* behavior together with the matching
// production fix, and the `FIXME(bug)` markers are gone. This file is deliberately
// self-contained (its own TfFacade test double, its own tfcmd/execute harness) so it stays
// disjoint from the other checkpoint test files.
//
// Not pinned here, by design:
//   - B6 (manager.go shutdownCalled) and B10 (runcontextinfo.go reportStatus shallow copy) are
//     genuine data races. D13 exempts them from "pin verbatim, fix in 2b": they were fixed
//     structurally in phase 2 (atomic.Bool / deep-copy) and are guarded by `-race` (on in CI/
//     Taskfile since phase 2), not by a functional pin here.
//   - B8 (tfbinaries.go installTofuBinaries uses context.Background()) lives in a file excluded
//     from the coverage gate (§7) and needs a live network download to reach; inventory-only.
//   - B9 (gitsource.go nil-deref logging `*g.path` when it is nil) is covered by a unit test in
//     gitsource_test.go (R10), not here.
//   - B11 (main.go single-run failure only logged, process exits 0) lives in `package main`,
//     outside the `tfrun` gate; fixed in tf-block-runner/main.go (R12).

import (
	"context"
	"errors"
	"io"
	"log/slog"
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
// execute()). The ws/init timeouts those methods read are now threaded through TfCmdParams
// (FOLLOW_UP P2.3, formerly AppConfig.WsTimeoutMins/InitTimeoutMins); set positive so the derived
// context.WithTimeout is not already-expired.
func makeBugInventoryTfCmd(t *testing.T, buildingBlockId, suggestedWorkspace string) *GenericTfCmd {
	t.Helper()

	lw, err := NewLogWrap(slog.New(slog.NewTextHandler(io.Discard, nil)), "/dev/null")
	require.NoError(t, err)

	return &GenericTfCmd{
		ctx: context.Background(),
		params: &TfCmdParams{
			buildingBlockId:    buildingBlockId,
			suggestedWorkspace: suggestedWorkspace,
			useWorkspaces:      true,
			wsTimeoutMins:      1,
			initTimeoutMins:    1,
		},
		runContextInfo: &RunContextInfo{
			logwrap: lw,
		},
	}
}

// --- B1 (fixed): selectWorkspace propagates the WorkspaceSelect error ---------------------------

// Test_BugInventory_B1_WorkspaceSelectErrorPropagated pins the fixed tfcmd.go:selectWorkspace:
// when WorkspaceList *did* find a match but the subsequent WorkspaceSelect call on it errors,
// selectWorkspace now propagates that error instead of swallowing it into ("", nil). The caller,
// useWorkspaceIfNeeded, then fails the run instead of silently creating a brand-new workspace and
// splitting the workspace tf actually has on disk from the one meshStack now believes is active.
func Test_BugInventory_B1_WorkspaceSelectErrorPropagated(t *testing.T) {
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

	require.Error(t, err, "the WorkspaceSelect error now surfaces to the caller")
	assert.Equal(t, []string{realWorkspaceName}, facade.workspaceSelectCalls,
		"the existing workspace was found and a select was attempted (and failed)")
	assert.Empty(t, facade.workspaceNewCalls,
		"fixed: no NEW workspace is created when a matching one already exists on disk but failed to select")
}

// --- B2 (fixed): selectWorkspace returns the real matched workspace name -------------------------

// Test_BugInventory_B2_WorkspaceSelectReturnsMatchedName pins the fixed tfcmd.go:selectWorkspace:
// on the "found in the available list" branch, selectWorkspace now returns the actual matched
// workspace name `ws`, not the bare `params.buildingBlockId` — consistent with the "already on
// the expected workspace" branch above it, which always returned the full `current` name.
func Test_BugInventory_B2_WorkspaceSelectReturnsMatchedName(t *testing.T) {
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
	assert.Equal(t, realWorkspaceName, workspace,
		"fixed: returns the real matched workspace name, not the bare buildingBlockId")
}

// Test_BugInventory_B2_DeleteWorkspaceIfNeeded_DeletesMatchedName shows the fix's downstream
// effect: deleteWorkspaceIfNeeded deletes whatever selectWorkspace returned, so it now deletes the
// real workspace name instead of a bare buildingBlockId that never existed as an actual tf
// workspace — a DESTROY run's workspace is actually removed, not left behind on disk.
func Test_BugInventory_B2_DeleteWorkspaceIfNeeded_DeletesMatchedName(t *testing.T) {
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
	assert.Equal(t, realWorkspaceName, facade.workspaceDeleteCalls[len(facade.workspaceDeleteCalls)-1],
		"fixed: deletes the real workspace %q, not the bare buildingBlockId", realWorkspaceName)
}

// --- B3 (fixed): deleteWorkspaceIfNeeded returns after a selectWorkspace error --------------------

// Test_BugInventory_B3_DeleteWorkspaceIfNeeded_ReturnsAfterSelectError pins the fixed
// tfcmd.go:deleteWorkspaceIfNeeded: when selectWorkspace itself fails (e.g. the underlying `tofu
// workspace list` errors), deleteWorkspaceIfNeeded now logs "won't attempt deletion again" and
// returns immediately, instead of falling through to WorkspaceSelect("default")/
// WorkspaceDelete("") with the zero-value workspace name.
func Test_BugInventory_B3_DeleteWorkspaceIfNeeded_ReturnsAfterSelectError(t *testing.T) {
	uut := makeBugInventoryTfCmd(t, "bb-3", "org.proj.plat:bb-3")

	facade := &workspaceFacade{
		workspaceListFunc: func(ctx context.Context) ([]string, string, error) {
			return nil, "", errors.New("boom: workspace list failed")
		},
		workspaceSelectFunc: func(ctx context.Context, workspace string) error { return nil },
	}

	uut.deleteWorkspaceIfNeeded(facade)

	assert.NotContains(t, facade.workspaceSelectCalls, "default",
		"fixed: does not attempt WorkspaceSelect(\"default\") after the earlier WorkspaceList error")
	assert.Empty(t, facade.workspaceDeleteCalls,
		"fixed: never attempts WorkspaceDelete after the earlier WorkspaceList error")
}

// --- B4 (fixed): plainInit's retry pause is a full second, as the log message promises ----------

// Test_BugInventory_B4_PlainInitRetrySleepIsOneSecond pins the fixed tfcmd.go:plainInit: the log
// message says "Wait one second and retry", and the retry pause is now `time.Second`, not the
// `1000` (nanoseconds) it used to be. No Clock port exists yet in this codebase (that lands with
// the phase-2 engine/ports step), so this asserts a real ~1s sleep rather than a fake clock.
func Test_BugInventory_B4_PlainInitRetrySleepIsOneSecond(t *testing.T) {
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
	assert.GreaterOrEqual(t, elapsed, time.Second,
		"fixed: the retry pause is a full second, matching the log message's promise")
}

// --- B5 (fixed): every sensitive input type is decrypted, not just CODE/STRING/FILE -------------

// Test_BugInventory_B5_SensitiveNonCodeLikeTypeIsDecrypted pins the fixed
// Variable.decryptIfSensitive (run.go): it no longer type-switches on DataType before deciding
// whether to decrypt — every sensitive value is decrypted regardless of type. A sensitive
// BOOLEAN input (previously falling through the switch untouched) now yields the decrypted
// plaintext, not the ciphertext.
func Test_BugInventory_B5_SensitiveNonCodeLikeTypeIsDecrypted(t *testing.T) {
	crypto := testCrypto(t)
	ciphertext := encryptForTest(t, crypto, "true")

	v := Variable{value: ciphertext, isSensitive: true, Type: DATA_TYPE_BOOLEAN}

	result, err := v.decryptIfSensitive(certDecryptor{crypto: crypto})

	require.NoError(t, err)
	assert.Equal(t, "true", result,
		"fixed: a sensitive BOOLEAN input is now decrypted like any other sensitive type")
	assert.NotEqual(t, ciphertext, result)
}

// --- B7 (fixed): NewLogWrap returns (nil, error) on file-open failure ----------------------------

// Test_BugInventory_B7_NewLogWrapReturnsErrorOnOpenError pins the fixed logwrapper.go: NewLogWrap
// now returns a non-nil error when the log file cannot be opened, so its caller,
// initRunContextInfo, can fail the run cleanly instead of nil-dereffing on the first log write.
func Test_BugInventory_B7_NewLogWrapReturnsErrorOnOpenError(t *testing.T) {
	lw, err := NewLogWrap(slog.New(slog.NewTextHandler(io.Discard, nil)), "/nonexistent-dir/does-not-exist/x.log")

	assert.Nil(t, lw)
	require.Error(t, err, "fixed: a file-open failure now returns an error instead of a silent nil")
}

// Test_BugInventory_B7_InitRunContextInfoPropagatesLogWrapError pins the fixed
// initRunContextInfo (runcontextinfo.go): it now propagates NewLogWrap's error instead of
// constructing a RunContextInfo with a nil logwrap that panics on first write.
func Test_BugInventory_B7_InitRunContextInfoPropagatesLogWrapError(t *testing.T) {
	run := &Run{
		Id:                  "bug-inv-b7",
		BuildingBlockId:     "bb-7",
		WorkspaceIdentifier: p("_"),
	}

	// wd's "logs" subdir is deliberately not created, so the outFile path
	// ("<wd>/logs/logs-<runId>.txt") cannot be opened.
	wd := t.TempDir()
	rci, err := initRunContextInfo(run, slog.New(slog.NewTextHandler(io.Discard, nil)), wd)

	assert.Nil(t, rci)
	require.Error(t, err, "fixed: the log-file-open error now surfaces instead of a nil-logwrap RunContextInfo")
}

// --- B12 (fixed): Behavior.str()'s default branch no longer calls log.Fatalf --------------------

// Test_BugInventory_B12_UnmappedBehaviorStringReturnsUnknown pins the fixed behavior.go:
// Behavior.str() on an unmapped value (e.g. UNKNOWN_BEHAVIOR) now returns "UNKNOWN" instead of
// calling log.Fatalf (which used to os.Exit(1) the whole process) — directly testable in-process,
// unlike the former fatal branch.
func Test_BugInventory_B12_UnmappedBehaviorStringReturnsUnknown(t *testing.T) {
	assert.Equal(t, "UNKNOWN", UNKNOWN_BEHAVIOR.str())
	assert.Equal(t, "UNKNOWN", Behavior(99).str(), "any value outside the declared range also returns UNKNOWN, not a crash")
}

// Test_BugInventory_B12_DetermineBehaviorUnknownStringNeverReachesFatalStringer keeps the
// DetermineBehavior contract pinned unchanged by the B12 fix: it is still the parser callers use
// to reject an unrecognized run-JSON behavior string, returning UNKNOWN_BEHAVIOR + an error.
func Test_BugInventory_B12_DetermineBehaviorUnknownStringNeverReachesFatalStringer(t *testing.T) {
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

	repo := makeLocalGitRepo(t, map[string]string{"main.tf": "# no variable blocks\n"})
	run := makeBugInventoryRun(repo, behavior)

	wd := t.TempDir()
	// initRunContextInfo (runcontextinfo.go:56) opens "<wd>/logs/logs-<runId>.txt" via
	// NewLogWrap; worker.go/singlerunworker.go always os.Mkdir the "logs" subdir first
	// (worker.go:103) — mirrored here so this harness matches production wiring.
	require.NoError(t, os.Mkdir(path.Join(wd, "logs"), 0700))
	runContextInfo, err := initRunContextInfo(run, slog.New(slog.NewTextHandler(io.Discard, nil)), wd)
	require.NoError(t, err)
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
		initTimeoutMins:    1,
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

// Test_BugInventory_B13_HintInitFailedConsistentAcrossBehaviors pins the fixed tfapply.go: APPLY
// now prints HINT_INIT_FAILED on an init failure, exactly like DETECT (tfplan.go) and DESTROY
// (tfdestroy.go) already did — an APPLY user now gets the same "Check provider and / or backend
// config" guidance for an init failure that a DETECT/DESTROY user gets for an identical failure.
func Test_BugInventory_B13_HintInitFailedConsistentAcrossBehaviors(t *testing.T) {
	t.Run("DETECT prints the hint", func(t *testing.T) {
		msg := runToInitFailure(t, DETECT)
		assert.Contains(t, msg, HINT_INIT_FAILED)
	})

	t.Run("DESTROY prints the hint", func(t *testing.T) {
		msg := runToInitFailure(t, DESTROY)
		assert.Contains(t, msg, HINT_INIT_FAILED)
	})

	t.Run("fixed: APPLY now also prints the hint", func(t *testing.T) {
		msg := runToInitFailure(t, APPLY)
		assert.Contains(t, msg, HINT_INIT_FAILED)
	})
}
