# Detail Plan 06A — Manual Runner Port (Phase 6, PR 1 — the template PR)

**Phase:** 6a · **Branch:** `refactor/single-go-binary/phase-6a-manual` (stacked on
`refactor/single-go-binary/phase-5-dispatcher`) · **Delivery:** one single-commit PR ·
**Binding:** umbrella `PLAN_DETAIL_06_kotlin_ports_umbrella.md` (§5 template contract,
§7 consistency rules, §8 frozen contracts) + `PLAN_HIGH_LEVEL.md` §3 P1–P8, D5, D6
(Kotlin corollary), D7, D9, D11 (`internal/manual`), D12 (port 8104), D15, D16.

Kotlin references are `main` @ `c3fce61`; Go references marked *post-N* are shapes
promised by plan N. This sub-plan additionally establishes the phase-6 template
artifacts (umbrella §6) that 06B–D copy; §17 records the mandated fit review.

## 1. Assumptions from prior phases

Plans 00–05 are **not implemented yet**. Implementation begins by running **all umbrella
§1 verification steps (A1–A12)** — they are incorporated here by reference and not
repeated — plus the 06A-specific ones below. Any material failure is a **STOP** per the
umbrella's STOP-A.

| # | Assumption | Promised by | Verification step |
|---|---|---|---|
| B1 | The manual module and block-runner-core are byte-identical to `main` @ `c3fce61` (all §2 file:line citations still hold). | Plans 00–05 scope (umbrella A10) | `git diff main..phase-5-dispatcher -- manual-block-runner/ block-runner-core/` — empty |
| B2 | `dispatch` promise set: `RunHandler.Execute(ctx, ClaimedRun) error`, `ClaimedRun{Id RunId, Type, Details meshapi.RunDetailsDTO, RawJson string}`, `InProcess` (ALL-rejected, `Done()` wake, `Wait()`), `Loop` + `LoopConfig{PollInterval, ClaimBackoff, MaxConcurrent}` + injectable `ClaimClassifier`, loop-level fail-fast via `StatusApi`. | Plan 05 §4, §17 | read `internal/dispatch`; run its loop-cadence + unhandled-type tests |
| B3 | `meshapi.RunClient` supports per-run construction with runToken-only auth and a caller-supplied `RegistrationDTO` (one step, PENDING) via `RegisterSource`, and `PatchStatus(runId, sourceId string, payload any)` accepts an arbitrary marshalable body — i.e. the lean `SourceUpdate` shape (§4.3) needs **no client change**. Claim POST / status PATCH are never retried. | Plan 03 §5.2.4, Plan 05 A4 | read `internal/meshapi/client.go` signatures; `grep -n "payload any" internal/meshapi` |
| B4 | `RunDetailsDTO` models everything the manual service reads: `Metadata.Uuid`, `Spec.RunToken`, `Spec.BuildingBlock.Spec.Inputs[]{Key, Value, Type, IsSensitive}`, `Links{Self, RegisterSource, UpdateSource, MeshstackBaseUrl}` with `LinkDTO.Templated` (`go-meshapi-client/meshapi/dtos.go:10-72` today, moved by plan 04). | Plan 03/04 moves | read `internal/meshapi/dtos.go` |
| B5 | `config.Path/LoadFile/Env`, `config.Api` (+`NewAuthProvider`, API-key-wins precedence), `config.ManagementPort(log, def, aliases…)` exist per plans 03/04; `mgmt.NewServer` + `mgmt.RunMetrics` + the plan-05 counters exist and are persona-reusable. | Plan 03 §5.3, Plan 04 §4.3, Plan 05 §10.3 | read `internal/{config,mgmt}`; run the alias-precedence tests |
| B6 | Persona mechanics: adding a persona = `cmd/<name>/main.go` (package main, wiring only — links just that persona's deps) + register its handler in the `cmd/bbrunner` superset + `containers/<name>-block-runner/Dockerfile` with a direct entrypoint + build-matrix leg `./cmd/<name>`; single-run tail implements the 2b-R12 exit rule (non-zero only when no terminal/handover status was reported). | Plan 04 §11, Plan 05 A9 | read `cmd/tf/main.go` (single-run tail), `cmd/bbrunner/main.go` handler registration, a per-persona `containers/*/Dockerfile` |
| B7 | The Kotlin manual suite is green as-is: `./gradlew :manual-block-runner:check` and `:block-runner-core:check` pass on the phase-5 branch (the pinning step builds on them). | Current `main` CI | run both gradle tasks once before writing pins |
| B8 | `report` package has `Progress`/`RunStatus`/`StepStatus`/`ExecutionStatus` and the `Reporter` port; the `Observer` ticker exists but is **not** consumed by this port (umbrella §7.5). | Plan 03 §5.4 | read `internal/report` |

**STOP markers.** The umbrella's STOP-A–E apply verbatim; in this plan they bite at:
STOP-A = step 0 of §9; STOP-B = any pin of §3 that cannot be ported per §10's mapping;
STOP-C = `internal/manual` (or a touched shared package) below 90 at any §9 checkpoint;
**STOP-D = §17's fit review finds a `RunHandler`/`ClaimedRun`/`registration:`/reporter
shape that does not fit gitlab/azdevops/github** — the fix is a reviewed revision of
plan 05 §4 and the umbrella, never a 06A-local workaround; STOP-E = §11's gate fails.

## 2. Kotlin behavior inventory

Full study of `manual-block-runner` (6 production files, ~200 lines) plus the
block-runner-core mechanics it exercises — every coordinator-visible behavior with
evidence. Deepens umbrella §3.2 "manual"; the umbrella's §4 core map applies unchanged.

### 2.1 The service (`NoOpBlockRunnerService.kt`)

1. **Claim-and-swallow:** `processBlock()` fetches a run client; **any** exception is
   caught, logged (`"Unexpected error while getting a block run."`) and treated as
   no-run (`NoOpBlockRunnerService.kt:16-23`). No backoff beyond the next 10s tick.
2. **Register:** exactly one step, `stepId = "manual"` (companion `STEP_ID`, `:71`),
   `stepDisplayName = "Manual Block Run"` (`:25-28`). Registration failures are **not**
   caught here — they propagate (see §2.5).
3. **Echo update:** one `SourceUpdate{status: SUCCEEDED, steps: [one StepUpdate]}` with
   `id="manual"`, `status=SUCCEEDED`, and `outputs` = the run's inputs keyed by input
   key (`:35-59`). Per output: `value` = input value verbatim (any JSON type),
   `type = toOutputType(input.type)`, `isSensitive` = input flag echoed (`:50-56`).
4. **Duplicate input keys — last wins:** inputs are collapsed via
   `associateBy { it.key }` (`:36-42`); the shipped k8s scenario fixture even contains
   a duplicate `"test"` input (`ManualRunnerKubernetesStartupScenario.kt:163-177`).
5. **Type mapping** (`toOutputType`, `:77-88`): STRING→STRING, INTEGER→INTEGER,
   BOOLEAN→BOOLEAN, CODE→CODE, FILE→STRING, LIST→CODE, SINGLE_SELECT→STRING,
   MULTI_SELECT→CODE. Exhaustive over the enum (`MeshBuildingBlockIO.kt:9-18`); an
   unknown type string never reaches it — Jackson enum parsing would already have
   failed the fetch (⇒ behavior 1).
6. **No decryption, ever:** the crypto bean is a placeholder with an empty key
   (`BlockRunnerApplication.kt:22-32`); the service never calls a decryption service.
   Standalone ⇒ sensitive inputs are echoed as **ciphertext**; k8s mode ⇒ the
   controller already decrypted (`run-controller/controller/decryption.go:113-114`
   confirms MANUAL has no impl secrets), so plaintext is echoed.
7. **Secret-hygiene quirk (not ported):** the service logs `variables: $inputs` at
   INFO (`:61-64`) — in k8s mode that writes **decrypted sensitive input values** to
   pod stdout. Logs are not a wire contract (umbrella §8); the Go port does not
   reproduce this leak (flag §16.3).
8. Terminal `SUCCEEDED` in one update; no async, no handover, no abort handling, no
   ticker (umbrella §4 row 7, §7.5).

### 2.2 Debug mode (`DebugBlockRunnerService.kt` — untested today)

`blockrunner.debugMode: true` swaps the service (`BlockRunnerServiceConfiguration.kt:30-38`).
Behavior (`DebugBlockRunnerService.kt:22-58`): overrides only `updateBlockStatus` —
claim/register are inherited. Sends **4 updates**: three `IN_PROGRESS` (5s sleeps
between, `:23,30,36,42`), then a final update whose status is random
`SUCCEEDED`/`FAILED` at p=0.5 (`:44-50`). Every update carries two steps
(`makeUpdate`, `:64-103`): `manual` (`SUCCEEDED`, fixed user/system messages
`"this is a message for the user"`/`"…system"`) and `additionalDebugStep`
(`PENDING` + `outputs=null` on non-final, `SUCCEEDED` + outputs on final). **Quirk:**
debug outputs echo `it.type` raw — `toOutputType` is *not* applied (`:69-75`), unlike
the production service. Dev-only helper; per umbrella §3.2 the port is
behavior-equivalent (update sequence/shape pinned, sleep cadence and RNG not).

### 2.3 Wiring, scheduling, modes (Spring)

- Standalone: `@Scheduled(fixedRate=10000)` (`BlockRunRequestScheduler.kt:14`) drives
  `ImmediateRetryDecorator(service)` (`BlockRunnerServiceConfiguration.kt:15-19`) —
  after a processed run it immediately re-claims until no-run
  (`ImmediateRetryDecorator.kt:16-25`). Scheduling is disabled under `test`/`kubernetes`
  profiles (`ManualBlockRunnerSchedulingConfiguration.kt:10-13`).
- k8s (`SPRING_PROFILES_ACTIVE=kubernetes`, operator config
  `run-controller/runner-config.yml:139-142`): **no decorator** ("Kubernetes based
  services should not retry", `BlockRunnerServiceConfiguration.kt:21-28`);
  `SingleShotRunner` runs `processBlock()` once — normal return ⇒ exit 0, uncaught
  exception ⇒ log + exit 1 (`SingleShotRunner.kt:38-49`); the run file comes from
  `RUN_JSON_FILE_PATH` (`RunFileJsonBlockRunClientFetcher.kt:15-26`).
- Health: `/healthz` → `"OK"` on Spring `PORT`, default **8104**
  (`application.yml:8`, `HealthController.kt:10-11`); image bakes `PORT=8080`
  (`containers/jvm.Dockerfile:18-19`).

### 2.4 Wire mechanics exercised (block-runner-core — the 06A-owned pin surface)

- **Claim:** POST `{api.url}/api/meshobjects/meshbuildingblockruns/create?forRunnerUuid=<uuid>`,
  empty body, `Content-Type` + `Accept` = `application/vnd.meshcloud.api.meshbuildingblockrun.v1.hal+json`
  (`MeshObjectApiBlockRunClientFetcher.kt:35-45`, `MeshHalMediaTypes.kt:10`); 404 ⇒
  no-run (`:58-61`), 409 ⇒ warn + no-run (`:63-66`), 200/201 ⇒ parse body incl.
  `_links` (`ProcessableRunFactory.kt:14-27`), anything else ⇒ throw (`:77-79`).
  Auth: API key (POST `/api/login`, cached Bearer) when configured, else Basic
  (`AuthHttpClientFactory.kt:46-68`); `X-Meshcloud-Runner-Version` header on every
  claim-client request (`:70-82`). No node-id/name headers (umbrella §7.7).
- **Per-run client:** fresh OkHttp with **Bearer runToken only** — and *without* the
  version header (`HttpRunTokenRunClientFactory.kt:29-40`); URLs from the run's HAL
  links; `updateSource` is a URI template whose `{sourceId}` is replaced by the runner
  uuid, missing placeholder ⇒ error (`ActiveRunBasedUrlProvider.kt:15-25`).
- **Register:** POST `registerSource` href, body
  `{source: {id: <uuid>, externalRunId: null, externalRunUrl: null}, steps: [{id, displayName, status: "PENDING"}]}`
  (`HttpBlockRunClient.kt:27-51`, defaults `MeshBuildingBlockRun.kt:25-40`); 409 ⇒
  tolerated (debug log), 200 ⇒ ok, else ⇒ throw (`:53-59`).
- **Update:** PATCH `updateSource(sourceId)` with the lean
  `SourceUpdate{status, steps[]}` body (`MeshBuildingBlockRun.kt:56-79`); response body
  **ignored — abort flag deliberately not honored** (`HttpBlockRunClient.kt:62-88`);
  non-200 ⇒ throw. **Serialization note:** the shared Jackson mapper
  (`MeshObjectApiObjectMapper.kt:12-18`) uses default inclusion, so unset optional
  fields serialize as explicit JSON `null`s; the Go DTO uses `omitempty` (umbrella
  §7.4) — pins therefore compare parsed JSON with null ≡ absent (flag §16.4).

### 2.5 Failure surfaces by mode (the exit/reporting matrix)

| Failure | Standalone (scheduler) | k8s (single shot) |
|---|---|---|
| fetch/parse/claim error | caught ⇒ no-run, next tick (`NoOpBlockRunnerService.kt:16-23`) | caught the same way ⇒ `processBlock()` returns null ⇒ **exit 0**, run never reported (umbrella §7.9 quirk) |
| register/update HTTP error | propagates out of the service; Spring's scheduler catches+logs ⇒ run stays claimed and unreported (coordinator timeout) | propagates to `SingleShotRunner` ⇒ **exit 1** (`SingleShotRunner.kt:44-48`); `BackoffLimit: 1` retries the Job |
| happy path | SUCCEEDED reported, immediate re-claim | SUCCEEDED reported, exit 0 |

### 2.6 Config & defaults (`runner-config.yml`, `application.yml`)

Shipped defaults: `version: ${VERSION:dev}`, `uuid: ${RUNNER_UUID:d943b032-7836-4fef-a4a0-158817beecf3}`,
`debugMode: false`, `api.url: ${RUNNER_API_URL:http://localhost:8301}` (the mux MANUAL
port, umbrella A11), `auth.username/password: ${RUNNER_API_USERNAME:bb-api}/${RUNNER_API_PASSWORD:guest}`,
`auth.api-key.client-id/client-secret: ${RUNNER_API_CLIENT_ID:}/${RUNNER_API_CLIENT_SECRET:}`
(`manual-block-runner/src/main/resources/runner-config.yml:1-14`). Blank API-key creds
disable API-key auth (`StandaloneBlockRunnerApiConfig.kt:35`). Config file lookup:
classpath then `./runner-config.yml` (`application.yml:13-16`). No private key is
configured or needed (§2.1.6). **No `blockrunner.privateKey*` keys in the manual yaml** —
but core would bind them if a customer-mounted file carries them (§6.4).

## 3. Kotlin pin tests (tests-first step)

Tests-only commits in `manual-block-runner` and `block-runner-core`
(`git diff -- ':!*Test*' ':!*Scenario*'` empty for this step), proven green by the
existing `jvm-runners-ci` legs before any Go code exists (umbrella §5.2).

### 3.1 What already exists (kept, later ported per §10)

- `NoOpBlockRunnerServiceTest` — **11 tests** (no-run, fetch-exception, happy path
  register+update via mockk, 8 `toOutputType` mappings). *Correction to umbrella §3.3,
  which counts 12 — verified 11 `@Test` methods (`NoOpBlockRunnerServiceTest.kt:35-123`);
  no content difference, flag §16.7.*
- `ManualRunnerKubernetesStartupScenario` — full k8s single-shot boot with captured
  register/update (step id/display name, SUCCEEDED, outputs echo incl. the
  duplicate-key collapse) against `SAMPLE_RUN_JSON` (`:119-144,148-211`).
- `ManualRunnerStartupScenario` — standalone context boot smoke.
- block-runner-core: auth/config scenarios, `ApiKeyAuthInterceptorTest`,
  `RunFileJsonBlockRunClientFetcherTest`, `ImmediateRetryDecoratorTest`,
  `AuthHttpClientFactoryTest`, `UrlSanitizerServiceTest`, `MeshCertDecryptionServiceTest`.

### 3.2 New manual-module pins (closing the umbrella §3.3 gap column)

| Id | Pin (scenario-level where possible, D16) | Anchors |
|---|---|---|
| M-P1 | **Sensitive ciphertext echo (standalone):** an `isSensitive: true` STRING input with an opaque ciphertext value is echoed verbatim (same string, `isSensitive: true`) — no decryption attempted | §2.1.6; service-level test with mockk client capture |
| M-P2 | **Duplicate key last-wins:** two inputs with the same key, different values ⇒ one output carrying the *last* value | §2.1.4 |
| M-P3 | **Value passthrough fidelity:** INTEGER input with a large numeric value and a CODE input with a nested-object value are echoed as the same JSON values (drives the Go-side `json.Number` decision, §4.2) | §2.1.3 |
| M-P4 | **Debug update sequence:** with `debugMode: true`, exactly 4 updates — statuses `IN_PROGRESS×3` then terminal; every update has steps `manual`(SUCCEEDED, the two fixed messages) + `additionalDebugStep`; `outputs` only on the final update; **debug outputs carry the raw input type** (no `toOutputType`) | §2.2; sleeps stubbed/ignored, RNG pinned as set-membership {SUCCEEDED, FAILED} |
| M-P5 | **Debug swap wiring:** `debugMode: true` ⇒ `DebugBlockRunnerService` is the active service; `false` ⇒ `NoOpBlockRunnerService` | `BlockRunnerServiceConfiguration.kt:30-38` |
| M-P6 | **k8s exit 1 on report failure:** update (or register) throws ⇒ `RunTerminator.exit(1)` | §2.5; extend the k8s scenario's `TestRunTerminator` capture |
| M-P7 | **k8s exit 0 on fetch failure:** missing/unreadable `RUN_JSON_FILE_PATH` ⇒ swallowed, `exit(0)`, zero API interactions — the quirk the Go port deliberately tightens (umbrella §7.9/§10.3); the pin documents the Kotlin baseline the delta is measured against | §2.5 |
| M-P8 | **Ordering + cardinality:** exactly one `registerAsSource` before exactly one update (production service) | `NoOpBlockRunnerService.kt:25-32` |

### 3.3 block-runner-core wire pins (06A-owned; 06B–D verify existence, umbrella §3.3 last row)

MockWebServer transcript tests in `block-runner-core` for the untested
`HttpBlockRunClient` + `MeshObjectApiBlockRunClientFetcher` + `ActiveRunBasedUrlProvider`:

| Id | Pin | Anchors |
|---|---|---|
| C-P1 | Claim request shape: POST path + `forRunnerUuid` query, empty body, both v1 media-type headers, `X-Meshcloud-Runner-Version` present, auth header per configured mode | §2.4; `MeshObjectApiBlockRunClientFetcher.kt:35-45` |
| C-P2 | Claim responses: 404 ⇒ null; 409 ⇒ null (warn); 200 and 201 ⇒ parsed run with links; 500 ⇒ throws | `:55-81` |
| C-P3 | Register request: POST to the `registerSource` href verbatim, body `{source:{id:<uuid>},steps:[{id,displayName,status:"PENDING"}]}` (null ≡ absent for `externalRunId/Url`), v1 media type both headers, `Authorization: Bearer <runToken>`, **no version header on the per-run client** | `HttpBlockRunClient.kt:27-51`, `HttpRunTokenRunClientFactory.kt:29-40` |
| C-P4 | Register responses: 200 ok; **409 ⇒ success (no throw)**; 500 ⇒ throws | `HttpBlockRunClient.kt:53-59` |
| C-P5 | Update request: PATCH to `updateSource` href with `{sourceId}` → runner uuid; lean `SourceUpdate` body (status + steps only; step fields null ≡ absent); Bearer runToken | `HttpBlockRunClient.kt:62-81`, `ActiveRunBasedUrlProvider.kt:15-25` |
| C-P6 | Update responses: 200 ⇒ ok, **response body ignored** (a body containing an abort flag has no effect); non-200 ⇒ throws | `HttpBlockRunClient.kt:62-66,82-88` |
| C-P7 | `updateSource` template without `{sourceId}` ⇒ error naming the template and uuid | `ActiveRunBasedUrlProvider.kt:20-24` |

JSON-body assertions in C-P3/C-P5 compare **parsed JSON with null ≡ absent** (§2.4
serialization note, flag §16.4) so the Go `omitempty` twins can assert the identical
semantic content. Bugs/quirks pinned as-is: M-P7 (exit-0 swallow), C-P6 (abort ignored),
the debug raw-type quirk (M-P4) — all listed in §16, none fixed in this PR (umbrella §5.2).

## 4. Go handler design

Package `internal/manual` (D11). Illustrative signatures only. The handler
follows the umbrella §5.3 shape exactly — this section *instantiates the template*;
deviations discovered by 06B–D are umbrella revisions (STOP-D).

### 4.1 Handler

```go
// package manual — the MANUAL run handler (echo inputs → outputs, terminal SUCCEEDED).
func NewHandler(cfg Config, deps HandlerDeps) Handler        // value type (P4)
func (h Handler) Execute(ctx context.Context, run dispatch.ClaimedRun) error

type HandlerDeps struct {
    Reporters ReporterFactory // per-run report.Reporter, runToken-only (§4.3)
    Clock     Clock           // debug-mode waits; fake in tests
    Rand      func() float64  // debug-mode outcome; injectable (Kotlin Math.random)
    Log       *slog.Logger    // D15; per-run via Log.With("run", run.Id)
}
```

- `Execute` skeleton = the Kotlin skeleton: build the per-run `report.Reporter` from
  `run.Details.Links` + `run.Details.Spec.RunToken` → assemble a one-step `RunStatus`
  (step `manual` / "Manual Block Run", echoed outputs, `SUCCEEDED`) → `Register(status)`
  then `Report(status)` (**the `abort` return is discarded**) → return nil.
  Transport errors from register/update return a non-nil error (A1 contract:
  infrastructure failure; the run stays unreported exactly as in Kotlin, §2.5) —
  manual never reports run-level FAILED because no execution can fail.
- **No decryptor dep.** Manual echoes ciphertext in standalone mode and pre-decrypted
  values in single-run mode (§2.1.6) — the handler never decrypts, so `HandlerDeps`
  omits the `meshapi.Decryptor` the umbrella template names; 06B–D add it (recorded in
  §17 so the omission is a manual-specific narrowing, not a template change).
- Debug mode: `cfg.DebugMode` selects a debug execution path inside the same package
  (same claim/register; the update sequence of M-P4 with `Clock` waits and `Rand`
  outcome). Not a separate handler type — Kotlin's subclass override becomes a small
  strategy branch (D15 §5, no inheritance mimicry).
- Step id/display-name are typed constants (`StepId = "manual"`, umbrella §7.1).
- **Handler purity (the manual litmus test, plan 05 §4.2).** The manual handler is the
  proof that a `RunHandler` can be pure "inputs → outputs" logic: its only injected
  collaborators are a run-scoped reporter (via `ReporterFactory`, runToken-only),
  `Clock`/`Rand`, and `*slog.Logger` — **no direct `meshapi`/HTTP import** in the
  handler's own logic, logging excepted. Reading inputs off
  `run.Details.Spec.BuildingBlock.Spec.Inputs` couples the handler to the `meshapi` DTO
  shape; the §17 fit review decides whether that stays (thin, acceptable) or whether
  inputs/outputs are lifted to a small domain type so the handler imports no client
  package at all. Either way, the reporter stays a port, not a `meshapi.RunClient` the
  handler builds itself. **STOP-D** gates this via the fit review.

### 4.2 Echo semantics (the only decision surface)

- `toOutputType(t string) string` — the 8-row table of §2.1.5 as a pure function over
  the DTO's string type. Unknown value ⇒ **identity passthrough + warn log**: Kotlin
  could never see one (enum parse would have failed the whole fetch, §2.1.5); inventing
  a run-failing path here would be new behavior. Flag §16.5.
- Duplicate keys: map assignment in input order ⇒ last-wins (M-P2 parity).
- **Number fidelity:** `BuildingBlockInputSpecDTO.Value` is `interface{}`; default
  `encoding/json` decoding would float64-ize INTEGER values and can reformat large
  numbers. The claim/file decode path for this handler uses `json.Decoder.UseNumber()`
  (or the DTO's existing behavior if plans 02–05 already decode with `UseNumber` —
  verify at step 0) so M-P3's echo is byte-faithful. This is a *handler-visible*
  requirement recorded for the template: gitlab/github embed run JSON in outbound
  payloads and need the same fidelity (§17).
- Sensitive flag echoed verbatim; values never logged (§2.1.7 quirk not ported).

### 4.3 The event-driven reporting seam (template artifact, umbrella §6 item 2)

The ports never use `report.Observer` (no ticker; the `abort` return is **discarded** —
umbrella §7.5) and PATCH the **lean** `SourceUpdate` shape (umbrella §7.4). 06A adds,
designed against 06B–D's needs (multi-step updates, partial updates, dedup-by-caller,
IN_PROGRESS handover):

```go
// meshapi (wire DTO home — the third PATCH shape, alongside the tf and controller DTOs):
type SourceUpdateDTO struct {
    Status string          `json:"status,omitempty"`
    Steps  []StepUpdateDTO `json:"steps,omitempty"`
}
type StepUpdateDTO struct {
    Id            string               `json:"id"`
    DisplayName   string               `json:"displayName,omitempty"`
    UserMessage   string               `json:"userMessage,omitempty"`
    SystemMessage string               `json:"systemMessage,omitempty"`
    Outputs       map[string]OutputDTO `json:"outputs,omitempty"`
    Status        string               `json:"status,omitempty"`
}

// report — the UNIFIED reporter port consumed by ALL five runners (plan 05 §4.2), over a
// run-scoped meshapi.RunClient. Report sends only the steps present in RunStatus.Steps
// (changed/new since the last send); the meshfed endpoint upserts steps by id and each
// included step carries its FULL current message text (backend overwrites, does not
// append). The lean SourceUpdateDTO above is its wire form.
type Reporter interface {
    Register(RunStatus) error
    Report(RunStatus) (abort bool, err error)
}
func NewReporter(rc RunPatcher, sourceId string, log *slog.Logger) Reporter // register: one PENDING step, 409 = success; Report: PATCH, response body ignored, abort discarded by non-tf runners
```

The unified `report.Reporter` shown above is the one port all five runners consume (plan
05 §4.2). **Manual is the litmus proof that this unified port serves a trivial one-shot
runner too:** it builds a **one-step `RunStatus`** (the echoed inputs→outputs step, status
`SUCCEEDED`), calls `Register(status)` then `Report(status)`, and **discards the `abort`
return** — no Observer, no ticker. The handler-purity boundary holds: manual MAY read the
meshapi DTOs off `run.Details`, and the reporter is injected as a use-case-level port
(never a raw `meshapi.RunClient` the handler assembles with its own HTTP transport/auth).

- `RunPatcher` is a consumer-side two-method interface satisfied by
  `meshapi.RunClient` (B3) — fakeable without HTTP.
- Deliberately **stateless** (no accumulated step state): the Kotlin runners re-send
  only what changed (ado stage dedup, github job batches live in the *handlers*), so
  state here would be speculative (P3). Callers own dedup — verified against 06C/06D
  inventories in §17.
- `ReporterFactory` (in `manual`/consumer packages): builds the run-scoped
  `report.Reporter` (runToken-only client underneath) from
  base-URL-independent HAL links + runToken. **Link-based URLs, not EP templates**
  (umbrella §4 row 5 decision); C-P5/C-P7 pin `{sourceId}` substitution + the
  missing-placeholder error, reproduced in the Go client construction.

### 4.4 External-API error shape (umbrella §6 item 4 — choice stated)

Manual performs no external calls. Decision: the `MeshHttpException`-equivalent
(`ExternalCallError{UserMessage, SystemMessage, StatusCode, RequestUrl, ResponseBody}`)
is **specified here, implemented in 06B with its first consumer** — no dead type in
06A (P3). Its contract: fields map into step `userMessage`/`systemMessage` exactly as
`MeshHttpException` does today (umbrella §4 row 14); 06B–D instantiate the per-runner
message strings. §17 records the shape review against all three consumers.

## 5. Kotlin-isms → idiomatic Go (D15)

The umbrella §7.13 table instantiated for every Kotlin-ism this module actually uses.
Semantic-parity notes where the translation is not mechanical.

| Kotlin-ism (evidence) | Idiomatic Go replacement | Parity note |
|---|---|---|
| catch-all around fetch (`NoOpBlockRunnerService.kt:16-23`) | the persona's `ClaimClassifier`: every claim error ⇒ no-run-logged, `ClaimBackoff: 0` (umbrella §4 row 2) — the handler never sees claim errors | same observable: log + next-tick retry; new additive `runner_poll_errors_total` |
| propagated register/update exceptions (§2.5) | `Execute` returns wrapped errors (`fmt.Errorf("registering as source for run %s: %w", …)`) | same outcome: run unreported, loop logs; k8s exit semantics via §7.3 |
| `@Scheduled(10s)` + `ImmediateRetryDecorator` (`BlockRunRequestScheduler.kt:14`, `ImmediateRetryDecorator.kt:16-25`) | `dispatch.Loop{PollInterval: 10s}` + `InProcess.Done()` wake (immediate re-drain) | cadence-equivalent by construction (umbrella §4 row 1); pinned by loop-wiring tests §10.2 |
| `@Profile("kubernetes")` bean split + `SingleShotRunner`/`RunTerminator` (`BlockRunnerServiceConfiguration.kt:15-28`, `SingleShotRunner.kt:15-49`) | persona mode switch in `cmd/manual/main.go` (§7.3); exit code = return value of the single-run tail, no terminator interface (tests drive the handler, not the process) | exit-code delta on pre-report failures is the sanctioned §7.9 tightening |
| Spring DI / `@ConfigurationProperties(prefix="blockrunner")` (`ManualRunnerConfig.kt:5-7`) | constructor injection wired in `cmd/manual/main.go`; persona config struct over shared `config` + the `blockrunner:` compat block (§6) | key spellings preserved per §6.2 |
| subclass override `DebugBlockRunnerService : NoOpBlockRunnerService` (`DebugBlockRunnerService.kt:14-22`) | one package, config-selected debug execution path; no embedding-as-inheritance | pinned by M-P4/M-P5 → Go scenario twins |
| companion-object `STEP_ID` + `toOutputType` (`NoOpBlockRunnerService.kt:69-88`) | package-level typed constant + pure function | table pinned by the 8 kept unit tests |
| `associateBy { it.key }` (`:36-42`) | ordered loop into a map (last-wins) | M-P2 |
| kotlin-logging + logback pattern with MDC `requestId` (`application.yml:1-5`) | `log/slog` text handler, run id as attr (`Log.With("run", id)`) | log format is not a contract (umbrella §8); the §2.1.7 input-value logging is dropped |
| Jackson enum `MeshBuildingBlockIOType` (`MeshBuildingBlockIO.kt:9-18`) | DTO keeps the string type; typed mapping function with identity fallback (§4.2) | flag §16.5 |
| Jackson null-serializing `SourceUpdate` (`MeshObjectApiObjectMapper.kt`) | `omitempty` structs (§4.3) | null ≡ absent equivalence, flag §16.4 |
| OkHttp interceptors (Bearer/ApiKey/version-header) | existing `meshapi` AuthProvider/client composition; per-run client = runToken-only | header deltas are the uniform §7.7 sanctioned additive set |
| `Thread.sleep(5000)` debug waits (`DebugBlockRunnerService.kt:23-42`) | injected `Clock` wait respecting `ctx.Done()` | cadence not pinned (dev-only); cancellation is new-but-inert |
| `Math.random()` (`:44`) | injected `Rand func() float64` | outcome-set pinned (M-P4), distribution not |

## 6. Config

### 6.1 Persona config struct

```go
// manual.Config — persona extras only; the shared parts ride config.Api.
type Config struct {
    Uuid              string     // RUNNER_UUID / blockrunner.uuid
    Api               config.Api // url + auth (API key wins, B5)
    DebugMode         bool       // blockrunner.debugMode (manual-only key)
    MaxConcurrentRuns int        // new, default 3 (plan 05)
    Registration      *dispatch.RegistrationConfig // opt-in (plan 05 §9 shape)
}
```

Validation (P5, fail fast at startup): `uuid` and `api.url` required in polling mode;
auth required unless single-run mode (mirrors the tf single-run exemption, plan 03
§5.3); `debugMode` needs no validation.

### 6.2 Alias table (umbrella §5.4 instantiated — every shipped name keeps working)

| Existing name | Evidence | Handling |
|---|---|---|
| env `RUNNER_UUID`, `RUNNER_API_URL`, `RUNNER_API_USERNAME`, `RUNNER_API_PASSWORD`, `RUNNER_API_CLIENT_ID`, `RUNNER_API_CLIENT_SECRET` | `manual-block-runner/src/main/resources/runner-config.yml:3-14` | bound via `config.Env`, identical to the tf persona |
| env `VERSION` (feeds `blockrunner.version` → `X-Meshcloud-Runner-Version`) | `runner-config.yml:2`, `AuthHttpClientFactory.kt:70-82`, `jvm.Dockerfile:16` | honored as an **override of the ldflags build version** for `meshapi.Identity.Version` when set (else headers would silently change for operators who set VERSION at runtime); flag §16.6 |
| env `PORT` (Spring port, default 8104; image bakes 8080) | `application.yml:8`, `jvm.Dockerfile:18` | `MANAGEMENT_PORT` > `PORT` (deprecation-logged once) > default **8104** — plan-04 mechanics verbatim; the image keeps `ENV PORT=8080`, never bakes `MANAGEMENT_PORT` (plan 04 §10.7) |
| env `SPRING_PROFILES_ACTIVE=kubernetes` | `run-controller/runner-config.yml:139-142` | single-run trigger alias (§6.3) |
| yaml `blockrunner.uuid`, `.version`, `.debugMode`, `.api.url`, `.auth.username`, `.auth.password`, `.auth.api-key.client-id`, `.auth.api-key.client-secret` (kebab-case) | module yaml + `StandaloneBlockRunnerApiConfig.kt`, `ManualRunnerConfig.kt` | the `blockrunner:` compat block (§6.4) |
| yaml `logging.*`, `server.*`, `spring.*` | `application.yml` | ignored-with-warning when present in a mounted file (umbrella §5.4) |
| blank API-key credentials ⇒ Basic auth | `StandaloneBlockRunnerApiConfig.kt:35` | preserved by `config.Api.NewAuthProvider` (verify at step 0; else a normalization step blanks them before provider construction) |

New, additive only: `MANAGEMENT_PORT`, `RUNNER_CONFIG_FILE`, `maxConcurrentRuns` /
`RUNNER_MAX_CONCURRENT_RUNS`, `registration:`. Spring relaxed-binding spellings beyond
the literal shipped ones (e.g. `BLOCKRUNNER_UUID`) are not carried (umbrella §10.4).

### 6.3 Single-run activation (template artifact — shared helper)

```go
// config: single-run mode when EXECUTION_MODE == "single-run" (Go convention) OR
// SPRING_PROFILES_ACTIVE contains "kubernetes" (deployed operator contract, umbrella
// A12/§7.8; deprecation-logged once). Used by all four phase-6 personas; tf untouched.
func SingleRunMode(log *slog.Logger) bool
```

"Contains" = comma-separated list membership (Spring semantics: profiles are a list —
`kubernetes,extra` must still activate). Neither variable is ever *required*
(rollback symmetry, umbrella §5.9).

### 6.4 `blockrunner:` yaml compat block (template artifact — shared helper)

```go
// config: the Kotlin-era yaml surface, normalized into flat persona config after load.
// Zero-value fields = "not present"; every use is deprecation-logged once.
type BlockRunnerCompat struct {
    Uuid, Version  string
    DebugMode      *bool  // manual-only; other personas warn-and-ignore
    Api            struct{ Url string }
    Auth           struct{ Username, Password string; ApiKey struct{ ClientId, ClientSecret string `yaml:"client-id"…` } `yaml:"api-key"` }
    PrivateKey     string `yaml:"privateKey"`     // consumed by 06B–D personas
    PrivateKeyFile string `yaml:"privateKeyFile"`
}
```

Precedence: defaults < flat Go-native keys < `blockrunner:` block < env (a mounted
Kotlin-era file must fully configure the persona; explicit flat keys and env still
win — matches D7's defaults < file < env with both spellings inside "file").
Manual accepts-and-ignores `privateKey`/`privateKeyFile` with a notice (§2.6).

### 6.5 Private-key resolution order (template artifact — 06A defines, ships with tests)

Kotlin (`PrivateKeyLoader.kt:8-24`): env `RUNNER_PRIVATE_KEY_FILE` (non-blank) > yaml
`privateKeyFile` (non-blank) > default path `/app/runner-private.pem`; if the resolved
file does not exist ⇒ fall back to inline yaml `privateKey`. tf today binds
`RUNNER_PRIVATE_KEY_FILE` to a file path only. One resolution order for phase-6
personas, `config.ResolvePrivateKey(log, fileKey, inlineKey string) (pem string, err)`,
reproducing the Kotlin order exactly (incl. the missing-file → inline fallback).
Shipped in 06A per umbrella §6 item 3 with table-driven tests, even though the manual
persona does not call it — first caller is 06B (flag §16.8; reviewer may defer the
implementation to 06B, the *contract* is fixed here either way). The tf persona's key
handling is not touched (umbrella §2 out-scope).

## 7. Persona wiring & modes

### 7.1 Persona wiring & polling mode (`cmd/manual/main.go`, package main — only main wires, D11)

- `cmd/manual/main.go` is the manual runner's own `package main` — it links only the
  manual handler and its deps (no persona registry, no argv[0] switch of a shared binary).
  The same handler is *also* registered in the `cmd/bbrunner` superset (the opt-in
  all-in-process / local-dev build that links every handler and both dispatchers, persona
  by subcommand). Persona bootstrap sets `meshapi.Identity{Name: "manual-block-runner",
  Version: build.Version-or-VERSION}` (§6.2). No legacy alias names exist (the JVM image
  had no binary path — §8).
- Polling wiring: `dispatch.NewLoop(LoopConfig{PollInterval: 10s, ClaimBackoff: 0,
  MaxConcurrent: cfg.MaxConcurrentRuns /* default 3 */}, …)` +
  `dispatch.NewInProcess(map[…]{meshapi.RunnerTypeManual: handler})`, wake from
  `InProcess.Done()`; graceful shutdown = `Loop.Stop()` + `InProcess.Wait()` (plan 05
  §6). Type key comes from `meshapi.ToRunnerType`/the enum — no new literals
  (umbrella §7.12).
- **ClaimClassifier (template artifact, shared by all four ports):** 404 ⇒ no-run,
  409 ⇒ no-run-logged (`"Conflict at coordinator-api"` class), any other error ⇒
  no-run-logged + `runner_poll_errors_total`, always next tick (backoff 0) — the
  Kotlin catch-all policy (§2.1.1, umbrella §4 row 2), deliberately not tf's 60s
  backoff. Declared once (in `dispatch` or persona-shared helper), constructor-injected.
- Node id: the plain runner uuid (no worker suffix — plan 05 §16.5); header set =
  shared-client set, the uniform sanctioned additive delta (umbrella §7.7), verified
  once against mux + coordinator in this PR's acceptance step (§11).
- `mgmt.NewServer` on `config.ManagementPort(log, 8104, PORT-alias)` +
  `mgmt.RunMetrics` + plan-05 counters, wired exactly like `cmd/tf/main.go`. Metrics
  classification per umbrella §7.2 (terminal SUCCEEDED ⇒ succeeded; manual has no
  async handover case).
- Self-registration: **off by default** (Kotlin parity — the runner object is
  pre-created); the plan-05 `registration:` section available opt-in, default
  capability `MANUAL`.

### 7.2 Config loading

`config.Path` (`RUNNER_CONFIG_FILE`, default `runner-config.yml`) → `LoadFile` into the
persona struct (flat keys + `BlockRunnerCompat` block, §6.4) → normalize → `config.Env`
bindings (§6.2) → validate. Missing file tolerated (defaults + env — the Kotlin
classpath default plays that role today, §2.6).

### 7.3 Single-run mode (k8s Job)

Activated by `config.SingleRunMode` (§6.3). Flow: read `RUN_JSON_FILE_PATH` (required;
its default mount `/var/run/secrets/meshstack/run.json` is controller-side, frozen) →
parse into `dispatch.ClaimedRun` (raw JSON + `RunDetailsDTO`; `UseNumber` per §4.2) →
build the handler with a run-scoped reporter (runToken from the file's spec — the
controller strips nothing; k8s trust model unchanged) → `handler.Execute` once, no
loop, no mgmt listener (umbrella §7.10). Exit semantics = the 2b-R12 rule (umbrella
§7.9): exit 0 iff a terminal status was reported — for manual that is the single
SUCCEEDED update. Consequences: update/register failure ⇒ non-zero (Kotlin: exit 1 —
parity); file-missing/parse failure ⇒ non-zero (Kotlin: exit 0 swallow — the
sanctioned, flagged delta anchored by pin M-P7, umbrella §10.3). `BackoffLimit: 1`
then retries only runs meshStack never heard about (plan 05 §16.3).

### 7.4 Modes × behavior summary

| | polling (standalone) | single-run (k8s Job) |
|---|---|---|
| claim | Loop 10s + immediate re-drain, classifier §7.1 | none — file source |
| decryption | none (ciphertext echo, §2.1.6) | none (controller pre-decrypted) |
| reporting auth | per-run runToken only | runToken from run JSON |
| mgmt listener | 8104 (aliases §6.2) | none |
| debug mode | honored (`debugMode`) | honored (config-driven, as Kotlin) |
| exit | long-running | R12 rule (§7.3) |

## 8. Dockerfile & image switch

The template all of 06B–D copy (umbrella §5.6):

- New per-persona `containers/manual-block-runner/Dockerfile` building only the manual binary
  (`go build ./cmd/manual`): `alpine:3.22.4` (same digest pin as the other runner
  images), `apk add ca-certificates bash` only (HTTP-only runner — no git/tofu/nix),
  meshcloud uid 2000, the fit binary at `/app/manual-block-runner` (its own binary — no
  shared `bbrunner`, no symlink entrypoint), config
  `COPY containers/manual-block-runner/runner-config.yml /app/runner-config.yml`,
  `ENV PORT=8080`, `EXPOSE 8080` (parity with `jvm.Dockerfile:18-19`), and a **direct**
  `ENTRYPOINT ["/app/entrypoint.sh", "/app/manual-block-runner"]` (the Go entrypoint's
  CA-import + `exec "$@"` runs the persona binary directly — no argv[0] multiplexing,
  plan 04 §4.4).
- `containers/manual-block-runner/runner-config.yml`: a **per-impl** config file that
  **deep-merges over** a shared top-level base `runner-config.yml` (base < per-impl < env)
  — the template layering the other three
  ports reuse (umbrella §5.4/§10.5). It is effect-equivalent to the Kotlin classpath
  defaults (§2.6) in Go-native flat keys — uuid `d943b032-…`, api url
  `http://localhost:8301`, `bb-api`/`guest`, `debugMode: false` — with comments naming
  the env overrides. No private key at either layer for manual (manual needs none; the
  well-known dev key lives in the shared base only for 06B–D, umbrella §10.5).
- Published name/tags unchanged: `ghcr.io/meshcloud/manual-block-runner:main` + release
  tags. Deployed controller configs keep working because the image honors their baked
  `SPRING_PROFILES_ACTIVE: kubernetes` (§6.3, umbrella A12).
- CI flip **in the same PR as the module removal** (§12): `ci.yml` — drop the
  `manual-block-runner` entries from `jvm-runners-ci`
  (`ci.yml:30-31`) and `jvm-runners-image` (`:66-67`), add a `manual-block-runner` leg to
  the `go-runners-image` matrix (`file: containers/manual-block-runner/Dockerfile`) plus a
  `./cmd/manual` leg to the go build matrix; `build-images.yml:32-34` — the manual
  leg becomes `dockerfile: containers/manual-block-runner/Dockerfile` (a per-persona
  Dockerfile — no shared `target:` stage).
- Explicit non-goal (umbrella §5.6): no `java`-shaped compat. The JVM entrypoint was
  `["/app/entrypoint.sh","java","-jar","/app/executable"]` (`jvm.Dockerfile:27`);
  an operator `command:` override with java arguments breaks — flagged §16.9, accepted
  because the shipped controller config uses the default entrypoint (umbrella A12) and
  no `/app/<binary>` path existed on the JVM image to alias.

## 9. Migration sequence

Always-green steps sized for one reviewable single-commit PR; after every step
`task test` + `task lint` green, `task coverage` ≥ gate, **and** `./gradlew check`
green until step 9. Gradle CI stays green until the removal step (umbrella §5.1.9).

| # | Step | What changes | What proves it |
|---|---|---|---|
| 0 | **Preflight.** Run umbrella A1–A12 + B1–B8 verifications on the phase-5 branch; branch `phase-6a-manual`. Record: the A7/plan-05 metric classification, the R12 exit condition, whether DTO decoding already uses `UseNumber` (§4.2), whether `config.Api.NewAuthProvider` blanks empty API-key creds (§6.2). Run the §17 fit review and record its outcome in the PR. | nothing | STOP-A / STOP-D gate |
| 1 | **Kotlin pins (tests only).** §3.2 M-P1–M-P8 in `manual-block-runner`, §3.3 C-P1–C-P7 in `block-runner-core`. | Kotlin test files only | `./gradlew :manual-block-runner:check :block-runner-core:check` green; `git diff -- ':!*test*'` empty |
| 2 | **Wire seam.** `meshapi.SourceUpdateDTO`/`StepUpdateDTO` + the unified `report.Reporter` (§4.3) + the link-based run-scoped client construction (`{sourceId}` substitution, missing-placeholder error). | `internal/meshapi`, `internal/report` | new transcript tests = Go twins of C-P3–C-P7 (fake transport); both packages stay ≥90 |
| 3 | **Config compat helpers.** `config.SingleRunMode` (§6.3), `config.BlockRunnerCompat` + normalization (§6.4), `config.ResolvePrivateKey` (§6.5). | `internal/config` | table-driven tests: profile-list membership, precedence per §6.4, the full Kotlin key-resolution order incl. missing-file→inline fallback; `config` ≥90 |
| 4 | **Handler.** `internal/manual`: `Config`, `NewHandler`, echo path incl. `toOutputType` + last-wins + number fidelity, debug path (Clock/Rand injected). | `internal/manual` | Go scenario suite (§10.1): run JSON in → fake meshStack transcript out, matching the Kotlin pins; unit tests for the mapping table |
| 5 | **Persona wiring, polling.** `cmd/manual/main.go` + register the manual handler in the `cmd/bbrunner` superset; ClaimClassifier (§7.1); mgmt on 8104 + metrics; loop wiring. | `cmd/manual/main.go`, `cmd/bbrunner/main.go` | loop-wiring scenario: claim 200→register→update→immediate re-claim→404; classifier table tests; `cmd/bbrunner` subcommand-dispatch test row; alias-precedence test (`MANAGEMENT_PORT`>`PORT`>8104) |
| 6 | **Single-run mode.** `SingleRunMode` activation, file source, R12 exit tail. | `cmd/manual/main.go` (+ small `manual` glue) | single-run scenario: the `ManualRunnerKubernetesStartupScenario` fixture JSON (§3.1) driven through the persona path produces the pinned register/update wire; exit-condition tests for M-P6/M-P7 twins (§10.1) |
| 7 | **Gate + tooling.** `thresholds.txt` gains `internal/manual 90` (no exclusions); depguard: `manual` may import `dispatch`/`meshapi`/`report`/`config` + stdlib only, nothing imports `manual` but main. | `tools/coverage/*`, `.golangci.yml` | induced-failure check on the new line; `task coverage` green |
| 8 | **Image.** `containers/manual-block-runner/Dockerfile` + `containers/manual-block-runner/runner-config.yml` (§8). | containers/ | `docker build -f containers/manual-block-runner/Dockerfile` + container smoke: healthz `OK` on 8080, boots to claim loop against a stub |
| 9 | **Acceptance gate (§11).** local-dev-stack with the Go persona; k8s single-run smoke; side-by-side transcript check. | — | STOP-E lives here; evidence in the PR description |
| 10 | **Removal.** Delete `manual-block-runner/`; `settings.gradle:4` include dropped; CI legs flipped per §8; meshfed-release lock-step edits (§15); grep gate: no `manual-block-runner/` path references outside CHANGELOG/plan docs (image/persona name references remain). | module dir, gradle, workflows | full CI green incl. the flipped image leg; `./gradlew check` still green for the remaining modules |

## 10. Test plan & gate (D16)

### 10.1 Pin → Go mapping (N:1 into scenarios by design, umbrella §5.2)

| Kotlin pin/test | Go destination | Kind |
|---|---|---|
| `NoOpBlockRunnerServiceTest` no-run / fetch-exception | loop scenario: claim 404 / claim 500 ⇒ no handler call, next tick, poll-error metric | scenario (consolidated) |
| `NoOpBlockRunnerServiceTest` happy path + M-P8 (ordering/cardinality) | `Scenario_Manual_PollingRun_EchoesInputsAndSucceeds`: transcript asserts one register (PENDING `manual` step) then one PATCH (SUCCEEDED + outputs), nothing else | scenario |
| 8 × `toOutputType` unit tests | `Test_ToOutputType` table (8 rows + unknown-identity row) | **keep-as-unit** (pure mapping table — the §5.2 criterion) |
| M-P1 ciphertext echo, M-P2 last-wins, M-P3 value fidelity | assertions inside the echo scenario's fixture (sensitive + duplicate + large-number inputs in one run JSON) | scenario |
| M-P4/M-P5 debug sequence + swap | `Scenario_Manual_DebugMode`: fake clock, seeded Rand both branches; 4 PATCHes with the pinned shapes; raw-type quirk asserted | scenario |
| `ManualRunnerKubernetesStartupScenario` | `Scenario_Manual_SingleRun_FileSource`: same fixture JSON, captured wire equal to the Kotlin capture (modulo §16.4 null-equivalence + §7.7 headers) | scenario |
| M-P6 exit-1-on-report-failure | single-run exit test: PATCH 500 ⇒ error ⇒ non-zero condition | scenario |
| M-P7 exit-0-on-fetch-failure | **deliberate non-port**: Go asserts non-zero (the §7.9 delta); the test comment cites M-P7 as the measured baseline | flagged delta, not STOP-B |
| C-P1–C-P7 core wire pins | Go twins in `meshapi`/`report` tests (claim shape lives with the plan-05 claim adapter tests; register/update via the unified `report.Reporter`) | scenario/transcript |
| `ImmediateRetryDecoratorTest` | loop wake test (claim→run→`Done()`→immediate re-claim) — already the plan-05 loop suite's shape, re-asserted for this persona's wiring | scenario |
| `ManualRunnerStartupScenario`, Spring auth/config scenarios | persona boot smoke (`go run . manual-block-runner` to config-read stage) + existing `config`/`meshapi` auth tests | existing + smoke |

No Kotlin assertion changes shape in a way STOP-B forbids; the only asserted-behavior
change is M-P7, sanctioned by umbrella §7.9/§10.3.

### 10.2 New Go-only tests

Loop-wiring cadence for this persona (10s tick, backoff 0, classifier table), alias
precedence (§6.2), `SingleRunMode` table, `BlockRunnerCompat` precedence,
`ResolvePrivateKey` table, mgmt-on-8104 smoke, unknown-type warn path, ctx-cancel in
debug waits. All hermetic (fake transport, fake clock, temp files).

### 10.3 Gate

`tools/coverage/thresholds.txt` += `github.com/meshcloud/building-block-runner/internal/manual 90`.
**No exclusion entries** (whole package hermetic — umbrella §5.3). Touched shared
packages (`meshapi`, `report`, `config`) stay ≥90 via the step-2/3 tests. Coverage
arithmetic: the package is ~200 lines of echo/debug/config logic fully driven by the
scenario suite — comfortably ≥90; a shortfall is STOP-C (add scenario cases, never
exclusions). `-race` stays on; the debug clock/rand injection keeps it deterministic.
Nobody adds unit tests to move the number — the keep-as-unit list is exactly
`Test_ToOutputType` + the config tables (real decision surface).

## 11. Acceptance validation

Manual is the one runner with real acceptance coverage (umbrella §5.7 finding) — the
gate before removal (step 9), all three legs required:

1. **local-dev-stack flow with the Go persona:** start via `go run . manual-block-runner`
   (env `RUNNER_API_URL=http://localhost:8301`, config per §15's SKILL edit) replacing
   the gradle bootRun; run **≥1 MANUAL acceptance run** through the meshfed-release
   suite; the whole acceptance flow stays green as the outer net (umbrella A11/§5.7).
2. **k8s single-run smoke:** the `ManualRunnerKubernetesStartupScenario` fixture JSON
   as `RUN_JSON_FILE_PATH` + `SPRING_PROFILES_ACTIVE=kubernetes` against a captured
   fake meshStack → wire transcript equal to the Kotlin capture (modulo the sanctioned
   deltas: §7.7 headers, §16.4 null ≡ absent); exit 0. Executed against the built image
   (docker run) so the direct-entrypoint / persona-binary / env path is proven, not just the test suite.
3. **Side-by-side transcript equivalence (template for 06B–D):** the same run JSON
   (sensitive + duplicate + typed inputs) driven through the Kotlin runner (pin-suite
   capture) and the Go handler (fake-transport capture); diff empty modulo the
   sanctioned-delta list. 06A establishes the comparison procedure and the delta
   allowlist wording that 06B–D reuse verbatim.

Evidence (commands, transcripts, acceptance-run link) goes in the PR description
(STOP-E). Only after this gate do the §9 step-10 removal commits land.

## 12. Kotlin module removal + Gradle shrink

Umbrella §5.8 recipe instantiated (last commits of the PR, after §11 passes):

1. `git rm -r manual-block-runner/` (tracked files only — `build/` is untracked).
   **The §3.2 M-pins die with the module** — acceptable because their Go twins (§10.1)
   are the surviving pin; the §3.3 C-pins live in `block-runner-core` and **stay**
   (06B–D inherit them; they are deleted only in 06D's core removal).
2. `settings.gradle`: drop `include 'manual-block-runner'` (`settings.gradle:4`).
3. `.github/workflows/ci.yml`: drop the `manual-block-runner` matrix entries from
   `jvm-runners-ci` (`:30-31`) and `jvm-runners-image` (`:66-67`); add the go image
   leg (§8).
4. `.github/workflows/build-images.yml`: manual leg (`:32-34`) →
   `dockerfile: containers/manual-block-runner/Dockerfile` (a per-persona Dockerfile —
   no shared `target:` stage; drop `runner-module:`).
5. meshfed-release lock-step doc edits (§15) merged together with this PR.
6. Grep gate: `grep -rn "manual-block-runner" --exclude-dir=.git` — remaining hits
   must be image/persona *names* (workflows, containers/, run-controller sample
   config, plan docs, CHANGELOG), never module *paths* (`manual-block-runner/src`,
   `:manual-block-runner:` gradle refs).

No other Gradle shrink here — `block-runner-core`, root `build.gradle`, wrapper,
`jvm.Dockerfile` all stay until 06D (umbrella §5.8).

## 13. Frozen contracts touched

Umbrella §8 instantiated for MANUAL. **Preserved (proven by pins → ported tests):**

- Claim wire: endpoint + query + v1 media types both headers; 404/409 = no-run.
- Register wire: one-step PENDING body, source id = runner uuid, 409 = success.
- Update wire: the lean `SourceUpdate` PATCH (§4.3) to the `{sourceId}`-substituted
  HAL link; runToken-only auth on run-scoped calls; response/abort flag ignored.
- Step id `manual` + display name `Manual Block Run`; echo semantics (type-mapping
  table, sensitivity flag, last-wins, value fidelity); terminal SUCCEEDED in one update.
- k8s single-run contract: `RUN_JSON_FILE_PATH`, `RUNNER_UUID`, `RUNNER_API_URL`,
  `SPRING_PROFILES_ACTIVE: kubernetes` accepted (D10 both directions: old controller
  config → new image, and rollback to the JVM image).
- Image name/tags; `ENV PORT=8080`/`EXPOSE 8080`; healthz body `OK` on the resolved
  legacy port; all §6.2 env vars and yaml keys; mux MANUAL port `:8301`; debugMode
  semantics.

**Sanctioned, flagged deltas (uniform umbrella wording):** additive client headers
(§7.7); single-run exit tightening on pre-report failures (§7.9, M-P7); no listener in
single-run pods (§7.10 — the Spring pod served an unprobed healthz); additive
metrics/config (`runner_*` series, `MANAGEMENT_PORT`, `maxConcurrentRuns`,
`registration:`); JVM `command:`-override incompatibility (§8); slog text format —
including the deliberate drop of the §2.1.7 input-value log line; null ≡ absent JSON
serialization of optional PATCH/register fields (§16.4).

## 14. Rollback story

One squash commit ⇒ one `git revert` restores the Kotlin module, its `settings.gradle`
include, both CI matrix entries and the JVM image leg, and deletes `internal/manual`,
`cmd/manual/main.go` + its `cmd/bbrunner` handler registration, the per-persona
`containers/manual-block-runner/Dockerfile`,
`containers/manual-block-runner/`, the thresholds line, the depguard rules, and the
shared helpers of §4.3/§6.3–6.5 (their only consumers revert with them). Because the
image name and every wire/k8s contract are frozen (§13), `:main` floats back to a
JVM-built image on the next CI run and **deployed operator configs need no change in
either direction** — `SPRING_PROFILES_ACTIVE` is honored by both generations and
`EXECUTION_MODE` never became required (§6.3). Release tags are immutable. Lost on
revert (documented cost): `MANAGEMENT_PORT`/metrics on the manual persona,
`maxConcurrentRuns` > 1, opt-in registration, the exit-code tightening. The
meshfed-release SKILL edit (§15) reverts in the same motion — its PR is linked from
this one so the pair reverts together. The block-runner-core wire pins (C-P1–C-P7) are
tests-only and **survive the revert harmlessly** (they pin current behavior).

## 15. Cross-repo touch points

Umbrella §9 subset for 06A — the only phase-6 sub-plan with mandatory cross-repo edits:

- **meshfed-release `.agents/skills/local-dev-stack/SKILL.md` (lock-step PR, §9 step
  10):** the manual-runner block (lines 64-71, `./gradlew :manual-block-runner:bootRun`)
  becomes the Go start (`cd ../building-block-runner`, env
  `RUNNER_API_URL=http://localhost:8301` +
  `RUNNER_CONFIG_FILE=containers/manual-block-runner/runner-config.yml`,
  `nohup go run . manual-block-runner > /tmp/manual-runner.log 2>&1 &`); readiness
  table (~line 103): `Started BlockRunnerApplication` marker → the persona's Go
  readiness line; pgrep hint (lines 88-91): `BlockRunnerApplication` →
  `manual-block-runner` (appears as the persona arg). Exact line numbers re-verified at
  step 0 against the post-04 SKILL state (plan 04 §9 already edited the tf block).
- **meshfed-release `how-to-run-building-block-runners.md`:** doc-truth check (umbrella
  §9) — image name unchanged; if the page documents `SPRING_PROFILES_ACTIVE` semantics
  for the manual image, add the `EXECUTION_MODE` note in the same lock-step PR; full
  docs pass stays phase 7.
- **meshfed-release acceptance tests / mux:** read-only; MANUAL claims via mux `:8301`
  (wire frozen); the suite is §11 leg 1.
- **This repo, `run-controller/runner-config.yml` sample:** valid unchanged (the new
  image honors the profile env); optional comment noting `EXECUTION_MODE: single-run`
  as the preferred form — flipping the sample is deferred to phase 7 (umbrella §9).
- **terraform-provider-meshstack:** grep its skills for manual-runner references at
  step 0 (umbrella §9 expects none) — no edit.

## 16. Flags + Open questions

Findings the umbrella / prior plans did not anticipate, plus judgment calls for review:

1. **Manual needs no `Decryptor` dep at all** — the umbrella §5.3 `HandlerDeps`
   prose lists one for every handler; manual omits it (§4.1). Recorded as a
   manual-specific narrowing so 06B–D's addition is not read as a template deviation.
2. **The per-run Kotlin client sends no `X-Meshcloud-Runner-Version`** (only the claim
   client does, §2.4) — the umbrella §4 row 4 header delta is therefore *larger* on
   run-scoped calls than stated (version header is additive there too). Same
   sanctioned-additive treatment (§7.7); C-P3 pins the Kotlin baseline.
3. **Kotlin logs decrypted sensitive input values at INFO in k8s mode**
   (`NoOpBlockRunnerService.kt:61-64`, §2.1.7) — a secret-hygiene bug in the log
   surface. Not ported (logs are not contract); not fixed Kotlin-side in this PR
   (D13 discipline; noted for a follow-up).
4. **Jackson serializes explicit `null`s** for unset optional fields in register/update
   bodies (`MeshObjectApiObjectMapper` default inclusion, §2.4) while the Go DTOs use
   `omitempty` — the umbrella §8 "frozen byte-identically" wire claim is actually
   "identical modulo null ≡ absent". Pins and side-by-side comparison (§11.3) are
   defined at parsed-JSON level; 06B–D inherit this equivalence rule.
5. **Unknown input-type strings are representable in Go but not in Kotlin** (enum parse
   failure ⇒ whole-claim failure, §2.1.5). Go maps unknown → identity + warn instead of
   inventing a run-failing path. Reviewer may prefer fail-the-run.
6. **`VERSION` env is a runtime version override in Kotlin** (feeds the header via
   config, §6.2) but ldflags-baked in Go — 06A honors `VERSION` as an override to keep
   the shipped-name promise literal. Reviewer may drop it (image build-arg already
   aligns the values in practice).
7. **Umbrella erratum:** `NoOpBlockRunnerServiceTest` has 11 tests, not 12 (§3.1) —
   no content impact.
8. **`config.ResolvePrivateKey` ships consumer-less in 06A** (§6.5): umbrella §6 item 3
   orders the helpers into the template PR; P3 argues for shipping with 06B. Shipped
   here with tests + fixed contract; reviewer may move the implementation to 06B.
9. **JVM `command:`-override incompatibility** (§8): no `/app/<binary>` existed in the
   JVM image, so nothing on the Go image can alias `java` for operators who exec java
   directly — accepted per umbrella §5.6, restated here because 06A sets the wording
   B–D copy.
10. **M-P7 exit-code delta** (§7.3): the one place a pinned Kotlin behavior is
    deliberately not preserved — umbrella §7.9/§10.3 sanction it; the pin documents
    the baseline. Fixed in phase 6; the old exit-0 behavior stays pinned (M-P7) for audit.

**Open questions:** none — every decision branch was walked and resolved from the
sources; the reviewer-vetoable judgment calls are flags 5, 6, 8, 10 above and the
umbrella-level calls they instantiate (§7.4 lean body, §7.9 exit rule, §10.4
relaxed-binding boundary).

## 17. Template review against 06B–D

The mandated fit review (umbrella §6 item 6, STOP-D): every template artifact this PR
establishes, walked against the umbrella §3 inventories of the three remaining runners.
Executed now at plan level; re-run mechanically at §9 step 0 before coding.

| Template artifact (06A) | gitlab (06B) | azure-devops (06C) | github (06D) | Fit |
|---|---|---|---|---|
| `dispatch.RunHandler`/`ClaimedRun` (plan 05 §4) | needs impl JSON (`ClaimedRun.Details…Implementation` raw), decrypted trigger token, HAL links for callback vars — all in `Details`/deps | needs impl + PAT + `async` flag + poll loop inside `Execute` (ctx-cancelable, own 30-min bound — A1 "handler owns its timeout") | needs impl + `AppPem` + dual input modes + find-run/poll loops | **fits; no new `Execute` parameter** (confirms plan 05 §4.3) |
| `HandlerDeps` pattern (§4.1) | + `meshapi.Decryptor` + external HTTP seam | same + `Clock` for 10s/30-min polling | same + JWT signer inputs (all constructor-injected) | fits — deps grow per runner, shape unchanged (flag §16.1) |
| Unified `report.Reporter` seam (§4.3) | 2 updates max (trigger step FAILED/SUCCEEDED; final = **IN_PROGRESS handover**, status field carries it — lean DTO covers) | many partial updates: stage steps `ado-stage-<id>` re-reported only when new/COMPLETED — caller-side dedup, stateless seam suffices; `displayName`/messages per step covered by `StepUpdateDTO` | job-step batches `gh-workflow-job-<id>`, trigger step only in first batch — again caller-side | fits — statelessness confirmed as the right altitude; **no ticker/abort** holds for all three (umbrella §7.5) |
| Lean `SourceUpdateDTO` (§4.3) | needs status-only and steps-only updates ⇒ both fields `omitempty` ✓ | needs step updates without run status ✓ | same ✓ | fits |
| Register = one PENDING step (§4.3) | `gl-trigger` | `azure-devops-trigger` | `gh-trigger` | fits (all four register exactly one step) |
| ClaimClassifier policy (§7.1) | identical catch-all (`GitLabBlockRunnerService` twin of §2.1.1) | identical | identical | fits — one shared classifier, defined here |
| `SingleRunMode` alias (§6.3) | same operator config (`runner-config.yml:149-152`) | same (`:154-157`) | same (`:144-147`) | fits verbatim |
| `BlockRunnerCompat` (§6.4) | + `privateKey`/`privateKeyFile` consumed (baked dev key, umbrella §10.5) | same | same | fits — fields already present, manual merely ignores them |
| `ResolvePrivateKey` (§6.5) | first consumer | consumer | consumer | fits (contract = Kotlin `PrivateKeyLoader`, shared by all three) |
| `ExternalCallError` shape (§4.4) | 404/verification/generic message pairs map onto {User,System,Status,Url,Body} | MeshHttpException fields 1:1 | 422-heuristic needs `ResponseBody` access ✓ | fits; ships in 06B as stated |
| Per-persona Dockerfile + config layout (§8) | + baked dev private key file placement (conscious, umbrella §10.5) | same | same | fits — one extra COPY per runner, pattern unchanged |
| Metrics classification (§7.1/umbrella §7.2) | always-async: handover + nil return ⇒ **succeeded** — rule already fixed umbrella §7.2 | async flag ⇒ same rule; sync ⇒ terminal-status keyed | same | fits |
| `UseNumber` decode fidelity (§4.2) | required — `MESHSTACK_RUN` embeds run JSON | stringified `templateParameters` | base64 `buildingBlockRun` payload | fits; recorded as template requirement here so B–D don't rediscover it |
| Removal recipe + CI flip (§8/§12) | mechanical repeat | mechanical repeat | + core/Gradle teardown (umbrella §5.8) | fits |

**Gaps deliberately left to their owners (not template defects):**
`meshapi.DecryptInputs` (input-only decryption preserving the impl-secret asymmetry) —
owner 06B, signature reviewed against 06D (umbrella §4 row 8, §7.6);
`ExternalCallError` implementation — 06B (§4.4); poll-loop helpers — 06C/06D local
(two consumers with different step semantics; a shared poller would be speculative, P3).

**Result: no `RunHandler`/`ClaimedRun`/`registration:`/reporter shape change required —
plan 05 §4 and the umbrella stand unrevised.** Any contradiction discovered when
06B–D's plans are reviewed in parallel (umbrella §6 review protocol) or during
implementation is a STOP-D umbrella/plan-05 revision, never a local workaround.
