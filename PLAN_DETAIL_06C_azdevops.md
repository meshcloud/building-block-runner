# Detail Plan 06C — Azure DevOps Runner Port (Phase 6, PR 3)

**Phase:** 6c · **Branch:** `refactor/single-go-binary/phase-6c-azdevops` (stacked on
`refactor/single-go-binary/phase-6b-gitlab`) · **Delivery:** one single-commit PR ·
**Binding:** umbrella `PLAN_DETAIL_06_kotlin_ports_umbrella.md` (§5 template contract,
§7 consistency rules, §8 frozen contracts) + `PLAN_HIGH_LEVEL.md` §3 P1–P8, D5, D6
(Kotlin corollary), D7, D9, D11 (`internal/azdevops`), D12 (port 8101), D15, D16 —
authored in parallel with 06B/06D against umbrella + 06A per the umbrella §6 protocol.

Kotlin references are `main` @ `c3fce61`; Go references marked *post-N* are shapes
promised by plan N or template artifacts promised by 06A/06B. This runner carries the
**heaviest pin load of the four**: `AzureDevOpsPipelinePoller`, `AzureDevOpsStatusUpdater`
and `AzureDevOpsStatusMapper` have zero direct Kotlin tests today (umbrella §3.3).

## 1. Assumptions from prior phases

Plans 00–05 and sub-plans 06A/06B are **not implemented yet**. Implementation begins by
running **all umbrella §1 verification steps (A1–A12)** — incorporated by reference —
plus the 06C-specific ones below. Any material failure is a **STOP** per umbrella STOP-A.

| # | Assumption | Promised by | Verification step |
|---|---|---|---|
| C1 | The azure-devops module and block-runner-core are byte-identical to `main` @ `c3fce61` (all §2 file:line citations hold). | Plans 00–06B scope (umbrella A10) | `git diff main..phase-6b-gitlab -- azure-devops-block-runner/ block-runner-core/` — empty |
| C2 | 06A template artifacts exist: the unified `report.Reporter{Register(RunStatus) error; Report(RunStatus) (abort bool, err error)}` (stateless, link-based run-scoped client, `{sourceId}` substitution; handlers call `Report(RunStatus)` with only changed/new steps and **discard the abort return**) marshaling the lean `meshapi.SourceUpdateDTO`/`StepUpdateDTO` PATCH body (both fields `omitempty`) as the wire body, the shared `ClaimClassifier` (404/409/other ⇒ no-run, backoff 0), `config.SingleRunMode`, `config.BlockRunnerCompat` (+`privateKey`/`privateKeyFile` fields), `config.ResolvePrivateKey`, the Dockerfile final-stage pattern, the R12 single-run exit tail, `UseNumber` decode on the claim/file path. | 06A §4.3, §6.3–6.5, §7, §8 | read `runner/internal/{meshapi,report,config}`; run 06A's transcript + alias tests; `grep -rn "UseNumber" runner/internal` |
| C3 | block-runner-core wire pins C-P1–C-P7 exist and are green (claim/register/update transcripts) — 06C verifies, never re-writes (umbrella §3.3 last row). | 06A §3.3 | `./gradlew :block-runner-core:check`; grep the pin test names |
| C4 | 06B shipped `ExternalCallError{UserMessage, SystemMessage, StatusCode, RequestUrl, ResponseBody}` (the `MeshHttpException` twin, 06A §4.4: "specified in 06A, implemented in 06B with its first consumer") and `meshapi.DecryptInputs` (input-only decryption: sensitive STRING/CODE/FILE decrypted, other sensitive types logged + left as-is, impl secrets untouched — umbrella §4 row 8). | 06A §4.4/§17; umbrella §4 rows 8+14 | read the 06B-added types in `runner/internal/{meshapi,gitlab-shared location per 06B}`; run their tests. **STOP-C4:** if 06B did not land them (e.g. 06B descoped), 06C ships both to the 06A-specified contracts and flags the ownership move — an umbrella §4 row-8/14 owner correction, reviewed, not a silent fork |
| C5 | `meshapi.AzureDevOpsImplementation` models everything the Kotlin service reads: `AzureDevOpsBaseUrl`, `Organization`, `Project`, `PipelineId`, `PersonalAccessToken`, `Async bool`, `RefName *string` (`go-meshapi-client/meshapi/dtos.go:155-164`, moved by plan 04) — cross-checked §2.6. | Plan 03/04 moves | read `runner/internal/meshapi/dtos.go` |
| C6 | `dispatch.RunHandler`/`ClaimedRun` per plan 05 §4.2 incl. "the handler owns its execution timeout" — the 30-min poll bound lives inside `Execute`, no loop-level timeout exists (reconciliation §4.4). | Plan 05 §4.2/§17 | read `runner/internal/dispatch`; confirm no deadline is imposed on `Execute` by loop or dispatcher |
| C7 | The Kotlin azdevops suite is green as-is: `./gradlew :azure-devops-block-runner:check` passes on the phase-6b branch (pinning builds on it). | Current `main` CI | run the gradle task once before writing pins |
| C8 | `crypto.MeshCertBasedCrypto` decrypts the PAT fixtures meshStack produces (algorithm parity A9); the controller's AzDO branch (`run-controller/controller/decryption.go:97-111`) keeps decrypting `personalAccessToken` for k8s mode unchanged. | umbrella A9, plans 03–05 scope | run `crypto` tests; `git diff main..phase-6b-gitlab -- run-controller/controller/decryption.go` (path per plan-04 moves) — semantics unchanged |

**STOP markers.** Umbrella STOP-A–E apply verbatim; here they bite at: STOP-A = §9
step 0; STOP-B = any §3 pin that cannot be ported per §10's mapping; STOP-C =
`internal/azdevops` (or a touched shared package) below 90 at any §9 checkpoint;
STOP-D = the §4.6 fit-check finds a template/interface misfit ⇒ reviewed umbrella/plan-05
revision, never a local workaround; STOP-E = §11's gate fails ⇒ the Kotlin module stays.
**STOP-C4** (above) is this plan's one sub-plan-specific stop.

## 2. Kotlin behavior inventory

Full study of `azure-devops-block-runner` (10 production files) deepening umbrella §3.2;
the umbrella §4 core map applies unchanged. Every coordinator- or ADO-visible behavior
with evidence.

### 2.1 Service orchestration (`AzureDevOpsBlockRunnerService.kt`)

1. **Claim-and-swallow:** any fetch exception ⇒ log `"Unexpected error while getting a
   block run."` + no-run (`AzureDevOpsBlockRunnerService.kt:19-24`) — the shared
   catch-all policy (umbrella §4 row 2).
2. **Register first:** exactly one step, `STEP_ID = "azure-devops-trigger"` (`:74-76`),
   display name `"Trigger Azure DevOps Pipeline"` (`:30-33`) — **before** implementation
   extraction, client construction, or any decryption. Register failures propagate
   uncaught (standalone: scheduler logs, run stays unreported; k8s: exit 1).
3. **Failure ladder after register** — each rung reports run `FAILED` + step
   `azure-devops-trigger` `FAILED` and returns:
   - wrong implementation type (`getImplementation` throws `IllegalStateException`
     "The building block implementation of run <uuid> was not of expected type.",
     `MeshBuildingBlockRun.kt:133-139`) ⇒ `updateFailedBlockStatusWithException` (`:35-40`);
   - client-factory failure — **PAT decryption error lands here** (`:42-47`);
   - trigger `MeshHttpException` ⇒ `updateFailedBlockStatusWithMeshException`; any other
     trigger exception ⇒ `…WithException` (`:49-57`).
4. **Trigger success update** (`AzureDevOpsStatusUpdater.kt:72-97`): run `IN_PROGRESS`,
   step `SUCCEEDED`, user `"Triggered Azure DevOps Pipeline. <extra>"`, system
   `"Triggered pipeline run <id>. View run: <webUrl>. <extra>"` where `<extra>` =
   `"Polling for completion status..."` (sync) / `"Will wait for API updates on
   status..."` (async). `webUrl` = `_links.web.href` ?? `url` ?? `"N/A"` (`:81`).
5. **Async split:** `implementation.async == true` ⇒ done (IN_PROGRESS handover, D9
   pin); `false` ⇒ `AzureDevOpsPipelinePoller.pollPipelineCompletion` (`:61-69`).

### 2.2 The poller (`AzureDevOpsPipelinePoller.kt` — zero tests today)

Loop semantics (`:29-70`), in order per iteration:

1. `triggerTime = Instant.now(clock)` at **poller start** (not the actual ADO trigger
   moment); budget = 30 min (`MAX_POLLING_MINUTES_PIPELINES`, `:77`).
2. While `state != COMPLETED` (`:35,73-75`): **timeout check first** — `now >
   triggerTime + 30min` ⇒ `updateFailedBlockStatusWithException(Exception("Pipeline
   polling timeout after 30 minutes"))` and return (`:36-41`); then
   `Thread.sleep(10s)` (`:43,78`); then `getPipelineRun` — **any error ⇒ warn + retry**
   (`continue`, back to timeout check; `:61-64`), errors are never fatal within budget.
