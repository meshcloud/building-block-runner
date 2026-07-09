# Detail Plan 02 — DDD Refactor of the tf Runner (Phase 2) + Bug-Fix Pass (Phase 2b)

**Phases:** 2 and 2b · **Branches:**
`refactor/single-go-binary/phase-2-tf-ddd-refactor` (stacked on
`refactor/single-go-binary/phase-1-characterization-tests`) and
`refactor/single-go-binary/phase-2b-bugfixes` (stacked on phase 2) ·
**Delivery:** one single-commit PR each (§5 high-level plan) ·
**Binding:** §3 P1–P8 (esp. P3 consumer-side interfaces / no `context.Value` smuggling, P4
value semantics, P8 misuse-resistant types), D4 (ports), D11 (package layout), D13 (bug
policy), D9/D10 (frozen contracts) of `PLAN_HIGH_LEVEL.md`.

Phase character: **phase 2 is behavior-preserving** — every characterization test from
phase 1 stays green with its *assertions untouched* (test-harness construction code may
change, see §1 A5 and STOP-B). **Phase 2b executes the phase-1 bug inventory** (B1–B13,
`PLAN_DETAIL_01` §6) — one flip per bug, nothing else.

---

## 1. Assumptions from prior phases

Plans 00 and 01 are **not implemented yet**; everything below is a promise, not a fact.
Implementation of phase 2 **begins by running every verification step**. Any material
failure is a **STOP**: update this plan (and cascading plans) first, get the revision
reviewed, then resume.

| # | Assumption | Promised by | Verification step |
|---|---|---|---|
| A1 | Coverage gate is ON at 90 for `tfrun`: `tools/coverage/thresholds.txt` contains `github.com/meshcloud/building-block-runner/tf-block-runner/tfrun 90`, and `task coverage` fails below it. | Plan 00 §12, Plan 01 CP13 | `cat tools/coverage/thresholds.txt && task coverage` on the phase-1 branch. |
| A2 | The gate script matches profile lines by **import-path prefix** (so a rename `tfrun` → `internal/…` is a one-line thresholds edit, and a prefix line can cover several packages). | Plan 00 §5.4 ("`<import-path-prefix> <min-percent>`") | Read `tools/coverage/check.sh`; test with a dummy prefix line. |
| A3 | Measured coverage on the phase-1 branch is ≈92% (plan 01 §8 projected 1281/1387), i.e. ≥ ~2pp buffer above the gate. | Plan 01 §8 | `task coverage`, record the number. **STOP-A2** if < 91%: the refactor will shave fractions during moves; replan buffer (extra tests) before starting. |
| A4 | The exclusion list contains exactly `tfrun/git.go` and `tfrun/tfbinaries.go` (per-file, with justifications). | Plan 01 §7 | `cat tools/coverage/exclusions.txt`. |
| A5 | Characterization suites are **black-box at the declared seams**: run-JSON in → captured HTTP requests + `MockedTfFacade` calls out, driven through `Worker.work()` / `SingleRunWorker.ExecuteRun()`; plus the three *declared* seam-test groups that phase 2 keeps or retargets: `auth` (CP10), manager token protocol (CP12), constructor-default pins (CP3/CP12). Harness construction is centralized in helpers (CP1 promised extraction of `SetupTest` worker construction). | Plan 01 §4, §9 CP1, Q2 | Read the suites' `SetupSuite`/`SetupTest`; confirm assertions never dereference `RunContextInfo`, `GenericTfCmd`, or `reportStatus` internals. |
| A6 | Suites are hermetic (local git fixture repos, no network except gate-excluded `tfbinaries_test.go`). | Plan 01 CP1 | Run `go test ./tfrun` with network disabled (or inspect: no `github.com` URLs left in scenario fixtures). |
| A7 | `-race` is OFF in CI and Taskfile; bugs B6/B10 are pinned functionally only (the races themselves are not asserted). | Plan 01 A5/Q4 | `grep -rn "\-race" .github/workflows Taskfile.yml`. |
| A8 | Bug inventory is final: 13 entries B1–B13, with `// FIXME(bug):` markers in tests for B1–B5, B7, B12, B13 (+ B6/B10 functional-only pins); B8, B9, B11 are inventory-only (no pin test). | Plan 01 §6 | `grep -rn "FIXME(bug)" tf-block-runner/` and diff against plan 01 §6. |
| A9 | Task targets from plan 00 §12 exist (`task test`, `task lint -- --fix`, `task coverage`, …); `.golangci.yml` contains a temporary `tfrun` exclusions block whose removal is owned by phase 2/2b. | Plan 00 §12 | `task --list`; read `.golangci.yml` exclusion comments. |
| A10 | `Test_UseCustomPredicate_*` (`runapi_test.go:533-742`) still test a non-existent feature and are earmarked for phase-2 renaming. | Plan 01 F4 | `grep -n CustomPredicate tf-block-runner/tfrun/runapi_test.go`. |

**STOP markers for the assumption run:**

- **STOP-A** (any of A1–A9 materially false): do not code around it; update this plan first.
- **STOP-B** (discovered any time): a characterization test can only be kept green by
  changing its **assertions** — i.e. it pins an internal seam this refactor must move,
  beyond the retargets declared in §5.6. That is the risk-#2 failure mode; stop, record the
  pin as a plan deviation, review, then resume.
- **STOP-C** (mid-sequence): a step would push gated coverage below 90 and no
  test-relocation step is planned for it (see the per-step "proves" column, §6). Do not
  extend the exclusion list to paper over it (D6 failure mode); replan the step.

---

## 2. Scope

**In (phase 2):**

- Restructure everything under `tf-block-runner/tfrun/` + `tf-block-runner/main.go` into
  the target packages of §5 — behavior-preserving under the phase-1 suite.
- Eliminate package-level mutable state reachable from `tfrun`: `tfrun.AppConfig`,
  `meshcrypto.Crypto` (incl. deleting the now-dead `var Crypto` from
  `go-meshapi-client/crypto` — a flagged, minimal cross-module edit, §10).
- Eliminate `context.Value` smuggling (`runInfoContextKey` / `RunContextInfo`).
- Collapse `Worker` + `SingleRunWorker` into one execution engine; collapse the three
  `TfCmd` implementations into one step pipeline.
- Structural elimination of the two data races (B6, B10) — **documented D13 exception**,
  see §5.5 and STOP-D.
- Test-suite hygiene deferred to phase 2 by plan 01: rename `Test_UseCustomPredicate_*`
  (F4); dissolve `util/` (D11); delete the *mechanical* part of the `.golangci.yml`
  `tfrun` exclusion block.
- Update `tools/coverage/thresholds.txt` / `exclusions.txt` paths in lock-step with moves;
  extend depguard with the new `internal/` direction rules (D11/D14).

**Out (deferred, with destination):**

