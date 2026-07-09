# Detail Plan 06B — GitLab Runner Port (Phase 6, PR 2)

**Phase:** 6b · **Branch:** `refactor/single-go-binary/phase-6b-gitlab` (stacked on
`refactor/single-go-binary/phase-6a-manual`) · **Delivery:** one single-commit PR ·
**Binding:** umbrella `PLAN_DETAIL_06_kotlin_ports_umbrella.md` (§5 template contract,
§7 consistency rules, §8 frozen contracts) + `PLAN_HIGH_LEVEL.md` §3 P1–P8, D5, D6
(Kotlin corollary), D7, D9 (always-async handover), D11 (`internal/gitlab`), D12
(port 8103), D15, D16.

Kotlin references are `main` @ `c3fce61`; Go references marked *post-N* are shapes
promised by plan N or by 06A (the template PR). This sub-plan additionally ships two
umbrella-assigned artifacts: **`meshapi.DecryptInputs`** (umbrella §4 row 8, §7.6) and
the **`ExternalCallError`** type specified in 06A §4.4 (§4.5 here).

## 1. Assumptions from prior phases

Plans 00–05 and 06A are **not implemented yet**. Implementation begins by running **all
umbrella §1 verification steps (A1–A12)** — incorporated by reference — plus the
06A-template verifications and the 06B-specific rows below. Any material failure is a
**STOP** per the umbrella's STOP-A.

**06A template artifacts this plan consumes (verify each exists as promised):**

| # | Assumption | Promised by | Verification step |
|---|---|---|---|
| T1 | `meshapi.SourceUpdateDTO`/`StepUpdateDTO` (the marshaled lean PATCH body the adapter produces from the changed steps, all fields `omitempty`) + the unified `report.Reporter{Register(RunStatus) error, Report(RunStatus) (abort bool, err error)}` over a run-scoped `RunPatcher`, stateless, link-based URL construction with `{sourceId}` substitution + missing-placeholder error. Ported handlers call `Report(RunStatus)` with only the changed/new steps present in `RunStatus.Steps` (backend upserts steps by id) and **discard the `abort` return** (no Observer/ticker for the ports; the handler still owns its own step dedup — stateless in the no-ticker sense). | 06A §4.3, steps 2 | read `runner/internal/report`; run the Go twins of C-P3–C-P7 |
| T2 | `config.SingleRunMode` (`EXECUTION_MODE=single-run` OR `SPRING_PROFILES_ACTIVE` list-contains `kubernetes`, deprecation-logged), `config.BlockRunnerCompat` (incl. `privateKey`/`privateKeyFile` fields), `config.ResolvePrivateKey(log, fileKey, inlineKey)` reproducing `PrivateKeyLoader.kt:8-24` order (env `RUNNER_PRIVATE_KEY_FILE` > yaml file key > `/app/runner-private.pem`; missing file ⇒ inline fallback). | 06A §6.3–6.5 | read `runner/internal/config`; run its table tests. If 06A's reviewer deferred the `ResolvePrivateKey` *implementation* to 06B (06A flag §16.8), implement it here in step 3 against the fixed contract |
| T3 | Shared `ClaimClassifier` (404 ⇒ no-run, 409 ⇒ no-run-logged, other ⇒ no-run-logged + `runner_poll_errors_total`, backoff 0) + persona wiring pattern (`cmd/manual/main.go` + its `cmd/bbrunner` superset registration), `MANAGEMENT_PORT`/`PORT` alias mechanics, R12 single-run exit tail, per-persona `containers/<persona>-block-runner/Dockerfile` pattern (direct entrypoint), `containers/<persona>-block-runner/runner-config.yml` layout, removal recipe, side-by-side comparison procedure + sanctioned-delta allowlist wording. | 06A §7, §8, §11.3, §12 | read `runner/cmd/manual/main.go`, `runner/cmd/bbrunner/main.go`, `containers/manual-block-runner/Dockerfile`; re-read 06A §11.3 evidence in its merged PR |
| T4 | The block-runner-core wire pins C-P1–C-P7 exist and are green (06B inherits, never re-writes). | 06A §3.3 | `./gradlew :block-runner-core:check`; grep the pin test names |
| T5 | `dispatch.ClaimedRun.RawJson` carries the claimed run JSON **base64-encoded** (today's controller shape, `runapi.go:59`) and `Details` is the parsed `RunDetailsDTO` with `Links{Self, RegisterSource, UpdateSource, MeshstackBaseUrl}` (`go-meshapi-client/meshapi/dtos.go:19-28`). | Plan 05 §4.1 | read `runner/internal/dispatch`; `grep -n "RawJson" runner/internal/dispatch` |
| T6 | Handler-visible JSON decoding preserves number fidelity (`json.Decoder.UseNumber` or equivalent) — recorded as a template requirement in 06A §17 precisely because gitlab embeds run JSON in outbound payloads. | 06A §4.2/§17 | read the decode path; run 06A's M-P3 Go twin |
| T7 | The Kotlin gitlab module + block-runner-core are byte-identical to `main` @ `c3fce61` (all §2 citations hold); `./gradlew :gitlab-block-runner:check` green on the phase-6a branch. | Umbrella A10, 06A scope | `git diff main..phase-6a-manual -- gitlab-block-runner/ block-runner-core/` — tests-only additions (the 06A C-pins) |
| T8 | `crypto.MeshCertBasedCrypto.DecryptMeshCertBased` errors on empty input (`"encrypted value empty or too short"`, `go-meshapi-client/crypto/meshcertbasedcrypto.go:117-132`) while Kotlin's `decrypt("")` returns `""` (`MeshCertDecryptionService.kt:34-37`) — the `meshapi.Decryptor` seam (plan 03/05) either already skips empty strings or 06B adds that rule (§4.4). | Current `main` + plan 03 §5.2 | read the `Decryptor` implementation; scratch test `Decrypt("")` |

**STOP markers.** Umbrella STOP-A–E verbatim: STOP-A = §9 step 0; STOP-B = any §3 pin
unportable per §10's mapping; STOP-C = `internal/gitlab` (or a touched shared package)
below 90 at any checkpoint; STOP-D = the §4.6 fit-check finds a template shape that
does not fit — umbrella/plan-05 revision, never a local workaround; STOP-E = §11 gate
fails. Additional 06B-specific: **STOP-F** — if pin G-P1 (§3.2) reveals that the
serialized `MESHSTACK_RUN` payload shape differs materially from the claim-response
JSON shape assumed in §4.2 (the `@JsonUnwrapped` question, flag §16.6), revise §4.2
before coding the payload builder.

## 2. Kotlin behavior inventory

Full study of `gitlab-block-runner` (7 production files, ~330 lines) deepening umbrella
§3.2 "gitlab". The block-runner-core mechanics are the umbrella §4 map + 06A §2.4 wire
pins — inherited, not restudied.

### 2.1 Service flow (`GitLabBlockRunnerService.kt`)

1. **Claim-and-swallow:** any fetch exception is caught, logged (`"Unexpected error
   while getting a block run."`) and treated as no-run (`:21-27`) — the manual twin
   (06A §2.1.1); next 10s tick, no backoff.
2. **Register first, always:** exactly one step, `STEP_ID = "gl-trigger"` (companion,
   `:128-131`), display name `"Trigger GitLab CI/CD"` (`:30-33`). Registration happens
   **before** implementation extraction; register failures propagate uncaught (§2.5).
3. **Implementation extraction:** `getImplementation<MeshBuildingBlockGitlabImplementation>()`
   (`:35-41`); wrong type ⇒ `IllegalStateException` (`MeshBuildingBlockRun.kt:132-139`,
   message `"The building block implementation of run <uuid> was not of expected type."`)
   ⇒ the internal-error failure update (§2.3 row C) and return (run FAILED, no trigger).
4. **Client construction:** `GitLabClientFactory.provideClientFor(impl.gitlabBaseUrl)`
   sanitizes the base URL (`GitLabClientFactory.kt:13-17`; `UrlSanitizerService.kt:8-20`:
   trim, drop one trailing `/`, **empty ⇒ `IllegalArgumentException("URL should not be
   empty")`**) — any exception ⇒ internal-error failure update (`:43-49`).
5. **Trigger call** (`:51-57`): `triggerPipeline(pipelineToken =
   decryptionService.decrypt(impl.pipelineTriggerToken), refName = impl.refName,
   projectId = impl.projectId, run = decryptionService.decryptBlockRunInputs(activeBlockRun))`.
   Both decrypt calls happen **inside** the try: a decrypt failure takes the
   internal-error path (run FAILED), not the claim-swallow path.
6. **Failure updates** (§2.3): every trigger-path failure reports run `FAILED` + step
   `gl-trigger` `FAILED` in one lean `SourceUpdate` and returns null (`:58-66`).
   Update-transport failures inside these handlers propagate uncaught (§2.5).
7. **Success = the always-async handover (D9):** one final update `status: IN_PROGRESS`,
   step `gl-trigger` `SUCCEEDED`, `userMessage: "Triggered the configured GitLab
   pipeline"`, `systemMessage: "Triggered pipeline in project '<projectId>'"`
   (`:109-126`). The impl DTO has **no `async` field** (`MeshBuildingBlockRun.kt:221-226`)
   — handover is unconditional; the runner's job ends here (metrics: succeeded,
   umbrella §7.2). Terminal status comes later from the **external pipeline** via the
   callback contract (§2.2.3); the coordinator times the run out if it never calls back.