3. On a successful run GET: `getPipelineTimeline`; success ⇒
   `updatePipelineAndStageStatuses` (§2.3); **timeline failure ⇒ fallback**
   `updatePipelineStatusDuringPolling` only when the run state changed since the last
   report (`lastReportedState` dedup, `:50-59`) — the dedup exists **only** on this
   fallback path.
4. Loop exit (COMPLETED) ⇒ `updateFinalBlockStatusFromPipeline` (§2.4). If the trigger
   response is already COMPLETED, the loop body never runs: final update immediately,
   zero GETs, zero sleeps.
5. **Outer catch-all** (`:67-70`): any escaped exception — including a **PATCH failure
   inside the updater** — triggers one `updateFailedBlockStatusWithException` attempt
   (itself a PATCH; if that throws too, it propagates to §2.1's caller: scheduler-log /
   exit 1).

### 2.3 Stage-step emission (`AzureDevOpsStatusUpdater.kt:118-171` — zero tests today)

- Stage selection: timeline records with `type == STAGE && parentId == null`, sorted by
  `order` (`:123-125`); `name` null ⇒ `"Unknown Stage"` (`:142`).
- **Empty stage list ⇒ run-state-only update** `updatePipelineStatusDuringPolling`
  (`:126-129`): run `IN_PROGRESS`, step `azure-devops-trigger` `IN_PROGRESS`, user =
  `mapPipelineStateToUserMessage`, system `"Pipeline run <id> state: <state.value>.
  View run: <webUrl>"` (`:99-116`) — sent **every poll** on this path (no dedup here;
  the `lastReportedState` dedup applies only to §2.2's fallback).
- Non-empty: the update **always re-includes the trigger step** first — `SUCCEEDED`,
  user `"Triggered Azure DevOps Pipeline"`, system `"Pipeline run <id>. View run:
  <webUrl>"` (`:132-139`) — note: *different* messages than §2.1.4's initial trigger
  update (no polling suffix).
- Stage dedup is **one-way**: a stage row is added when `!reportedStages.contains(id)
  || state == COMPLETED` (`:143`) — new stages once, **COMPLETED stages re-sent on
  every subsequent poll**. Step shape: id `ado-stage-<id>`, displayName
  `"Stage: <name>"`, status = `mapStageToStatus`, user = `mapStageToUserMessage`,
  system = `buildStageSystemMessage` (`:144-161`).
- The PATCH is sent **unconditionally every poll** when stages exist (`:165-170`) —
  even if it carries only the trigger step; run status is `IN_PROGRESS` on every
  intermediate update.

### 2.4 Final + failure updates (`AzureDevOpsStatusUpdater.kt`, `AzureDevOpsStatusMapper.kt`)

- **Final** (`:19-40`): run status = `mapPipelineResultToStatus` (SUCCEEDED ⇒
  SUCCEEDED, anything else incl. null ⇒ FAILED, `AzureDevOpsStatusMapper.kt:10-13`);
  **only the trigger step** is included (no stage steps), carrying the same final
  status — the trigger step can flip SUCCEEDED → FAILED here. User =
  `mapPipelineResultToUserMessage` (`:15-20`: succeeded/failed/canceled/else message
  set); system = `"Pipeline run <id> completed with state: <STATE>, result: <RESULT>.
  View run: <webUrl>"` — **Kotlin enum `toString` = enum NAMES** (`COMPLETED`,
  `SUCCEEDED`, possibly literal `null`), while §2.3's polling messages use `.value`
  camelCase (`inProgress`) — two rendering schemes (`:26` vs `:102`).
- **Failure** (`:42-70`): user always `"Could not trigger the Azure DevOps Pipeline"`;
  system = `"Request: <url>\nAzure DevOps responded with status: <code> and body:
  <body>"` (MeshHttpException) or `"There was an internal error while trying to
  contact Azure DevOps: <msg>"`. The **timeout** therefore reports user "Could not
  trigger…" + system "…internal error…: Pipeline polling timeout after 30 minutes" —
  misleading after a successful trigger, but UI-visible bytes (pinned as-is).
- **Stage mapping table** (`AzureDevOpsStatusMapper.kt:29-48`): PENDING/IN_PROGRESS ⇒
  IN_PROGRESS; COMPLETED+SUCCEEDED|SKIPPED ⇒ SUCCEEDED; COMPLETED+FAILED|CANCELED|
  ABANDONED ⇒ FAILED; **COMPLETED+SUCCEEDED_WITH_ISSUES or COMPLETED+null ⇒
  IN_PROGRESS** (the `else` hole — such a stage step never reaches a terminal status);
  null/other state ⇒ IN_PROGRESS. Stage user messages (`:50-58`) incl. the `else`
  branch: COMPLETED+CANCELED/ABANDONED renders `"<name>: completed"` (state.value, not
  the result). System message composition with optional `Result/Started/Finished`
  parts (`:60-66`).

### 2.5 The ADO client (`client/AzureDevOpsClient.kt`, `client/AzureDevOpsClientFactory.kt`)

- **Factory** (`AzureDevOpsClientFactory.kt:13-26`): PAT =
  `decryptionService.decrypt(implementation.personalAccessToken)` (unconditional;
  standalone = cert crypto, k8s profile = NoOp); the client's run copy =
  `decryptBlockRunInputs(run)` — sensitive STRING/CODE/FILE decrypted, other sensitive
  types logged + left encrypted (`MeshCertDecryptionService.kt:58-97`). The PAT itself
  never enters the payload (auth header only) — the §7.6 asymmetry.
- **Trigger** (`AzureDevOpsClient.kt:59-88`): POST
  `{base}/{org}/{project}/_apis/pipelines/{pipelineId}/runs?api-version=7.1`, JSON body
  `{templateParameters, resources?}` with Jackson `NON_NULL` inclusion (`:39-43`).
  `templateParameters` = inputs with `!isEnvironment`, each `key → value.toString()`
  (`:62-64`) — **environment inputs are excluded entirely** (no gitlab-style
  `variables` channel) and sensitive non-env inputs are included **decrypted**; plus
  `MESHSTACK_BEHAVIOR = behavior.name` (APPLY/DETECT/DESTROY, overwriting a same-keyed
  user input, `:66`). `resources.repositories.self.refName` only when `refName != null`
  (`:70`). Stringification is Kotlin/Java `toString` over Jackson-decoded values
  (§16.6). Non-2xx ⇒ `MeshHttpException(userMessage="Failed to trigger Azure DevOps
  pipeline", …, responseBody)` (`:78-85`).
- **GET run** `…/pipelines/{pipelineId}/runs/{runId}?api-version=7.1` (`:90-112`);
  **GET timeline** `…/build/builds/{runId}/timeline?api-version=7.1` — the *build* API
  keyed by the pipeline-run id (`:117-139`), records unwrapped from
  `TimelineResponse.records`.
- Headers: `Accept: application/json`; auth `Basic base64(":" + PAT)` — empty username
  (`:141-144`). OkHttp with `followRedirects(false)` (`:45-48`) and OkHttp default
  connect/read timeouts (10 s each) — a redirect or a hung read surfaces as an error,
  never silently followed/blocked. Base URL is **not** sanitized — no
  `UrlSanitizerService` in this module (grep-verified; umbrella §4 row 13 erratum,
  §16.7); a trailing slash yields double-slash URLs, sent as-is.
- DTOs (`client/PipelineRun.kt`, `client/Timeline.kt`): `@JsonValue` enums —
  `PipelineRunState` {unknown, inProgress, canceling, completed}, `PipelineRunResult`
  {unknown, succeeded, failed, canceled}, `TimelineRecordState` {pending, inProgress,
  completed} (nullable, **no UNKNOWN member** — an unrecognized state string fails the
  timeline parse ⇒ §2.2's fallback path), `TimelineRecordResult` {succeeded,
  succeededWithIssues, failed, canceled, skipped, abandoned},
  `TimelineRecordType.STAGE = "Stage"`. `FAIL_ON_UNKNOWN_PROPERTIES` disabled (`:54`).

### 2.6 Wiring, modes, config

- Scheduling: `@Scheduled(10s)` + `ImmediateRetryDecorator` standalone; **no decorator**
  under `kubernetes` profile; scheduling disabled under `test`/`kubernetes`
  (`BlockRunnerServiceConfiguration.kt:15-37`,
  `AzureDevOpsBlockRunnerSchedulingConfiguration.kt:10-13`). Single-threaded scheduler
  ⇒ a sync poll **blocks claiming for up to 30 min** — concurrency 1 by construction.
- k8s single shot: `SingleShotRunner` + `RUN_JSON_FILE_PATH`, exit 0/1 semantics as
  manual (06A §2.3); crypto = NoOp under the profile.
- Config (`runner-config.yml:17-28`, `application.yml:7-8`): `PORT` default **8101**;
  shipped defaults uuid `a9786b14-ecfe-44dd-b04c-2bcfd326aa23`, api url
  `${RUNNER_API_URL:http://localhost:8304}` (mux AZURE_DEVOPS_PIPELINE port, umbrella
  A11), `bb-api`/`guest`, blank API-key creds, **a baked dev `privateKey`**
  (`runner-config.yml:28`, umbrella §10.5). No runner-specific keys beyond core
  (umbrella §3.1); `PrivateKeyLoader.resolve` wired via
  `AzureDevOpsBlockRunnerCryptoConfiguration.kt:11-17`.
- Go DTO cross-check (C5): `meshapi.AzureDevOpsImplementation` and the Kotlin
  `MeshBuildingBlockAzureDevOpsImplementation` (`MeshBuildingBlockRun.kt:242-250`)
  agree field-for-field; `decryption.go:97-111` decrypts `personalAccessToken`
  if non-empty — matches the factory's k8s expectation. One strictness delta: Kotlin's
  `async: Boolean` is required at claim parse (missing ⇒ whole claim fails ⇒ swallowed
  no-run); Go's `Async bool` zero-values to `false` — §16.9.

### 2.7 Failure surfaces by mode (exit/reporting matrix)

| Failure | Standalone (scheduler) | k8s (single shot) |
|---|---|---|
| fetch/parse/claim error | caught ⇒ no-run, next tick (§2.1.1) | caught ⇒ exit 0, run never reported (umbrella §7.9 quirk) |
| register HTTP error | propagates; run stays claimed + unreported | propagates ⇒ exit 1 |
| impl-type / PAT-decrypt / trigger error | run FAILED + step FAILED reported (§2.1.3), return | same, then exit 0 (terminal status was reported) |
| poll GET errors within budget | retried, never fatal (§2.2.2) | same |
| 30-min timeout | FAILED with the §2.4 timeout messages | same, exit 0 |
| PATCH failure mid-poll | one FAILED-update attempt; if that fails ⇒ propagates (unreported) | same; propagation ⇒ exit 1 |
| happy sync / async | terminal per result / IN_PROGRESS handover | same, exit 0 |

## 3. Kotlin pin tests (tests-first step)

Tests-only commits in `azure-devops-block-runner` (`git diff -- ':!*Test*'
':!*Scenario*'` empty for this step), proven green by the existing `jvm-runners-ci` leg
before any Go code exists (umbrella §5.2). The block-runner-core wire pins C-P1–C-P7
exist from 06A (assumption C3) — verified, not re-written.

### 3.1 What already exists (kept, later ported per §10)

- `AzureDevOpsBlockRunnerServiceTest` — 3 tests: fetch-throws, no-run, sync
  trigger+poll delegation (mockk-verification only, thin —
  `AzureDevOpsBlockRunnerServiceTest.kt:40-92`).
- `AzureDevOpsClientTest` — 4 wiremock tests: trigger POST path + no-`resources`
  default, `resources.repositories.self.refName` when set, GET run path, GET timeline
  path (`AzureDevOpsClientTest.kt:35-152`).
- `AzureDevOpsRunnerKubernetesStartupScenario` — **boot smoke only**; unlike manual's
  it captures no wire interactions (`AzureDevOpsRunnerKubernetesStartupScenario.kt:32-35`;
  flag §16.8).

### 3.2 New pins — the umbrella §3.3 gap column closed

The Poller/StatusUpdater/StatusMapper trio is pinned at three levels. Kotlin's
`Thread.sleep` is not injectable (tests-only step — no production refactor allowed), so
poller pins are chosen to need **at most one real 10 s sleep each** (two slow tests
total); everything else runs sleep-free (timeout pin: fake `Clock` already past the
budget ⇒ returns before the first sleep; already-completed pin: loop never entered).
The Go twins use an injected clock and are fast (§10).

**Mapper pins (pure tables, `AzureDevOpsStatusMapperTest` — keep-as-unit per §5.2):**

| Id | Pin | Anchors |
|---|---|---|
| S-P1 | `mapPipelineResultToStatus`: succeeded ⇒ SUCCEEDED; failed, canceled, unknown, null ⇒ FAILED | `AzureDevOpsStatusMapper.kt:10-13` |
| S-P2 | result user messages, all 4 branches incl. `"…completed with unknown status"` | `:15-20` |
| S-P3 | state user messages: inProgress, completed, null, other (`"…state: <value>"`) | `:22-27` |
| S-P4 | `mapStageToStatus` **full cross-product**: pending/inProgress ⇒ IN_PROGRESS; completed+succeeded\|skipped ⇒ SUCCEEDED; completed+failed\|canceled\|abandoned ⇒ FAILED; **completed+succeededWithIssues and completed+null ⇒ IN_PROGRESS** (the else-hole quirk, §2.4); null state ⇒ IN_PROGRESS | `:29-48` |
| S-P5 | stage user messages incl. the else branch (`"<name>: completed"` for completed+canceled/abandoned) and null-state `"<name> is in unknown state"` | `:50-58` |
| S-P6 | `buildStageSystemMessage` composition: all-fields, result-only, no-times, state null ⇒ `"State: Unknown"` | `:60-66` |

**StatusUpdater pins (mockk `BlockRunClient` capture, `AzureDevOpsStatusUpdaterTest`):**

| Id | Pin | Anchors |
|---|---|---|
| U-P1 | trigger-success update, sync vs async message variants byte-identically (user + system incl. `"Polling for completion status..."` / `"Will wait for API updates on status..."`), run IN_PROGRESS + step SUCCEEDED | §2.1.4 |
| U-P2 | webUrl precedence: `_links.web.href` > `url` > `"N/A"` (three cases) | `AzureDevOpsStatusUpdater.kt:24,81,101,130` |
| U-P3 | stage emission: STAGE+parentId==null filter, order-sorted, `ado-stage-<id>` / `"Stage: <name>"` / `"Unknown Stage"`; Phase/Job/Task and child-stage records ignored | `:123-142,153-161` |
| U-P4 | trigger step re-included first in every stage update with the §2.3 no-suffix messages; run status IN_PROGRESS | `:132-139,165-168` |
| U-P5 | one-way dedup across successive calls sharing one `reportedStages` set: new stage once; **COMPLETED stage re-sent every call**; unchanged non-completed stage not re-sent; PATCH sent even when only the trigger step remains | `:143-163,165-170` |
| U-P6 | empty stage list ⇒ `updatePipelineStatusDuringPolling` shape (run+step IN_PROGRESS, state message, `state.value` rendering) | `:99-116,126-129` |
| U-P7 | final update: only the trigger step, status = mapped result both at run and step level (incl. a SUCCEEDED→FAILED flip of the trigger step), system message with **enum-NAME rendering** (`state: COMPLETED, result: FAILED`) and the null-result rendering (`result: null`) | `:19-40`, §2.4 |
| U-P8 | failed update: user `"Could not trigger the Azure DevOps Pipeline"`; MeshHttpException system `"Request: <url>\nAzure DevOps responded with status: <code> and body: <body>"`; generic system `"There was an internal error while trying to contact Azure DevOps: <msg>"` | `:42-70` |

**Poller pins (`AzureDevOpsPipelinePollerTest`, mockk client + updater capture):**

| Id | Pin | Anchors |
|---|---|---|
| P-P1 | timeout: fixed `Clock` already past 30 min ⇒ exactly one failed update `"…Pipeline polling timeout after 30 minutes"` (via the U-P8 generic wrapper), **no** GET, no sleep | `AzureDevOpsPipelinePoller.kt:36-41` |
| P-P2 | already-COMPLETED trigger response ⇒ final update immediately; zero GET run/timeline calls | `:35,66` |
| P-P3 | *(slow, ~10 s)* one iteration inProgress→completed: GET run, GET timeline, stage update, then final update — call order pinned | `:43-66` |
| P-P4 | *(slow, ~10 s)* GET-run failure ⇒ warn + retry, no FAILED update, poll continues; timeline failure ⇒ fallback state-only update **only on state change** (`lastReportedState` dedup) | `:50-64` |
| P-P5 | PATCH exception inside the updater escapes to the outer catch ⇒ one failed-update attempt; a second PATCH failure propagates out of `pollPipelineCompletion` | `:67-70` |

**Service/client/scenario pins:**

| Id | Pin | Anchors |
|---|---|---|
| A-P1 | async handover: `async=true` ⇒ register + exactly one update (U-P1 async shape), **zero** poll GETs, `processBlock` returns the run | §2.1.5; service test w/ wiremock |
| A-P2 | register-before-everything ordering; register failure propagates with **no** FAILED update | §2.1.2 |
| A-P3 | failure ladder: wrong impl type / factory (PAT-decrypt) failure / trigger 404 each ⇒ registered + FAILED update with the U-P8 messages, distinguishable system texts | §2.1.3 |
| A-P4 | trigger payload **field-by-field**: non-env inputs stringified (`value.toString()` — table: string verbatim, int, boolean, decimal, LIST `[a, b]`, CODE-object `{k=v}` Kotlin renderings), env inputs absent, sensitive non-env input **decrypted** in `templateParameters`, `MESHSTACK_BEHAVIOR` present + overwrites a same-keyed input, `resources` omitted vs `refName` shape, Jackson NON_NULL body | §2.5; extends `AzureDevOpsClientTest` |
| A-P5 | **leak pin (§7.6):** the trigger payload contains the PAT in neither encrypted nor decrypted form; auth header = `Basic base64(":"+PAT)` | §2.5; `AzureDevOpsClient.kt:141-144` |
| A-P6 | client error surface: non-2xx trigger/GET ⇒ MeshHttpException carrying status + response body + request URL; 302 is **not followed** (surfaces as non-2xx) | `AzureDevOpsClient.kt:45-48,78-85` |
| K-P1 | k8s captured-wire scenario (manual-scenario style, new): async run JSON via `RUN_JSON_FILE_PATH` ⇒ captured register + handover update, exit 0; PAT arrives plaintext and is **not** decrypted (NoOp crypto) | §2.6; `AzureDevOpsRunnerKubernetesStartupScenario` extended |
| K-P2 | k8s exit codes: update failure ⇒ exit 1; fetch/parse failure ⇒ exit 0 swallow (the Kotlin baseline the Go R12 delta is measured against, umbrella §7.9/§10.3) | §2.7 |
| F-P1 | factory: encrypted PAT fixture decrypted via `MeshCertDecryptionService`; inputs decrypted per the STRING/CODE/FILE-only branch rules (sensitive LIST stays encrypted — quirk pinned) | §2.5; `AzureDevOpsClientFactory.kt:13-26` |

28 pins total (6 S + 8 U + 5 P + 6 A + 2 K + 1 F) — the heaviest of the four ports, as
the umbrella predicts. Quirks pinned as-is, never fixed here (umbrella §5.2): the
misleading timeout user message, the succeededWithIssues else-hole, COMPLETED-stage
re-emission, the every-10s unconditional PATCH, the two enum-rendering schemes, the
unsanitized base URL, the K-P2 exit-0 swallow. All listed in §16.

## 4. Go handler design

Package `runner/internal/azdevops` (D11). Illustrative signatures only; the umbrella
§5.3 shape instantiated. The Kotlin class trio does **not** map 1:1 (D15/§7.13): the
mapper becomes pure functions, the updater dissolves into update-builder functions over
the unified `report.Reporter`, the poller becomes one ctx-aware loop function — three small
files (`handler.go`, `client.go`, `poll.go`) in one cohesive package (D11: no sibling
split; the seams are not real package seams).

### 4.1 Handler

```go
// package azdevops — the AZURE_DEVOPS_PIPELINE run handler: trigger, then
// IN_PROGRESS handover (async) or poll-to-terminal with stage steps (sync).
func NewHandler(cfg Config, deps HandlerDeps) Handler        // value type (P4)
func (h Handler) Execute(ctx context.Context, run dispatch.ClaimedRun) error

type HandlerDeps struct {
    Reporters ReporterFactory   // per-run report.Reporter, runToken-only (06A §4.3)
    Decryptor meshapi.Decryptor // cert-based (polling) | NoOp (single-run)
    HTTP      *http.Client      // external-API seam; fake transport in tests (§4.3)
    Clock     Clock             // poll waits + timeout budget; fake in tests
    Log       *slog.Logger      // D15; per-run via Log.With("run", run.Id)
}
```

`Execute` skeleton = the Kotlin skeleton, order preserved (§2.1): build per-run
reporter from `run.Details.Links` + `Spec.RunToken` → `Register("azure-devops-trigger",
"Trigger Azure DevOps Pipeline")` → unmarshal `meshapi.AzureDevOpsImplementation` from
`Details.Spec.Definition.Spec.Implementation` → decrypt PAT (`Decryptor`), decrypt
inputs (`meshapi.DecryptInputs`, C4) → trigger → report U-P1 → async ⇒ return nil;
sync ⇒ `pollCompletion` (§4.4) → return nil. Every rung of the §2.1.3 failure ladder
reports run FAILED + step FAILED via the reporter and returns **nil** (A1 contract);
only register/report transport failures return non-nil errors (run stays unreported,
Kotlin parity §2.7). Step id/display name are typed constants (umbrella §7.1).

### 4.2 External-API client (unexported, same package)

```go
type adoClient struct { baseUrl, organization, project, pipelineId, pat string; http *http.Client }
func (c adoClient) TriggerPipeline(ctx, params map[string]string, refName *string) (pipelineRun, error)
func (c adoClient) GetPipelineRun(ctx, id int64) (pipelineRun, error)
func (c adoClient) GetTimeline(ctx, id int64) ([]timelineRecord, error)
```

- URLs exactly as §2.5 (`?api-version=7.1`; timeline via `_apis/build/builds`); base
  URL used verbatim — **no sanitization** (§2.5 parity, flag §16.7). Auth header
  `Basic base64(":"+pat)`; `Accept: application/json`.
- Trigger body = an explicit payload struct with `omitempty`/pointer `resources` —
  byte-twin of the Jackson NON_NULL shape (frozen wire, umbrella §8). Parameter
  stringification via a package-local `renderValue` (§16.6): decoded with `UseNumber`
  (C2), strings verbatim, `json.Number` literal, bools `true`/`false`, arrays/objects
  rendered as **compact JSON** (**RULED (grill r2):** composite/exotic values emit JSON,
  not Kotlin collection-`toString` `[a, b]` / `{k=v}` — a deliberate flagged byte change,
  see §16.6) — pinned by the A-P4 table.
- `PipelineRun`/`Timeline` DTOs are **package-local** (umbrella §10.11): typed string
  states/results (`runState("completed")` etc.) with two rendering helpers — wire value
  and Kotlin-enum-NAME (for the U-P7 final message). Unknown strings are representable
  (unlike Kotlin enums): §4.5 defines the tolerant behavior per call site.
- Redirects not followed: `CheckRedirect` returns `http.ErrUseLastResponse` (OkHttp
  `followRedirects(false)` twin, A-P6). Timeouts mirror OkHttp defaults (~10 s
  connect/read via `http.Client.Timeout` per request) so a hung ADO endpoint becomes a
  retryable poll error instead of stalling the 30-min budget (§16.10).
- Non-2xx ⇒ `ExternalCallError` (06B type, C4) carrying status/URL/body; mapped into
  the U-P8 message pair by the handler.

### 4.3 Status mapping + update building

- `statusmap.go`-level **pure functions** (keep-as-unit tests, §5.2 criterion):
  `mapPipelineResult`, `mapResultUserMessage`, `mapStateUserMessage`, `mapStageStatus`,
  `mapStageUserMessage`, `stageSystemMessage` — byte-identical tables incl. the S-P4
  else-hole and S-P5 else branch (quirk-preserving; fixes are post-port follow-ups).
- Update builders produce `meshapi.SourceUpdateDTO` values (lean shape, 06A §4.3) for:
  trigger-success (sync/async variants), state-only polling, stage batch (trigger step
  first + stage rows), final, failed — each fed into the unified `report.Reporter`'s
  `Report(RunStatus)` (abort discarded) as the changed/new steps in `RunStatus.Steps`.
  The `reportedStages map[string]bool` and `lastReportedState` live in the **poll loop**
  (caller-side dedup — exactly the stateless unified-`report.Reporter` altitude 06A §17
  verified against this runner, the heaviest dedup consumer). **Grill r3 (RunHandler
  purity):** no Observer/ticker runs; the handler owns dedup and feeds only changed steps
  into `Report`, the backend upserts by id.

### 4.4 The poller (06C-local design, umbrella §6 "gaps deliberately left to owners")

```go
// pollCompletion drives a sync run to a terminal report. Kotlin semantics
// (AzureDevOpsPipelinePoller) with ctx-awareness per D15/§5.3:
func (h Handler) pollCompletion(ctx context.Context, c adoClient, rep runReporter, initial pipelineRun) error
```

- Constants pinned as constructor defaults (umbrella §5.3, tf-engine style):
  `pollInterval = 10s`, `pollBudget = 30min` — unexported `Handler` fields set in
  `NewHandler`, overridable only by tests (no config surface; Kotlin has none).
- Loop, Kotlin-ordered (§2.2): `deadline := Clock.Now().Add(pollBudget)` at poller
  entry (not trigger time — parity); while state ≠ completed: timeout check **before**
  the wait (over-deadline ⇒ report the P-P1 timeout failure, return nil); wait 10 s as
  `select` on `Clock.After` / `ctx.Done()`; GET run — error ⇒ log + `continue`
  (retry-forever-within-budget); GET timeline — success ⇒ stage-batch update with the
  one-way dedup (U-P5), empty-stages ⇒ unconditional state-only update (U-P6); timeline
  error ⇒ fallback state-only update only on state change (P-P4). Then the final update
  (U-P7). Already-completed initial run ⇒ final immediately (P-P2).
- **Error semantics:** a PATCH failure anywhere in the loop triggers exactly one
  failed-update attempt (P-P5); if that PATCH also fails, `pollCompletion` returns the
  error and `Execute` propagates it (non-nil = infrastructure, A1) — the Go twin of
  Kotlin's outer-catch + propagation.
- **ctx cancellation (new, D15). RULED (grill r2 — plan-05 H7 amendment, not a
  06C-local fork):** `ctx.Done()` during a wait ⇒ the poller reports the in-flight run
  with a **terminal** status before returning `ctx.Err()` wrapped — it emits `ABORTED`
  (now added to the Go `ExecutionStatus` enum; meshStack's status source defines
  `ABORTED` as terminal), falling back to `FAILED` if the endpoint rejects it, **never
  `SUCCEEDED`**. This supersedes the earlier "no further PATCH / stays IN_PROGRESS"
  design so the coordinator never sees a stale IN_PROGRESS until its long timeout.
  Consequence: graceful shutdown (`Loop.Stop` + `InProcess.Wait`) drains a
  **configurable grace period (default 120s)** and does not wait out a 30-min poll —
  the run context is cancelled, the terminal report is sent, and clear logs are emitted
  throughout. Wiring recorded in §7.1; flag 11 resolved (§16.11).
- **Timeout reconciliation (C6):** A1's "handler owns its timeout" is satisfied by the
  30-min budget being *inside* `Execute`; no `LoopConfig` deadline exists or is added.
  With `maxConcurrentRuns` default 1, a sync poll blocks further claims for up to
  30 min — Kotlin parity (§2.6 single-threaded scheduler), documented in the persona
  config comments; raising `maxConcurrentRuns` is the additive escape hatch.

### 4.5 Strictness deltas at parse boundaries (each flagged, none silent)

| Boundary | Kotlin | Go decision |
|---|---|---|
| impl JSON missing/`async` absent | claim deserialization fails ⇒ swallowed no-run ⇒ coordinator timeout (§2.6) | handler reports run FAILED with the U-P8 internal-error message naming the parse problem — a claimed run can no longer be un-claimed; strictly better UX, sanctioned delta §16.9 |
| unknown `PipelineRun.state` | GET-run parse fails ⇒ retried like any poll error until budget (§2.5) | tolerated: unknown ≠ completed ⇒ keep polling — same observable (poll continues); state message renders the raw value via S-P3's else branch |
| unknown timeline state/result | timeline parse fails ⇒ **fallback path** (state-only updates) | tolerated: mapper else-branches already yield IN_PROGRESS/message defaults — stage steps keep flowing instead of degrading to fallback; sanctioned delta §16.12 (06A flag-5 precedent) |

### 4.6 Template fit-check (umbrella §6 review protocol; deviations = STOP-D)

| 06A/05 template artifact | 06C usage | Fit |
|---|---|---|
| `RunHandler.Execute(ctx, ClaimedRun) error` (A1) | impl JSON from `Details`, PAT via `Decryptor`, poll loop inside `Execute` with own timeout | fits — no new parameter (confirms 06A §17 row 1) |
| `HandlerDeps` pattern (06A §4.1) | + `Decryptor`, `HTTP`, `Clock` — constructor-grown, shape unchanged | fits (06A §16.1 narrowing reversed as anticipated) |
| stateless unified `report.Reporter` (06A §4.3) | many partial updates via `Report(RunStatus)` (abort discarded); handler-side dedup feeds changed steps, no ticker/Observer | fits — statelessness confirmed for the unified `report.Reporter` at this, the heaviest consumer |
| lean `SourceUpdateDTO` (`status` + `steps` `omitempty`) | intermediate updates always carry status IN_PROGRESS + steps; final carries both | fits |
| `ExternalCallError` (06B, C4) | ADO non-2xx surface → U-P8 messages; needs `ResponseBody` + `RequestUrl` ✓ | fits; **inherited, not redefined** (STOP-C4 otherwise) |
| `meshapi.DecryptInputs` (06B, C4) | templateParameters need decrypted inputs, impl PAT stays out of payload | fits — the §7.6 asymmetry is structural |
| `ClaimClassifier`, `SingleRunMode`, `BlockRunnerCompat`, `ResolvePrivateKey`, Dockerfile stage, R12 exit tail | consumed verbatim; `BlockRunnerCompat.PrivateKey*` consumed (unlike manual) | fits |
| `UseNumber` decode (06A §4.2) | required for `renderValue` fidelity | fits — 06A recorded it as a template requirement for exactly this |

No template change required; anything discovered during implementation is a reviewed
umbrella/plan-05 revision (STOP-D).

## 5. Kotlin-isms → idiomatic Go (D15)

Umbrella §7.13 instantiated for every Kotlin-ism this module actually uses; parity notes
where the translation is not mechanical.

| Kotlin-ism (evidence) | Idiomatic Go replacement | Parity note |
|---|---|---|
| catch-all around fetch (`AzureDevOpsBlockRunnerService.kt:19-24`) | the shared `ClaimClassifier` (06A §7.1): every claim error ⇒ no-run-logged, backoff 0 | same observable; handler never sees claim errors |
| exception-typed failure fan-out (`updateFailedBlockStatusWithMeshException` vs `…WithException`, §2.1.3) | `errors.As` on `ExternalCallError` selects the U-P8 system-message form; all other errors take the internal-error form; message strings byte-identical (§7.11) | plumbing changes, bytes don't |
| `object` singletons `AzureDevOpsPipelinePoller`/`AzureDevOpsStatusMapper` (`AzureDevOpsPipelinePoller.kt:14`, `AzureDevOpsStatusMapper.kt:9`) | package-level pure functions + a `Handler` method for the loop — no singleton state (P3) | S-/P-pins |
| class `AzureDevOpsStatusUpdater` holding client + uuid (`AzureDevOpsStatusUpdater.kt:14-17`) | update-builder functions over the per-run unified `report.Reporter`; run id is a slog attr, not struct state | U-pins; no 1:1 class mirror (§7.13 review rule) |
| `Thread.sleep(10_000)` loop + `Clock` param only for timeout (`AzureDevOpsPipelinePoller.kt:24,43`) | `select` on injected `Clock.After(10s)` / `ctx.Done()`; one `Clock` governs wait **and** budget | Go tests are sleep-free where Kotlin pins needed 2 slow tests (§3.2) |
| mutable `reportedStages: MutableSet<String>` threaded through calls (`:33,52`) | loop-local `map[string]bool` owned by `pollCompletion` | U-P5 dedup semantics identical |
| `MeshHttpException` (`MeshHttpException.kt:5-13`) | `ExternalCallError` (06B, C4) | fields 1:1 (06A §17) |
| Jackson `@JsonValue` enums + strict parse (`PipelineRun.kt:18-34`, `Timeline.kt:21-49`) | package-local typed string values + rendering helpers (wire value / enum-NAME); tolerant at the §4.5 boundaries | strictness deltas flagged §16.9/§16.12 |
| Jackson `NON_NULL` payload + nested data classes (`AzureDevOpsClient.kt:33-43`) | explicit payload struct, pointer `resources` + `omitempty` | frozen wire, A-P4 |
| `value.toString()` stringification (`AzureDevOpsClient.kt:62-64`) | `renderValue` over `UseNumber`-decoded values reproducing Kotlin renderings | A-P4 table; numeric edge flagged §16.6 |
| data-class `copy()` decrypt chain (`MeshCertDecryptionService.kt:58-97` via factory) | `meshapi.DecryptInputs` returning a value copy (P4) | branch rules pinned F-P1 |
| OkHttp `followRedirects(false)` + default timeouts (`AzureDevOpsClient.kt:45-48`) | `CheckRedirect` = `ErrUseLastResponse`; explicit client timeouts | §16.10 |
| Spring profiles/`@Scheduled`/`ImmediateRetryDecorator` (§2.6) | persona wiring: `dispatch.Loop{PollInterval: 10s, ClaimBackoff: 0}` + `Done()` wake; `SingleRunMode` for the k8s profile | cadence-equivalent (umbrella §4 rows 1-3) |
| kotlin-logging + MDC (`application.yml:1-5`) | `log/slog` text handler, run id attr | log format not a contract (umbrella §8) |
| companion `STEP_ID`/const poll numbers (`AzureDevOpsBlockRunnerService.kt:74-76`, poller `:77-78`) | typed package constants; poll numbers = constructor defaults (§4.4) | frozen strings §7.1 |

## 6. Config

### 6.1 Persona config struct

```go
// azdevops.Config — persona extras only; shared parts ride config.Api.
type Config struct {
    Uuid              string     // RUNNER_UUID / blockrunner.uuid
    Api               config.Api // url + auth (API key wins)
    PrivateKey        string     // resolved PEM via config.ResolvePrivateKey (06A §6.5)
    MaxConcurrentRuns int        // new, default 1 (plan 05)
    Registration      *dispatch.RegistrationConfig // opt-in (plan 05 §9)
}
```

No runner-specific Kotlin keys exist beyond core (§2.6, umbrella §3.1). Validation (P5):
`uuid` + `api.url` + a resolvable private key required in polling mode (the runner
cannot decrypt PAT/inputs without one — fail at startup, not per run); auth + key
requirements waived in single-run mode (NoOp decryptor; tf-style exemption). A
`blockrunner.debugMode` key in a mounted file is warn-and-ignored (06A §6.4).

### 6.2 Alias table (umbrella §5.4 instantiated — every shipped name keeps working)

| Existing name | Evidence | Handling |
|---|---|---|
| env `RUNNER_UUID`, `RUNNER_API_URL`, `RUNNER_API_USERNAME`, `RUNNER_API_PASSWORD`, `RUNNER_API_CLIENT_ID`, `RUNNER_API_CLIENT_SECRET`, `VERSION` | `azure-devops-block-runner/src/main/resources/runner-config.yml:18-27` | bound via `config.Env` identically to manual (06A §6.2, incl. the `VERSION`-override rule 06A §16.6) |
| env `RUNNER_PRIVATE_KEY_FILE`; yaml `blockrunner.privateKey`/`privateKeyFile`; default `/app/runner-private.pem` | `PrivateKeyLoader.kt:8-24`, `AzureDevOpsBlockRunnerCryptoConfiguration.kt:11-17` | `config.ResolvePrivateKey` (06A §6.5) — **first real consumer alongside 06B**; Kotlin order reproduced incl. missing-file→inline fallback |
| env `PORT` (Spring default **8101**; image bakes 8080) | `application.yml:7-8`, `jvm.Dockerfile:18-19` | `MANAGEMENT_PORT` > `PORT` (deprecation-logged) > default 8101 — plan-04 mechanics; image keeps `ENV PORT=8080` |
| env `SPRING_PROFILES_ACTIVE=kubernetes` | `run-controller/runner-config.yml:154-157` | single-run trigger alias via `config.SingleRunMode` (06A §6.3) |
| yaml `blockrunner.uuid/.version/.api.url/.auth.*` (kebab-case api-key), `blockrunner.privateKey` | module `runner-config.yml:17-28` | `config.BlockRunnerCompat` block (06A §6.4), `PrivateKey*` fields consumed here |
| yaml `logging.*`, `server.*`, `spring.*` | `application.yml` | ignored-with-warning (umbrella §5.4) |

New, additive only: `MANAGEMENT_PORT`, `RUNNER_CONFIG_FILE`, `maxConcurrentRuns`/
`RUNNER_MAX_CONCURRENT_RUNS`, `registration:`. Spring relaxed-binding spellings beyond
the shipped literals are not carried (umbrella §10.4).

## 7. Persona wiring & modes

### 7.1 Registry & polling mode (`persona_azdevops.go`, package main — only main wires, D11)

- Registry entry `"azure-devops-block-runner"` →
  `meshapi.Identity{Name: "azure-devops-block-runner", Version: …}` (06A §6.2 rule).
  File name `persona_azdevops.go` matches the package/branch token; the *registry key
  and image name* keep the full hyphenated module name (umbrella §7.1).
- Polling: `dispatch.NewLoop(LoopConfig{PollInterval: 10s, ClaimBackoff: 0,
  MaxConcurrent: cfg.MaxConcurrentRuns /* default 1 */}, …)` +
  `dispatch.NewInProcess(map[…]{meshapi.RunnerTypeAzureDevOpsPipeline: handler})`
  (`ToRunnerType`: AZURE_DEVOPS → AZURE_DEVOPS_PIPELINE, `dtos.go:289-290`, umbrella
  §7.12); wake from `Done()`; the shared `ClaimClassifier` (06A §7.1) verbatim.
- Shutdown: `Loop.Stop()` → cancel the run context → `InProcess.Wait()`, draining a
  **configurable grace period (default 120s)**. The cancel makes a mid-poll sync run
  return promptly (§4.4 ctx rule) instead of holding shutdown for ≤30 min.
  **RULED (grill r2):** the cancelled in-flight run is reported with a terminal status
  (`ABORTED`, fallback `FAILED`, never `SUCCEEDED`) and clear shutdown logs are emitted;
  this is a **reviewed amendment to plan 05's H7**, not a 06C-local fork (§16.11).
- Decryptor wiring: `crypto.MeshCertBasedCrypto` from `cfg.PrivateKey` (polling);
  node id = plain runner uuid; headers = shared-client set (umbrella §7.7 delta).
- `mgmt.NewServer` on `config.ManagementPort(log, 8101, PORT-alias)` + `mgmt.RunMetrics`
  + plan-05 counters. Metrics classification (umbrella §7.2): async handover with nil
  return ⇒ **succeeded**; sync ⇒ keyed on the reported terminal status (a 30-min
  timeout counts as failed; duration includes poll time).
- Self-registration off by default; opt-in `registration:` with default capability
  `AZURE_DEVOPS_PIPELINE`.

### 7.2 Single-run mode (k8s Job)

`config.SingleRunMode` activation → read `RUN_JSON_FILE_PATH` (`UseNumber` decode) →
handler with **NoOp decryptor** (controller pre-decrypted PAT + inputs,
`decryption.go:97-111`) → `Execute` once, no loop, no mgmt listener (umbrella §7.10) →
R12 exit tail (umbrella §7.9): exit 0 iff a terminal **or handover** status was
reported — async handover exits 0; sync exits 0 after the final/failure update
(including the pinned timeout failure); register/PATCH transport failure exits non-zero
(Kotlin exit-1 parity); pre-report fetch/parse failure exits non-zero (the sanctioned
K-P2 delta). **RULED (grill r2):** the exit-code tightening is CONFIRMED for phase 6 —
Go single-run pods exit non-zero on pre-report fetch/parse failures where Kotlin exited 0;
the old exit-0 swallow stays PINNED (K-P2) for audit. A sync run may hold the Job pod for
up to 30 min — unchanged from Kotlin.

### 7.3 Modes × behavior summary

| | polling (standalone) | single-run (k8s Job) |
|---|---|---|
| claim | Loop 10s + immediate re-drain, shared classifier | none — file source |
| decryption | cert crypto: PAT + inputs (handler-side) | NoOp (pre-decrypted) |
| reporting auth | per-run runToken only | runToken from run JSON |
| mgmt listener | 8101 (aliases §6.2) | none |
| sync poll | inside `Execute`, blocks a worker ≤30 min | inside the Job, same budget |
| exit | long-running | R12 rule (§7.2) |

## 8. Dockerfile & image switch

The 06A §8 template stage repeated (umbrella §5.6):

- New final stage `azure-devops-block-runner` in `containers/runner.Dockerfile`: same
  alpine digest pin, `ca-certificates bash` only (HTTP-only), uid 2000, binary +
  symlink `/app/azure-devops-block-runner`, `ENV PORT=8080`, `EXPOSE 8080`,
  `ENTRYPOINT ["/app/entrypoint.sh", "/app/azure-devops-block-runner"]`.
- `containers/azure-devops-block-runner/runner-config.yml`: effect-equivalent to the
  Kotlin classpath defaults (§2.6) in flat keys — uuid `a9786b14-…`, api url
  `http://localhost:8304`, `bb-api`/`guest` — **plus the baked dev private key placed
  consciously** (umbrella §10.5): it is the local-dev pair of meshfed's magic-runner
  key, shipped as the dev default and never consulted when `RUNNER_PRIVATE_KEY_FILE`
  is set (§6.2 resolution order).
- Published name/tags unchanged (`ghcr.io/meshcloud/azure-devops-block-runner:main` +
  release tags); deployed controller configs keep working via the baked
  `SPRING_PROFILES_ACTIVE: kubernetes` (§7.2, umbrella A12).
- CI flip in the same PR as module removal (§12): `ci.yml` — drop the
  `azure-devops-block-runner` entries from `jvm-runners-ci` (`ci.yml:36-37`) and
  `jvm-runners-image` (`:72-73`), add the go image leg
  (`target: azure-devops-block-runner`); `build-images.yml:41-43` — the leg becomes
  `dockerfile: containers/runner.Dockerfile` + `target:` (drop `runner-module:`).
- JVM `command:`-override non-goal restated per 06A §16.9 wording (umbrella §5.6).

## 9. Migration sequence

Always-green steps sized for one reviewable single-commit PR; after every step
`task test` + `task lint` green, `task coverage` ≥ gate, and `./gradlew check` green
until step 8.

| # | Step | What changes | What proves it |
|---|---|---|---|
| 0 | **Preflight.** Umbrella A1–A12 + C1–C8 verifications on the phase-6b branch; branch `phase-6c-azdevops`. Record: whether 06B shipped `ExternalCallError`/`DecryptInputs` (STOP-C4 gate), the `UseNumber` state, the shutdown-cancel wiring shape. Re-run the §4.6 fit-check mechanically. | nothing | STOP-A / STOP-C4 / STOP-D gate |
| 1 | **Kotlin pins (tests only).** §3.2: S-P1–6, U-P1–8, P-P1–5, A-P1–6, K-P1–2, F-P1 in `azure-devops-block-runner`; verify C-P1–7 exist (C3). | Kotlin test files only | `./gradlew :azure-devops-block-runner:check` green; `git diff -- ':!*Test*' ':!*Scenario*'` empty; the 2 slow poller tests tagged |
| 2 | **Mapper + client.** `internal/azdevops`: status-mapping pure functions; `adoClient` + package-local DTOs + `renderValue`; payload struct. | `internal/azdevops` | Go twins of S-P1–6 (tables), A-P4–6 (fake-transport transcripts incl. leak + redirect pins) |
| 3 | **Handler + poller.** `Config`, `NewHandler`, `Execute` skeleton, failure ladder, update builders, `pollCompletion` (fake clock). | `internal/azdevops` | scenario suite §10.1: async handover, sync happy, timeout, dedup/re-emission, fallback, resilience, PATCH-failure — fake meshStack + fake ADO transcripts matching the pins |
| 4 | **Persona wiring, polling.** `persona_azdevops.go` + registry entry; mgmt on 8101; loop + shutdown-cancel wiring. | `runner/main.go`, `persona_azdevops.go` | loop-wiring scenario (claim→execute→re-drain); `resolvePersona` row; alias test (`MANAGEMENT_PORT`>`PORT`>8101); ctx-cancel shutdown test |
| 5 | **Single-run mode.** `SingleRunMode` + file source + NoOp decryptor + R12 tail. | `persona_azdevops.go` (+ glue) | K-P1 twin (captured wire equal modulo sanctioned deltas); exit-condition tests (K-P2 twin asserting the R12 delta) |
| 6 | **Gate + tooling.** `thresholds.txt` += `runner/internal/azdevops 90` (no exclusions); depguard: `azdevops` imports `dispatch`/`meshapi`/`report`/`config`/`crypto` + stdlib only. | `tools/coverage/*`, `.golangci.yml` | induced-failure check; `task coverage` green |
| 7 | **Image.** Dockerfile stage + `containers/azure-devops-block-runner/runner-config.yml`. | containers/ | `docker build --target azure-devops-block-runner`; container smoke: healthz on 8080, claim loop against a stub |
| 8 | **Acceptance gate (§11).** Side-by-side transcripts + manual smoke + outer net. | — | STOP-E; evidence in PR description |
| 9 | **Removal.** Delete `azure-devops-block-runner/`; `settings.gradle:7`; CI legs per §8; grep gate. | module dir, gradle, workflows | full CI green incl. flipped image leg; remaining modules' `./gradlew check` green |

## 10. Test plan & gate (D16)

### 10.1 Pin → Go mapping (N:1 into scenarios by design, umbrella §5.2)

| Kotlin pin/test | Go destination | Kind |
|---|---|---|
| S-P1–S-P6 (mapper tables) | `Test_MapPipelineResult`, `Test_MapStage*`, `Test_StageSystemMessage` tables — every row incl. the else-holes | **keep-as-unit** (pure mapping tables, the §5.2 criterion) |
| A-P4/A-P5/A-P6 + existing `AzureDevOpsClientTest` (4) | `Test_TriggerPayload` transcript table (fields, stringification, env-exclusion, MESHSTACK_BEHAVIOR, refName, NON_NULL shape, PAT-leak, Basic header, redirect) against the fake ADO transport | transcript/unit (wire-shape decision surface) |
| A-P1 async + U-P1/U-P2 | `Scenario_AzDevOps_AsyncHandover`: run JSON in ⇒ register + one IN_PROGRESS/SUCCEEDED update, zero ADO GETs, metrics = succeeded | scenario |
| A-P2/A-P3 + U-P8 | `Scenario_AzDevOps_TriggerFailures` (table: wrong impl type, decrypt failure, ADO 404) ⇒ FAILED updates with pinned message pairs; register-failure case returns error, no update | scenario (consolidates the 3 thin mockk service tests) |
| P-P2/P-P3 + U-P3–U-P7 | `Scenario_AzDevOps_SyncPoll_Happy`: fake clock; inProgress→completed with stages; asserts stage steps, trigger-step re-emission, dedup + COMPLETED re-send, final trigger-only update with enum-NAME rendering | scenario |
| P-P1 | `Scenario_AzDevOps_SyncPoll_Timeout`: clock jump ⇒ the pinned timeout messages; run FAILED; metric failed | scenario |
| P-P4 | `Scenario_AzDevOps_SyncPoll_Resilience`: GET-run 500s then recovery; timeline 500 ⇒ fallback state-only dedup | scenario |
| P-P5 | `Scenario_AzDevOps_ReportFailure`: PATCH 500 mid-poll ⇒ one failed-update attempt; second failure ⇒ `Execute` error | scenario |
| K-P1/K-P2 | `Scenario_AzDevOps_SingleRun_FileSource` + exit-condition tests (R12; K-P2 asserted as the sanctioned delta, comment cites the Kotlin baseline) | scenario |
| F-P1 | decrypt assertions inside the sync/async scenarios' fixtures (encrypted PAT + sensitive STRING + sensitive LIST input: LIST stays ciphertext in templateParameters) | scenario |
| existing k8s boot smoke | persona boot smoke (config-read stage) | smoke |

Sleep-free throughout (injected clock); `-race` on. The only asserted-behavior changes
are K-P2 (umbrella §7.9) and the §4.5 tolerant-parse rows — flagged, not STOP-B.

### 10.2 New Go-only tests

`renderValue` table (incl. `json.Number` literals + the §16.6 edge documentation),
ctx-cancel mid-poll, shutdown-cancel wiring, alias precedence, `resolvePersona` row,
unknown-state tolerance rows (§4.5), leak test (payload never contains PAT in either
form — the §7.6 pin).

### 10.3 Gate

`thresholds.txt` += `…/runner/internal/azdevops 90`, **no exclusions** (whole package
hermetic: fake meshStack transport + fake ADO transport + fake clock). Touched shared
packages stay ≥90. The package is dominated by the poll/update logic which the scenario
suite drives branch-by-branch; a shortfall is STOP-C (add scenario cases, never
exclusions). Keep-as-unit list: mapper tables, `renderValue`, config aliases — real
decision surface only (D16).

## 11. Acceptance validation

No automated per-type acceptance exists for azure-devops (umbrella §5.7/§10.2). The
gate before removal (§9 step 8), all legs required:

1. **Side-by-side transcript equivalence (06A §11.3 procedure, reused verbatim):** the
   same run JSONs — async, sync-with-stages (multi-poll fixture), sync-timeout,
   trigger-404, sensitive+LIST inputs — driven through the Kotlin runner (wiremock ADO
   + captured meshStack updates, the §3 pin suite) and through the Go handler (fake
   twins). Diff empty modulo the 06A delta allowlist (headers §7.7, null ≡ absent
   06A §16.4) **plus this plan's sanctioned rows** (§4.5 tolerant-parse — not
   reachable with valid fixtures, listed for completeness).