- Every functional bug fix B1–B5, B7–B9, B11–B13 → **phase 2b** (§7).
- Flipping `-race` on → phase 2b (verifies B6/B10).
- `run-controller`, `go-meshapi-client` restructuring (incl. the `meshapi`
  `runnerName`/`runnerVersion` package globals, §4.1 item 3) → phase 3.
- Module consolidation / move to `runner/internal/` → phase 4 (§5.1 says NOW vs THEN).
- Any new feature, any contract change (D9/D10 stay frozen, §8).
- The errcheck-class lint pins in production code whose fix changes behavior → 2b.

---

## 3. Research evidence — current-state inventory

All references at branch `refactor/single-go-binary/plan` (worktree of `main` @ c3fce61).

### 3.1 Package-level mutable state

1. **`tfrun.AppConfig`** (`tfrun/config.go:14 var AppConfig TfRunnerConfig`), mutated by
   `ReadConfig`/`applyEnvVars` (`config.go:80,127-163`) and by every test suite
   (`worker_scenario_test.go:83`). Read at ~20 production sites: `manager.go:41,52,68`,
   `runapi.go:59,64-67,92,115,135`, `dtos.go:167` (`toExternal` stamps
   `Source: AppConfig.RunnerUuid`), `gitsource.go:35`, `authSsh.go:114`,
   `tfcmd.go:116,149,203,213,248,299,461`, `worker.go:179`, `singlerunworker.go:137`,
   `main.go:38-50,148-149`.
2. **`meshcrypto.Crypto`** (`go-meshapi-client/crypto/meshcertbasedcrypto.go:20
   var Crypto *MeshCertBasedCrypto = nil`), set only by `tf-block-runner/main.go:40`
   (polling mode), read at `tfrun/tfcmd.go:472,534,599` (passed into
   `Variable.decryptIfSensitive`, `run.go:45`) and `tfrun/authSsh.go:47,51`
   (`unwrapCert`: nil ⇒ passthrough). Nothing else in the repo uses it (grep over all
   modules). The nil/non-nil global doubles as the polling-vs-single-run mode switch.
3. **`meshapi.runnerName`/`runnerVersion`** (`go-meshapi-client/meshapi/client.go:26-29`),
   mutated via `SetClientMetadata` (`client.go:33-40`, called once from `main.go:26`).
   Write-once-before-goroutines; **deferred to phase 3** (client consolidation owns the
   meshapi package) — recorded here because the phase instruction asks for "anything else".
4. **`runInfoContextKey`** (`worker.go:31-33`) — immutable key, but it *is* the
   context.Value smuggling mechanism (next section).
5. Benign: `behaviors` (`behavior.go:17`, effectively const), `ErrResolve*` sentinel errors
   (`authSsh.go:30-31`), `hashicorpSecurityArmoredPublicKey` (embed, `tfbinaries.go:27-28`),
   `var _ TfFacade` compile assertion (`tffacade.go:10`).

### 3.2 `context.Value` usage map

`RunContextInfo` (`runcontextinfo.go:10-27`) is stored into the work context at
`worker.go:118` and `singlerunworker.go:80`
(`context.WithValue(parentCtx, runInfoContextKey, runContextInfo)`) and re-extracted with
unchecked type asserts at **six** sites: `worker.go:141,192`,
`singlerunworker.go:102,149`, `tfapply.go:20`, `tfplan.go:16`, `tfdestroy.go:12`. It
carries: run identity (`runId`, `bbId`, `runJsonBase64`), mode flags (`asyncRun`,
`useMeshBackendFallback`), paths (`workingDirectory`, `logFile_name`,
`artifactFilePath`), the mutable `runStatus *RunStatus` + `reportStatus RunStatus` pair,
the `logwrap`, and credentials (`runToken`, `meshstackBaseUrl`). This is the P3
anti-example named by the high-level plan.

### 3.3 `Worker` vs `SingleRunWorker` — divergence map (~95% identical, not 100%)

Identical verbatim (modulo receiver type): `workRoutine` (`worker.go:137-182` ≡
`singlerunworker.go:98-140`), `observerRoutine` (`worker.go:186-249` ≡
`singlerunworker.go:143-199` — including the async `SUCCEEDED→IN_PROGRESS` mapping and
abort-cancel), `sendInitFail` (`worker.go:251-264` ≡ `singlerunworker.go:201-214`), the
`TfCmdParams` construction and behavior switch. Divergences:

| # | Divergence | Worker | SingleRunWorker |
|---|---|---|---|
| 1 | Fields | `workerNumber`, `workerIn`/`workerOut` token channels (`worker.go:17,20-21`) | none; no channels (`singlerunworker.go:15-22`) |
| 2 | Run acquisition | fetches per token: `FetchRunDetails("worker-N")` (`worker.go:50`), routes fetch errors via `handleFetchRunError` (`worker.go:66-91`: 404/409⇒`norun`, chunked-transport quirk⇒`norun`, else `failed`) | `ExecuteRun(run *Run)` takes the run as a parameter (`singlerunworker.go:50`) |
| 3 | Token lifecycle | `ClearRunToken()` after each execution (`worker.go:56`) | never clears (process exits) |
| 4 | Working dir | relies on manager's `os.MkdirAll` (`manager.go:52`) | creates it itself, `MkdirAll(w.workerDir, 0777)` (`singlerunworker.go:54`) |
| 5 | Init-failure surface | `sendInitFail` + silent `return` (`worker.go:97-107`) | returns wrapped errors **and** `sendInitFail` — except the `MkdirAll` failure at `:54-56`, which returns an error *without* `sendInitFail` |
| 6 | End-of-run log | extra `"-----"` separator (`worker.go:132`) | none (`singlerunworker.go:92`) |
| 7 | Construction | built inline by the manager (`manager.go:66-76`) | `NewSingleRunWorker` / `NewSingleRunWorkerWithApi` (`singlerunworker.go:25-47`); **`NewSingleRunWorker` is dead production code** — only `NewSingleRunWorkerWithApi` is called (`main.go:146`) |

The manager (`manager.go`) drives Worker via the token protocol
`work/done/norun/failed/stop/stopped` (`manager.go:16-23`) with delays
`NORUN=10s`/`FAILED=60s` (`manager.go:13-14`) and the unsynchronized `shutdownCalled`
flag (`manager.go:119-135`, bug B6).

### 3.4 `TfCmd` structure and duplication

`TfCmd` interface: `initRunSteps/execute/setCurrentStepMessage/nextStep/fail`
(`tfcmd.go:62-68`). `GenericTfCmd` (`tfcmd.go:53-58`) stores `ctx context.Context` **in a
struct field** plus `runContextInfo`, `bin`, `params`; ~800 lines of shared step logic
hang off it. The three commands:

- `initRunSteps` is triplicated step-literal boilerplate: async ⇒ single
  `trigger`/"Prepare Run" step, sync ⇒ 6 steps (APPLY/DESTROY,
  `tfapply.go:33-106`/`tfdestroy.go:24-97`) or 5 steps (DETECT, no `output` step,
  `tfplan.go:28-92`). Step ids from `stepids.go:5-13`; ids + display names are pinned.
- `execute()` bodies share an identical prefix — `startRun`, `createFreshCommandWd`,
  `GetTF`, `buildTfEnv`+`SetEnv`, `assignOutput`, `advanceStep`, `saveInputFiles`, `vars`,
  `advanceStep`, `init`, `useWorkspaceIfNeeded`, `advanceStep`, `runPreRunScript`,
  `advanceStep` (`tfapply.go:108-167` ≡ `tfplan.go:94-154` ≡ `tfdestroy.go:99-159`) — and
  diverge only at the end: APPLY plain vs saved-plan replay (`tfapply.go:169-198`,
  `applyPredecessorPlan` `:216-247`), DETECT plan+artifact read (`tfplan.go:156-170`),
  DESTROY async-apply hack + destroy + `deleteWorkspaceIfNeeded` (`tfdestroy.go:161-188`).
- The B13 asymmetry: DETECT/DESTROY print `HINT_INIT_FAILED` on init failure
  (`tfplan.go:135-139`, `tfdestroy.go:140-144`), APPLY does not (`tfapply.go:149-152`).
- Bugs B1/B2/B3 live in the shared workspace logic (`tfcmd.go:210-269`), B4 in
  `plainInit` (`tfcmd.go:178 time.Sleep(1000)`).

### 3.5 Reporting mechanics (logwrap / RunStatus / observer)

- `RunStatus` holds `Steps []*StepStatus` (**pointers**, `runstatus.go:8`);
  `initRunContextInfo` seeds `runStatus` (IN_PROGRESS, `CurrentStepIndex: -1`,
  `runcontextinfo.go:36-42`) and `reportStatus: *status` (shallow copy, `:54`).
- The work goroutine mutates `runStatus` through `GenericTfCmd` helpers
  (`startRun/advanceStep/completeRun/failWithUserMsg`, `tfcmd.go:833-856,94-132`);
  `commitStatus` publishes via `reportStatus = *(runStatus)` (`tfcmd.go:829-831`) — the
  copy shares the `Steps` slice and its pointed-to `StepStatus` structs, so the observer's
  10s `UpdateState(&runContextInfo.reportStatus)` (`worker.go:232-246`) marshals structs
  the work goroutine concurrently mutates. The "atomic version" comment
  (`runcontextinfo.go:22`) is false — **bug B10**.
- Live logs: `logwrap.Write` (`logwrapper.go:31-40`) counts `logSize` and fires
  `callback`, which `assignOutput` wires to `setCurrentStepMessage(nil)+commitStatus`
  (`tfcmd.go:329-339`); `setCurrentStepMessage` re-reads the log file segment
  `[LogStartIdx, logSize)` via `fileContentOrEmpty` (`tfcmd.go:789-799`,
  `worker.go:271-278`). `NewLogWrap` returns nil on open error (bug B7,
  `logwrapper.go:16-20`).
- Final status: async + SUCCEEDED ⇒ IN_PROGRESS (`worker.go:206-211`); cancelled ctx ⇒
  final update omitted (`worker.go:202-204`); abort flag from PATCH ⇒ `cancel()`
  (`worker.go:239-245`).

### 3.6 GitSource / auth

`GitSource{url, path, refName, auth, log, gitFacade}` (`gitsource.go:19-26`) with the
late-mutation `setLog` pattern (`gitsource.go:28-31`, called from `worker.go:112` /
`singlerunworker.go:74`); `gitFacade` hardwired to `&Git{}` in the DTO mapping
(`dtos.go:198`). `auth` is already a consumer-side interface
(`auth.go:14-19`: `name/prepare/toTransport/done`) with `SshAuth` (decrypts the key via
the `meshcrypto.Crypto` global, `authSsh.go:45-53`; reads `AppConfig` at `:114`) and
`NoAuth`. `Git` (`git.go`) is the real go-git/`exec git` adapter (gate-excluded). Bug B9
at `gitsource.go:111-115` (`*g.path` deref when nil).

### 3.7 runapi token precedence & client boundary

`runApiAuth` (`runapi.go:16-31`): `runToken` Bearer wins over `baseAuth`
(Basic/ApiKey from `RunApiConfig.NewAuthProvider`, `config.go:42-50`); empty string when
neither. `FetchRunDetails` stores the fetched token (`runapi.go:98`) and builds a
**per-fetch client** with node-id `"<uuid>-worker-N"` (`runapi.go:87-90`) while all other
calls use the plain uuid; `ClearRunToken` after execution (`worker.go:56`) restores base
auth — all pinned by `Test_ClearRunToken_*`. `RunApi` interface (`runapi.go:45-54`)
bundles fetch + register + status + token lifecycle + artifact download. The
`go-meshapi-client/meshapi.Client` boundary (`client.go:57-243`) provides
`FetchRun/RegisterSource/PatchStatus/DownloadArtifact` (409-register = success
`:187-189`; 128MiB artifact cap `:153-159`; media type + runner headers `:235-243`) and
stays **untouched** in phase 2 apart from deleting the unused crypto global next door.

---

## 4. Objective & exit criteria (from the high-level plan)

Phase 2 exit: one execution engine; polling and single-run are `RunSource`
configurations; no package-level mutable state; coverage gate ≥90 throughout.
Phase 2b exit: bug inventory empty; no `FIXME(bug)` markers remain; `-race` on.

---

## 5. Target design

### 5.1 Package layout — where things live NOW vs THEN (D11)

Phase 2 creates the D11 shape **inside the existing `tf-block-runner` module**, so the
phase-4 move into the `runner` module is a mechanical `git mv` + import-path rewrite:

| Concept | NOW (phase 2, module `…/tf-block-runner`) | THEN (phase 4+, module `…/runner`) |
|---|---|---|
| tf domain + application engine + ports + meshapi/config adapters | `tf-block-runner/internal/tf` (package `tf`) | `runner/internal/tf` |
| git source acquisition (GitSource, auth, go-git/exec adapter) | `tf-block-runner/internal/gitsource` | `runner/internal/gitsource` |
| terraform/tofu binary install + tfexec + mock facade | `tf-block-runner/internal/tofu` | `runner/internal/tofu` |
| entrypoint | `tf-block-runner/main.go` (unchanged location — keeps meshfed-release `go run .` working, D10) | `runner/cmd/tf/main.go` (per-persona binary, D2) + the `cmd/bbrunner` superset |
| reporting facility seed (`progress`, `runLog`) | files inside `internal/tf` | extracted to `runner/internal/report` in **phase 3** (D4: runner-agnostic) |
| config loading | file inside `internal/tf` (tf-specific keys) | generalized into `runner/internal/config` in **phase 3** (D7) |
| meshapi client, crypto | `go-meshapi-client` module, unchanged | `runner/internal/meshapi`, `internal/crypto` (phase 3/4) |
| `util/` | **dissolved**: `SortedByKeys` becomes an unexported helper next to its only caller in `internal/tf` (D11: no `util`) | — |