### 2.2 The trigger request (`GitLabClient.kt`) — the frozen external contract

1. **Endpoint:** `POST {sanitizedBaseUrl}/api/v4/projects/{projectId}/trigger/pipeline`
   (`:52`), body `multipart/form-data` (OkHttp `MultipartBody.FORM`, `:118-119`); the
   HTTP client never follows redirects (`followRedirects(false)`, `:35-38`).
2. **Multipart fields, in build order** (`buildPayload`, `:111-175`) — **frozen:
   customer pipelines parse these** (umbrella §8):
   | Field | Value | Evidence |
   |---|---|---|
   | `token` | the **decrypted** pipeline trigger token | `:120` |
   | `ref` | `impl.refName` verbatim | `:121` |
   | `variables[MESHSTACK_BEHAVIOR]` | `spec.behavior` enum name (`APPLY`/`DETECT`/`DESTROY`) | `:122` |
   | `variables[MESHSTACK_RUN]` | the run JSON serialized from `ProcessableBlockRun` (§2.2.2) — inputs decrypted, impl secret per §2.6 | `:116,123` |
   | `variables[<key>]` | per input with `isEnvironment: true`: `value.toString()` (§2.2.4); duplicate keys last-wins (`associate`, `:126-132`) | `:126-132` |
   | `inputs[<key>]` | per input with `isEnvironment: false`, same stringification/dedup | `:136-142` |
   | `variables[MESHSTACK_SELF_URL]` | `_links.self.href` | `:145,150-154` |
   | `variables[MESHSTACK_REGISTER_SOURCE_URL]` | `_links.registerSource.href` | `:146,156-160` |
   | `variables[MESHSTACK_UPDATE_SOURCE_URL]` | `_links.updateSource.href` **verbatim incl. the `{sourceId}` template** | `:147,162-166` |
   | `variables[MESHSTACK_BASE_URL]` | `_links.meshstackBaseUrl.href` | `:148,168-172` |
   Any missing link ⇒ **warn + omit that part** (`logMissingUrl`, `:177-185`) — never a
   failure. No other fields; parts are plain form fields (Content-Disposition only).
3. **The callback contract (D9 async pin, concretely):** the customer pipeline receives
   everything it needs to become the run's next status source: `MESHSTACK_RUN` contains
   `spec.runToken` (the handover credential — `ProcessableBlockRun` serializes the full
   run), and the three URLs let it `POST registerSource` (its own source id),
   `PATCH updateSource` (substituting `{sourceId}` itself) and `GET self`, Bearer-authed
   with that runToken. The runner never polls GitLab; terminal status is entirely
   pipeline-side. This is why the trigger-success update is `IN_PROGRESS`, not terminal.
4. **Input stringification:** `value.toString()` on the Jackson-decoded value — strings
   verbatim, integers/booleans as literals; JSON arrays/objects become Java
   `AbstractCollection`/`AbstractMap` toString (`[a, b]` / `{k=v}`), exotic doubles
   become `Double.toString` (`1.0E20`) — see flag §16.4. A JSON `null` input value is
   unrepresentable (`value: Any` — Jackson fails the whole claim ⇒ no-run; flag §16.5).
5. **`MESHSTACK_RUN` serialization:** `mapper.writeValueAsString(run)` with the client's
   own `jacksonObjectMapper` (`:40-44,116`) over `ProcessableBlockRun{@JsonUnwrapped
   meshObject, @JsonUnwrapped @JsonProperty("_links") links}` (`ProcessableBlockRun.kt:13-19`)
   — intended shape: the claim-response JSON (`kind/apiVersion/metadata/spec/status/_links`)
   round-tripped through the typed model (drops unknown fields, writes explicit `null`s,
   default-inclusion mapper). The exact `_links` key spelling is pinned empirically by
   G-P1 (STOP-F; flag §16.6).

### 2.3 Error taxonomy (client + service) — what is wire-visible vs log-only

`GitLabClient` classifies failures into four `MeshHttpException` variants
(`MeshHttpException.kt:5-27`; fields `userMessage`, `systemMessage?`, `statusCode`,
`requestUrl`, `responseBody`):

| # | Condition | userMessage / systemMessage in the exception | Evidence |
|---|---|---|---|
| A1 | HTTP 404 | "GitLab pipeline could not be triggered successfully. Please contact support." / "GitLab reported 404, which can happen if you have entered a wrong projectId." | `:69-77` |
| A2 | error body not deserializable as `{"message":{"base":[…]}}` | "There was a problem while communicating with GitLab." / — | `:79-89` |
| A3 | body contains base entry `"Identity verification is required in order to run CI jobs"` | "There is a problem with the pipeline trigger token. Please contact support." / "Your GitLab account is not verified and can not trigger a pipeline. Please visit GitLab and verify your account first." | `:30-33,91-100` |
| A4 | any other non-2xx | "There was an error communicating with GitLab." / "GitLab did not process the request, and responded with: `<GitLabErrorBody.toString>`" | `:101-107` |

**Research finding (corrects the umbrella §3.2 reading — flag §16.1):** these
classified message pairs are **log-only**. The service's `MeshHttpException` handler
(`GitLabBlockRunnerService.kt:73-89`) discards `ex.userMessage`/`ex.systemMessage` and
reports the *same* step update for A1–A4:

- **Row B (MeshHttpException):** run `FAILED`, step `gl-trigger` `FAILED`,
  `userMessage: "Could not trigger the GitLab pipeline"` (`:82`), `systemMessage:
  "GitLab responded with status: <statusCode> and body: <responseBody>"` (`:83`).
  The classification is observable only via the ERROR log (`log.error(ex)` prints the
  exception message built as `"<user> - <system> [HTTP <code> <url>]"`,
  `MeshHttpException.kt:16-26`).
- **Row C (any other exception):** run `FAILED`, step `gl-trigger` `FAILED`,
  `userMessage: "Could not trigger the GitLab pipeline"` (`:100`), `systemMessage:
  "There was an internal error while trying to contact GitLab: <ex.message>"` (`:101`)
  — covers impl-type mismatch (§2.1.3), URL sanitization failure (§2.1.4), decrypt
  failure (§2.1.5), network I/O errors, and 3xx responses read as errors (§2.2.1).
- A4's embedded `<GitLabErrorBody.toString>` is a plain (non-data) private class ⇒
  Java default `ClassName@hash` — garbage in the log, never on the wire (flag §16.2).

### 2.4 Wiring, scheduling, modes, config (Spring)

- Standalone: `@Scheduled(10s)` core scheduler + `ImmediateRetryDecorator`
  (`BlockRunnerServiceConfiguration.kt:15-28`); scheduling disabled under
  `test`/`kubernetes` profiles (`GitLabBlockRunnerSchedulingConfiguration.kt:10-13`).
- k8s (`SPRING_PROFILES_ACTIVE=kubernetes`, operator config
  `run-controller/runner-config.yml:149-152`): **no retry decorator** (`:30-41`),
  `SingleShotRunner` one-shot + exit semantics as manual (06A §2.3); run file from
  `RUN_JSON_FILE_PATH`; `NoOpDecryptionService` active (§2.6).
- Decryption wiring (standalone): `MeshCertDecryptionService` with the key resolved by
  `PrivateKeyLoader.resolve(props.privateKeyFile, props.privateKey)`
  (`GitLabBlockRunnerCryptoConfiguration.kt:11-17`) — unlike manual, gitlab **has** a
  real crypto path; the module yaml bakes a dev private key inline
  (`gitlab-block-runner/src/main/resources/runner-config.yml:12`, umbrella §10.5).
- Health `/healthz` → `"OK"` on Spring `PORT`, default **8103**
  (`gitlab-block-runner/src/main/resources/application.yml:8`); image bakes `PORT=8080`.
- Shipped defaults (`runner-config.yml:1-11`): uuid `bfe76555-7a69-48e8-8cc0-8e02eb76fc22`
  (mux GITLAB_PIPELINE port), `api.url ${RUNNER_API_URL:http://localhost:8303}`,
  `bb-api`/`guest`, blank API-key creds; **no gitlab-specific config keys** — GitLab
  coordinates (base URL, projectId, refName, token) arrive per run in the impl DTO.

### 2.5 Failure surfaces by mode (exit/reporting matrix)

| Failure | Standalone (scheduler) | k8s (single shot) |
|---|---|---|
| fetch/parse/claim error | caught ⇒ no-run, next tick (`:21-27`) | caught ⇒ **exit 0**, run never reported (umbrella §7.9 quirk; Go tightens per R12) |
| register HTTP error | propagates; scheduler catches+logs ⇒ run claimed-but-unreported (coordinator timeout) | propagates ⇒ **exit 1** (`SingleShotRunner.kt:38-49`) |
| impl-type / URL / decrypt / GitLab / network failure | FAILED update (§2.3 rows B/C), processBlock returns null | same FAILED update, then **exit 0** (a terminal status was reported) |
| update-transport error while reporting B/C or the handover | propagates (as register) | propagates ⇒ exit 1 |
| happy trigger | IN_PROGRESS handover reported, immediate re-claim | handover reported, exit 0 |

### 2.6 The secret-hygiene asymmetry (umbrella §7.6) — and its k8s limit