2. **Manual smoke against a real Azure DevOps org** (umbrella §5.7): one async and one
   sync trigger (sync pipeline with ≥2 stages, one skipped stage if arrangeable);
   verify UI messages, `ado-stage-*` steps and the final status; evidence (redacted
   transcript + run links) in the PR description.
3. **Outer net:** meshfed-release local-dev-stack + acceptance suite green (the
   azdevops mux port `:8304` claims are wire-frozen, A11); no azdevops-specific
   acceptance runs exist — verified, not assumed (§15).

STOP-E lives here: the Kotlin module is not removed until all legs pass.

## 12. Kotlin module removal + Gradle shrink

Umbrella §5.8 recipe instantiated (last commits, after §11):

1. `git rm -r azure-devops-block-runner/` — the §3.2 pins die with the module; their
   Go twins (§10.1) are the surviving pin. C-P1–C-P7 stay in `block-runner-core`
   (deleted only in 06D).
2. `settings.gradle`: drop `include 'azure-devops-block-runner'` (`settings.gradle:7`).
3. `.github/workflows/ci.yml`: drop the `jvm-runners-ci` entry (`:36-37`) and the
   `jvm-runners-image` entry (`:72-73`); add the go image leg (§8).
4. `.github/workflows/build-images.yml`: the leg (`:41-43`) →
   `dockerfile: containers/runner.Dockerfile`, `target: azure-devops-block-runner`.