The `gitsource`/`tofu` sibling split is justified per D11's "only if the seams prove
real": both packages exist to isolate the gate-excluded real-I/O adapter files
(`git.go`, `tfbinaries.go`, A4) behind consumer-side ports, and they give depguard a
package boundary to enforce (§5.7). `internal/tf` stays otherwise cohesive — no
domain/application/ports layer-cake directories (P3: packages map to concepts, not
layers; ports are just interface declarations in the consuming package).

Coverage-gate continuity: because the gate matches by import-path **prefix** (A2), the
thresholds line becomes `github.com/meshcloud/building-block-runner/tf-block-runner/internal 90`
when the move happens (§6 step 11), covering `tf` + `gitsource` + `tofu` minus the same
two per-file exclusions (paths updated in `exclusions.txt` in the same step). The
denominator is unchanged file-for-file, so plan 01 §8 arithmetic carries over.

### 5.2 Domain model (P8 — misuse-resistant types)

All in `internal/tf`; illustrative signatures only:

```go
type RunId string          // crosses package/API boundaries; no bare strings
type RunToken string       // opaque credential; fmt.Stringer redacts ("***")
type NodeSuffix string     // "worker-1" postfix used only on fetch (runapi.go:87)
type StepId string         // stepids.go constants become typed: const StepSources StepId = "sources"
type WorkspaceName string
// NewWorkspaceName replaces Run.toWorkspaceStr (run.go:75-98), keeping the "_" placeholders.
func NewWorkspaceName(workspace, project, platform *string, bbId string) WorkspaceName
```

- `Behavior`, `ExecutionStatus`, `DataType` move as-is (they are the P8 house pattern);
  their panicking/fatal stringers (B12) are preserved verbatim until 2b.
- `Run` becomes a **value type** (P4): it is immutable input, constructed once by the DTO
  mapping and safe to hand to goroutines. `Run.Source` holds the consumer-side `Source`
  interface (§5.4) instead of `*GitSource`. `Variable` stays a small value; its
  `decryptIfSensitive` gains a `Decryptor` parameter instead of the concrete
  `*MeshCertBasedCrypto` (`run.go:45`), preserving the B5 type-switch verbatim.
- `RunStatus.Steps` becomes `[]StepStatus` (**values**, not `[]*StepStatus`) — the P4/B10
  keystone, see §5.5. `StepStatus.UserMessage/SystemMessage` stay `*string` (genuinely
  optional in the API, P4 rule).
- `TfCmdParams` (`tfcmd.go:38-50`) dissolves: its fields are `Run` fields plus per-run
  derived data; the same-typed-string-soup constructor risk it embodies is removed by the
  `execution` struct (§5.4) whose fields are the named types above.

### 5.3 Application engine (replaces `Worker` + `SingleRunWorker` + `TfCmd` triplication)

```go
// Engine executes exactly one run: work goroutine (step pipeline) + observer goroutine
// (status ticker), unifying worker.go:93-264 and singlerunworker.go:50-214.
type Engine struct { /* cfg Config, tf TfProvider, reporter StatusReporter,
                        artifacts ArtifactSource, dec Decryptor, clock Clock,
                        workDir string, timeout, statusInterval time.Duration, log *log.Logger */ }

func NewEngine(cfg Config, deps EngineDeps) Engine        // deps = the ports, wired by main
func (e Engine) Execute(ctx context.Context, run Run) error
```

- `Execute` reproduces today's shared skeleton: temp `cmdDir` + `logs/`, init-fail status
  update on failure, build `execution`, spawn work+observer, wait. Divergences 4–6 of
  §3.3 are preserved by the two thin call sites, not inside the engine: the polling loop
  keeps the manager-side `MkdirAll` and the `"-----"` log line; the single-run path keeps
  its own `MkdirAll` and error returns (incl. the missing `sendInitFail` on `MkdirAll`
  failure — quirk preserved).
- **Step pipeline** replaces the three `TfCmd` types: one table
  `stepsFor(b Behavior, async bool) []step` (collapsing the triplicated `initRunSteps`
  literals — ids/display names verbatim) and one driver running
  `step{id StepId, displayName string, run func(*execution, TfFacade) error}` with the
  `advanceStep` boundaries exactly where today's `execute()` bodies put them. The
  behavior-specific tails (saved-plan replay, plan-artifact capture, async-apply hack +
  workspace delete) are the per-behavior final steps. The B13 hint asymmetry is
  **parametrized per behavior** (`initFailureHint(b Behavior) string` returning "" for
  APPLY) — deliberately awkward, so 2b's fix is a one-liner and phase 2 changes no pinned
  message.
- **Polling loop:** the manager token protocol is a pinned seam (plan 01 CP12/Q2), so
  `Manager` survives with the same channel protocol (`work/done/norun/failed/stop/
  stopped`), delays (10s/60s) and `Start/Stop` surface — de-globaled (takes `Config`,
  `RunSource`, `Engine`) and with `shutdownCalled` as `atomic.Bool` (§5.5). The poll
  worker is a ~40-line loop: fetch (keeping `handleFetchRunError` semantics verbatim,
  incl. the chunked-transport string match `worker.go:84`), `engine.Execute`,
  `ClearRunToken`, token out.
- **Single-run:** `main.go`'s `executeSingleRun` shrinks to: read file → parse DTO →
  single DTO→Run mapping (§5.4 Decryptor kills the `ToInternalWithoutDecryption` fork) →
  `engine.Execute`. Exit-code behavior (B11) preserved verbatim until 2b.
- `Worker`, `SingleRunWorker` (incl. the dead `NewSingleRunWorker`), `TfCmd`,
  `ApplyCmd/PlanCmd/DestroyCmd`, `GenericTfCmd` are **deleted** at the end of the
  sequence. Constructor-default pins (10s interval, timeout-from-minutes; plan 01 CP3
  pin 3) are retargeted to `NewEngine` defaults — a declared retarget, assertions
  (the values) unchanged.

### 5.4 Ports (D4) — consumer-side, defined in `internal/tf`