Standalone: only `decryptBlockRunInputs` is applied to the payload run (`:56`) —
sensitive inputs of type STRING/CODE/FILE are decrypted (`MeshCertDecryptionService.kt:58-97`;
other sensitive types: error-log + left as-is, `:74-77`), while
`impl.pipelineTriggerToken` inside `MESHSTACK_RUN`'s `buildingBlockDefinition` stays
**encrypted** — only the `token` form field carries the decrypted value (`:53`).
The existing test `GitLabBlockRunnerServiceTest.kt:128-188` pins this asymmetry at
mock level.

**k8s mode caveat (flag §16.3, not anticipated by the umbrella):** the controller
decrypts inputs **and** the trigger token into the mounted run JSON
(`run-controller/controller/decryption.go:27-39` inputs — note: without the Kotlin
type restriction — and `:81-95` `PipelineTriggerToken`); the pod's
`NoOpDecryptionService` passes everything through, so today `MESHSTACK_RUN` embeds the
**plaintext** trigger token in k8s mode. The Go port reproduces both modes as-is
(polling: asymmetry + leak test; single-run: NoOp passthrough — pinned by G-P13).

## 3. Kotlin pin tests (tests-first step)

Tests-only commits in `gitlab-block-runner` (`git diff -- ':!*Test*' ':!*Scenario*'`
empty), proven green by the existing `jvm-runners-ci` leg before any Go code exists.
The core wire pins (C-P1–C-P7) exist per T4 — verified, not re-written.

### 3.1 What already exists (kept, later ported per §10)

- `GitLabBlockRunnerServiceTest` — **5 tests** (`:46-188`): trigger-throws ⇒ FAILED
  update reported; no-run; fetch-exception swallow; happy trigger (decrypted token
  reaches the client, decrypted-inputs run passed); decrypted-inputs/impl-asymmetry
  (`:128-188` — inputs decrypted via mock, `pipelineTriggerToken` decrypted only into
  the `token` parameter). All mockk-verification style ⇒ consolidate into Go scenarios.
- `GitLabClientTest` — **1 test** (`:14-105`): wiremock multipart pin of `token`, `ref`,
  `MESHSTACK_BEHAVIOR`, env/non-env split, all four callback URLs — but
  `MESHSTACK_RUN` matched only as `.*` (`:35`) and no error paths.
- `GitLabClientFactoryTest` (1: sanitize called), `GitLabRunnerStartupScenario` (boot),
  `GitLabRunnerKubernetesStartupScenario` (boot only — **no captured wire**, unlike
  manual's k8s scenario).

### 3.2 New gitlab pins (closing the umbrella §3.3 gap column)

| Id | Pin (scenario-level where possible, D16) | Anchors |
|---|---|---|
| G-P1 | **`MESHSTACK_RUN` content pin:** parse the captured multipart part as JSON and assert: top-level shape incl. the exact links key spelling (STOP-F resolver); `spec.runToken` present; sensitive STRING/CODE/FILE input values decrypted; sensitive non-decryptable-type input left as-is; `buildingBlockDefinition…implementation.pipelineTriggerToken` **still ciphertext** while the `token` part is plaintext — the §7.6 leak pin at wire level | §2.2.2/.5, §2.6; extend `GitLabClientTest` with a real `MeshCertDecryptionService` + test key |
| G-P2 | **Missing-link omission:** run without `meshstackBaseUrl` (and one without `self`) ⇒ no corresponding `variables[MESHSTACK_*]` part, other parts unaffected, trigger still succeeds | `:145-172` |
| G-P3 | **404 error UX:** fake GitLab returns 404 + body ⇒ captured meshStack PATCH is `{status: FAILED, steps:[{id: gl-trigger, status: FAILED, userMessage: "Could not trigger the GitLab pipeline", systemMessage: "GitLab responded with status: 404 and body: <body>"}]}` | §2.3 rows A1+B |
| G-P4 | **Classification is wire-invisible:** identity-verification body (403) and generic error body (400) produce the *same* update shape as G-P3 modulo code/body — pinning that A1–A4 differ only in logs (flag §16.1 baseline) | §2.3 |
| G-P5 | **Undeserializable error body** (e.g. HTML 500) ⇒ same row-B update, `systemMessage` embeds the raw body | §2.3 row A2 |
| G-P6 | **Always-async handover wire pin (D9):** 2xx trigger ⇒ PATCH `{status: IN_PROGRESS, steps:[{id: gl-trigger, status: SUCCEEDED, userMessage: "Triggered the configured GitLab pipeline", systemMessage: "Triggered pipeline in project '<id>'"}]}`; nothing further; register precedes trigger (ordering + cardinality: one register, one update) | §2.1.2/.7 |
| G-P7 | **Internal-error UX:** wrong impl type / blank `gitlabBaseUrl` / decrypt failure ⇒ row-C update with `systemMessage: "There was an internal error while trying to contact GitLab: <msg>"`; register already happened | §2.1.3-.5, §2.3 row C |
| G-P8 | **Stringification + dedup:** INTEGER env input `4` ⇒ part body `"4"`; BOOLEAN ⇒ `"true"`; duplicate env keys ⇒ one part, last value; a LIST-valued env input's Java-toString output captured as the baseline for flag §16.4 | §2.2.4 |
| G-P9 | **Base-URL sanitization e2e:** trailing-slash base URL triggers against the slashless endpoint; whitespace trimmed | `UrlSanitizerService.kt:8-20`, existing `UrlSanitizerServiceTest` kept |
| G-P10 | **Redirects not followed:** 302 from GitLab ⇒ row-B FAILED update (status 302), no second request | `:35-38,65-67` |
| G-P11 | **Empty trigger token:** `decrypt("")` ⇒ `""` ⇒ `token` part present and empty (request still sent) | `MeshCertDecryptionService.kt:34-37`; T8 twin |
| G-P12 | **k8s captured-wire scenario** (manual's `ManualRunnerKubernetesStartupScenario` capture style): pre-decrypted run file ⇒ register + trigger (plaintext token in both `token` part *and* `MESHSTACK_RUN`, §2.6 caveat) + IN_PROGRESS handover; exit 0 | §2.4, §2.6 |
| G-P13 | **k8s exit codes:** update-transport failure ⇒ exit 1; unreadable run file ⇒ swallowed, exit 0 (the umbrella §7.9 quirk the Go port tightens — baseline pin, twin of M-P7) | §2.5 |

Bugs/quirks pinned as-is, never fixed here (umbrella §5.2): the k8s exit-0 swallow
(G-P13), the wire-invisible classification (G-P4), the k8s plaintext-token embedding
(G-P12), the `GitLabErrorBody` toString garbage (log-only — not pinned, logs are not
contract). JSON-body assertions compare parsed JSON with null ≡ absent (06A §16.4 rule).

## 4. Go handler design

Package `runner/internal/gitlab` (D11). Illustrative signatures only; umbrella §5.3
shape followed exactly (deviations = STOP-D via §4.6).

### 4.1 Handler

```go
// package gitlab — the GITLAB_PIPELINE run handler (trigger + always-async handover).
func NewHandler(cfg Config, deps HandlerDeps) Handler        // value type (P4)
func (h Handler) Execute(ctx context.Context, run dispatch.ClaimedRun) error

type HandlerDeps struct {
    Reporters ReporterFactory   // per-run report.Reporter, runToken-only (06A §4.3)
    Decryptor meshapi.Decryptor // cert-based in polling mode, NoOp in single-run mode
    HTTP      *http.Client      // external seam; redirects disabled at construction (§4.3)
    Log       *slog.Logger      // D15; per-run via Log.With("run", run.Id)
}
```

`Execute` skeleton (= §2.1 semantically, not structurally):

1. Build the per-run reporter from `run.Details.Links` + `run.Details.Spec.RunToken`.
2. `Register("gl-trigger", "Trigger GitLab CI/CD")` — transport error ⇒ return wrapped
   error (A1 contract: infrastructure; run stays unreported, §2.5 parity).
3. Unmarshal `run.Details.Spec.Definition.Spec.Implementation` into
   `meshapi.GitlabImplementation` (`dtos.go:146-152`); failure or wrong `type` ⇒
   row-C FAILED update (§2.3 message, byte-identical) ⇒ return nil.
4. Sanitize base URL (§4.3); decrypt the trigger token (empty ⇒ `""`, T8/G-P11 rule);
   `meshapi.DecryptInputs(rawRunJson, deps.Decryptor)` (§4.4). Any failure ⇒ row-C
   FAILED update ⇒ return nil.
5. Build + POST the multipart payload (§4.2); `*ExternalCallError` (via `errors.As`) ⇒
   row-B FAILED update (`"GitLab responded with status: %d and body: %s"`); other
   error ⇒ row-C. Both ⇒ return nil (run-level failure was reported).
6. Success ⇒ the handover update `{Status: IN_PROGRESS, Steps: [{Id: gl-trigger,
   Status: SUCCEEDED, UserMessage/SystemMessage per §2.1.7}]}` ⇒ return nil.
   Update-transport errors in 5/6 ⇒ return the error (§2.5 parity: propagate).

Step id/display name/message strings are typed constants (umbrella §7.1/§7.11).
No ticker, no abort handling, no GitLab polling (umbrella §7.5); `ctx` is passed to the
HTTP request (`http.NewRequestWithContext`) — cancellation is new-but-inert (no Kotlin
counterpart; same treatment as 06A's debug waits).

### 4.2 Payload builder (the frozen §2.2.2 field set)

- Unexported `buildTriggerForm(…) (*bytes.Buffer, contentType string, err error)` using
  stdlib `mime/multipart` (`writer.WriteField` per §2.2.2 row, same build order —
  order is convenience for byte-diffing, the *contract* is the field set, flag §16.7).
- `MESHSTACK_RUN` = the `DecryptInputs` output bytes (§4.4) — **raw-preserving**, not a
  typed round-trip: base64-decode `run.RawJson` (T5), decrypt input values in place,
  emit. Deltas vs Kotlin's model round-trip (drops unknown fields, adds explicit
  nulls): parsed-JSON-equivalent on all modeled fields; unknown-field forwarding is a
  flagged, sanctioned delta (§16.6). `_links` key spelling asserted against G-P1.
- `variables[<key>]`/`inputs[<key>]` values: iterate the **decrypted** inputs (parse
  the `DecryptInputs` output with `UseNumber`, T6) in document order into a last-wins
  ordered map; stringify per §4.2.1.
- Callback URLs from `run.Details.Links` (T5 — same values the raw JSON carries;
  C-P5/G-P1 pin equality): empty `Href` ⇒ `slog` warn + omit the part (G-P2).

**4.2.1 Stringification (`valueString(v any) string`)** — pure function, unit-tested:
`string` verbatim; `json.Number` literal; `bool` → `true`/`false`; `nil` → `"null"`
(Kotlin-unreachable edge, flag §16.5); arrays/objects → compact JSON via
`json.Marshal` — a **flagged delta** from Java toString (`[a, b]`/`{k=v}`), G-P8
captures the Kotlin baseline, §16.4 argues the delta.

### 4.3 External client + URL sanitization

- Unexported `triggerPipeline(ctx, httpClient, baseUrl, projectId string, form …) error`
  in the same package (D11: no sibling split — the package is one cohesive concept).
- `sanitizeBaseUrl(s string) (string, error)`: trim space; empty ⇒ error `"URL should
  not be empty"`; drop one trailing `/` (`UrlSanitizerService.kt:8-20` — umbrella §4
  row 13: package-local helper, no shared package for 6 lines). Additionally
  `url.Parse` validation replaces OkHttp's `toHttpUrl` throw (same row-C outcome).
- The `*http.Client` is constructed in `cmd/gitlab/main.go` with
  `CheckRedirect: func(…) error { return http.ErrUseLastResponse }` (§2.2.1 parity,
  G-P10) and injected — tests swap the transport (`httptest` fake GitLab).
- Response handling reproduces §2.3 exactly: 2xx ⇒ nil; 404 ⇒ `ExternalCallError` A1;
  body deserialization into `struct{ Message struct{ Base []string } }` (stdlib
  `encoding/json` — unknown fields ignored by default, matching
  `FAIL_ON_UNKNOWN_PROPERTIES=false`); parse failure ⇒ A2; identity-verification
  membership ⇒ A3; else A4 (with the parsed body's compact JSON in the message — the
  Kotlin toString garbage is *not* reproduced, log-only, flag §16.2).

### 4.4 `meshapi.DecryptInputs` (umbrella-assigned artifact, §4 row 8 / §7.6)

The structural fix for the umbrella §10.9 leak hazard: outbound payloads must never be
built from `DecryptRunDetails` output (it decrypts impl secrets — `decryption.go:81-95`
would put the plaintext trigger token into `MESHSTACK_RUN`).

```go
// meshapi — decrypts sensitive input values ONLY; implementation secrets stay
// encrypted (the Kotlin decryptBlockRunInputs asymmetry, MeshCertDecryptionService.kt:58-97).
// rawRunJson is the claimed run JSON (not base64); the result preserves all other
// bytes' semantics (generic-JSON transform, UseNumber — no typed round-trip).
func DecryptInputs(rawRunJson []byte, dec Decryptor, log *slog.Logger) ([]byte, error)
```

- **Branch rules = Kotlin's, verbatim** (`MeshCertDecryptionService.kt:58-97`): only
  inputs with `isSensitive: true` **and** `type` ∈ {`STRING`, `CODE`, `FILE`} are
  decrypted; other sensitive types ⇒ warn-log + left as-is (`:74-77`); non-sensitive
  untouched. Deliberately **not** the controller's looser rule (any sensitive string,
  `decryption.go:29-31`) — the payload path is Kotlin-parity, flag §16.8.
- Implementation: decode into `map[string]any` with `UseNumber`, walk
  `spec.buildingBlock.spec.inputs[]`, replace `value` where the rules match and the
  value is a string, re-marshal. Everything else — impl secret, `_links`, `runToken`,
  unknown fields — passes through untouched (that *is* the asymmetry, made structural).
- Decrypt error ⇒ error (caller maps to row C); with the NoOp decryptor (single-run
  mode) the function is an identity transform modulo re-marshaling.
- **Signature reviewed against 06D** (umbrella row 8 duty): github's
  `buildingBlockRun` payload needs the same inputs-decrypted JSON and then strips the
  implementation object — a *caller-side* `map` deletion or payload struct on the
  same output; no extra parameter needed. 06C needs only decrypted input *values*
  (stringified `templateParameters`) — served by parsing the output. Fit confirmed;
  recorded in §4.6.
- **Empty-string rule (T8):** the shared `Decryptor` seam must return `""` for `""`
  (Kotlin `decrypt("")`, `:34-37`) instead of the Go crypto's error. If plan 03's
  `Decryptor` doesn't already do this, 06B adds the guard at the seam (one place, all
  consumers incl. the trigger-token decrypt) — verification in step 0.