5. Cross-repo doc-truth check edits if any (§15) ride the lock-step PR.
6. Grep gate: `grep -rn "azure-devops-block-runner" --exclude-dir=.git` — remaining
   hits must be image/persona *names* (workflows, containers/, run-controller sample,
   plan docs, CHANGELOG), never module *paths* or `:azure-devops-block-runner:` gradle
   refs.

No other Gradle shrink here — core, root build files, wrapper, `jvm.Dockerfile` stay
until 06D (umbrella §5.8).

## 13. Frozen contracts touched

Umbrella §8 instantiated for AZURE_DEVOPS. **Preserved (proven by pins → ported tests):**

- meshStack wire: claim/register/update per C-P1–C-P7 (06A-owned); lean `SourceUpdate`
  PATCH; runToken-only run-scoped auth; async **IN_PROGRESS handover** when
  `impl.async` (D9).
- ADO wire (customer pipelines parse this): trigger POST URL shape + `api-version=7.1`,
  `templateParameters` = stringified non-env inputs + `MESHSTACK_BEHAVIOR`,
  environment-input exclusion, `resources.repositories.self.refName` only when set,
  Jackson-NON_NULL body shape, `Basic base64(":"+PAT)` auth, redirects not followed.
- Step ids `azure-devops-trigger`, `ado-stage-<id>` + display names; every §2.3/§2.4
  message string byte-identically (trigger sync/async variants, polling state
  messages, stage user/system messages, final enum-NAME rendering, the failure pair
  incl. the timeout text); the stage-mapping table incl. its else-holes; the one-way
  dedup / COMPLETED re-emission cadence; 10s/30min poll constants.
