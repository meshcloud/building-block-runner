# Detail Plan 01 — tf Characterization Tests to ≥90%

**Phase:** 1 · **Branch:** `refactor/single-go-binary/phase-1-characterization-tests` (stacked on
`refactor/single-go-binary/phase-0-guardrails`) · **Delivery:** one single-commit PR (checkpoints
below are internal always-green working increments, squashed on merge) · **Binding:** §3 P1–P8,
D6 (gate), D9 (pins), D13 (bug policy) of `PLAN_HIGH_LEVEL.md`.

Phase character: **tests only.** No production `.go` file outside `*_test.go` is modified, with
exactly one sanctioned exception class defined in "Scope" (test fixtures / exclusion-list config
promised by phase 0). Every bug found is pinned verbatim (`// FIXME(bug):`), never fixed (D13).

---

## 1. Assumptions from prior phases

Implementation **starts by running all verification steps**. Any materially failed assumption is a
**STOP** (see markers): update this plan (and cascading plans) first, get the revision reviewed,
then resume.

| # | Assumption | Promised by | Verification step |
|---|---|---|---|
| A1 | `task test` / `task lint` exist and pass on the phase-0 branch (Makefile→Taskfile done, golangci-lint v2 configured). | Plan 00 (D14) | `git checkout refactor/single-go-binary/phase-0-guardrails && task test && task lint` |
| A2 | A coverage-threshold mechanism exists (script or task target) that (a) computes **Go statement coverage** for the `tfrun` package, (b) subtracts files named in a visible exclusion list (one justification line per file), (c) can be switched from report-only to gating. | Plan 00 (D6) | Read the phase-0 plan's gate section; run the coverage task, e.g. `task coverage` (exact name per plan 00); confirm it prints a per-package number and honors an exclusion file. |
| A3 | The exclusion mechanism is **per-file** granularity (filters `coverprofile` lines by filename). This plan's arithmetic (§8) assumes whole files are excluded, not functions. | Plan 00 (D6) | Inspect the threshold script; feed it a dummy exclusion entry and confirm the denominator changes. |
| A4 | Baseline numbers documented by phase 0 match this plan's measured baseline: `tfrun` = **59.4%** statements (916/1541), module total 56.6% (measured 2026-07-08 on `main`, `go test -coverprofile` + `go tool cover -func`). Small drift from intervening `main` commits is acceptable; >2pp drift is not. | Plan 00 exit criteria | `cd tf-block-runner && go test -coverprofile=/tmp/c.out ./... && go tool cover -func /tmp/c.out \| tail -1` |
| A5 | CI does **not** run `go test -race` on `tfrun`, and phase 0 does not enable it. Two genuine data races are pinned-not-fixed in this phase (bug inventory B6, B10); `-race` would fail the suite until phase 2b. | Plan 00 (CI wiring) | grep the phase-0 plan and `.github/workflows/ci.yml` for `-race`. |
| A6 | Tests may use the network today (existing suites clone GitHub and download terraform/tofu binaries — see §3 finding F1); phase 0 did not forbid network in tests. This plan *removes* most network use but keeps `tfbinaries_test.go` as-is (its file is excluded from the gate). | Plan 00 | Confirm phase-0 plan has no "hermetic tests" CI rule that would already be violated by `main`. |
| A7 | The meshfed-release acceptance suite / local-dev-stack was verified green by phase 0 (outer safety net exists before we start pinning). | Plan 00 exit criteria | Read phase-0 plan's verification record; if absent, run the local-dev-stack acceptance flow once. |

**STOP-A (before any coding):** if A2 or A3 fail — the gate counts something other than per-file
statement coverage — the target arithmetic in §8 is invalid. Replan §8 with the real mechanism.

**STOP-B:** if A5 fails (CI already runs `-race`), the D13 "pin, don't fix" policy collides with a
red CI for bugs B6/B10. Do not silently fix the races; escalate — either phase 0 descopes `-race`
until 2b, or D13 gets a reviewed exception for data races.

---

## 2. Scope

**In scope**

- New and extended tests for `tf-block-runner/tfrun` (and, where a D9 pin physically lives in
  `go-meshapi-client/meshapi`, a pin test there too — outside the gate, still part of the PR).
- Making the existing scenario suites hermetic w.r.t. git (test-code-only change, see CP1): the
  suites currently clone `https://github.com/meshcloud/meshstack-hub.git` over the network
  (`worker_scenario_test.go:224` et al.).
- Test fixtures: local git repositories built at test runtime, run-JSON builders, genuinely
  encrypted inputs using the repo's test key material
  (`tf-block-runner/resources/test.pem` + `test.key`; the crypto round-trip is proven by
  `go-meshapi-client/crypto/meshcertbasedcrypto_test.go:10-38` and `EncryptMeshCertBased` exists
  at `meshcertbasedcrypto.go:89`).
- The adapter exclusion list file (mechanism from phase 0) with justifications (§7).
- The bug inventory (§6) with `// FIXME(bug):` markers in the pinning tests.

**Out of scope (deferred, with destination)**

- Any production-code change, including trivially safe ones (1000ns sleep B4, typo "perpare") → phase 2b.
- Real-tofu / real-git e2e task (opt-in, per D6) → phase 0/7 CI concern, not this gate.
- Coverage for `tf-block-runner/main.go` and `util/` — the D6 gate starts on `tfrun` only.
  The k8s single-run *wiring* in `main.go:112-159` is pinned only partially (see §5 pin 16 and
  Open-questions resolution Q3).
- run-controller / go-meshapi-client coverage (phase 3).

---

## 3. Research findings the plan rests on (evidence)

- **F1 — the "hermetic" scenario style is not hermetic.** `worker_scenario_test.go` drives the
  full use case through `Worker.work()` with a fake HTTP `RoundTripper`
  (`testRoundTripper`, `worker_scenario_test.go:672-676`) and `MockedTfFacade` via
  `ForTestNewTfBin` (`tfbinaries.go:56`), **but** the run built by `runDTOToInternal` hardwires
  the real git facade — `gitsource: … gitFacade: &Git{}` (`dtos.go:198`) — so every scenario test
  clones `github.com/meshcloud/meshstack-hub.git` over the network (48s suite runtime).
  `tfbinaries_test.go:15-62` downloads real terraform/tofu binaries.
  *Consequence:* CP1 replaces remote URLs with **local on-disk git repositories** (go-git
  `PlainClone` accepts filesystem paths), which keeps the tests black-box (the URL is just data in
  the run JSON) and makes them hermetic and fast. This contradicts the high-level plan's
  description of the existing suites as "hermetic" (§2, D6) — flagged, not silently deviated from.
  CP1 uses bare git repos in `testdata` with per-testcase branches (pattern copyable from
  `terraform-provider-meshstack`). Phase 1 keeps the fake `http.RoundTripper` to pin behavior
  with minimal diff; replacing it with a reusable `net/http/httptest` meshfed-API **server**
  mock package shared across runner types is structural churn deferred to phase 2/3 (plan 03,
  shared core). Phase-1 fixtures are authored as replayable request/response transcripts so
  they port onto that server mock without rewriting the scenarios.