```go
// RunSource yields the next run to execute. Impls: API claim (polling), mounted file
// (single-run); in-memory arrives in phase 5.
type RunSource interface {
    Fetch(node NodeSuffix) (Run, error)
}

// StatusReporter is the run-status backchannel (register + patch). The returned abort
// flag reproduces RunUpdateResponseDTO.Abort (runapi.go:129-146).
type StatusReporter interface {
    Register(RunStatus) error
    Report(RunStatus) (abort bool, err error)
}
// This is the tf-side view of the shared `report.Reporter` (same signature:
// Register(RunStatus) error; Report(RunStatus) (abort, err)). In phase 2 its wire send stays
// a FULL snapshot (behavior-preserving — no HTTP transcript change; the phase-1 pins are
// untouched). The reduction to lean changed-steps-only diffs is deferred to phase 3 (plan 03),
// where it is a flagged, backend-result-identical wire change (endpoint upserts steps by id)
// and only the tf transcript pins move.

// ArtifactSource streams a predecessor plan artifact (runapi.go:51-53); consumed only by
// the APPLY saved-plan step.
type ArtifactSource interface {
    Download(url string, w io.Writer) error
}

// Decryptor replaces the meshcrypto.Crypto global (D4). Impls: certDecryptor (wraps
// MeshCertBasedCrypto) and NoopDecryptor{} (identity — single-run mode, where the
// controller already decrypted; replaces both the Crypto==nil check in authSsh.go:47
// and the ToInternalWithoutDecryption fork, see below).
type Decryptor interface {
    Decrypt(ciphertext string) (string, error)
}

// TfProvider abstracts binary install + tfexec handle creation (tfbinaries.go:79).
type TfProvider interface {
    Terraform(ctx context.Context, workingDir, version string) (TfFacade, error)
}
// TfFacade moves unchanged (tffacade.go:15-31) — it is already the consumer-side seam;
// tofu's concrete types (*tfexec.Terraform, *MockedTfFacade) satisfy it structurally.
//
// Design refinement to weigh against churn (validate when the seams are cut, STOP if it
// fights the pins), not a mandate to rewrite the pinned seam in one step:
//   - git side: the gitsource package should center on a cohesive `git.LocalRepo` value
//     type (package `gitsource`/`git`) whose repo path is a field and whose repo-management
//     operations are methods on it — clone/checkout and e.g. WalkFiles(callback) for the
//     source copy — rather than the current thin passthrough facade (P8/data+methods
//     cohesion). `Source.Materialize` becomes the operation that produces/uses a LocalRepo.
//   - tf side: challenge "TfFacade moves unchanged" — the tf domain's vocabulary is
//     Init/Plan/Apply/Destroy; prefer the domain owning those operations (the step table
//     already names them) over threading a generic tfexec handle through call sites. Keep
//     the facade only as the thin adapter over `*tfexec.Terraform`.

// Source materializes run sources into the working dir; kills the setLog late-mutation
// (gitsource.go:28-31) by passing the log sink as a parameter (P3/P4).
type Source interface {
    Materialize(dir string, log *RunLog) error
    Describe() string // for the "Attempt to copy sources from …" message
}

// Clock makes the manager delays and the init retry testable without wall time.
type Clock interface {
    Sleep(d time.Duration)
}
```

- **One adapter** (today's `RunApiClient`, de-globaled: constructor takes
  `Config`) implements `RunSource`+`StatusReporter`+`ArtifactSource`; the run-token
  lifecycle (`SetRunToken` on fetch, `ClearRunToken` after execution) stays a concrete
  method pair on the adapter, invoked by the polling loop exactly as today
  (`worker.go:50,56`) — the `Test_ClearRunToken_*` pins hold. The `RunApi` god-interface
  (`runapi.go:45-54`) is deleted; the fat interface becomes three small consumer-side
  ones (P3: each has ≥2 impls or a fake-transport test seam).
- **`Decryptor` kills the `ToInternalWithoutDecryption` fork** (`dtos.go:63-117`): with
  `NoopDecryptor`, the single `runDTOToInternal` mapping yields identical behavior in
  single-run mode — sensitive CODE/STRING/FILE decrypt to identity, other sensitive types
  skip the switch exactly as before (B5 semantics untouched in both modes; verified
  case-by-case against `run.go:45-56`). The CP9 fixture test targeting
  `ToInternalWithoutDecryption` is retargeted to `runDTOToInternal` + `NoopDecryptor`
  with identical field assertions (declared retarget; **STOP-B** if any assertion must
  change).
- **`RunContextInfo` replacement:** deleted, together with `runInfoContextKey`. Its
  content becomes an explicit `execution` struct built once per run in `Engine.Execute`
  and passed **as a parameter** to the step driver and observer — never through
  `context.Context`, which from then on carries only cancellation/deadline:

```go
type execution struct {
    run      Run
    dirs     runDirs    // workingDir, logFileName, artifactFilePath — typed, not stringly
    progress *progress  // §5.5
    log      *RunLog    // replaces logwrap (same file-backed behavior, B7 preserved)
}
```

### 5.5 Reporting: B10 and B6 fixed **by construction** — documented D13 exception

**Design:** the `runStatus`/`reportStatus` pair and `commitStatus`'s shallow copy
(`tfcmd.go:829-831`) are replaced by a single mutex-guarded tracker:

```go
type progress struct {
    mu     sync.Mutex
    status RunStatus // Steps []StepStatus by value
}
func (p *progress) mutate(f func(*RunStatus))  // work-goroutine writes
func (p *progress) Snapshot() RunStatus        // observer reads: deep copy under lock (P4)
```

The observer ticks on `Snapshot()` — an immutable value snapshot including the current
step's log segment (read at snapshot time, replacing the `logwrap.callback`-driven
`commitStatus`-on-every-write; the pinned observable — PATCH bodies containing live logs,
`Test_UpdatesStatusWithLiveLogs` — is preserved because every tick still sees all logs
written so far). With value-typed steps and copy-under-lock, **the B10 race is
unrepresentable**. Likewise the manager's `shutdownCalled` becomes `atomic.Bool` —
**B6 unrepresentable** — with identical token-protocol behavior.

**Why this is allowed in a "behavior-preserving" phase (D13 exception, argued):**

1. Neither race is *pinned*: plan 01 pins B6/B10 "functional behavior only — race itself
   not assertable and `-race` must stay off" (plan 01 §6, A5). No test flips in phase 2.