- k8s single-run contract (`RUN_JSON_FILE_PATH`, `RUNNER_UUID`, `RUNNER_API_URL`,
  `SPRING_PROFILES_ACTIVE: kubernetes` accepted — D10 both directions); controller PAT
  pre-decryption (`decryption.go:97-111`) unchanged.
- Image name/tags; `ENV PORT=8080`/`EXPOSE 8080`; healthz `OK` on resolved legacy port
  (default 8101); all §6.2 env/yaml names; mux `:8304`.

**Sanctioned, flagged deltas (uniform umbrella wording + this plan's):** additive
client headers (§7.7); single-run exit tightening (§7.9, K-P2); no single-run listener
(§7.10); additive metrics/config; JVM `command:`-override incompatibility (§5.6/06A
§16.9); null ≡ absent serialization (06A §16.4); slog format; the §4.5 parse-tolerance
rows (§16.9/§16.12); ctx-cancel poll abort (§16.11); explicit HTTP timeouts (§16.10).

## 14. Rollback story

One squash commit ⇒ one `git revert` restores the Kotlin module, its
`settings.gradle` include, both CI matrix entries and the JVM image leg, and deletes
`internal/azdevops`, `persona_azdevops.go` + registry entry, the Dockerfile stage,
`containers/azure-devops-block-runner/`, the thresholds line and depguard rules.
Shared helpers are **not** deleted on revert — unlike 06A's, they have other consumers
(06B's gitlab port precedes this PR; `report.Reporter`/`SingleRunMode`/compat block stay).
Image name + wire/k8s contracts frozen (§13) ⇒ `:main` floats back to the JVM build on
the next CI run; deployed operator configs need no change in either direction
(`SPRING_PROFILES_ACTIVE` honored by both generations, `EXECUTION_MODE` never
required). Release tags immutable. Lost on revert (documented cost): `MANAGEMENT_PORT`/
metrics for this persona, `maxConcurrentRuns` > 1, opt-in registration, exit tightening,
parse tolerance. The §3 pin tests are tests-only and revert with the module directory —
they can be cherry-picked back if the port is retried.

## 15. Cross-repo touch points

Umbrella §9 subset — 06C has **no mandatory cross-repo edits**:

- **meshfed-release `local-dev-stack/SKILL.md`:** no azure-devops runner entry exists
  (umbrella §9) — verify by grep at §9 step 0 and state "no edit" in the PR; an
  optional start snippet is a maintainer choice, not gate-relevant.
- **meshfed-release acceptance tests / mux:** read-only; AZURE_DEVOPS_PIPELINE claims
  via mux `:8304` (SKILL.md:56, wire frozen); no per-type acceptance tests exist
  (umbrella §5.7 finding) — nothing to update.
- **meshfed-release `how-to-run-building-block-runners.md`:** doc-truth check — if the
  page documents `SPRING_PROFILES_ACTIVE` semantics for this image, add the
  `EXECUTION_MODE` note in a lock-step PR (06A wording); full docs pass stays phase 7.
- **This repo, `run-controller/runner-config.yml` sample:** valid unchanged
  (`:154-157` — the new image honors the profile env); optional `EXECUTION_MODE`
  comment, sample flip deferred to phase 7 (umbrella §9).
- **terraform-provider-meshstack:** no dependency (grep at step 0, umbrella §9) — no
  edit.

## 16. Flags + Open questions

Findings the umbrella / prior plans did not anticipate, plus judgment calls for review:

1. **Trigger-step re-emission with *different* messages.** Every stage update
   re-includes `azure-devops-trigger` SUCCEEDED with `"Triggered Azure DevOps
   Pipeline"` / `"Pipeline run <id>. View run: <url>"` — not the initial update's
   suffixed messages (§2.3). Umbrella §3.2 didn't spell this out; both message shapes
   are pinned (U-P1 vs U-P4).
2. **The stage dedup is one-way: COMPLETED stages are re-sent on every subsequent
   poll**, and on the stage path a PATCH goes out **every 10 s regardless of change**;
   the state-change dedup exists only on the timeline-*failure* fallback (§2.2/§2.3).
   Umbrella §3.2's "re-reported only when new or COMPLETED" is accurate but understates
   the traffic; pinned as-is (U-P5), a cadence fix is a post-port follow-up.
3. **The 30-min timeout reports user message "Could not trigger the Azure DevOps
   Pipeline"** (and system "There was an internal error…: Pipeline polling timeout
   after 30 minutes") — misleading after a successful trigger; UI-visible bytes,
   ported verbatim (P-P1/U-P8).
4. **Two enum-rendering schemes in one updater:** the final system message renders
   Kotlin enum NAMES (`COMPLETED`, `SUCCEEDED`, literal `null`), polling messages
   render wire values (`inProgress`) — §2.4. The Go DTOs carry both renderings (§4.2).
5. **Mapper else-holes:** `completed+succeededWithIssues` and `completed+null` map to
   IN_PROGRESS (such a stage step never turns terminal); `completed+canceled/abandoned`
   user message renders `"<name>: completed"` (S-P4/S-P5). Pinned as-is per D13
   discipline; reviewer may commission a post-port fix.
6. **templateParameters stringification. RULED (grill r2):** for composite/exotic
   `templateParameters` values (arrays, objects, exotic numeric literals) the Go
   `renderValue` emits **compact JSON** rather than reproducing Kotlin/Java `toString`
   (`[a, b]` / `{k=v}` / Java-style doubles). This is a **deliberate, flagged byte
   change** — recorded in the migration/release notes; the A-P4 pins assert the JSON
   rendering, not the Kotlin `toString` bytes. Plain scalar literals (strings verbatim,
   `json.Number` literal, bools) are unaffected and stay byte-identical; only
   composite/exotic values move to JSON, which also resolves the previously unpinnable
   numeric edge (`1e3`) by pinning its JSON form.
7. **azdevops never sanitizes its base URL** — no `UrlSanitizerService` usage in the
   module (grep-verified), so umbrella §4 row 13 ("gitlab/github/azdevops" consumers)
   is an erratum for this runner; a trailing-slash `azureDevOpsBaseUrl` produces
   double-slash request URLs today, preserved as-is (A-P4 fixture covers it).
8. **The existing k8s startup scenario is boot-only** (no captured wire, unlike
   manual's) — the K-P1 pin upgrades it to a captured-wire scenario before the port.
9. **Parse-strictness delta on the impl DTO:** Kotlin fails the whole claim when
    `async` (or the impl shape) doesn't parse ⇒ swallowed no-run ⇒ coordinator
    timeout; Go has already claimed and registered when it unmarshals the impl, so it
    reports run FAILED with an actionable message instead (§4.5) — strictly better UX,
    sanctioned in the §7.9 spirit; reviewer may veto toward… there is no faithful
    alternative (un-claiming is impossible), the realistic veto is message wording.
10. **OkHttp's default 10 s connect/read timeouts are load-bearing** for the poll
    budget: Go's zero-value `http.Client` never times out, so the port sets explicit
    equivalents — without them a hung ADO read could stall a "30-min" poll forever
    (§4.2). No prior plan mentions external-HTTP timeout parity.
11. **ctx cancellation of the poll loop. RULED (grill r2):** the contradiction with
    plan 05's shutdown ("Wait until in-flight == 0", which assumed short handlers) is
    resolved **in favor of CANCEL** for sync-polling handlers, as a **reviewed amendment
    to plan 05's H7** (not a 06C-local fork). On `ctx.Done()` the Go poller no longer
    leaves the run IN_PROGRESS: it reports a **terminal** status (`ABORTED` — now in the
    Go `ExecutionStatus` enum, defined terminal by meshStack's status source — falling
    back to `FAILED` if the endpoint rejects it, **never `SUCCEEDED`**) so the
    coordinator never waits out its long timeout. Persona graceful shutdown cancels run
    contexts, drains a **configurable grace period (default 120s)**, and emits clear
    shutdown logs so `InProcess.Wait()` is not held for ≤30 min (§4.4/§7.1).
12. **Timeline parse tolerance** (§4.5 row 3): Kotlin degrades to state-only fallback
    when ADO introduces an unknown timeline state/result string; Go keeps emitting
    stage steps via the mapper's else branches. 06A flag-5 precedent (tolerant-parse
    over invented failure); reviewer may prefer strict-parse-to-fallback for byte
    equivalence under future ADO API drift.
13. **Kotlin poller pins need two real-sleep tests** (~10 s each): `Thread.sleep` is
    not injectable and the pinning step is tests-only (§3.2). Accepted cost, tagged
    slow; the Go twins are sleep-free.
14. **`config.ResolvePrivateKey` and `ExternalCallError`/`DecryptInputs` ownership**:
    consumed here exactly as 06A §6.5 / 06B contracts define; if 06B lands without
    them, STOP-C4 moves implementation here with an umbrella owner-row correction —
    recorded so parallel authoring of 06B/06C cannot silently double-implement.

**Open questions:** none — every decision branch was walked and resolved from the
sources; the reviewer-vetoable judgment calls are flags 2, 5, 9, 12 above plus the
umbrella-level calls they instantiate (§7.4 lean body, §7.9 exit rule, §7.7 headers).