- **F2 — D9's "same-origin URL" artifact pin is stale.** Commit `88d67d4` ("fix: revert the
  artifact url same origin check", 2026-07-02) deleted the check from
  `go-meshapi-client/meshapi/client.go` and its tests. Only the 128MiB cap remains
  (`client.go:20`, `client.go:153-159`). **This plan pins the cap, not same-origin.**
  D9 must be corrected in review; pinning a deleted feature is impossible.
- **F3 — the mocks are production files.** `mockedtffacade.go` / `mockedgitfacade.go` are non-test
  sources counted by the coverage denominator, and `tfbinaries.go` references the mock from
  production code (`TfBinaries.tfMock`, `tfbinaries.go:35`). They stay in the denominator (they
  are 95%/85% covered anyway); untangling is a phase-2 job.
- **F4 — `runapi_test.go` contains tests for a non-existent feature.** `Test_UseCustomPredicate_*`
  (`runapi_test.go:533-742`) reference a "custom predicate" that exists nowhere in production code
  (grep over both modules finds it only in the test file); they abuse `RunnerUuid` and effectively
  re-test the V1 media type. Kept as-is (they pin real header behavior); renaming is phase-2
  cleanup. Recorded here so phase 2 doesn't mistake them for a contract.
- **F5 — gate arithmetic** is in §8, from `go tool cover -func` measured on this tree.

### Gap map (statement coverage per file, `tfrun` package, baseline)

| File | cov/total | % | Uncovered functions (from `go tool cover -func`) |
|---|---|---|---|
| `singlerunworker.go` | 0/78 | 0% | everything: `NewSingleRunWorker(WithApi)`, `ExecuteRun`, `workRoutine`, `observerRoutine`, `sendInitFail` |
| `authSsh.go` | 0/115 | 0% | everything: `unwrapCert`, `prepare`, `toTransport`, all knownhosts callbacks |
| `manager.go` | 0/31 | 0% | everything: `NewManager`, `Start`, `run`, `handleWorkers`, `handoutWorkerToken`, `Stop` |
| `git.go` | 23/95 | 24% | `checkoutRef`, `authClone`, `azureClone`, `azureCheckoutRef` 0% |
| `config.go` | 44/80 | 55% | `ReadConfig` 0%; branches of `NewAuthProvider` (60%), `applyPrivateKeyFile`, `validateAuthConfig` |
| `tfdestroy.go` | 33/58 | 57% | `execute` 53.8% (async-apply hack, failure branches, workspace delete) |
| `authNone.go` | 4/7 | 57% | `toTransport` 0% |
| `tfplan.go` | 31/53 | 58.5% | `execute` 55.3% (failure branches, init-hint, pre-run path) |
| `dtos.go` | 36/59 | 61% | `ToInternalWithoutDecryption` 0%; error branches of `runDTOToInternal` |
| `gitsource.go` | 37/60 | 62% | `logDirectoryContentsForWorktreeUnstagedChangedError` 0%; azure/ref/missing-path branches of `CopyToTargetDir` (76.1%) |
| `run.go` | 13/19 | 68% | `decryptIfSensitive` 33.3% (no test decrypts anything genuine) |
| `tfapply.go` | 50/72 | 69% | `execute` 65.4%, `applyPredecessorPlan` 78.6% (stale-plan failure), `initRunSteps` async branch |
| `behavior.go` | 7/10 | 70% | error branches of `str`/`DetermineBehavior` |
| `tfcmd.go` | 254/340 | 75% | `detectBackend` 0%, `init` 35.7%, `plainInit` 42.9% (retry), `selectWorkspace` 47.1%, `encodeVarValueForEnv` 33.3%, `buildTfEnv` 66.7%, `failWithUserMsg` 69.6% (timeout/cancel msgs), `useWorkspaceIfNeeded` 72.7%, `deleteWorkspaceIfNeeded` 76.9%, `saveInputFiles` 70% (decrypt-fail) |
| `worker.go` | 79/105 | 75% | `handleFetchRunError` 0%, `sendInitFail` 0%, registration-failure branch of `workRoutine` |
| `tfbinaries.go` | 46/59 | 78% | real download/install branches (network) |
| `runstatus.go` | 14/17 | 82% | error branches |
| `mockedgitfacade.go` | 22/26 | 85% | `setLog` |
| `backendsearch.go` | 12/14 | 86% | diagnostics branch |
| `executionstatus.go` | 6/7 | 86% | `str` PENDING/panic branch |
| `logwrapper.go` | 13/15 | 87% | open-error branch of `NewLogWrap`, write-error branch |
| `scriptcmd.go` | 65/75 | 87% | error branches (`writeScriptFile`, `decodeRunJSON`, `readUserMsgFile`, `extractExitCode -1`) |
| `runapi.go` | 34/38 | 89.5% | empty-auth branch, fetch/update error branches |
| `tfcmd_vars.go` | 36/40 | 90% | diag branches |
| `mockedtffacade.go` | 19/20 | 95% | — |
| `tfconfig_parse.go` | 33/43 | 77% | error/diagnostic branches |
| `runcontextinfo.go` | 5/5 | 100% | — |
| **tfrun total** | **916/1541** | **59.4%** | |

(`main.go` 0/71 and `util/` 0/7 are outside the gate; `util.SortedByKeys` is in fact exercised by
`tfrun` tests but not counted because coverage is per-package — do not "fix" this with
`-coverpkg` without updating §8.)

---

## 4. Use-case matrix

Rows = behaviors × mode; columns = variants. ✅ existing test (named in §5), ▢ new test (checkpoint
in parentheses). All new tests are black-box through `Worker.work()` (polling) or
`SingleRunWorker.ExecuteRun()` (single-run), fake HTTP transport + `MockedTfFacade` + local git
fixture repos — the survival contract for phase 2.