### 4.5 `ExternalCallError` (06A §4.4 assignment — first consumer ships it)

```go
// gitlab — typed error for failed external-system calls; the MeshHttpException
// equivalent (umbrella §4 row 14). Handlers map it into step user/system messages.
type ExternalCallError struct {
    UserMessage   string // per-classification (§2.3 A1–A4) — surfaces in logs (§16.1)
    SystemMessage string
    StatusCode    int
    RequestUrl    string
    ResponseBody  string
}
func (e *ExternalCallError) Error() string // "<user> - <system> [HTTP <code> <url>]", MeshHttpException.kt:16-26
```

- Lives **in package `gitlab`** per the umbrella §4 row 14 ruling ("per-package typed
  error with the same fields") — 06C/06D define their own identical-shaped type; the
  *shape and contract* are fixed by 06A §4.4 + this section, the duplication is a
  5-field struct (P3 beats a speculative shared package; escalation path in §16.9 if
  review prefers one shared type — that is an umbrella revision).
- Contract: the handler formats step messages from the fields exactly as the Kotlin
  service does today (§2.3 row B uses only `StatusCode` + `ResponseBody`; the
  classified `UserMessage`/`SystemMessage` reach slog via `Error()` — byte-identical
  strings per umbrella §7.11, wire-invisible per G-P4).
- Classification helper `isIdentityVerificationRequired(body []byte) bool` = the exact
  base-string membership (`GitLabClient.kt:30-33`).

### 4.6 Template fit-check (umbrella §6 review protocol)

Every deviation from the 06A artifacts, mapped to an anticipating rule or escalated:

| Point of deviation vs 06A | Umbrella/06A anchor | Verdict |
|---|---|---|
| `HandlerDeps` gains `Decryptor` + `HTTP` | 06A §16.1 (manual-specific narrowing) + umbrella §5.3 | anticipated — shape unchanged |
| Handler reports run-level FAILED (manual never does) | A1 contract; umbrella §5.3 skeleton | anticipated |
| Always-async handover: final `IN_PROGRESS`, `Execute` returns nil ⇒ metrics **succeeded** | umbrella §7.2 rule, §10.10 | anticipated |
| `report.Reporter`: `Register` + one `Report(RunStatus)` (the trigger step), `abort` discarded | 06A §17 row 3 (gitlab column) | anticipated |
| New shared code in `meshapi` (`DecryptInputs`) | umbrella §4 row 8 names 06B owner | assigned, not a deviation |
| `ExternalCallError` ships here | 06A §4.4 + §17 gap list | assigned |
| First consumer of `config.ResolvePrivateKey` + `BlockRunnerCompat.privateKey*` | 06A §6.4-6.5, §16.8 | anticipated |
| External HTTP client with redirect-disable | umbrella §5.3 "external-API HTTP client seam (fakeable)" | anticipated |
| No poll loop, no `Clock` dep | umbrella §3.1 (gitlab has no polling) | narrower than 06C/06D — fine |
| `valueString` composite-JSON delta | **not anticipated** — new sanctioned-delta candidate | ruled via flag §16.4: compact JSON, no Java-toString fallback |
| k8s plaintext-token embedding limits §7.6 | **umbrella correction** | escalated as flag §16.3 (documentation-level; no shape change) |

**Result: no `RunHandler`/`ClaimedRun`/reporter/config-helper shape change required —
plan 05 §4, the umbrella, and the 06A template stand.** Flags §16.1/§16.3 correct
umbrella *prose*, not contracts.

## 5. Kotlin-isms → idiomatic Go (D15)

Umbrella §7.13 instantiated for every Kotlin-ism this module actually uses; parity
notes where the translation is not mechanical.

| Kotlin-ism (evidence) | Idiomatic Go replacement | Parity note |
|---|---|---|
| exception fan-out: `catch (MeshHttpException)` then `catch (Exception)` (`GitLabBlockRunnerService.kt:58-66`) | one error return from the trigger path; `errors.As(&ExternalCallError{})` selects row B, everything else row C | catch *order* becomes type-switch order; message strings byte-identical (§2.3) |
| `MeshHttpException` carrying user/system/status/url/body (`MeshHttpException.kt:5-27`) | `gitlab.ExternalCallError` (§4.5) | `Error()` reproduces `buildMessage` for log parity |
| catch-all claim swallow (`:21-27`) | shared `ClaimClassifier` (T3) — handler never sees claim errors | same observable: log + next tick; additive poll-error metric |
| OkHttp `MultipartBody.Builder().setType(FORM).addFormDataPart(…)` (`GitLabClient.kt:118-174`) | stdlib `mime/multipart`: `multipart.NewWriter(buf)`, `WriteField(name, value)` per part, `writer.FormDataContentType()` as request Content-Type | both emit plain form-data parts (Content-Disposition only); boundary differs per request — never a contract |
| `followRedirects(false)` (`:35-38`) | `http.Client{CheckRedirect: func(…) { return http.ErrUseLastResponse }}` | 3xx becomes the row-B path in both (G-P10) |
| `jacksonObjectMapper` + `FAIL_ON_UNKNOWN_PROPERTIES=false` + Jdk8/JavaTime modules (`:40-44`) | `encoding/json` (ignores unknown fields by default); no module ceremony | error-body struct §4.3; MESHSTACK_RUN is raw-preserving, §4.2 |
| `ProcessableBlockRun` re-serialization via `@JsonUnwrapped` (`ProcessableBlockRun.kt:13-19`) | `meshapi.DecryptInputs` generic-JSON transform over the claimed bytes (§4.4) | parsed-JSON-equivalent; G-P1/STOP-F guard the `_links` spelling; null ≡ absent rule (06A §16.4) |
| data-class `copy()` decrypt-and-rebuild chains (`MeshCertDecryptionService.kt:84-96`) | in-place value replacement on the decoded map (P4 local mutation) | same output, no structure mimicry |
| private nested `GitLabErrorBody` + `isIdentityVerificationRequired()` (`GitLabClient.kt:23-33`) | unexported struct + pure predicate function | toString garbage not reproduced (§16.2) |
| `GitLabClientFactory` + `UrlSanitizerService` beans (`GitLabClientFactory.kt:9-18`) | `sanitizeBaseUrl` package-local function; client construction inline in the handler | factory existed only as a test seam — the injected `*http.Client` is the Go seam |
| Spring `@Profile("kubernetes")` bean split + `SingleShotRunner` (`BlockRunnerServiceConfiguration.kt:15-41`) | persona mode switch via `config.SingleRunMode` (T2) + R12 exit tail (T3) | exit-code delta on pre-report failures is the sanctioned §7.9 tightening (G-P13 baseline) |
| `@Scheduled(10s)` + `ImmediateRetryDecorator` | `dispatch.Loop{PollInterval: 10s, ClaimBackoff: 0}` + `Done()` wake | cadence-equivalent (umbrella §4 row 1) |
| `@ConfigurationProperties(prefix="blockrunner")` + inline `privateKey` yaml | persona config struct + `BlockRunnerCompat` + `ResolvePrivateKey` (T2) | key spellings preserved (§6.2) |
| kotlin-logging + MDC `requestId` (`application.yml:1-5`) | `log/slog` text handler, `Log.With("run", id)` | log format not a contract (umbrella §8); the classified GitLab error messages surface here |
| companion `STEP_ID` (`:128-131`) | package-level typed constant | frozen string (umbrella §7.1) |
| `value.toString()` stringification (`:131,141`) | `valueString` pure function (§4.2.1) | scalars byte-identical; composites flagged delta §16.4 |

## 6. Config

### 6.1 Persona config struct

```go
// gitlab.Config — persona extras only; shared parts ride config.Api (06A §6.1 pattern).
type Config struct {
    Uuid              string     // RUNNER_UUID / blockrunner.uuid
    Api               config.Api // url + auth (API key wins, umbrella A6)
    PrivateKey        string     // inline PEM (blockrunner.privateKey — the Kotlin key)
    PrivateKeyFile    string     // path (blockrunner.privateKeyFile / RUNNER_PRIVATE_KEY_FILE)
    MaxConcurrentRuns int        // new, default 1 (plan 05)
    Registration      *dispatch.RegistrationConfig // opt-in (plan 05 §9)
}
```

Validation (P5, startup): `uuid` + `api.url` + auth required in polling mode
(single-run exempt, 06A §6.1); polling mode additionally requires a resolvable private
key via `config.ResolvePrivateKey(log, cfg.PrivateKeyFile, cfg.PrivateKey)` (T2) —
fail fast with the key-resolution context, since every gitlab run needs the token
decrypted (unlike manual). Single-run mode uses the NoOp decryptor and needs no key.
No gitlab-specific keys exist beyond the private key (§2.4) — GitLab coordinates are
per-run data.

### 6.2 Alias table (umbrella §5.4 instantiated — every shipped name keeps working)

| Existing name | Evidence | Handling |
|---|---|---|
| env `RUNNER_UUID`, `RUNNER_API_URL`, `RUNNER_API_USERNAME`, `RUNNER_API_PASSWORD`, `RUNNER_API_CLIENT_ID`, `RUNNER_API_CLIENT_SECRET`, `VERSION` | `gitlab-block-runner/src/main/resources/runner-config.yml:2-11` | bound via `config.Env`, identical to the manual persona (06A §6.2 incl. the `VERSION`-override rule) |
| env `RUNNER_PRIVATE_KEY_FILE` | `PrivateKeyLoader.kt:8-24` | first arg of the T2 resolution order (env > yaml file key > `/app/runner-private.pem` default > inline yaml key) |
| env `PORT` (Spring port, default 8103; image bakes 8080) | `application.yml:8`, `jvm.Dockerfile:18-19` | `MANAGEMENT_PORT` > `PORT` (deprecation-logged) > default **8103**; image keeps `ENV PORT=8080` |
| env `SPRING_PROFILES_ACTIVE=kubernetes` | `run-controller/runner-config.yml:149-152` | single-run trigger alias via `config.SingleRunMode` (T2) |
| yaml `blockrunner.uuid`, `.version`, `.api.url`, `.auth.username/password`, `.auth.api-key.client-id/client-secret`, `.privateKey`, `.privateKeyFile` | module yaml + `StandaloneBlockRunnerApiConfig.kt`, `BlockRunnerPrivateKeyProperties.kt` | `config.BlockRunnerCompat` block (T2) — gitlab **consumes** `privateKey`/`privateKeyFile` (manual ignored them, 06A §17 row 8); `debugMode` warn-and-ignored (manual-only key) |
| yaml `logging.*`, `server.*`, `spring.*` | `application.yml` | ignored-with-warning (umbrella §5.4) |
| blank API-key creds ⇒ Basic auth | `StandaloneBlockRunnerApiConfig.kt:35` | preserved (06A §6.2 verification row) |

New, additive only: `MANAGEMENT_PORT`, `RUNNER_CONFIG_FILE`, `maxConcurrentRuns` /
`RUNNER_MAX_CONCURRENT_RUNS`, `registration:`. Relaxed-binding spellings beyond the
shipped literals are not carried (umbrella §10.4).

### 6.3 The baked dev private key (umbrella §10.5 — conscious placement)

The Kotlin classpath yaml bakes an inline dev private key
(`gitlab-block-runner/src/main/resources/runner-config.yml:12`) — the local-dev pair
of meshfed's magic-runner public key. The port keeps it
**verbatim** but places it in the **shared top-level base** `runner-config.yml`
(deep-merged under the per-impl `containers/gitlab-block-runner/runner-config.yml`,
base < per-impl < env) as the flat `privateKey:` value, with a one-line comment marking
it the well-known dev key (so scanner hits self-answer) and naming the env override. It
is **not** duplicated into the per-impl file. Per the T2 resolution order it is used
only when neither `RUNNER_PRIVATE_KEY_FILE` nor a mounted file key resolves — never a
silent fallback over an operator-set key (the Kotlin order, preserved). Removing it from
published-image defaults is a **phase-7** ledger item.

## 7. Persona wiring & modes

### 7.1 Persona wiring & polling mode (`cmd/gitlab/main.go`, package main — only main wires, D11)

- `cmd/gitlab/main.go` is the gitlab runner's own `package main` (links only the gitlab
  handler + its deps; no persona registry, no argv[0] switch of a shared binary); the same
  handler is *also* registered in the `cmd/bbrunner` superset (persona by subcommand,
  06A §7.1). Persona bootstrap sets
  `meshapi.Identity{Name: "gitlab-block-runner", Version: build-or-VERSION}` (06A §6.2).