2. Preserving the races would require deliberately re-introducing shared mutable state
   into a design whose stated purpose (P4, high-level risk #4) is to remove it — the
   "however awkward" clause cannot reasonably extend to hand-crafting undefined behavior.
3. The 2b "test flip" for B6/B10 is therefore **turning `-race` on** (2b step R1, §7),
   which fails on the old code and passes on the new — the verification exists, it just
   lives one PR later because A7 keeps `-race` off until the inventory PR.

This structural elimination of B6/B10 is the **only** sanctioned in-phase-2 behavior
change — all other inventory bugs wait for 2b.

**STOP-D (for review):** this exception is called out in the phase-2 PR description; if
review rejects it, the fallback is mechanical (keep the shallow-copy publication and the
bare bool behind the same tracker API, flip both in 2b) — isolated to two types, no
sequence change.

**All other inventory bugs are preserved verbatim in phase 2** (with their
`// FIXME(bug):` comments moved alongside the code):

| Bug | Phase-2 treatment |
|---|---|
| B1/B2/B3 (workspace select/delete, `tfcmd.go:231-269`) | logic moves into the workspace step **unchanged**, incl. `return "", nil` swallow, wrong `buildingBlockId` return, delete-after-error |
| B4 (`time.Sleep(1000)` ns, `tfcmd.go:178`) | routed through the `Clock` port with the **same 1000ns argument**; 2b changes the duration |
| B5 (sensitive non-decryptable types) | `decryptIfSensitive` switch verbatim against the `Decryptor` port |
| B7 (`NewLogWrap` nil return) | `RunLog` constructor keeps the nil-on-error return |
| B8 (`context.Background()` in tofu download) | moves into `internal/tofu` unchanged (gate-excluded file) |
| B9 (`*g.path` nil deref) | moves into `internal/gitsource` unchanged |
| B11 (single-run exit 0 on failure) | `main.go` glue unchanged |
| B12 (`log.Fatalf` in `Behavior.str`) | moves unchanged |
| B13 (init-hint asymmetry) | parametrized per behavior, APPLY keeps "" (§5.3) |

### 5.6 Declared characterization-test retargets (the complete list)

Assertions never change; construction/target changes only — anything beyond this list is
**STOP-B**:

1. Suite harness: `AppConfig = TfRunnerConfig{…}` global assignment and struct-literal
   `Worker`/`RunApiClient` construction (`worker_scenario_test.go:83-98,120-131`) →
   construct `Config` value + engine/adapter via constructors (one helper, per plan 01
   CP1's extraction).
2. `meshcrypto.Crypto` install/restore helpers (plan 01 CP1) → pass
   `certDecryptor`/`NoopDecryptor` into the harness constructor.
3. `SingleRunWorker.ExecuteRun` / `Worker.work()` entrypoints → `Engine.Execute` /
   polling-loop entry (same inputs, same observable outputs).
4. Constructor-default pins (CP3 pin 3: 10s interval, minutes→timeout) → `NewEngine`
   defaults.
5. `ToInternalWithoutDecryption` fixture test (CP9 / pin 16a) → `runDTOToInternal` +
   `NoopDecryptor`, identical field assertions.
6. CP12 manager tests: same token protocol on the de-globaled `Manager` (constructor
   signature changes; channel/timing assertions unchanged; `handoutWorkerToken` via
   injected `Clock` with zero delay as today).
7. CP10 auth seam tests: move with `authSsh.go` into `internal/gitsource`;
   `unwrapCert`'s `Crypto == nil` case becomes the `NoopDecryptor` case.

### 5.7 Lint/depguard growth (D11/D14)

depguard rules added in the final move step: `internal/gitsource` and `internal/tofu`
must not import `internal/tf` (adapters don't import the consumer); `internal/tf` must
not import `internal/tofu` (it sees only `TfProvider`/`TfFacade`; test files exempt for
wiring, same as `main.go`). `internal/tf` **may** import `internal/gitsource` in phase 2
— the DTO→`Run` mapping (an adapter file inside `tf`) constructs the concrete source
(today `dtos.go:187-200`); tightening this (moving the mapping out) is phase-3 work with
the shared client. The *mechanical* `.golangci.yml` `tfrun` exclusion entries (paths now
dead or fixed by the restructure) are deleted; entries whose fix is behavioral
(errcheck-class production pins) move to the 2b obligations list.

---

## 6. Migration sequence (phase 2) — always-compiling, always-green

Rules: after every step, `task test` + `task lint` green and `task coverage` ≥ gate
(record the number per step in the working-branch commit message; squashed on merge).
Steps 2–8 restructure **inside package `tfrun`** first (dependency-breaking before
moving); steps 9–11 do the package moves last, when the code is already shaped — this
keeps every intermediate diff reviewable and the gate arithmetic stable.

| # | Step | What moves/changes | What proves it |
|---|---|---|---|
| 0 | **Preflight.** Run all §1 verifications on the phase-1 branch; branch `phase-2-tf-ddd-refactor` off it. | nothing | all A1–A10 verified; coverage number recorded (STOP-A/-A2 gates) |
| 1 | **Harness funnel.** Ensure/extend the single test-construction helper (per CP1) so every suite builds worker+api+config through one function; rename `Test_UseCustomPredicate_*` (F4, plan-01-sanctioned phase-2 cleanup). | test files only | suite green; `git diff -- '*.go' ':!*_test.go'` empty for this step |
| 2 | **Decryptor port.** Introduce `Decryptor` + `certDecryptor` + `NoopDecryptor`; thread through `decryptIfSensitive` call sites (`tfcmd.go:472,534,599`) and `authSsh.unwrapCert`; `main.go` wires cert (polling) / noop (single-run); delete all `meshcrypto.Crypto` reads from `tfrun`; delete `var Crypto` from `go-meshapi-client/crypto` (flagged cross-module edit, §10). | `run.go`, `tfcmd.go`, `authSsh.go`, `main.go`, crypto pkg | suite green (decrypt scenarios CP4 esp.); grep: zero `meshcrypto.Crypto` refs |
| 3 | **Kill the DTO fork.** Delete `ToInternalWithoutDecryption` (`dtos.go:63-117`); single-run path uses `runDTOToInternal` + `NoopDecryptor`; retarget the CP9 fixture test (§5.6.5). | `dtos.go`, `main.go` | single-run suite (CP3) green; retargeted test asserts identical fields |
| 4 | **Config de-global.** `LoadConfig(logger) (Config, error)` returns a value; every reader (§3.1 item 1 list) takes `Config` (or the needed field) via constructor/params; delete `var AppConfig`; harness constructs `Config` (§5.6.1). | `config.go` + ~20 call sites + suites' setup | suite green; grep: zero `AppConfig`; CP9 config tests green against the returned value |
| 5 | **Explicit run context.** Replace all six `ctx.Value(runInfoContextKey)` sites with an explicit `*RunContextInfo` parameter (constructors + goroutines); delete `contextKey`/`runInfoContextKey`. | `worker.go`, `singlerunworker.go`, `tfapply.go`, `tfplan.go`, `tfdestroy.go` | suite green; grep: zero `context.WithValue`/`ctx.Value` in tfrun |
| 6 | **Reporting tracker.** Introduce `progress` (snapshotting, `[]StepStatus` values) + `RunLog`; observer/final-update use `Snapshot()`; delete `commitStatus`'s shallow copy and the write-callback publication. **B10 structurally fixed — STOP-D review flag.** | `runstatus.go`, `runcontextinfo.go`→`execution`, `tfcmd.go`, both observers | suite green, esp. `Test_UpdatesStatusWithLiveLogs`, async pins (CP7), abort pin |
| 7 | **Unify the step pipeline.** One `stepsFor(behavior, async)` table + one driver; behavior tails as final steps; B1–B3/B13 verbatim with FIXME comments; delete `TfCmd`, `GenericTfCmd`, `ApplyCmd/PlanCmd/DestroyCmd` types. | `tfcmd.go`, `tfapply.go`, `tfplan.go`, `tfdestroy.go` | suite green: step ids/display names, advanceStep log segmentation (SystemMessage contents), CP5/CP6/CP7 pins byte-identical |
| 8 | **Engine + ports.** Extract `Engine.Execute`; split `RunApi` into `RunSource`/`StatusReporter`/`ArtifactSource`; polling loop (Manager kept, `atomic.Bool` — **B6, STOP-D flag**) and single-run main drive the engine; delete `Worker`, `SingleRunWorker`, `RunApi` interface; retargets §5.6.3/.4/.6. | `worker.go`→`poll.go`, `singlerunworker.go` deleted, `manager.go`, `runapi.go`, `main.go` | full suite green incl. CP2 fetch-error paths, `Test_ClearRunToken_*` cycle, CP12 protocol; coverage ≥ gate (dedup shrinks denominator — verify, STOP-C) |
| 9 | **`internal/gitsource` split.** Move `gitsource.go`, `git.go`, `auth*.go`, `mockedgitfacade.go` (+ their tests); `Source` interface consumed by `tf`; `setLog` replaced by log parameter; update `exclusions.txt` path for `git.go`. | new package; `dtos.go` mapping imports it | suite green; CP10/CP11 tests moved & green; `task coverage` unchanged denominator |
| 10 | **`internal/tofu` split.** Move `tfbinaries.go`, `mockedtffacade.go` (+ tests); `tf` consumes `TfProvider`/`TfFacade`; update `exclusions.txt` for `tfbinaries.go`. | new package | suite green; B8 comment moved; coverage unchanged |
| 11 | **`tfrun` → `internal/tf`.** Move the remainder; dissolve `util/` (`SortedByKeys` inlined, `util/` deleted); update `main.go` imports, `thresholds.txt` (prefix `…/tf-block-runner/internal 90`), depguard rules (§5.7), delete mechanical lint exclusions. | package rename/move | `task test`+`task lint`+`task coverage` green with the **new** thresholds line; induced-failure check of the new prefix (temporarily set 99 → fails → revert) |
| 12 | **Self-review gate + PR.** P1–P8 walk (esp. P8: no free-floating string params survived; P1: pitfall comments from `tfcmd.go` preserved verbatim); confirm `FIXME(bug)` count unchanged (B-inventory intact); PR description lists the STOP-D exception and the §10 cross-module edit. | — | reviewer checklist; `grep -c "FIXME(bug)"` equals phase-1 count |

12 steps + preflight — within the ≤~15 budget. If any step's coverage check trips
(STOP-C), the sequence pauses; the expected riskiest point is step 8 (deleting the
duplicated worker halves both numerator and denominator — projected net-positive since
the duplicate was fully covered by then).

**Phase-2 runtime smokes (step-12 exit criteria):** two manual runtime smokes are required
before the phase-2 PR merges:
1. a local-dev-stack acceptance run in **polling mode** (per plan 00 §6 step 9);
2. a **single-run** smoke — run the binary with `EXECUTION_MODE=single-run` +
   `RUN_JSON_FILE_PATH` pointing at a fixture run JSON, and confirm it executes and reports.

Rationale: the coverage gate cannot reach `main.go` wiring and local-dev-stack only
exercises the polling path, so the next runtime check of single-run wiring would otherwise
slip to phase 4. Evidence (log excerpts / exit codes) recorded in the phase-2 PR
description.

## 7. Phase 2b — bug-fix pass (own stacked PR)

One PR on `refactor/single-go-binary/phase-2b-bugfixes`, working through plan 01 §6.
Per entry: fix → test flip → risk. Ordered so the race verification guards the rest and
fixes cluster by area. "Moot" = structurally eliminated in phase 2.

| Order | Bug | Fix | Test flip | Risk |
|---|---|---|---|---|
| R1 | **B6, B10 — moot** (eliminated by §5.5) | none — enable `-race` in `task test:tf-block-runner` + the `go-runners-ci` tf leg (ends A7) | the flip **is** `-race` turning on and staying green; remove the two functional-only pin annotations | low; if `-race` finds a *third* race, that is a new inventory entry — STOP, extend this plan |
| R2 | B1 (`tfcmd` workspace-select swallow) | propagate the `WorkspaceSelect` error | CP5 pin: select error now fails the run, `WorkspaceNew` **not** called | low — failure was previously masked into silent state-split |
| R3 | B2 (returns `buildingBlockId` not `ws`) | return the matched workspace name | CP5 pin: `WorkspaceDelete` receives the full `ws.proj.platform:bbId` name; DESTROY actually deletes | **medium**: changes a D9-pinned behavior (workspace delete naming) — deletes workspaces that were previously orphaned; note to meshfed-release maintainers (plan 01 §12) |
| R4 | B3 (delete continues after select error) | return after the error | CP5 pin: no `WorkspaceSelect("default")`/`WorkspaceDelete("")` after list error | low |
| R5 | B4 (1000ns sleep) | `clock.Sleep(time.Second)` through the phase-2 `Clock` port | CP6 retry pin gains an asserted 1s on the fake clock (no wall time) | none (fake clock) |
| R6 | B5 (sensitive non-decryptable types pass ciphertext) | decrypt **every** sensitive value regardless of `DataType` (drop the switch), fail fast on error | flip the CP4 `FIXME(bug): B5` pin: sensitive BOOLEAN now decrypts (or fails run on bad ciphertext) | **medium**: runs that "worked" by feeding ciphertext into tfvars now behave differently; customer-visible — call out in release notes |
| R7 | B7 (`NewLogWrap` returns nil) | return `(nil, error)`; engine turns it into a clean init-fail | flip the CP9 pin + add an engine-level init-fail assertion | low |
| R8 | B12 (`log.Fatalf` in stringer) | `String()` returns `"UNKNOWN"`; callers needing errors use `DetermineBehavior` | flip the B12 pin (no process exit path left) | low |
| R9 | B13 (init hint missing for APPLY) | hint for all three behaviors (delete the phase-2 parametrization) | flip the CP6 asymmetry pin: APPLY logs `HINT_INIT_FAILED` too | low (message-only) |
| R10 | B9 (`*g.path` nil deref) | guard the nil in the log statement | new unit test in `gitsource` (was inventory-only) | none |
| R11 | B8 (`context.Background()` in tofu download) | pass the caller's ctx | none required (gate-excluded file); optional opt-in e2e note | none |
| R12 | B11 (single-run failure exits 0) | exit non-zero **only for failures before the first potentially state-mutating step** (workdir setup / run-JSON parse / registration — before `tofu init`/`apply` begins); once a run has begun applying, keep **exit 0** even if the final terminal-status PATCH fails (today's hung-run behavior) | new `main`-adjacent test where feasible; otherwise documented decision + acceptance-suite check | **high-care**: the controller's Job template uses `BackoffLimit: 1` + `RestartPolicy: Never` (`run-controller/controller/kubernetes.go:135,151`), so a blanket non-zero exit would make k8s **re-run a failed terraform run once** — double execution. The scoped condition avoids that: re-triggering stateful terraform is a user action, never an automatic k8s Job re-run, and the meshapi client's retry/backoff makes a lost final PATCH unlikely. The full solution (BackoffLimit alignment) is a controller change → phase 5 note. This is narrower than the "no terminal status reported" framing in `PLAN_DETAIL_05` §16.3 — a failed final PATCH on an already-applying run does **not** force non-zero exit. |
| R13 | Cleanup | delete the remaining behavioral `.golangci.yml` `tfrun`-successor exclusions (errcheck-class production pins, plan 00 §5.3 category 2) fixing each site; remove all `FIXME(bug)` markers; confirm `grep -c "FIXME(bug)"` = 0 | lint green with the block gone | low; each errcheck fix is reviewed as its own hunk |

Exit: inventory empty, `-race` on, no `FIXME(bug)` markers (D13/phase-2b exit criteria).

## 8. Frozen contracts touched (D9/D10)

**Phase 2 modifies none.** It re-exercises (and must keep byte-identical): final-status
mapping incl. async `IN_PROGRESS`; abort-PATCH cancel; 10s ticker; runToken precedence +
`ClearRunToken` cycle; 409-register = success; 404/409-claim = no run; media type +
runner headers incl. the `<uuid>-worker-N` fetch node-id (the `NodeSuffix` must survive
the `RunSource` port); 128MiB artifact cap; backend fallback + `TF_HTTP_*` ephemeral auth
(the `tfcmd.go:281-327` pitfall comments move verbatim, P1); pre-run script contract;
tfvars/`meshStack_run_vars.tf` rules; env whitelist; decrypt-failure UX; workspace naming
(bugs and all); k8s single-run contract (`RUN_JSON_FILE_PATH`, `RUNNER_UUID`,
`RUNNER_API_URL`, `EXECUTION_MODE=single-run`, runToken-only auth — `config.go:52-66`,
`main.go:19-22`); healthz on `PORT` default 8100 (`main.go:89-110`); `go run .` from
`tf-block-runner/` (D10, main.go stays put).

**Phase 2b knowingly updates two pinned behaviors:** B2 (workspace-delete naming — a D9
pin whose pinned form was the bug; D13 makes 2b the sanctioned place to change it) and
B11 (single-run exit semantics, adjacent to the frozen k8s Job contract — hence the R12
STOP). Both are called out in the 2b PR description.

## 9. Rollback story

Both phases are single squash commits on stacked branches: reverting the 2b commit
restores all pinned-buggy behavior with the phase-2 architecture intact; reverting the
phase-2 commit restores the phase-1 tree exactly (tests included — retargeted harness
code lives in the same commit as the restructure). No image names, ports, env vars,
config keys, API surfaces, or k8s contracts change in phase 2, so rollback is purely
local. The one cross-module edit (deleting `crypto.Crypto`) is inside the same repo and
commit; `go.work` keeps the modules in lock-step, and no published tag of
`go-meshapi-client` is consumed externally from this repo's runners.

## 10. Cross-repo touch points

- **meshfed-release:** none in code. `local-dev-stack` starts the tf runner via
  `go run .` in `tf-block-runner/` — unaffected (main.go location unchanged). After 2b,
  note B2 to its maintainers: DESTROY now really deletes workspaces (long-lived
  local-dev state changes shape).
- **terraform-provider-meshstack:** patterns only (D3); no edit.
- **meshStack/meshfed API:** none; fake transports keep replaying today's shapes.
- **In-repo, cross-module (flagged):** deleting `var Crypto` from
  `go-meshapi-client/crypto` (step 2) — a shared-module edit inside the tf-phase PR.
  Justified: after step 2 it is provably dead (§3.1 item 2: only tf-block-runner ever
  used it), and leaving a mutable global in a "no package-level mutable state" exit-gate
  phase is self-defeating. Alternative (defer to phase 3) is acceptable if review prefers
  strict module scoping — one-line change either way.

## 11. Flags — findings the high-level/prior plans did not anticipate

1. **`NewSingleRunWorker` is dead production code** (only `NewSingleRunWorkerWithApi` is
   called, `main.go:146`) — yet plan 01 CP3 pins its constructor defaults. The retarget
   (§5.6.4) covers it; worth knowing that phase 1 will pin a constructor nobody calls.
2. **The k8s Job `BackoffLimit: 1`** (`kubernetes.go:135`) turns the "obvious" B11 fix
   (non-zero exit) into a double-execution hazard — plan 01's "non-zero exit or explicit
   decision documented" understated this; §7 R12 resolves it with a conditional exit and
   a STOP-for-review.
3. **`EXECUTION_MODE` is not set by the controller** (`buildEnvVars`,
   `kubernetes.go:617-635` sets only `RUN_JSON_FILE_PATH`/`RUNNER_UUID`/`RUNNER_API_URL`);
   single-run mode is selected via job-template `Env`/image config. The frozen-contract
   wording in D9 should note this: the mode switch is deployment config, not a controller
   guarantee.
4. **A third package-level global** exists beyond the two the plan names:
   `meshapi.runnerName`/`runnerVersion` (`client.go:26-40`). Deferred to phase 3 with
   justification (§3.1 item 3) — phase 3's plan must pick it up.
5. **The D13 exception for data races** (B6/B10, §5.5) is new policy this plan adds:
   "pin everything" cannot pin a race, and "preserve behavior" cannot preserve undefined
   behavior. B6/B10 are fixed structurally in phase 2 (the only sanctioned in-phase-2
   behavior change), called out in the PR (STOP-D). See §5.5.
6. **The timeout-message quirk** (`tfcmd.go:116` prints `TfCommandTimeoutMins` while the
   engine's actual timeout is a separately-plumbed duration) becomes *visible* once
   config is injected — preserved verbatim (the message keeps printing the config value);
   recorded so nobody "fixes" it silently during step 4.

## 12. Open questions

None open. Three judgment calls are recorded as reviewable STOPs at their decision sites:
the B6/B10 structural-fix exception (STOP-D, §5.5), the B11 exit-code condition (R12, §7),
and the `crypto.Crypto` cross-module deletion (§10).