| Use case | polling | single-run |
|---|---|---|
| APPLY plain, sync, success | ✅ `Test_ApplySucceeded` | ▢ (CP3) |
| APPLY plain, sync, tf failure | ✅ `Test_ApplyTfFailure` | ▢ (CP3) |
| APPLY saved-plan artifact: download+`DirOrPlan` | ✅ `Test_ApplyWithPlanArtifact_DownloadsAndAppliesSavedPlan` | ▢ (CP3, one representative) |
| APPLY no artifact = plain apply (regression) | ✅ `Test_ApplyWithoutPlanArtifact_PlainApply` | — |
| APPLY artifact download 404 → FAILED, no apply | ✅ `Test_ApplyWithPlanArtifact_DownloadFailureFailsRun` | — |
| APPLY artifact: stale-plan apply error → actionable msg | ▢ (CP4) | — |
| APPLY artifact: oversized (>128MiB) rejected | ▢ (CP4; pin lives in `meshapi/client_test.go`, streamed reader, `io.Discard`-style sink) | — |
| DETECT sync success + artifact in final update | ✅ `Test_DetectSucceeded_ArtifactInStatusUpdate` | ▢ (CP3) |
| DETECT plan file missing → FAILED | ✅ `Test_DetectFailed_WhenPlanFileNotWritten` | — |
| DESTROY sync success (incl. workspace delete calls) | ✅ `Test_DestroySucceeded` (extend: assert `WorkspaceDelete` interplay, CP5) | ▢ (CP3) |
| DESTROY tf failure | ✅ `Test_DestroyTfFailure` | — |
| async APPLY / DETECT / DESTROY: single `trigger` step, final `IN_PROGRESS` on success, destroy-apply hack | ▢ (CP7) | ▢ (CP7, one) |
| async failure still reports FAILED | ▢ (CP7) | — |
| abort flag → context cancelled | ✅ `Test_ApplyRunAborted` | ▢ (CP3) |
| live log ticker updates | ✅ `Test_UpdatesStatusWithLiveLogs` | — |
| registration 409 → continue, never PENDING | ✅ `Test_RegistrationConflict_ContinuesExecution` | — |
| registration hard failure (500) → FAILED final, tf never runs | ▢ (CP2) | ▢ (CP3) |
| claim 404 / 409 → `norun`; other status → `failed`; double-chunked transport error → `norun` | ▢ (CP2) | n/a |
| init-fail (workdir creation) → `sendInitFail` FAILED update | ▢ (CP2/CP3) | ▢ (CP3) |
| git source: clone failure → FAILED with "copy sources from" | ✅ `Test_MissingAuth` (becomes local-path-based, CP1) | — |
| git source: refName branch/tag/commit checkout; missing repo path | ▢ (CP11) | — |
| pre-run script variants (user msg, failure, stdin JSON, PATH) | ✅ unit `tfcmd_prerunscript_test.go` (14 tests) + ▢ scenario-level (CP8) | — |
| timeout (DeadlineExceeded) user message | ▢ (CP8) | — |
| FILE inputs (data-URL, overwrite, env-error, non-file skip) | ✅ unit `Test_saveInputFiles_*` + ▢ encrypted-FILE scenario (CP4) | — |
| encrypted STRING input decrypted into tfvars (genuine crypto) | ▢ (CP4) | — |
| decrypt failure → FAILED + key-mismatch guidance | ▢ (CP4) | — |
| sensitive non-decryptable type keeps ciphertext (bug pin B5) | ▢ (CP4) | — |
| env-var inputs incl. `TF_VAR_` precedence, MULTISELECT JSON encoding | ✅ unit `Test_vars_*` + ▢ `encodeVarValueForEnv` MULTISELECT/err (CP4) | — |
| tfvars + `meshStack_run_vars.tf` rules incl. run-scoped omission | ✅ `Test_vars_OmitsRunScopedVarValuesOnDetectAndSavedPlanReplay`, `Test_setEnvWith_*`, `Test_vars_SkipsDeclaring…` | — |
| backend fallback: no backend → http backend file + `TF_HTTP_*` env + workspaces off | ✅ unit `Test_createMeshStackHttpBackendFile_*`, `Test_buildTfEnv_With…` + ▢ use-case (CP6) | — |
| backend fallback: existing backend detected → no file, workspaces on | ▢ (CP6, `detectBackend` is 0%) | — |
| backend fallback: empty runToken → fail fast | ✅ unit `Test_createMeshStackHttpBackendFile_MissingRunToken_ReturnsError` | — |
| init retry-once behavior | ▢ (CP6, pins B4 context) | — |
| workspace select/create/delete naming logic (+ bug pins B1–B3) | ▢ (CP5) | — |
| runToken precedence + `ClearRunToken` cycle | ✅ `Test_ClearRunToken_ResetsToBasicAuth`, `Test_ClearRunToken_MultipleRunCycle` | ▢ runToken-only, no base auth (CP3) |
| media types + `X-Block-Runner-Node-Id` (+ worker-N suffix) | ✅ `Test_FetchRun`/`Test_RegisterSource`/`Test_UpdateState` (`runapi_test.go:256-346`) + ▢ `User-Agent`/`X-Meshcloud-Runner-*` (CP2) | — |
| config precedence, validation, private-key file (`ReadConfig` 0%) | ▢ (CP9) | ▢ single-run validation branch (CP9) |
| SSH auth prepare/known-hosts resolution (hermetic) | ▢ (CP10) | — |
| manager token protocol / graceful stop | ▢ (CP12) | n/a |

---

## 5. D9 pin map (every pin → existing or specified test)

1. **Async → final `IN_PROGRESS`** (`worker.go:206-211`, `singlerunworker.go:160-165`): **missing** → CP7.
2. **Abort flag cancels run context** (`worker.go:239-245`): ✅ `Test_ApplyRunAborted`; single-run twin → CP3.
3. **10s status ticker**: hardwired at `manager.go:75`, `singlerunworker.go:32,45`. **Missing** →
   CP3 asserts `NewSingleRunWorker(…).statusUpdateInterval == 10*time.Second`; the manager-side
   value is pinned by a constructor-level assertion in CP12 (black-box timing tests at 10s are
   deliberately not written — too slow; the *ticker behavior* is already pinned at 500ms by
   `Test_UpdatesStatusWithLiveLogs`).