- `dispatch.NewLoop(LoopConfig{PollInterval: 10s, ClaimBackoff: 0, MaxConcurrent:
  cfg.MaxConcurrentRuns /* default 1 */}, …)` + `dispatch.NewInProcess(map[…]{
  meshapi.RunnerTypeGitLabPipeline: handler})` — the type key via
  `meshapi.ToRunnerType(ImplTypeGitLabCICD)` (`dtos.go:285-295`), no new literals
  (umbrella §7.12). Shared `ClaimClassifier` (T3), `Done()` wake, graceful shutdown =
  `Loop.Stop()` + `InProcess.Wait()`.
- Decryptor wiring: polling ⇒ cert-based from the resolved private key (§6.1);
  the same decryptor instance serves token decryption and `DecryptInputs`.
- `mgmt.NewServer` on `config.ManagementPort(log, 8103, PORT-alias)` + `mgmt.RunMetrics`
  + plan-05 counters, wired like `cmd/manual/main.go`. Metrics classification: the
  IN_PROGRESS handover with nil return counts **succeeded** (umbrella §7.2/§10.10).
- Node id = plain runner uuid; header set = shared-client set (uniform sanctioned
  additive delta, umbrella §7.7 — wording copied from 06A).
- Self-registration off by default; opt-in `registration:` with default capability
  `GITLAB_PIPELINE`.