4. **runToken > base-auth precedence, `ClearRunToken` after execution** (`runapi.go:21-31`,
   `worker.go:56`): ✅ `Test_ClearRunToken_ResetsToBasicAuth`, `Test_ClearRunToken_MultipleRunCycle`.
5. **409-on-register = success** (`meshapi/client.go:187-189`): ✅
   `Test_RegistrationConflict_ContinuesExecution`, `TestRegister_409Conflict_ReturnsNil`.
6. **404/409-on-claim = no run** (`worker.go:66-91`): **missing** (0% covered) → CP2.
7. **Media types + runner headers** (`meshapi/client.go:235-243`): ✅ partially
   (`runapi_test.go:256-266,334-346`); CP2 adds `User-Agent`/`X-Meshcloud-Runner-Name/Version`
   and the `<uuid>-worker-N` node-id suffix on fetch (`runapi.go:87`).
8. **Plan-artifact download: 128MiB cap** (`client.go:153-159`): **missing** → CP4 (in
   `meshapi/client_test.go`; streaming fake body, no real 128MiB allocation).
   **Same-origin: does not exist anymore** (F2) — pin dropped, D9 correction flagged.
9. **meshStack HTTP backend fallback incl. `TF_HTTP_USERNAME/PASSWORD`** (`tfcmd.go:152-167,
   281-327, 496-501`): ✅ unit level; use-case level with/without existing backend **missing** → CP6.
10. **Pre-run script contract** (`scriptcmd.go:87-129`, `$MESHSTACK_USER_MESSAGE`, run JSON on
    stdin, run mode as `$1`, bash `--noprofile --norc -e -o pipefail`): ✅
    `tfcmd_prerunscript_test.go` (14 tests); scenario-level wiring (user msg becomes step
    UserMessage on failure through the Worker) → CP8.
11. **`aaaaaa_…auto.tfvars` + `meshStack_run_vars.tf` rules** (`tfcmd.go:632-704`): ✅
    `Test_vars_OmitsRunScopedVarValuesOnDetectAndSavedPlanReplay` (tfcmd_test.go:474),
    `Test_setEnvWith_CreatesMeshStackRunVarsFileAndSetsTfVarEnv` (425),
    `Test_setEnvWith_DoesNotOverwriteExistingMeshStackRunVarsFile` (555),
    `Test_vars_SkipsDeclaringVariablesAlreadyDeclaredByBuildingBlock` (528).
12. **FILE inputs as data-URLs** (`tfcmd.go:512-560,724-735`): ✅ `Test_saveInputFiles_*`,
    `Test_extractContentFromDataUrl`; encrypted-FILE + scenario integration → CP4.
13. **Env whitelist `cleanSystemEnv`** (`tfcmd.go:406-445`): ✅ mostly
    (`Test_buildScriptEnvironmentVariables`, buildTfEnv tests); CP4 adds a direct pin that a
    poisoned ambient env var (e.g. `AWS_SECRET_ACCESS_KEY`) does **not** reach `SetEnv`.
14. **Decrypt-failure UX (key-mismatch guidance)** (`run.go:56-64`): **missing** → CP4 with genuine
    crypto (encrypt with a *different* generated cert, decrypt with `resources/test.key`).
15. **Workspace select/create/delete naming logic** (`tfcmd.go:186-269`, `run.go:75-98`
    `toWorkspaceStr`): partially incidental; explicit pins **missing** → CP5 (includes bug pins
    B1–B3).
16. **k8s single-run contract** (`RUN_JSON_FILE_PATH`, `/var/run/secrets/meshstack/run.json`,
    `RUNNER_UUID`, `RUNNER_API_URL`, runToken-only auth): **missing**. Pinned in three reachable
    pieces: (a) `ToInternalWithoutDecryption` fixture test (CP9), (b) `SingleRunWorker` suite with
    runToken-only auth — every API request carries `Bearer <runToken>`, never Basic (CP3),
    (c) `validateAuthConfig` single-run branch incl. required `RUN_JSON_FILE_PATH` (CP9; the env
    contract constants live in `config.go:64-65` and `run-controller/controller/kubernetes.go:614-631`).
    The `main.go:112-159` glue itself stays uncovered (outside the `tfrun` gate) — the
    controller-side k8s Job contract is re-verified by the acceptance suite (A7) and pinned again
    in phase 3/5 plans. **This is a known, declared pin gap, not a silent one.**

**STOP-C:** if during implementation any pin above proves unreachable through
`Worker`/`SingleRunWorker` black-box + the seams that already exist (fake transport,
`MockedTfFacade`, `MockedGitFacade`, local git repos, `AppConfig`, `meshcrypto.Crypto`) — i.e. it
would require adding a production seam — do **not** add the seam. Stop, record the pin as
phase-2-entry criterion, and get the plan revision reviewed. (Predicted candidates: none; pin 16
is already resolved by declaration above.)

---

## 6. Bug inventory (D13 — pinned verbatim, fixed only in phase 2b)

Each entry: location · what's wrong · pinned (current) behavior · correct behavior (2b). Pin tests
carry `// FIXME(bug): Bnn — <one-liner>` and are listed in the checkpoint that writes them.

| # | Location | What's wrong | Pinned behavior | Correct (2b) |
|---|---|---|---|---|
| B1 | `tfcmd.go:231-234` `selectWorkspace` | `WorkspaceSelect` error is swallowed: `return "", nil` | On select error the caller (`useWorkspaceIfNeeded`) creates a **new** workspace with the suggested name → silent state split | Propagate the error; fail the step |
| B2 | `tfcmd.go:236` `selectWorkspace` | Returns `params.buildingBlockId` instead of the actual matched workspace name `ws` (contrast `:222` which returns full `current`) | `deleteWorkspaceIfNeeded` then calls `WorkspaceDelete(ctx, "<bbId>")` — a name that normally doesn't exist (real names are `ws.proj.platform:bbId`), so DESTROY leaves the workspace behind and only logs the failure (`:266-268`) | Return `ws` |
| B3 | `tfcmd.go:253-259` `deleteWorkspaceIfNeeded` | On `selectWorkspace` error: logs "won't attempt deletion again" but **continues**, deleting `workspace == ""` | `WorkspaceSelect("default")` then `WorkspaceDelete("")` is attempted | Return after the error |
| B4 | `tfcmd.go:178` `plainInit` | `time.Sleep(1000)` = 1000 **nanoseconds**; comment says "Wait one second" | Retry happens effectively immediately (pin: retry-once occurs; timing itself not asserted — flaky) | `time.Sleep(time.Second)` (or injected clock) |
| B5 | `run.go:45-56` `decryptIfSensitive` | Sensitive variables whose `Type` is not CODE/STRING/FILE (INTEGER, BOOLEAN, SINGLE_SELECT, MULTI_SELECT, LIST) skip the switch entirely | Ciphertext is passed through **silently** as the variable value into tfvars/env | Decrypt every sensitive value or fail fast |
| B6 | `manager.go:119-135` | `shutdownCalled` written by `Stop()` (signal goroutine) and read in `handoutWorkerToken` goroutines with no synchronization | Data race (pin: functional behavior only — token protocol; race itself not assertable and `-race` must stay off, A5) | atomic.Bool / channel |
| B7 | `logwrapper.go:16-27` `NewLogWrap` | Returns `nil` on file-open error; `initRunContextInfo` (`runcontextinfo.go:56`) never checks → nil deref on first log write | Pin: direct call `NewLogWrap(logger, "/nonexistent-dir/x/y")` returns nil (unit-level; the worker-level panic is not provoked) | Return `(nil, error)` and fail the run cleanly |
| B8 | `tfbinaries.go:158` `installTofuBinaries` | Uses `context.Background()` instead of the caller's ctx | Download ignores run timeout/abort (file is gate-excluded; **inventory-only, no pin test**) | Pass ctx through |
| B9 | `gitsource.go:111-115` `CopyToTargetDir` | When `g.path == nil` and `os.Stat(sourceDir)` fails, the log statement dereferences `*g.path` → panic | Latent nil-deref; requires tmp-clone dir to vanish — **inventory-only, no pin test** (not reachable without contrived facade behavior) | Guard the nil |
| B10 | `tfcmd.go:829-831` `commitStatus` + `runcontextinfo.go:22-23` | `reportStatus = *(runStatus)` shallow-copies `Steps []*StepStatus`; observer marshals the same `StepStatus` structs the work goroutine mutates. The "atomic version" comment is false | Data race under `-race` (pin: functional behavior only; see A5/STOP-B) | Deep-copy steps (or lock) in phase 2's reporting facility |
| B11 | `main.go:154-156` | Single-run failure only logged; process exits 0, k8s Job shows `Succeeded` for failed runs | Inventory-only (outside `tfrun`); revisit in phase 2/4 with the persona `main` | Non-zero exit or explicit decision documented |
| B12 | `behavior.go:28` `Behavior.str()` | `log.Fatalf` inside a stringer kills the whole process for an unmapped value | Pin: `DetermineBehavior("bogus")` returns error + `UNKNOWN_BEHAVIOR` (the fatal branch itself is not invokable in-process) | Return `"UNKNOWN"`/error |
| B13 | `tfapply.go:149-152` vs `tfplan.go:135-139`/`tfdestroy.go:140-144` | Init-failure hint `HINT_INIT_FAILED` is printed for DETECT/DESTROY but **not** APPLY | Pin the asymmetry in CP6 assertions | Consistent hint |

Test-suite hygiene findings (not code bugs, fixed *in this phase* because they are test-only):
F1 (network in scenario/gitsource-adjacent suites — fixed by CP1; `tfbinaries_test.go` retained
as the opt-in-ish real-download test of a gate-excluded file), F4 (`Test_UseCustomPredicate_*`
misleading names — **left untouched** in phase 1 to keep the diff reviewable; renamed in phase 2).

---

## 7. Adapter exclusion list (D6)

Lives in the phase-0 mechanism's file (A2/A3), one line each. Everything **not** listed counts
toward the 90%.

| File | Justification |
|---|---|
| `tfrun/git.go` | Real go-git/`exec git`+`bash` adapter: SSH clones need a live server, azure path shells out to `git`; behavior is exercised additionally by local-repo tests (CP11) but its full surface (auth transports, azure remote quirks) only by real I/O e2e. |
| `tfrun/tfbinaries.go` | Real HashiCorp/OpenTofu release downloads (network, signature verification); `tfbinaries_test.go` keeps exercising it outside the gate. |

Explicitly **not** excluded (with reasoning, because D6 named them as candidates):