- Unhandled-type fail-fast (D5): loop-level, plan 05 §10.1 wording — relevant if an
  operator points a `GITLAB_PIPELINE`-registered uuid at other run types.

### 7.2 Config loading

Identical to 06A §7.2: `config.Path` → `LoadFile` (flat keys + `BlockRunnerCompat`) →
normalize → `config.Env` → validate (§6.1). Missing file tolerated (defaults + env).

### 7.3 Single-run mode (k8s Job)

Activated by `config.SingleRunMode` (T2). Flow = 06A §7.3 with the gitlab handler:
read `RUN_JSON_FILE_PATH` → parse into `dispatch.ClaimedRun` (raw + DTO, `UseNumber`)
→ handler with **NoOp decryptor** (controller pre-decrypted inputs *and* trigger
token, §2.6 — `DecryptInputs` degenerates to identity, the `token` field gets the
already-plaintext value) → `Execute` once, no loop, no mgmt listener (umbrella §7.10).
Exit = R12 rule (umbrella §7.9): reported FAILED or handover ⇒ exit 0; register/update
transport failure ⇒ non-zero (Kotlin exit-1 parity); file-missing/parse failure ⇒
non-zero (the sanctioned delta, baseline pin G-P13).

### 7.4 Modes × behavior summary

| | polling (standalone) | single-run (k8s Job) |
|---|---|---|
| claim | Loop 10s + immediate re-drain, classifier T3 | none — file source |
| decryption | cert-based: trigger token + `DecryptInputs` (§2.6 asymmetry) | NoOp (controller pre-decrypted; §2.6 caveat pinned G-P12) |
| reporting auth | per-run runToken only | runToken from run JSON |
| mgmt listener | 8103 (aliases §6.2) | none |
| exit | long-running | R12 rule (§7.3) |

## 8. Dockerfile & image switch

The 06A §8 template, mechanically repeated:

- New per-persona `containers/gitlab-block-runner/Dockerfile` building only the gitlab binary
  (`go build ./runner/cmd/gitlab`): same alpine digest pin, `ca-certificates bash` only
  (HTTP-only runner), meshcloud uid 2000, the fit binary at `/app/gitlab-block-runner`
  (its own binary — no shared `bbrunner`, no symlink), `ENV PORT=8080`, `EXPOSE 8080`,
  and a **direct** `ENTRYPOINT ["/app/entrypoint.sh", "/app/gitlab-block-runner"]` (no
  argv[0] multiplexing).
- `containers/gitlab-block-runner/runner-config.yml`: a **per-impl** file that
  deep-merges over the shared top-level base (base < per-impl < env, §6.3);
  effect-equivalent to the Kotlin classpath defaults (§2.4) in flat keys — uuid
  `bfe76555-…`, api url `http://localhost:8303`, `bb-api`/`guest`. **The baked dev
  private key lives in the shared base, not this per-impl file** (§6.3, umbrella §10.5;
  anticipated by 06A §17 row 11).
- Published name/tags unchanged (`ghcr.io/meshcloud/gitlab-block-runner:main` +
  release tags); deployed controller configs keep working via the honored
  `SPRING_PROFILES_ACTIVE: kubernetes` (umbrella A12).
- CI flip in the same PR as removal (§12): `ci.yml` — drop the gitlab entries from
  `jvm-runners-ci` (`ci.yml:34-35`) and `jvm-runners-image` (`:70-71`), add the
  `go-runners-image` leg (`dockerfile: containers/gitlab-block-runner/Dockerfile`) plus a
  `./runner/cmd/gitlab` leg to the go build matrix; `build-images.yml:38-40` — the gitlab
  leg becomes `dockerfile: containers/gitlab-block-runner/Dockerfile` (a per-persona
  Dockerfile — no shared `target:` stage; drop `runner-module:`).
- JVM `command:`-override incompatibility: same flag wording as 06A §16.9 (umbrella
  §5.6), restated in §16.

## 9. Migration sequence

Always-green steps sized for one reviewable single-commit PR; after every step
`task test` + `task lint` green, `task coverage` ≥ gate, and `./gradlew check` green
until step 9.

| # | Step | What changes | What proves it |
|---|---|---|---|
| 0 | **Preflight.** Umbrella A1–A12 + T1–T8 verifications on the phase-6a branch; branch `phase-6b-gitlab`. Record: the T8 empty-string `Decryptor` behavior, whether `ResolvePrivateKey` shipped in 06A or lands here (T2), the `_links` spelling expectation for G-P1. Re-run the §4.6 fit-check mechanically. | nothing | STOP-A / STOP-D gate |
| 1 | **Kotlin pins (tests only).** §3.2 G-P1–G-P13 in `gitlab-block-runner` (wiremock GitLab + captured meshStack updates; k8s capture scenario in the manual style). Resolve STOP-F from G-P1's capture. | Kotlin test files only | `./gradlew :gitlab-block-runner:check` green; `git diff -- ':!*Test*' ':!*Scenario*'` empty |
| 2 | **`meshapi.DecryptInputs`** (§4.4) + the `Decryptor` empty-string guard (T8) if missing. | `internal/meshapi` | table-driven tests: STRING/CODE/FILE decrypted, other-sensitive-type skip+warn, non-sensitive untouched, impl secret untouched, unknown-field passthrough, `UseNumber` fidelity, NoOp identity; cross-checked against a `MeshCertDecryptionServiceTest` fixture ciphertext (umbrella A9 style); `meshapi` stays ≥90 |
| 3 | **`internal/gitlab` package:** `ExternalCallError` (§4.5), `sanitizeBaseUrl`, error classification, `valueString`, `buildTriggerForm`, `triggerPipeline`. | `internal/gitlab` | unit tests for the pure functions (sanitize table = `UrlSanitizerServiceTest` port, classification table, stringify table incl. G-P8 rows); fake-GitLab transcript tests for the multipart request (G-P1/G-P2 twins) and response taxonomy (G-P3–G-P5, G-P10 twins) |
| 4 | **Handler.** `gitlab.Config`, `NewHandler`, `Execute` flow (§4.1). | `internal/gitlab` | Go scenario suite (§10.1): run JSON in → fake meshStack + fake GitLab transcripts out, matching the Kotlin pins |
| 5 | **Persona wiring, polling.** `cmd/gitlab/main.go` + register the gitlab handler in the `cmd/bbrunner` superset; mgmt on 8103; loop + classifier + metrics; cert decryptor from resolved key. | `runner/cmd/gitlab/main.go`, `runner/cmd/bbrunner/main.go` | loop-wiring scenario (claim→register→trigger→handover→immediate re-claim→404); `cmd/bbrunner` subcommand-dispatch row; alias-precedence test (`MANAGEMENT_PORT`>`PORT`>8103); key-resolution failure = actionable startup error |
| 6 | **Single-run mode.** `SingleRunMode` activation, file source, NoOp decryptor, R12 exit tail. | `cmd/gitlab/main.go` (+ glue) | single-run scenario twin of G-P12 (pre-decrypted fixture ⇒ captured wire equal to the Kotlin capture modulo sanctioned deltas); exit-condition tests (G-P13 twins incl. the flagged tightening) |
| 7 | **Gate + tooling.** `thresholds.txt` += `runner/internal/gitlab 90` (no exclusions); depguard: `gitlab` imports `dispatch`/`meshapi`/`report`/`config` + stdlib only; nothing imports `gitlab` but main. | `tools/coverage/*`, `.golangci.yml` | induced-failure check; `task coverage` green |
| 8 | **Image.** `containers/gitlab-block-runner/Dockerfile` + `containers/gitlab-block-runner/runner-config.yml` (§8, incl. dev key §6.3). | containers/ | `docker build -f containers/gitlab-block-runner/Dockerfile`; container smoke: healthz `OK` on 8080, boots to claim loop against a stub |
| 9 | **Acceptance gate (§11).** Side-by-side transcripts + manual smoke against a real GitLab; outer local-dev-stack/acceptance net green. | — | STOP-E lives here; evidence in the PR description |
| 10 | **Removal.** Delete `gitlab-block-runner/`; `settings.gradle:6` include dropped; CI legs flipped per §8; grep gate. | module dir, gradle, workflows | full CI green incl. the flipped image leg; remaining modules' `./gradlew check` green |

## 10. Test plan & gate (D16)

### 10.1 Pin → Go mapping (N:1 into scenarios by design, umbrella §5.2)

| Kotlin pin/test | Go destination | Kind |
|---|---|---|
| `GitLabBlockRunnerServiceTest` no-run / fetch-exception | loop scenario (claim 404/500 ⇒ no handler call, next tick, poll metric) — shared shape with 06A, re-asserted for this persona's wiring | scenario (consolidated) |
| `GitLabBlockRunnerServiceTest` happy trigger + decrypted-inputs test + G-P1 + G-P6 | `Scenario_Gitlab_PollingRun_TriggersAndHandsOver`: one fixture with sensitive STRING/CODE/FILE + sensitive-LIST + env/non-env + duplicate-key inputs; asserts register → multipart field set (incl. MESHSTACK_RUN parsed content, token asymmetry, callback URLs) → IN_PROGRESS handover PATCH, nothing else | scenario |
| G-P2 missing links | scenario variant: fixture without `meshstackBaseUrl`/`self` links ⇒ parts omitted, warn logged | scenario |
| G-P3/G-P4/G-P5 error UX | `Scenario_Gitlab_TriggerFails_*` (404 / identity-403 / generic-400 / html-500): captured PATCH = row-B shape; classification asserted on the logged `ExternalCallError` only | scenario |
| G-P7 internal errors | scenario variants: wrong impl type / blank base URL / decrypt failure ⇒ row-C PATCH | scenario |
| G-P8 stringification | `Test_ValueString` table (scalar rows byte-equal to the Kotlin capture; composite rows assert the flagged JSON delta with a comment citing the pin baseline) | **keep-as-unit** (pure mapping — §5.2 criterion) |
| G-P9 sanitization (+ kept `UrlSanitizerServiceTest`) | `Test_SanitizeBaseUrl` table | keep-as-unit |
| G-P10 redirects | fake GitLab 302 scenario variant | scenario |
| G-P11 empty token | `Decryptor` empty-string unit row + a scenario variant asserting the empty `token` part | unit + scenario |
| G-P12 k8s captured wire | `Scenario_Gitlab_SingleRun_FileSource`: same fixture, wire equal to the Kotlin capture modulo sanctioned deltas (§11.2) | scenario |
| G-P13 exit codes | single-run exit tests: PATCH 500 ⇒ non-zero; **file-missing ⇒ non-zero (deliberate non-port, comment cites G-P13 baseline — twin of 06A's M-P7 handling)** | scenario + flagged delta |
| `GitLabClientFactoryTest` | dissolved into `Test_SanitizeBaseUrl` (the factory was a test seam, §5) | consolidated |
| startup scenarios | persona boot smoke (config-read stage) | existing + smoke |
| C-P1–C-P7 core wire pins | inherited Go twins from 06A (T1/T4) — verified present, not duplicated | — |

Only G-P13's file-missing case changes asserted behavior — sanctioned by umbrella
§7.9/§10.3, identical wording to 06A (fixed in phase 6; the old exit-0 behavior stays
pinned as G-P13 for audit). Everything else ports semantically (STOP-B otherwise).

### 10.2 New Go-only tests

`DecryptInputs` table (step 2 — the umbrella-assigned artifact gets its own decision-
surface suite incl. a real-ciphertext cross-check), classification table, ctx-cancel
on the trigger request, alias precedence (8103), `BlockRunnerCompat.privateKey`
consumption + `ResolvePrivateKey` integration, mgmt smoke, leak test: **the multipart
body and MESHSTACK_RUN must not contain the decrypted trigger token in polling mode**
(umbrella §7.6 pin, structural via `DecryptInputs`). All hermetic (fake transports,
temp files, test keypair).

### 10.3 Gate

`tools/coverage/thresholds.txt` += `…/runner/internal/gitlab 90`; **no exclusion
entries** (whole package hermetic — fake meshStack + `httptest` GitLab). Touched
shared packages (`meshapi`, `config`) stay ≥90 via step-2/3 tests. The package is
~350 lines of trigger/payload/error logic fully driven by the scenario suite;
shortfall = STOP-C (add scenario cases, never exclusions). `-race` on. Keep-as-unit
list is exactly: `Test_ValueString`, `Test_SanitizeBaseUrl`, the classification table,
and the `DecryptInputs` table (real decision surface, D16) — nobody adds unit tests to
move the number.

## 11. Acceptance validation

No automated per-type acceptance exists for gitlab (umbrella §5.7/§10.2 finding) —
the gate before removal (step 9) is:

1. **Hermetic side-by-side transcript equivalence** (the 06A §11.3 procedure, reused
   verbatim): the same run JSON (sensitive + env/non-env + duplicate-key + LIST
   inputs, full link set) driven through the Kotlin runner (wiremock GitLab + captured
   meshStack updates — the §3.2 pin suite) and through the Go handler (fake-transport
   twins); diff empty modulo the sanctioned-delta allowlist: §7.7 headers, null ≡
   absent JSON, multipart boundary string, §16.4 composite stringification (if the
   fixture exercises it), §16.6 unknown-field forwarding (fixture uses modeled fields
   only, so empty in practice). Repeat for one error path (404) and the k8s fixture
   (G-P12 vs `Scenario_Gitlab_SingleRun_FileSource`).
2. **Manual smoke against a real GitLab** (documented in the PR description): one
   APPLY trigger from the Go persona against a real project with a real trigger token
   — pipeline starts, receives the MESHSTACK_* variables (job log echo), the
   IN_PROGRESS handover appears in meshStack, and the pipeline's callback (runToken +
   `MESHSTACK_UPDATE_SOURCE_URL`) lands a terminal status — proving the §2.2.3
   callback contract end-to-end. One negative smoke: wrong projectId ⇒ the 404 UX in
   the meshStack UI.
3. **Outer regression net:** meshfed-release local-dev-stack + acceptance suite still
   green (the gitlab runner claims from mux `:8303` when exercised; no per-type
   acceptance to run — umbrella A11/§5.7).

Evidence (commands, transcript diffs, smoke screenshots/links) in the PR description
(STOP-E). Only then do the §9 step-10 removal commits land.

## 12. Kotlin module removal + Gradle shrink

Umbrella §5.8 recipe instantiated (last commits, after §11 passes):

1. `git rm -r gitlab-block-runner/` — the §3.2 G-pins die with the module (their Go
   twins are the surviving pin, §10.1); the core C-pins stay in `block-runner-core`
   (deleted only in 06D).
2. `settings.gradle`: drop `include 'gitlab-block-runner'` (`settings.gradle:6`).
3. `.github/workflows/ci.yml`: drop the gitlab entries from `jvm-runners-ci`
   (`:34-35`) and `jvm-runners-image` (`:70-71`); add the go image leg (§8).
4. `.github/workflows/build-images.yml`: gitlab leg (`:38-40`) →
   `dockerfile: containers/gitlab-block-runner/Dockerfile` (a per-persona Dockerfile —
   no shared `target:` stage).
5. No meshfed-release edits required (§15 — verified, unlike 06A).
6. Grep gate: `grep -rn "gitlab-block-runner" --exclude-dir=.git` — remaining hits
   must be image/persona *names* (workflows, containers/, run-controller sample
   config, plan docs, CHANGELOG), never module *paths* (`gitlab-block-runner/src`,
   `:gitlab-block-runner:` gradle refs).

No other Gradle shrink — `block-runner-core`, root build files, wrapper,
`jvm.Dockerfile` stay until 06D (umbrella §5.8).

## 13. Frozen contracts touched

Umbrella §8 instantiated for GITLAB_PIPELINE. **Preserved (proven by pins → ported
tests):**

- meshStack wire: claim/register/update per the inherited C-pins; the lean
  `SourceUpdate` PATCH; runToken-only run-scoped auth; 409-register tolerated.
- **The GitLab trigger contract (customer pipelines parse this):** endpoint path
  `/api/v4/projects/{projectId}/trigger/pipeline`; multipart form fields `token`,
  `ref`, `variables[MESHSTACK_BEHAVIOR]`, `variables[MESHSTACK_RUN]`,
  `variables[<key>]` (env inputs), `inputs[<key>]` (non-env inputs),
  `variables[MESHSTACK_SELF_URL]`, `variables[MESHSTACK_REGISTER_SOURCE_URL]`,
  `variables[MESHSTACK_UPDATE_SOURCE_URL]` (templated `{sourceId}` verbatim),
  `variables[MESHSTACK_BASE_URL]`; missing-link ⇒ omit; the §2.6 secret asymmetry
  (inputs decrypted, impl token encrypted in `MESHSTACK_RUN`, standalone);
  `spec.runToken` inside `MESHSTACK_RUN` as the callback credential; redirects never
  followed; last-wins key dedup; scalar stringification.
- **Always-async semantics (D9):** register one PENDING `gl-trigger` step → exactly
  one final update, `IN_PROGRESS` + step `SUCCEEDED` on success, `FAILED`+`FAILED` on
  any trigger failure; the runner never reports terminal success.
- Step id `gl-trigger`, display name `"Trigger GitLab CI/CD"`, and all §2.3 row-B/C
  and §2.1.7 message strings (UI-visible, byte-identical).
- k8s single-run contract incl. `SPRING_PROFILES_ACTIVE: kubernetes` acceptance (D10
  both directions); `RUN_JSON_FILE_PATH`, `RUNNER_UUID`, `RUNNER_API_URL`.