- `tfrun/authSsh.go` — hermetically testable: key parsing with generated PEM keys, knownhosts
  callbacks with `knownhosts.New` over fixture files + synthetic `ssh.PublicKey`/`net.TCPAddr`,
  env/HOME fallbacks. CP10 covers it. **STOP-D:** if CP10 shows material parts (e.g.
  `toTransport`'s go-git integration) cannot be reached hermetically, move `authSsh.go` to the
  exclusion list *via a reviewed plan update* (arithmetic fallback in §8 already computed).
- `tfrun/runapi.go` — fully covered via fake transports today (89.5%).
- `tfrun/mockedtffacade.go`, `tfrun/mockedgitfacade.go` — test doubles in the prod source set
  (F3); excluding them would be exclusion-list abuse (they're not real-I/O adapters). They stay
  in the denominator and are near-fully covered.
- `main.go`, `util/` — outside the `tfrun` gate scope by D6, no exclusion entry needed.

---

## 8. Coverage arithmetic (target feasibility)

- Denominator after exclusions: 1541 − 95 (`git.go`) − 59 (`tfbinaries.go`) = **1387** statements.
- 90% gate ⇒ **1249** covered required.
- Baseline covered in scope: 916 − 23 − 46 = **847** (= 61.1%). Required uplift: **+402** of the
  540 currently uncovered in-scope statements (74% of the remaining gap).
- Uplift budget by checkpoint (conservative): CP3 singlerunworker +74 · CP10 authSsh +95 ·
  CP9 config/dtos/run/behavior +65 · CP5+CP6 tfcmd workspace/backend/init/env +60 ·
  CP7 async (tfapply/tfplan/tfdestroy/tfcmd.nextStep) +45 · CP2 worker fetch/register/init-fail
  +25 · CP12 manager +20 · CP11 gitsource +15 · CP4 decrypt/env/file paths +20 ·
  CP8/CP13 misc error branches +15 ⇒ ≈ **+434** → projected ≈ 1281/1387 ≈ **92%** (buffer ~2pp).
- Fallback if STOP-D triggers (exclude `authSsh.go`): denominator 1272, need 1145; projected
  847 + 339 ≈ 1186 ≈ **93%**. Feasible either way.

**STOP-E:** if after CP12 the measured number is < 88% with only CP13 left, do not start
excluding convenient files to close the gap — that is the D6 failure mode. Stop and replan
(either a reviewed exclusion with genuine real-I/O justification, or additional test specs).

---

## 9. Test specifications — always-green checkpoints

Ordering rule: after every checkpoint the full suite compiles and passes
(`cd tf-block-runner && go test ./...`) and the in-scope coverage number is ≥ the previous
checkpoint's (monotone rise; record the number in the checkpoint's commit message on the working
branch — squashed at the end).

**CP1 — Hermetic fixtures (coverage-neutral).**
New `tfrun/fixtures_test.go` (or similar `_test.go`-only helpers):
- `makeLocalGitRepo(t, files map[string]string, refs …)` — `go-git` `PlainInit` + commit; options
  to create a branch, tag, and return commit hash; returns filesystem path usable as `url` (go-git
  `PlainClone` accepts local paths; keeps the run JSON black-box).
- `runDetailsDTO(t, opts…)` builder wrapping today's `mockValidRunDetailsFetchCall` /
  `mockApplyRunWithPlanArtifactFetchCall` duplication (`worker_scenario_test.go:567-665`) with
  options: behavior, repo URL/path/ref, async, inputs (incl. sensitive/env/FILE), preRunScript,
  `useMeshHttpBackendFallback`, planArtifact href, runToken.
- Encrypted-input helper: load `../resources/test.pem`/`test.key`
  (mirrors `crypto/meshcertbasedcrypto_test.go:12-18`), `EncryptMeshCertBased` fixture values;
  helper to install/uninstall `meshcrypto.Crypto` per test (global — restore in `t.Cleanup`;
  document as phase-2 injection target, D4).
- Switch existing scenario tests from `https://github.com/meshcloud/meshstack-hub.git` to the
  local fixture repo. Gate: suite passes with network disabled (spot-check via
  `GOFLAGS= go test -run 'WorkerSuite' ./tfrun` offline or by assertion that no request leaves the
  fake transport), and runs materially faster.
- Extract the `SetupTest` worker construction into a helper reusable by the SingleRunWorker suite.

**CP2 — Worker fetch/register failure paths + header pins.**
- `handleFetchRunError`: table test via `suite.calls.fetch` returning 404 → expect `norun` token
  on `workerOut`; 409 → `norun`; 500 → `failed`; transport error containing
  `too many transfer encodings: ["chunked" "chunked"]` → `norun` (pin the string match,
  `worker.go:84`); other transport error → `failed`.
- Registration 500: fetch OK, register 500 → final update FAILED, `MockedTfFacade` records **no**
  `Init/Apply` calls, exactly one final PATCH (pin `worker.go:170-181`).
- `sendInitFail`: make `os.MkdirTemp` fail by pointing `w.workerDir` at a non-directory file →
  one FAILED update with `Summary == "Something went wrong while starting the run."`, `Steps` nil.
- Header pins: extend `ApiTestSuite` asserts with `User-Agent == meshcloud-<name>/<version>`,
  `X-Meshcloud-Runner-Name/-Version`, fetch node-id `== "<uuid>-<postfix>"` (`runapi.go:87`).
- `UpdateState` malformed JSON response → error (unmarshal branch, `runapi.go:140-143`).

**CP3 — SingleRunWorker suite (mirror of WorkerTestSuite).**
New `singlerunworker_scenario_test.go`: build `Run` via `ToInternalWithoutDecryption` from a run
JSON fixture (pin 16a), `NewSingleRunWorkerWithApi` with fake-transport `RunApiClient` whose
`runApiAuth.baseAuth == nil` and `SetRunToken(<token>)`:
- APPLY/DETECT/DESTROY success (final SUCCEEDED, steps as in polling twin).
- APPLY tf failure (final FAILED, step messages).
- **Auth pin:** every captured request has `Authorization == "Bearer <runToken>"`; none Basic.
- Abort flag via PATCH response → ctx cancelled (twin of `Test_ApplyRunAborted`).
- Registration failure → FAILED final (mirrors `singlerunworker.go:128-135`).
- Init-fail: unwritable `workerDir` parent → `ExecuteRun` returns error **and** `sendInitFail`
  update observed; also pin that `ExecuteRun` creates `workerDir` via `MkdirAll` (`:54`).
- Constructor pins: `NewSingleRunWorker` and `NewSingleRunWorkerWithApi` set
  `statusUpdateInterval == 10s`, timeout from minutes (pin 3).
- Saved-plan APPLY representative test (download + `DirOrPlan`).

**CP4 — Inputs, crypto, artifact cap.**
- Scenario: run JSON with sensitive STRING (+ sensitive FILE) inputs genuinely encrypted with the
  test cert; `meshcrypto.Crypto` from `test.key`; assert decrypted plaintext lands in the
  generated `aaaaaa_…auto.tfvars` / written file (read from the working dir inside a
  `tfMock.applyFunc` hook, as `tfplan_scenario_test.go:28-31` does).
- Decrypt failure: input encrypted with a *freshly generated* other cert → run FAILED, step
  UserMessage contains the key-mismatch guidance text (`run.go:58-63` "private key provided to
  this building block block runner does not match").
- `// FIXME(bug): B5` — sensitive `BOOLEAN` input: assert the **ciphertext** string appears
  verbatim in the tfvars file.
- `encodeVarValueForEnv`: MULTISELECT env var JSON-encoded; unmarshalable value → error path
  (`tfcmd.go:707-722`); env-var decrypt-failure path in `buildTfEnv` (`:481-484`).
- `cleanSystemEnv` negative pin: set `AWS_SECRET_ACCESS_KEY` via `t.Setenv`, capture `SetEnv` map
  through a `MockedTfFacade` extension hook (mock change = test infra, allowed), assert absence.
- 128MiB cap pin in `go-meshapi-client/meshapi/client_test.go`: fake body = lazy 128MiB+1 reader,
  expect "exceeds the maximum allowed size" error; plus stale-plan apply-error message pin
  (`tfapply.go:238-244`) at scenario level.

**CP5 — Workspace logic pins (scenario-level via `tfMock.workspace*` hooks; the mock needs
configurable `workspaceListFunc`/`workspaceSelectFunc`/`workspaceNewFunc`/`workspaceDeleteFunc` —
extend `MockedTfFacade` accordingly, test-infra change).**
- Current workspace already suffix-matches bbId → no select/new (pin `tfcmd.go:222-225`).
- Listed workspace matches → `WorkspaceSelect(ws)` called, no `WorkspaceNew`.
- No match → `WorkspaceNew(run.toWorkspaceStr())` with the full
  `ws.proj.platform:bbId` name (pin `run.go:91-97`, incl. `_` placeholders for nil identifiers).
- `// FIXME(bug): B1` — `WorkspaceSelect` returns error → run **succeeds** and `WorkspaceNew` is
  called (error swallowed).
- `// FIXME(bug): B2` — DESTROY: real workspace `a.b.c:bbId` exists; assert
  `WorkspaceDelete(_, "bbId", …)` is called with the **wrong** (suffix-only) name and the run
  still SUCCEEDS while the delete error is only logged.
- `// FIXME(bug): B3` — `WorkspaceList` error during delete → `WorkspaceSelect("default")` and
  `WorkspaceDelete("")` still attempted.
- `useWorkspaces == false` (via backend fallback, CP6) → no workspace calls.

**CP6 — Backend fallback + init behavior (scenario-level).**
- Fallback on, fixture repo **without** backend block: assert a `meshStack_httpbackend-*.tf` file
  exists in the working dir during `applyFunc`, containing `EP_State`-shaped URL with
  `meshstackBaseUrl` host, `TF_HTTP_USERNAME == "x"` / `TF_HTTP_PASSWORD == <runToken>` in the
  `SetEnv` map, and **no** workspace calls (`tfcmd.go:152-167` sets `useWorkspaces = false`).
- Fallback on, fixture repo **with** `backend "local" {}` block: no backend file written,
  workspaces active, update logs contain "Using existing backend." (`detectBackend`, 0% → covered).
- `meshstackBaseUrl` empty → falls back to `AppConfig.RunApiBackend.Url` (`tfcmd.go:297-300`).
- Init retry: `initFunc` fails once then succeeds → run SUCCEEDS, both calls observed
  (pin `tfcmd.go:170-184`; B4 recorded, timing not asserted). Init fails twice → FAILED; DETECT
  and DESTROY show `HINT_INIT_FAILED` in step logs, APPLY does **not**
  (`// FIXME(bug): B13` asymmetry pin).

**CP7 — Async runs.**
- Async APPLY: single step `trigger`/"Prepare Run" (`tfapply.go:34-45`), registration DTO has one
  step, intermediate statuses never advance past it (`nextStep` no-op, `tfcmd.go:801-803`), final
  status **IN_PROGRESS** despite internal SUCCEEDED (pin 1).
- Async DESTROY: `Apply` called **before** `Destroy` (pin the hack `tfdestroy.go:161-169`).
- Async DETECT: final IN_PROGRESS; plan artifact still attached.
- Async failure (tf error): final **FAILED** (the IN_PROGRESS mapping applies only to SUCCEEDED).
- One async single-run twin (CP3 harness).

**CP8 — Pre-run script + failure UX at use-case level.**
- Run JSON with `preRunScript` writing to `$MESHSTACK_USER_MESSAGE` and exiting 0 → step
  `pre_run_script` SUCCEEDED, UserMessage set (`tfcmd.go:759-772`, `advanceStep(preRunUserMsg)`).
- Failing script with user message → run FAILED, step UserMessage == script's message
  (`failWithUserMsg` override, `tfcmd.go:123-127`), SystemMessage contains combined output and
  "pre-run script exited with code N".
- Script reads stdin run JSON (`jq`-free: `cat`), asserts it round-trips
  (`runJsonBase64` → stdin, `scriptcmd.go:101-113`).
- Timeout: worker `timeout` set to ~1s, `applyFunc` sleeps past it → step UserMessage contains
  "exceeded the configured timeout of %d minutes" (`tfcmd.go:115-117`;
  note `AppConfig.TfCommandTimeoutMins` is what's printed, not the actual worker timeout —
  record as quirk, config-vs-worker duplication for phase 2).
- "exit status N" rewrite pin: error text starting `exit status ` becomes
  "command failed (exit status N) — check the step logs above…" (`tfcmd.go:110-112`).

**CP9 — Config, DTOs, small types.**
- `ReadConfig` black-box (`config.go:68-122`, 0%): temp yaml (reuse the shape of
  `resources/application-test.yml`), `RUNNER_CONFIG_FILE` + `t.Setenv` matrix: file-only,
  env-overrides-file, missing file → defaults + env, unreadable/invalid yaml → error,
  private-key file loading incl. default path fallback + non-ENOENT warning path
  (`applyPrivateKeyFile`, `config.go:169-180`), auth validation failures, missing runnerUuid.
  (Global `AppConfig` mutation — save/restore in `t.Cleanup`, same pattern the suites already use.)
- `NewAuthProvider`: apikey precedence over basic, nil when neither (`config.go:42-50`).
- `ToInternalWithoutDecryption` (0%): golden run-JSON fixture (shape of
  `run-controller` decrypted output) → all fields incl. `isSensitive: false` forcing
  (`dtos.go:87-95`), plus error branches (bad behavior string, unparsable implementation JSON).
- `runDTOToInternal` error branches; `knownHostsToInternal` nil/non-nil;
  `terraformImplAuthMethod` SSH branch.
- `DetermineBehavior("bogus")` → error (B12 pin); `ExecutionStatus.str()` panic branch via
  `assert.Panics`; `RunStatus.nextStep`/`firstStep` error branches; `toWorkspaceStr` nil-pointer
  placeholders.
- `// FIXME(bug): B7` — `NewLogWrap` with unopenable path returns nil.

**CP10 — SSH auth (hermetic seam tests on `SshAuth`/`NoAuth`).**
Generated keys via stdlib (`crypto/ed25519`/`rsa` + `golang.org/x/crypto/ssh`): 
- `prepare`: writes `ssh_cert` (0600, trailing-newline normalization `authSsh.go:69-72`) and
  `known_hosts_tmp` when a `KnownHost` is set; write-failure paths via read-only dir.
- `unwrapCert`: `Crypto == nil` passthrough vs genuine decrypt vs decrypt error.
- `toTransport`: valid PEM key → `PublicKeys` with `HostKeyAlgorithms` pinned to the configured
  key type (`:121-124`); garbage PEM → `ERR_MSG_INVALID_KEY` logged, error returned.
- `knownHostsKeyCallback`: insecure mode → `InsecureIgnoreHostKey`; secure mode → the combined
  callback. Drive `combinedKnownHostsKeyCallback` directly with fixture knownhosts files,
  synthetic `ssh.PublicKey` + `&net.TCPAddr{}`: method-1 hit, method-2 hit via
  `SSH_KNOWN_HOSTS` (`t.Setenv`), method-3 via `HOME` override, all-miss →
  `ErrResolveNoExtraKnownHost` (no entry configured) vs `ErrResolveGeneric` (entry configured);
  broken knownhosts file → parse-skip branches.
- `NoAuth.toTransport`: `git@…` URL → "cannot clone via ssh without auth" error; https → nil,nil.
- Altitude note: these are seam tests on the `auth` interface (a real consumer-side seam,
  `gitsource.go:23`), acceptable per the brittleness rule because phase 2 moves this file intact
  into an adapter package (D11). **STOP-D applies** (see §7) if coverage here stalls.

**CP11 — GitSource with local repositories (black-box through Worker where possible).**
- Scenario: run JSON refName = branch / tag / commit hash against the CP1 fixture repo → cloned
  content matches ref (covers `git.go:23-78 checkoutRef` resolution order — informative even
  though `git.go` is excluded; primarily lifts `gitsource.go` branches `:71-96`).
- Missing `repositoryPath` in repo → FAILED with "The specified path '%s' does not exist"
  (`gitsource.go:111-115`).
- `logDirectoryContentsForWorktreeUnstagedChangedError` (0%): direct `GitSource` test with a
  `MockedGitFacade` whose `checkoutRef` returns `errors.New("worktree contains unstaged changes")`
  (mock extension: configurable error returns — test infra).
- Azure route stays covered by existing `Test_GitSourceCloneSimpleAzure`/`WithRefAzure`.

**CP12 — Manager token protocol.**
Direct tests on `DefaultRunManager` with injected channels (the manager *is* the polling use-case
boundary; its channel protocol is what `Worker.work()` consumes and survives phase 2 as the
engine loop's contract):
- `handleWorkers`: `done` → new `work` token; `stopped` → loop ends and "Stopped" logged.
- `handoutWorkerToken(0)` with `shutdownCalled` true/false → `stop` vs `work`; post-sleep
  shutdown re-check branch (delay 0 keeps it fast). `norun`/`failed` delays (10s/60s,
  `manager.go:13-14`) pinned as constant assertions, not timed runs.
- `NewManager` pins defaults (timeout from `AppConfig`, buffered channels).
- `Stop()` sets shutdown + logs (B6 recorded; no race assertion, A5).
- `Start`/`run` remain partially uncovered (spawns `NewRunApi()` + real 10-60s sleeps); their
  ~10 statements are inside the §8 buffer. Do **not** add seams to reach them (STOP-C logic).

**CP13 — Gate-on + reconciliation.**
- Fill measured stragglers found by `go tool cover -html` (target list from §3: `runapi.go`
  empty-auth branch, `scriptcmd.go` error branches, `worker.go` observer final-update-error
  branch via failing final PATCH, `logwrapper` write-error branch, `tfconfig_parse` diags,
  `backendsearch` error diag, `collectOutput`/`matchOutputType` remaining types).
- Write the exclusion file entries (§7), flip the phase-0 threshold to **gating** at 90 for
  `tfrun`, run `task test` + gate + lint. Record final number in the PR description together
  with the bug inventory table (§6).

---

## 10. Frozen contracts touched (D9/D10)

This phase **changes no contract** — it freezes them. Contracts exercised and now enforced by
tests: claim/register/status endpoints + media type V1 + runner headers (`meshapi/client.go:11-24,
235-243`); 409-register idempotency; 404/409-claim semantics; abort-flag PATCH response
(`RunUpdateResponseDTO.Abort`); final-status mapping incl. async IN_PROGRESS; plan-artifact
download (bearer runToken, octet-stream accept, 128MiB cap — **same-origin check no longer
exists**, D9 correction required, F2); tfvars/`meshStack_run_vars.tf` generation; state-backend
URL shape `EP_State` (`runapi.go:42`) + `TF_HTTP_*` ephemeral auth; pre-run script env/stdin/argv
contract; k8s single-run env contract (partial, §5 pin 16). The k8s Job contract itself
(`run-controller/controller/kubernetes.go:581-631`) is untouched and re-pinned in later phases.

## 11. Rollback story

Tests-only, single squashed commit. Rollback = revert that one commit; production behavior is
untouched by construction. Reviewable safety check before merge:
`git diff --stat phase-0-branch..HEAD -- '*.go' | grep -v '_test.go'` must list nothing
(allowed non-`_test.go` diffs: the exclusion-list/config file from phase 0's mechanism and this
plan document). If the coverage gate turns out too strict in practice after merge, the gate flag
(phase-0 mechanism) can be flipped back to report-only without reverting the tests.

## 12. Cross-repo touch points

- **meshfed-release:** none in code. The acceptance suite is the outer net (A7). Note for its
  maintainers: bug B2 means DESTROY runs currently leave TF workspaces behind — visible in
  long-lived local-dev-stack state, do not "fix" it there.
- **terraform-provider-meshstack:** none (patterns only, D3).
- **meshStack/meshfed API:** none — fake transports replay today's observed shapes; no server
  change required or implied.

## 13. Design decisions

- **Local-path git URLs vs mocked GitFacade for scenario tests:** local paths (CP1) — zero
  production change, still black-box, exercises real clone/checkout logic; the mock stays for
  azure/error-injection cases.
- **Seam-level tests (CP10 authSsh, CP12 manager, existing tfcmd unit tests):** allowed. The
  "use-case level only" rule (risk #2) protects against phase-2 churn; `auth` and the manager
  token protocol are seams phase 2 keeps (D4 ports, engine loop). Anything phase 2 dissolves
  (e.g. `GenericTfCmd` internals) gets no *new* unit tests.
- **Pin 16 (k8s single-run) cannot be fully covered:** the env-reading glue is in
  `package main`; three-part pin in `tfrun` (§5), remainder owned by acceptance tests + later
  phases.
- **`-race`:** later (2b) — B6/B10 make `-race` red; A5/STOP-B govern.
- **90% includes the mock files** (F3, §7) — they are covered anyway; excluding them would
  misuse the adapter list.

**Open questions:** none.