- Image name/tags; `ENV PORT=8080`/`EXPOSE 8080`; healthz `OK` on the resolved legacy
  port (8103 default); all §6.2 env vars and yaml keys incl. `privateKey`/
  `privateKeyFile`; mux GITLAB_PIPELINE port `:8303`; shipped uuid default.

**Sanctioned, flagged deltas (uniform umbrella wording):** additive client headers
(§7.7); single-run exit tightening on pre-report failures (§7.9, G-P13); no listener
in single-run pods (§7.10); additive metrics/config; JVM `command:`-override
incompatibility (§5.6); slog text format; null ≡ absent serialization (06A §16.4);
**plus 06B-specific:** composite/exotic-float stringification (§16.4), unknown-field
forwarding in `MESHSTACK_RUN` (§16.6), multipart boundary/part-order (§16.7).

## 14. Rollback story

One squash commit ⇒ one `git revert` restores the Kotlin module, its
`settings.gradle` include, both CI matrix entries and the JVM image leg, and deletes
`internal/gitlab`, `cmd/gitlab/main.go` + its `cmd/bbrunner` handler registration, the
per-persona `containers/gitlab-block-runner/Dockerfile`,
`containers/gitlab-block-runner/`, the thresholds line and depguard rules, **and
`meshapi.DecryptInputs`** (its only consumer reverts with it — 06C/06D plans consume
it *after* 06B merges, so a 06B revert forces their STOP-A verification to fail
loudly, which is the intended behavior). Image name and every wire/k8s contract frozen
(§13) ⇒ `:main` floats back to the JVM image on the next CI run; deployed operator
configs need no change in either direction (`SPRING_PROFILES_ACTIVE` honored by both
generations, `EXECUTION_MODE` never required). Release tags immutable. Lost on revert
(documented cost): `MANAGEMENT_PORT`/metrics for gitlab, `maxConcurrentRuns` > 1,
opt-in registration, the exit-code tightening. The G-pin Kotlin tests revert with the
module restore commit's parent — re-land them if the revert is followed by a second
attempt. No cross-repo edits exist to revert (§15).

## 15. Cross-repo touch points

Umbrella §9 subset for 06B — **no mandatory cross-repo edits** (the contrast to 06A):

- **meshfed-release `local-dev-stack/SKILL.md`:** no gitlab-runner entry exists
  (verified by grep at plan time; re-verify at step 0). State "no edit"; an optional
  Go start snippet (`go run . gitlab-block-runner`, env `RUNNER_API_URL=http://localhost:8303`)
  is a maintainer decision, not required for the gate (umbrella §9).
- **meshfed-release acceptance tests / mux:** read-only; GITLAB_PIPELINE fan-out on
  `:8303` (wire frozen); no per-type acceptance tests exist (§11).
- **meshfed-release `how-to-run-building-block-runners.md`:** doc-truth check only —
  if the page names the gitlab image's `SPRING_PROFILES_ACTIVE` semantics, the 06A
  precedent note applies; full docs pass is phase 7.
- **This repo, `run-controller/runner-config.yml` sample (`:149-152`):** valid
  unchanged; optional `EXECUTION_MODE` comment per the umbrella §9 deferral.
- **terraform-provider-meshstack:** no dependency (pattern source only) — no edit;
  step-0 grep confirms.
- **Customer-side (not a repo, but the real coupling):** customer GitLab pipelines
  consume the §13 multipart contract — the reason the field set, `MESHSTACK_RUN`
  content and stringification are treated as frozen bytes, and why §16.4/§16.6 are
  flagged for explicit review rather than silently shipped.

## 16. Flags + Open questions

Findings the umbrella / 06A / prior plans did not anticipate, plus judgment calls:

1. **The classified GitLab error messages are log-only (umbrella §3.2 correction).**
   The 404/identity-verification/generic user+system message pairs live in
   `MeshHttpException` and never reach the step update — the service always reports
   the same pair: user `"Could not trigger the GitLab pipeline"`, system
   `"GitLab responded with status: <code> and body: <body>"`
   (`GitLabBlockRunnerService.kt:73-89` vs `GitLabClient.kt:69-107`). The umbrella
   §3.3 gap row asked for "404 / identity-verification / generic error message pins" —
   G-P3/G-P4 pin what is actually observable (identical wire shape; classification in
   logs only). The Go port keeps the classified strings byte-identical in
   `ExternalCallError` → slog (§4.5). Umbrella prose correction, no contract change.
2. **`GitLabErrorBody` toString garbage:** the A4 systemMessage embeds a non-data
   class ⇒ `ClassName@hash` in the Kotlin log (`GitLabClient.kt:101-107`). Log-only;
   Go logs the parsed body as compact JSON instead — not a pin (logs are not contract).
3. **The §7.6 secret asymmetry only holds standalone.** In k8s mode the controller has
   already decrypted `pipelineTriggerToken` into the mounted run JSON
   (`decryption.go:81-95`) and the NoOp decryptor cannot restore ciphertext — today's
   `MESHSTACK_RUN` embeds the **plaintext** token in k8s mode. Pinned as-is (G-P12);
   the umbrella §7.6 leak-test rule is scoped to polling mode. A fix (controller-side
   inputs-only decryption for pipeline runners) would change the k8s contract —
   out of scope, noted for a follow-up.
4. **Composite/exotic-scalar stringification is a genuine byte delta.** Kotlin
   `value.toString()` renders arrays/maps as Java toString (`[a, b]`/`{k=v}`) and
   exotic doubles as `1.0E20`; Go emits **compact JSON** / the literal `json.Number`
   token (§4.2.1), and the pins assert JSON. Scalars (string/int/bool — the realistic
   cases) are byte-identical and pinned; composites are pinned as baseline (G-P8) and
   shipped as a flagged delta (Java toString is not parseable; a pipeline relying on it
   gets strictly better bytes). 06C (stringified `templateParameters`) adopts the same
   compact-JSON decision (umbrella §2 resolution rule).
5. **JSON `null` input values are Kotlin-unrepresentable** (`value: Any` fails the
   claim parse ⇒ run silently stuck until coordinator timeout) but Go-representable.
   Go stringifies `null` and proceeds (§4.2.1) rather than inventing a claim-failure
   path — the 06A §16.5 unknown-enum precedent. Reviewer may prefer fail-the-run.
6. **`MESHSTACK_RUN` fidelity strategy: raw-preserving beats model round-trip.**
   Kotlin round-trips through its typed model (drops unknown fields, emits explicit
   nulls, default-inclusion mapper); Go's `DecryptInputs` transforms the claimed bytes
   generically (§4.4) — unknown fields the API sends are now forwarded where Kotlin
   dropped them (additive; parsed-JSON-equal on all modeled fields). The exact
   `_links` key spelling under `@JsonUnwrapped`-on-Map is pinned empirically by G-P1
   (STOP-F) rather than asserted from Jackson lore. Flagged for review as the one
   place the port is deliberately *more* faithful to the API than Kotlin was.
7. **Multipart boundary and part order are not contract.** OkHttp and `mime/multipart`
   generate different boundaries; Go writes parts in the Kotlin build order for
   diff-convenience, but pins assert the field *set* + values (G-P1) — recorded so
   the side-by-side diff procedure (§11.1) normalizes multipart before comparing.
8. **`DecryptInputs` deliberately diverges from the controller's input rule.** The
   controller decrypts any sensitive string value regardless of type
   (`decryption.go:29-31`); Kotlin's payload path restricts to STRING/CODE/FILE with
   warn-skip (`MeshCertDecryptionService.kt:58-97`). `DecryptInputs` implements the
   Kotlin rule (umbrella §4 row 8) — the two rules coexist in the binary
   (`DecryptRunDetails` keeps the controller behavior for the k8s Secret path). Both
   are pinned; unifying them is a post-refactor follow-up, not phase 6.
9. **`ExternalCallError` is per-package by umbrella ruling** (§4 row 14: "per-package
   typed error with the same fields") although 06A §4.4 reads as one type shipped in
   06B. Resolved here as: 06B ships `gitlab.ExternalCallError` as the reference
   implementation of the 06A-specified shape; 06C/06D copy the 5-field shape. If
   review prefers a single shared type, that is an umbrella §4-row-14 revision (a
   home would need to be found outside `meshapi` — new tiny package), not a 06B-local
   call.
10. **Empty-string decrypt mismatch between the Kotlin and Go crypto seams** (T8):
    Kotlin `decrypt("")` ⇒ `""`; Go `DecryptMeshCertBased("")` ⇒ error. G-P11 pins the
    Kotlin behavior (empty `token` part, request still sent); the guard lands at the
    shared `Decryptor` seam. Prior plans never surfaced this because tf/controller
    only decrypt values already known non-empty.
11. **Kotlin's k8s scenario for gitlab is boot-only** — unlike manual, there is no
    captured-wire k8s test to inherit; G-P12 adds it (manual's capture style). Umbrella
    §3.3 listed the gitlab gaps but not this asymmetry between the two modules' suites.

**Open questions:** none — every decision branch was walked and resolved from the
sources; the reviewer-vetoable judgment calls are flags 4, 5, 6, 9 above plus the
umbrella-level calls they instantiate (§7.4 lean body, §7.9 exit rule, §10.4
relaxed-binding boundary, §10.5 dev-key placement).
