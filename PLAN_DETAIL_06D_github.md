# Detail Plan 06D — GitHub Runner Port (Phase 6, PR 4 — the last port + JVM endgame)

**Phase:** 6d · **Branch:** `refactor/single-go-binary/phase-6d-github` (stacked on
`refactor/single-go-binary/phase-6c-azdevops`) · **Delivery:** one single-commit PR ·
**Binding:** umbrella `PLAN_DETAIL_06_kotlin_ports_umbrella.md` (§5 template contract,
§7 consistency rules, §8 frozen contracts) + `PLAN_HIGH_LEVEL.md` §3 P1–P8, D5, D6
(Kotlin corollary), D7, D9, D11 (`internal/github`), D12 (port 8102), D15, D16.

Kotlin references are `main` @ `c3fce61`; Go references marked *post-N* are shapes
promised by plan N or by 06A/06B/06C. 06D is the **last** port: besides the
`github-block-runner` module it removes `block-runner-core` and every remaining piece of
Gradle/JVM machinery (umbrella §5.8 "06D additionally"; phase exit "Gradle build gone").

## 1. Assumptions from prior phases

Plans 00–05 and 06A–C are **not implemented yet**. Implementation begins by running
**all umbrella §1 verification steps (A1–A12)** — incorporated by reference — plus the
06D-specific ones below. Any material failure is a **STOP** per the umbrella's STOP-A.

| # | Assumption | Promised by | Verification step |
|---|---|---|---|
| D1 | The github module and block-runner-core are byte-identical to `main` @ `c3fce61` (all §2 file:line citations hold). | Plans 00–06C scope (umbrella A10) | `git diff main..phase-6c-azdevops -- github-block-runner/ block-runner-core/` — only the 06A C-pin test additions (§3.3 there) appear |
| D2 | 06A template artifacts exist and are consumed by 06B/06C already: the unified `report.Reporter{Register(RunStatus) error; Report(RunStatus) (abort bool, err error)}` (abort discarded here; stateless, link-based run-scoped client, `{sourceId}` substitution) whose marshaled lean PATCH body stays `meshapi.SourceUpdateDTO`/`StepUpdateDTO`, `config.SingleRunMode`, `config.BlockRunnerCompat`, `config.ResolvePrivateKey`, the shared `ClaimClassifier`, the R12 single-run exit tail, the Dockerfile final-stage pattern + `containers/<persona>/runner-config.yml` layout, the removal recipe + CI-flip mechanics, the side-by-side transcript procedure + delta allowlist wording. | 06A §4.3/§6.3–6.5/§7/§8/§11.3 | read `runner/internal/{report,config}`; `grep -rn "report.Reporter\|SingleRunMode\|ResolvePrivateKey" runner/` |
| D3 | 06B artifacts exist: `ExternalCallError{UserMessage, SystemMessage, StatusCode, RequestUrl, ResponseBody}` (the `MeshHttpException` twin, 06A §4.4 contract) and `meshapi.DecryptInputs` (input-only decryption: sensitive STRING/CODE/FILE decrypted, other types logged + left as-is, impl secrets untouched — umbrella §4 row 8, signature reviewed against 06D's needs). | 06B (first consumer), umbrella §4 rows 8/14 | `grep -rn "ExternalCallError\|DecryptInputs" runner/internal` and read both contracts |
| D4 | 06C established the in-handler sync-poll pattern (ctx-cancelable clock waits, 10s/30min constants as constructor defaults, poll-error-resilience shape) that 06D mirrors; poll-loop helpers are deliberately **local** per runner (06A §17 "gaps left to owners" — different step semantics), so no shared poller is expected. | 06C, 06A §17 | read `runner/internal/azdevops` poller; confirm no shared `poll` package exists |
| D5 | `dispatch.RunHandler`/`ClaimedRun` per plan 05 §4 (A1): handler owns its timeout, decrypts per run, runToken-only run-scoped reporting, run-level FAILED reported by the handler then `nil` returned. | Plan 05 §4/§17 | read `runner/internal/dispatch` |
| D6 | `meshapi.RunDetailsDTO` models everything the github service reads: `Metadata.Uuid`, `Spec.Behavior`, `Spec.RunToken`, `Spec.BuildingBlock.Spec.Inputs[]{Key, Value, Type, IsSensitive, IsEnvironment}`, `Spec.Definition.Spec.Implementation` (raw JSON), `Links{Self, RegisterSource, UpdateSource, MeshstackBaseUrl}`; `meshapi.GithubImplementation` carries all 11 fields incl. `OmitRunObjectInput` (`go-meshapi-client/meshapi/dtos.go:130-144` today, moved by plan 04; cross-checked against `MeshBuildingBlockGithubImplementation`, `MeshBuildingBlockRun.kt:179-198` — umbrella §10.11: no DTO gap). | Plans 03/04 moves | read `runner/internal/meshapi/dtos.go` |
| D7 | `crypto.MeshCertBasedCrypto` decrypts `appPem` in polling mode exactly as the controller does in k8s mode (`run-controller/controller/decryption.go:65-79` GitHub branch: `AppPem` non-empty ⇒ `DecryptMeshCertBased`); single-run mode receives the pre-decrypted PEM. | Umbrella A9, plan 05 §5 | run `crypto` tests; the controller goldens |
| D8 | Run JSON decoding on the claim/file path preserves number fidelity (`json.Decoder.UseNumber()` or equivalent), recorded as a template requirement in 06A §17 precisely because github's `buildingBlockRun` payload embeds run values. | 06A §4.2/§17 | `grep -rn "UseNumber" runner/internal` |
| D9 | The block-runner-core wire pins C-P1–C-P7 exist (06A §3.3) and are green — 06D deletes them **with** `block-runner-core` (§12); their Go twins are the surviving pin. | 06A step 1 | `./gradlew :block-runner-core:check` green on the phase-6c branch |
| D10 | The Kotlin github suite is green as-is: `./gradlew :github-block-runner:check` passes on the phase-6c branch. | Current `main` CI | run it once before writing pins |
| D11 | After 06A–C, the only remaining `jvm-runners-ci` matrix entries are `block-runner-core` and `github-block-runner`, and the only `jvm-runners-image`/`build-images.yml` JVM leg is github (`ci.yml:27-36`, `:66-73` and `build-images.yml:35-37` on `main`, shrunk by A/B/C). | 06A–C removal steps | read both workflows on the phase-6c branch |

**STOP markers.** Umbrella STOP-A–E verbatim; here they bite at: STOP-A = §9 step 0;
STOP-B = any §3 pin that cannot be ported per §10's mapping; STOP-C = `internal/github`
(or a touched shared package) below 90 at any §9 checkpoint; STOP-D = the §4.6 fit-check
finds a template/`ExternalCallError`/`DecryptInputs` shape that does not fit — fix is a
reviewed umbrella/06A/06B revision, never a 06D-local workaround; STOP-E = §11's gate
fails (Kotlin module and the JVM machinery are **not** removed until it passes).

## 2. Kotlin behavior inventory

Full study of `github-block-runner` (11 production files) plus the block-runner-core
mechanics it exercises. Deepens umbrella §3.2 "github"; the umbrella §4 core map applies
unchanged. Every coordinator-, GitHub- or customer-workflow-visible behavior with
evidence.

### 2.1 Service flow (`GitHubBlockRunnerService.kt`)

1. **Claim-and-swallow:** any fetch exception ⇒ log `"Unexpected error while getting a
   block run."` + no-run (`:27-33`) — the shared classifier policy (umbrella §4 row 2).
2. **Register first, validate later:** exactly one step `gh-trigger` (companion
   `STEP_ID`, `:592-593`) / `"Trigger GitHub Action"` registered **before** the
   implementation is parsed (`:38-41`); register failures propagate (infrastructure).
3. **Implementation extraction:** `getImplementation<MeshBuildingBlockGithubImplementation>()`
   throws `IllegalStateException("The building block implementation of run <uuid> was
   not of expected type.")` on type mismatch (`MeshBuildingBlockRun.kt:132-138`) ⇒
   caught ⇒ FAILED via the generic-exception message (`:43-48`, §2.6).
4. **Client per run:** `GitHubClientFactory.provideClientFor(implementation.githubBaseUrl)`
   (GHE support — the base URL is run data, not runner config) after
   `UrlSanitizerService.sanitize` (trim, drop one trailing `/`, empty ⇒
   `IllegalArgumentException("URL should not be empty")`) (`GitHubClientFactory.kt:10-14`,
   `UrlSanitizerService.kt:8-20`); factory errors ⇒ FAILED generic (`:50-57`).
5. **Auth chain:** decrypt `implementation.appPem` (`DecryptionService.decrypt`, `:59-68`)
   → mint App JWT (§2.2) → `GET {base}/repos/{owner}/{repo}/installation` ⇒
   installation id (`:70-82`) → `POST {base}/app/installations/{id}/access_tokens` ⇒
   installation token (`:84-95`). `MeshHttpException` from either GitHub call ⇒ FAILED
   with the request/status/body system message; any other exception ⇒ FAILED generic.
   **No caching anywhere**: JWT, installation id and installation token are fetched
   fresh per processed run (`AppTokenFactory` is stateless; the client is per-run).
6. **Workflow selection (frozen):** `APPLY`→`applyWorkflow`, `DETECT`→`applyWorkflow`,
   `DESTROY`→`destroyWorkflow` (`:97-101`); selected name `null` (only `destroyWorkflow`
   is nullable, `MeshBuildingBlockRun.kt:187`) ⇒ FAILED
   `"Workflow file name must not be null"` (`:103-109`).
7. **Trigger:** `decryptBlockRunInputs` (inputs only — impl secrets stay encrypted) then
   the §2.4 inputs builder; POST `…/actions/workflows/{workflowName}/dispatches` with
   `{ref: implementation.branch, inputs: …}` (`:166-213`, `GithubClient.kt:231-266`).
   Result handling: `Success` ⇒ the §2.6 trigger-success update; `UnsupportedInput` ⇒
   FAILED with the per-input guidance messages joined by `\n` (`:125-136`, §2.6);
   `Error` ⇒ FAILED `"GitHub API returned status <code> when triggering workflow:
   <body>"` (`:138-144`); thrown exception ⇒ FAILED generic (`:146-149`).
8. **Async vs sync:** `implementation.async == true` ⇒ return after the trigger-success
   update — run status `IN_PROGRESS`, the D9 handover (`:151,163`); `false` ⇒ §2.5
   polling. In both cases `processBlock` returns the run (metrics: handover = succeeded,
   umbrella §7.2).

### 2.2 App JWT (`AppTokenFactory.kt`)

- Claims: `iat = now − 10` (clock skew), `exp = now + 300`, `iss = appId` — epoch
  seconds via `Instant.now()` (**not** the injected service clock — flag §16.9)
  (`:30-34`). Signed RS256 via auth0 `JWT.create().withPayload(payload).sign(...)`
  (`:36-42`); header is the library default `{"alg":"RS256","typ":"JWT"}`. No `jti`,
  no audience.
- **PKCS#1 PEM parsing is whitespace-tolerant string surgery, not strict PEM** (`:45-67`):
  strip the literal `-----BEGIN/END RSA PRIVATE KEY-----` markers, remove **all**
  whitespace, base64-decode, parse the ASN.1 `RSAPrivateKey` (BouncyCastle), build the
  JVM key from **modulus + privateExponent only** (no CRT params). Consequences the port
  must preserve: a single-line PEM pasted without newlines parses fine; a PKCS#8
  (`BEGIN PRIVATE KEY`) PEM fails (markers don't match ⇒ base64/ASN.1 error ⇒ FAILED
  generic). GitHub Apps issue PKCS#1 PEMs — this is the supported format.
- Deps to be deleted with the module: `com.auth0:java-jwt`, `org.bouncycastle:bcprov-jdk18on`
  (`github-block-runner/build.gradle:12-13`, constrained in root `build.gradle:50-51`).

### 2.3 GitHub API wire (`GithubClient.kt`)

- Common headers on all five calls: `Accept: application/vnd.github+json` (source has a
  leading space, `:167,212,335,368` — OkHttp trims header values, inert),
  `X-GitHub-Api-Version: 2022-11-28`, `Authorization: Bearer <jwt|installation token>`.
  Redirects are **not** followed (`:142-144`).
- `GET /repos/{owner}/{repo}/installation` ⇒ `{id}` as installation id (`:201-229`).
- `POST /app/installations/{id}/access_tokens` ⇒ `{token, permissions}`;
  **permission gate:** `permissions["actions"] != "write"` ⇒ `MeshException("Your
  installed GitHub App is missing write permissions for actions. Required permissions:
  actions=write. Actual permissions: <map>")` (`:188-199`) ⇒ FAILED generic path.
- `POST /repos/{owner}/{repo}/actions/workflows/{file}/dispatches`, JSON body, expects
  204. **Quirk:** the request carries the body's `Content-Type: application/json;
  charset=UTF-8` *and* a second stray `Content-Type:
  application/vnd.meshcloud.api.meshbuildingblockrun.v1.hal+json` added via `addHeader`
  (`:273-282`) — GitHub ignores it; not ported (flag §16.2). 422 + body containing
  `"Unexpected inputs provided"` **and** a recognized input name (`buildingBlockRun`,
  `buildingBlockRunUrl`, `MESHSTACK_API_TOKEN`, `MESHSTACK_RUN_TOKEN`) ⇒
  `UnsupportedInput` (possibly several names, `:250-259,268-271`); any other non-2xx ⇒
  `Error{statusCode, responseBody}`.
- `GET …/actions/workflows/{file}/runs?per_page=N` ⇒ `workflow_runs[]`
  (`:287-319`); `GET …/actions/runs/{id}` (`:324-352`); `GET …/actions/runs/{id}/jobs`
  ⇒ `jobs[]` (`:357-385`). DTO fields read: run `{id, status, conclusion, created_at,
  html_url}`, job `{id, name, status, conclusion, started_at, completed_at, html_url}`
  (`:106-131`). `status` strings outside `queued|in_progress|completed` throw
  `IllegalArgumentException("Unknown workflow run|job status: <v>")` (`:41-79`) —
  swallowed by the poll loop's retry (§2.5).

### 2.4 The workflow-inputs contract (frozen toward customer workflows)

`BuildingBlockWorkflowInputsBuilder.kt` — the dispatch `inputs` map, selected by
`implementation.omitRunObjectInput` (`GitHubBlockRunnerService.kt:176-182`). Input
source is the run **with inputs decrypted** (`decryptBlockRunInputs`, `:173`).

**Mode A — `omitRunObjectInput: false` (legacy, default):** exactly one input,
`buildingBlockRun` = base64(UTF-8 JSON) of the `ProcessableBlockRun` serialized with the
sanitizing mapper (`:81-110`):

- Field set = the Kotlin DTO graph (unknown API fields were already dropped at claim
  parse time): `kind`, `apiVersion`, `metadata{uuid,…}`, `spec{runNumber,
  buildingBlock{uuid, spec{displayName, workspaceIdentifier, projectIdentifier,
  fullPlatformIdentifier, inputs[], parentBuildingBlocks[]}}, buildingBlockDefinition
  {uuid, spec{workspaceIdentifier, version, implementation}}, behavior, runToken},
  status, _links` (pinned byte-level by `GithubBlockRunnerServiceTest.kt:155-260`).
- The implementation object is stripped to **only** the type discriminator
  `{"type":"GITHUB_WORKFLOW"}` — every declared field incl. `appPem`, `owner`, `appId`,
  `branch`, workflows, flags is `@JsonIgnore`d via
  `IgnoreBuildingBlockGithubImplementationMixin` (`:5-39`); the `type` survives because
  it is the `@JsonTypeInfo` discriminator, not a property. Secret hygiene = umbrella §7.6.
- `spec.runToken` and `_links` **are included** — legacy workflows use them to call back.
- Inputs carry decrypted values with original JSON types (`"value": 4` stays a number —
  `GithubBlockRunnerServiceTest.kt:275-346`); unset optionals serialize as explicit
  `null` (`"templated": null`) — null ≡ absent equivalence per 06A §16.4.

**Mode B — `omitRunObjectInput: true` (modern):** built from the decrypted run
(`:37-75`):

- `buildingBlockRunUrl` = the run's `self` link href; missing self link ⇒
  `IllegalStateException("No self link found for building block run <uuid>")` ⇒ FAILED
  generic.
- `MESHSTACK_API_TOKEN` and `MESHSTACK_RUN_TOKEN`: passed **iff** an input with that
  exact key exists, value = decrypted `value.toString()`.
- `MESHSTACK_ENDPOINT`: passed **iff** `MESHSTACK_API_TOKEN` was passed **and** an
  input with key `MESHSTACK_ENDPOINT` exists (`:68-72`).
- Nothing else — regular user inputs are never dispatch inputs in either mode.

All input **names** (`buildingBlockRun`, `buildingBlockRunUrl`, `MESHSTACK_API_TOKEN`,
`MESHSTACK_RUN_TOKEN`, `MESHSTACK_ENDPOINT`), the base64+JSON encoding, the field set,
the `ref` value (= `implementation.branch`) and the behavior→workflow selection are
**frozen** — customer `workflow_dispatch` triggers declare these inputs and parse the
payload (umbrella §8).

### 2.5 Sync polling & run correlation (`GitHubBlockRunnerService.kt:215-333`)

Constants: `MAX_FIND_WORKFLOW_ATTEMPTS = 12`, `MAX_POLLING_MINUTES_WORKFLOWS = 30`,
`POLLING_INTERVAL_SECONDS = 10` (`:595-597`). `triggerTime` = injected-clock now at poll
start (`:223`).

1. **Find the dispatched run** (workflow_dispatch returns no run id — correlation is
   heuristic): ≤12 attempts; each lists the 5 most recent runs of the workflow file and
   picks the first with `created_at > triggerTime − 30s` (`:232-257`); miss or listing
   exception ⇒ 10s sleep, next attempt (exception logged as warn). All 12 fail ⇒ FAILED
   `"Could not find the triggered workflow run after 12 attempts"` (`:259-265`).
2. **Poll until COMPLETED:** while run status ≠ `completed`: timeout check first
   (`now > triggerTime + 30min` ⇒ FAILED timeout, §2.6), sleep 10s, then
   `getWorkflowRun` + `listWorkflowJobs`; any exception ⇒ warn + `continue` (retry
   forever within the 30-min budget) (`:275-308`). **Quirk:** a found run that is
   already `completed` skips the loop entirely — `getWorkflowRun` is never called
   (pinned by `GithubBlockRunnerServiceTest.kt:388-413`).
3. **Job steps** (`updateJobStatuses`, `:335-417`): report jobs that are new
   (`seenJobIds`) **or** `completed` — i.e. completed jobs are re-reported on every
   poll (quirk, pinned as-is, flag §16.6). Step id `gh-workflow-job-<job.id>`, display
   name `"GitHub Job: <name>"`; status map: completed+`success`⇒SUCCEEDED,
   completed+other⇒FAILED, in_progress/queued/other⇒IN_PROGRESS; userMessage variants
   `"Job '<name>' completed successfully|failed|was cancelled|was skipped|is running|is
   queued|status: <status>"`; systemMessage `"Job ID: <id>, Status: <status>[,
   Conclusion: <c>][, Started: <t>][, Completed: <t>], View job: <html_url>"`. The
   `gh-trigger` step (SUCCEEDED, `"GitHub workflow triggered successfully"` /
   `"Workflow started, monitoring individual jobs"`) is prepended **only when the first
   job batch is reported** (`seenJobIds.size == newOrUpdatedJobs.size`, `:393-407`).
   Each batch is one `SourceUpdate{status: IN_PROGRESS, steps}`.
4. **Final:** one last `listWorkflowJobs` (errors warn-swallowed, `:310-321`), then the
   terminal update from `conclusion`: `success`⇒SUCCEEDED, everything else
   (`failure`/`cancelled`/`timed_out`/unknown)⇒FAILED; user message per conclusion
   (`"GitHub workflow completed successfully|failed|was cancelled|timed out|completed
   with unknown status"`); system message `"Workflow run <id> completed with status:
   <status>, conclusion: <conclusion>. View run: <html_url>"`; steps = the `gh-trigger`
   step only (`:419-454`).
5. Any other exception during polling ⇒ FAILED generic (`:325-328`).

### 2.6 Failure/message surface (all UI-visible, ported byte-identically §7.11)

Every failure update is `SourceUpdate{status: FAILED, steps: [gh-trigger FAILED]}` with
user `"Could not trigger the GitHub Action"` and a system message (`:485-502`):

| Path | System message |
|---|---|
| `MeshHttpException` (installation id/token calls) | `"Request: <url>\nGitHub responded with status: <code> and body: <body>"` (`:456-463`) |
| generic exception (decrypt, JWT, factory, impl-type, self-link, permission gate, poll crash) | `"There was an internal error while trying to contact GitHub: <msg>"` (`:465-472`) |
| poll timeout | `"There was an internal error while trying to contact GitHub: Workflow polling timeout after 30 minutes"` (`:474-483`) |
| null workflow | `"Workflow file name must not be null"` (`:103-109`) |
| trigger `Error` | `"GitHub API returned status <code> when triggering workflow: <body>"` (`:138-144`) |
| trigger `UnsupportedInput` | the 4 long guidance messages of `:505-556` (one per recognized input, joined `\n`) — verbatim, incl. the YAML snippets and the actions-register-source release link |

Trigger success: `SourceUpdate{status: IN_PROGRESS, steps: [gh-trigger SUCCEEDED]}`,
user `"Triggered GitHub Action '<wf>'. <extra>"`, system `"Triggered action '<wf>'.
<extra>"` where `<extra>` = `"Polling for completion status..."` (sync) /
`"Will wait for API updates on status..."` (async) (`:558-589`).

### 2.7 Wiring, modes, config (Spring)

- Standalone: `@Scheduled(10s)` + `ImmediateRetryDecorator`; k8s profile: no decorator,
  `SingleShotRunner` (`BlockRunnerServiceConfiguration.kt:15-45`,
  `GitHubBlockRunnerSchedulingConfiguration.kt:10-13`) — identical to 06A §2.3; exit
  matrix = 06A §2.5 verbatim (fetch swallow ⇒ exit 0; report error ⇒ exit 1).
- Decryption: standalone uses `MeshCertDecryptionService` with the key resolved by
  `PrivateKeyLoader` (`GitHubBlockRunnerCryptoConfiguration.kt:11-17`); k8s profile ⇒
  NoOp (controller pre-decrypted `appPem` + inputs, D7 above).
- Health `/healthz` on Spring `PORT`, default **8102** (`application.yml:8`); image
  bakes `PORT=8080` (`jvm.Dockerfile:18-19`).
- Shipped defaults (`runner-config.yml:1-12`): `version ${VERSION:dev}`, uuid
  `${RUNNER_UUID:606f54c8-ed3b-4a79-ad80-971dfb4eff21}`, api url
  `${RUNNER_API_URL:http://localhost:8302}` (mux GITHUB_WORKFLOW port, umbrella A11),
  `bb-api`/`guest`, blank api-key creds, **and an inline baked dev `privateKey`**
  (PKCS#8, line 12 — umbrella §10.5). No github-specific config keys exist — the
  GitHub coordinates all arrive per run in the implementation object.
- Operator k8s dispatch: `SPRING_PROFILES_ACTIVE: kubernetes`
  (`run-controller/runner-config.yml:144-147`) — the §7.8/A12 single-run alias.

## 3. Kotlin pin tests (tests-first step)

Tests-only commits in `github-block-runner` (`git diff -- ':!*Test*' ':!*Scenario*' ':!*fixtures*'`
empty), proven green by the existing `jvm-runners-ci` leg before any Go code (umbrella
§5.2). The C-P1–C-P7 core wire pins already exist (D9) — 06D verifies, never re-writes.

### 3.1 What already exists (kept, later ported per §10)

**37 tests** across 7 files — *correction to umbrella §3.3's "~30", flag §16.1*:

- `GithubBlockRunnerServiceTest` — 10 (mockk): no-run, fetch-exception, happy trigger
  incl. byte-level `buildingBlockRun` assertion, decrypted-inputs + mixin-stripping,
  async (no polling calls), sync poll (already-completed run ⇒ `getWorkflowRun`
  exactly 0), UnsupportedInput ×2, Error result, and "deserializing fails" — which
  actually pins the **wrong-implementation-type** path (a Gitlab impl in the run,
  `:262-279`; mislabeled, flag §16.3).
- `GithubClientTest` — 11 (wiremock): dispatch payload run/url modes (byte-level JSON
  body + headers), missing-actions-permission gate, installation-token/id happy paths,
  listWorkflowRuns (`per_page=5`), getWorkflowRun, listWorkflowJobs, UnsupportedInput
  ×2, 422-generic, 500, 401.
- `BuildingBlockWorkflowInputsBuilderTest` — 7 (the Mode-B table of §2.4).
- `SensitiveSystemInputsIntegrationScenario` — 6 (full Spring context + wiremock GitHub,
  captured `SourceUpdate`s): token passing × omit flag × endpoint rules.
- `GitHubClientFactoryTest` — 1 (URL sanitized before client construction).
- `GitHubRunnerScenario` (boot smoke) — 1; `GitHubRunnerKubernetesStartupScenario`
  (k8s profile boot, `TestRunTerminator`) — 1.

### 3.2 New github-module pins (closing the umbrella §3.3 gap column)

| Id | Pin (scenario-level where possible, D16) | Anchors |
|---|---|---|
| G-P1 | **JWT claims + signature:** with a real test PKCS#1 key, the minted token verifies RS256 against the public key; header `alg=RS256, typ=JWT`; claims exactly `{iat: now−10, exp: now+300, iss: appId}` (fixed clock injected for the pin — see §16.9 for the `Instant.now()` wrinkle) | §2.2; today only bypassed via `TestAppTokenFactory` |
| G-P2 | **PKCS#1 parsing tolerance table:** multi-line PEM, single-line PEM (no newlines), PEM with stray spaces all parse; PKCS#8 `BEGIN PRIVATE KEY` and garbage base64 fail with an exception (⇒ FAILED generic path) | `AppTokenFactory.kt:45-67` |
| G-P3 | **Job-step emission:** a sync poll producing two jobs then completion yields updates with step ids `gh-workflow-job-<id>`, display names `GitHub Job: <name>`, the §2.5.3 status/message mapping, the `gh-trigger` step **only** in the first job batch, batch status always IN_PROGRESS | §2.5.3; wiremock sequence stubs |
| G-P4 | **Completed-job re-report quirk:** a job already reported as completed appears again in the next batch (pinned as-is, not fixed) | `:341-348` |
| G-P5 | **Find-run window:** a run with `created_at` older than `triggerTime−30s` is skipped; a newer one is picked; 12 misses ⇒ FAILED `"Could not find the triggered workflow run after 12 attempts"` (no terminal SUCCEEDED/step leak) | §2.5.1 |
| G-P6 | **Poll timeout:** run stuck `in_progress` past 30min ⇒ FAILED with the §2.6 timeout message; poll errors before that are retried, not fatal | §2.5.2 |
| G-P7 | **Permission gate message:** installation token with `actions: read` ⇒ FAILED generic embedding the `"missing write permissions"` message | `GithubClient.kt:188-199` |
| G-P8 | **Null destroy workflow:** DESTROY behavior + `destroyWorkflow: null` ⇒ FAILED `"Workflow file name must not be null"`; DETECT selects `applyWorkflow` | `:97-109` |
| G-P9 | **Register-before-validation ordering** + cardinality: wrong-impl run still registers `gh-trigger` before the FAILED update (extends the mislabeled existing test with an explicit order assertion) | `:38-48` |
| G-P10 | **Secret hygiene (leak pin):** in Mode A the base64 payload contains the decrypted *input* values, `runToken` and `_links`, but **no** `appPem`/`owner`/`appId`/`branch`/workflow fields and no ciphertext of them; in Mode B no input other than the five §2.4 names ever appears | §2.4, umbrella §7.6 |
| G-P11 | **k8s exit codes:** update failure ⇒ exit 1; fetch failure ⇒ exit 0 (the Kotlin baseline the §7.9 Go delta is measured against — the github twin of 06A M-P6/M-P7) | 06A §2.5, `SingleShotRunner.kt:38-49` |

Quirks pinned as-is and listed in §16 (D13 discipline, no fixes here): G-P4, the stray
dispatch Content-Type header (asserted loosely — wiremock matches the json content-type
regardless), the `Instant.now()` JWT clock, the exit-0 swallow (G-P11).

## 4. Go handler design

Package `runner/internal/github` (D11). Illustrative signatures only; umbrella §5.3
shape exactly.

### 4.1 Handler

```go
// package github — the GITHUB_WORKFLOW run handler (App JWT → installation token →
// workflow_dispatch; async handover or sync run/job polling).
func NewHandler(cfg Config, deps HandlerDeps) Handler        // value type (P4)
func (h Handler) Execute(ctx context.Context, run dispatch.ClaimedRun) error

type HandlerDeps struct {
    Reporters ReporterFactory   // per-run source reporter, runToken-only (06A §4.3)
    Decryptor meshapi.Decryptor // cert-based (polling) | NoOp (single-run)
    HTTP      *http.Client      // external-API seam; redirects disabled; fakeable
    Clock     Clock             // JWT claims + find/poll waits; fake in tests
    Log       *slog.Logger      // D15; per-run via Log.With("run", run.Id)
}
```

- `Execute` skeleton = §2.1: register `gh-trigger`/`"Trigger GitHub Action"` → parse
  `GithubImplementation` from `run.Details.Spec.Definition.Spec.Implementation` →
  sanitize base URL → decrypt `AppPem` (`Decryptor.Decrypt`; NoOp in single-run) →
  mint JWT (§4.2) → installation id → installation token (permission gate) → select
  workflow by behavior → build inputs (§4.4) → dispatch → success update or FAILED →
  async return / sync poll (§4.5) → nil. Reported FAILED ⇒ return nil (A1);
  register/update transport failures ⇒ non-nil error.
- Timeout ownership (A1): the 30-min poll budget and the 12×10s find window live inside
  `Execute` as constructor-default constants (`findAttempts=12`, `pollInterval=10s`,
  `pollTimeout=30m`), waits `select` on `Clock` timer vs `ctx.Done()` — cancellation now
  drives a terminal shutdown report per the §4.5 graceful-shutdown treatment (same
  as 06C).
- External-API errors use 06B's `ExternalCallError` (D3); the §2.6 message strings are
  produced in this package, byte-identical.

### 4.2 App JWT — the stdlib decision (umbrella §4 row 16, confirmed)

**No JWT dependency.** `com.auth0:java-jwt` + BouncyCastle are replaced by ~40 lines of
stdlib (P2; the meshfed mux stdlib bar):

```go
// appToken mints the GitHub App JWT: base64url(header).base64url(claims) signed
// RS256 (SHA-256 + PKCS#1 v1.5). Header {"alg":"RS256","typ":"JWT"};
// claims {iat: now-10, exp: now+300, iss: appId}.
func appToken(clock Clock, appId string, key *rsa.PrivateKey) (string, error)

// parseAppPem reproduces the Kotlin tolerance (§2.2/G-P2): strip the PKCS#1 armor
// lines, drop all whitespace, base64-decode, x509.ParsePKCS1PrivateKey.
func parseAppPem(pem string) (*rsa.PrivateKey, error)
```

- Justification: the JWS is three base64url segments + one `rsa.SignPKCS1v15` call —
  a dependency would import a whole claims/validation framework to *serialize* one
  static payload. Verification exists in tests via `rsa.VerifyPKCS1v15`. Hand-rolled
  RS256 (`crypto/rsa` `SignPKCS1v15` + `x509.ParsePKCS1PrivateKey`), no `golang-jwt/jwt`;
  the code only signs, never verifies untrusted tokens (G-P1/G-P2 cross-check claims +
  signature).
- `parseAppPem` deliberately does **not** use `encoding/pem` alone: `pem.Decode`
  rejects the single-line PEMs Kotlin accepts (G-P2). String-normalize first, exactly
  the Kotlin steps. PKCS#8 input fails as today (unsupported, same FAILED-generic UX).
  Note `x509.ParsePKCS1PrivateKey` validates the full CRT key while Kotlin rebuilds
  from modulus+exponent only — a corrupted-CRT PEM diverges (theoretical, flag §16.10).
- No token caching (parity, §2.1.5): JWT + installation token minted per run.

### 4.3 GitHub API client

Unexported `githubClient` in the same package (D11 sibling-split only if seams prove
real — they don't: ~5 calls): `installationId`, `installationToken` (with the
`actions=write` gate), `dispatchWorkflow` (returns a small sum: success /
unsupportedInputs(names, body) / apiError(status, body) — D15 §7.13 sealed-class rule),
`listWorkflowRuns(perPage)`, `workflowRun(id)`, `workflowJobs(id)`. Headers per §2.3
(single `Content-Type: application/json` on dispatch — sanctioned delta §16.2);
redirects disabled; run/job DTOs are package-local (`umbrella §10.11`), `status`
validated against the three known values (unknown ⇒ error into the retry path, §2.5.2).
The 422 heuristic (`"Unexpected inputs provided"` + name containment) is a pure
function — keep-as-unit (§10).

### 4.4 Workflow inputs (the frozen §2.4 contract)

```go
// dispatchInputs builds the workflow_dispatch inputs map per omitRunObjectInput.
func dispatchInputs(run decryptedRun, impl meshapi.GithubImplementation) (map[string]string, error)
```

- Mode B: self-link + the token/endpoint rules of §2.4 — a pure table (keep-as-unit).
- Mode A: an explicit **outbound payload struct** mirroring the Kotlin field set
  (`kind`…`_links`, §2.4) with the implementation field typed as
  `struct{ Type string }` — structural omission replaces the Jackson mixin (umbrella
  §7.13). Marshal → base64. Values ride `json.Number`/raw fidelity (D8); parity with
  the Kotlin bytes is asserted at parsed-JSON level (null ≡ absent, 06A §16.4) plus the
  G-P10 leak assertions. Input decryption via `meshapi.DecryptInputs` (D3) — never
  `DecryptRunDetails` (umbrella §10.9); `AppPem` is decrypted separately into a local
  only used for signing.

### 4.5 Sync poller (06D-local workflow-run tracking)

Per D4 the poller is package-local (06C's ADO poller tracks *stages* on a timeline;
this one must first *find* the run, then track *jobs* — a shared abstraction would be
speculative, P3):

```go
// pollWorkflow: find the dispatched run (≤12×10s, created_at > trigger−30s among the
// 5 newest), then poll run+jobs every 10s ≤30min; feeds changed job steps into
// report.Report(RunStatus) (abort discarded) and the terminal update from the run
// conclusion (§2.5). No Observer/ticker; the handler owns job-step dedup.
func (h Handler) pollWorkflow(ctx context.Context, r report.Reporter, gc githubClient, impl meshapi.GithubImplementation, workflow string, triggerTime time.Time) error
```

- `seenJobs map[int64]bool` is local per invocation — no handler state (P4; plan-05 H
  concurrency observables hold trivially).
- Dedup/first-batch/trigger-step rules exactly §2.5.3 incl. the G-P4 re-report quirk.
- Error handling: find-phase and poll-phase errors log warn + retry within their
  budgets; timeout and not-found produce the §2.6 messages. **ctx cancellation
  (plan-05 H7 amendment, same as 06C):** the sync-polling GitHub handler, on
  `ctx.Done()`, reports the in-flight run with a **terminal** status before returning the
  ctx error — `ABORTED` (in the Go `ExecutionStatus` enum, defined terminal by
  meshStack's status source), falling back to `FAILED` if the endpoint rejects it,
  **never `SUCCEEDED`** — so the coordinator never sees a stale IN_PROGRESS. Persona
  graceful shutdown cancels run contexts, drains a **configurable grace period
  (default 120s)**, and emits clear shutdown logs.

### 4.6 Template fit-check (umbrella §6 review protocol — required for B–D)

| 06A/06B/06C artifact | 06D usage | Fit |
|---|---|---|
| `RunHandler`/`ClaimedRun` (plan 05 §4) | impl JSON from `Details`, `AppPem` via Decryptor, dual input modes and both poll loops inside `Execute` | **fits; no new `Execute` parameter** (confirms 06A §17 row 1) |
| `HandlerDeps` pattern | + `Decryptor`, `HTTP`, `Clock` — constructor-grown, shape unchanged | fits |
| `report.Reporter` (no Observer) | job-step batches + trigger-step-in-first-batch + terminal update fed through `Report(RunStatus)` (abort discarded) — caller-side dedup exactly as 06A §17 anticipated | fits |
| Lean `SourceUpdateDTO` | needs per-step `displayName`/`userMessage`/`systemMessage`/`status`, run-status-bearing batches — all present | fits |
| `ExternalCallError` (06B) | `MeshHttpException` twin for installation calls; needs `ResponseBody` + `RequestUrl` for the §2.6 message — present per 06A §4.4 contract | fits |
| `meshapi.DecryptInputs` (06B) | Mode A/B payloads; impl-secret asymmetry preserved (G-P10) | fits (signature was reviewed against 06D per umbrella §4 row 8) |
| `ClaimClassifier`, `SingleRunMode`, `BlockRunnerCompat`, `ResolvePrivateKey`, Dockerfile stage, removal recipe | verbatim reuse; `ResolvePrivateKey` consumes the baked dev key placement (umbrella §10.5) | fits |

No template deviation found ⇒ no STOP-D at plan level; re-run mechanically at §9 step 0.

## 5. Kotlin-isms → idiomatic Go (D15)

Umbrella §7.13 instantiated for every Kotlin-ism this module actually uses; parity
notes where the translation is not mechanical.

| Kotlin-ism (evidence) | Idiomatic Go replacement | Parity note |
|---|---|---|
| auth0 `JWT.create().sign(Algorithm.RSA256(...))` + BouncyCastle ASN.1 (`AppTokenFactory.kt:36-67`) | stdlib JWS + `x509.ParsePKCS1PrivateKey` (§4.2) | G-P1/G-P2 pin claims, signature and parsing tolerance; two JVM deps die |
| per-exception `try/catch` fan-out with three updateFailed… helpers (`GitHubBlockRunnerService.kt:43-149`) | returned error chains (`fmt.Errorf("obtaining installation token: %w", err)`); one `failRun(reporter, systemMessage)` funnel; `errors.As(&ExternalCallError{})` selects the §2.6 message shape | message strings byte-identical (§7.11); only plumbing changes |
| sealed `TriggerWorkflowResult` + `when` (`GithubClient.kt:33-39`) | small typed result value (§4.3) — no interface hierarchy | the three outcomes pinned by the existing trigger tests |
| Jackson mixin `@JsonIgnore` sanitization (`IgnoreBuildingBlockGithubImplementationMixin.kt`) | explicit outbound payload struct whose implementation field is `struct{ Type string }` (§4.4) | structural omission over annotation magic (umbrella §7.13); G-P10 leak pin |
| custom enum deserializers throwing on unknown status (`GithubClient.kt:41-79`) | named string types + a validating parse function returning an error | unknown status still lands in the poll-retry path (§2.5.2) |
| `Thread.sleep(10_000)` find/poll loops (`:251,255,281`) | `select` on injected `Clock` timer / `ctx.Done()` (§4.5) | constants pinned as constructor defaults; cancellation new-but-inert |
| `Instant`/`DateTimeFormatter.ISO_INSTANT` created-at comparison (`:245-247`) | `time.Parse(time.RFC3339, …)` | ISO_INSTANT ≡ RFC3339 for GitHub's timestamps; unparsable ⇒ error into the find-retry path (Kotlin: exception, same path) |
| mutable `seenJobIds` set threaded through calls (`:270,301,318`) | local `map[int64]bool` in `pollWorkflow` | no shared state (P4) |
| `Clock` bean default `Clock.systemUTC()` + unclocked `Instant.now()` in the token factory (`:24`, `AppTokenFactory.kt:31`) | one injected `Clock` used for JWT **and** polling | deliberate unification, test-visible only (flag §16.9) |
| Spring DI/profiles, `@ConfigurationProperties`, `ImmediateRetryDecorator`, `SingleShotRunner` | `cmd/github/main.go` wiring (own binary + `cmd/bbrunner` registration), `config.SingleRunMode`, `dispatch.Loop` + `Done()` wake — all 06A template artifacts | umbrella §4 rows 1–3 |
| kotlin-logging + MDC pattern (`application.yml:1-5`) | `log/slog` text handler, run id as attr | log format not a contract (umbrella §8) |
| `UrlSanitizerService` (Spring `@Service`) | unexported `sanitizeBaseUrl(string) (string, error)` in this package (umbrella §4 row 13) | behavior pinned by `GitHubClientFactoryTest` + `UrlSanitizerServiceTest` twins |
| companion constants (`STEP_ID`, `MAX_*`, input keys) | package-level typed consts (`StepId`, `inputKeyApiToken`, …) | frozen strings (umbrella §7.1) |
| Kotlin `Map` toString in the permission-gate message (`GithubClient.kt:196`) | deterministic `{k=v, k2=v2}` rendering in **JSON field order is not preserved by Go maps** ⇒ sorted keys | flagged byte-level delta in one system message (§16.5) |

## 6. Config

### 6.1 Persona config struct

```go
// github.Config — persona extras only; GitHub coordinates are per-run data (§2.7).
type Config struct {
    Uuid              string
    Api               config.Api
    PrivateKey        config.PrivateKeySource // via config.ResolvePrivateKey (06A §6.5)
    MaxConcurrentRuns int                     // new, default 1 (plan 05)
    Registration      *dispatch.RegistrationConfig // opt-in (plan 05 §9)
}
```

Validation (P5): `uuid` + `api.url` required in polling mode; a resolvable private key
required in polling mode (unlike manual — this runner decrypts `appPem` and sensitive
inputs; startup failure with the key-mismatch guidance wording, not first-run failure);
auth + key exempted in single-run mode (NoOp decryptor). **No github-specific keys** —
`ManualRunnerConfig`-style extras don't exist for this module (umbrella §3.1).

### 6.2 Alias table (umbrella §5.4 instantiated)

| Existing name | Evidence | Handling |
|---|---|---|
| env `RUNNER_UUID`, `RUNNER_API_URL`, `RUNNER_API_USERNAME`, `RUNNER_API_PASSWORD`, `RUNNER_API_CLIENT_ID`, `RUNNER_API_CLIENT_SECRET`, `VERSION` | `github-block-runner/src/main/resources/runner-config.yml:2-11` | `config.Env` bindings, identical to the manual persona (06A §6.2 incl. the `VERSION`-override rule) |
| env `RUNNER_PRIVATE_KEY_FILE`; yaml `blockrunner.privateKey`/`privateKeyFile`; default `/app/runner-private.pem` | `PrivateKeyLoader.kt:8-24`, `GitHubBlockRunnerCryptoConfiguration.kt:13` | `config.ResolvePrivateKey` (06A §6.5) — first-class consumer, full Kotlin resolution order incl. missing-file → inline fallback |
| env `PORT` (default 8102; image bakes 8080) | `application.yml:8`, `jvm.Dockerfile:18` | `MANAGEMENT_PORT` > `PORT` (deprecation-logged) > **8102** (D12); image keeps `ENV PORT=8080` |
| env `SPRING_PROFILES_ACTIVE=kubernetes` | `run-controller/runner-config.yml:144-147` | `config.SingleRunMode` alias (06A §6.3) |
| yaml `blockrunner.uuid`, `.version`, `.api.url`, `.auth.*` (kebab-case api-key), `.privateKey` | module `runner-config.yml` | `config.BlockRunnerCompat` block (06A §6.4); `debugMode` warn-and-ignore (manual-only key) |
| yaml `logging.*`, `server.*`, `spring.*` | `application.yml` | ignored-with-warning (umbrella §5.4) |
| blank api-key creds ⇒ Basic auth | `StandaloneBlockRunnerApiConfig.kt:35` | preserved via `config.Api.NewAuthProvider` (verified in 06A step 0) |

New, additive only: `MANAGEMENT_PORT`, `RUNNER_CONFIG_FILE`, `maxConcurrentRuns`/
`RUNNER_MAX_CONCURRENT_RUNS`, `registration:` — the uniform phase-6 set.

## 7. Persona wiring & modes

`cmd/github/main.go` (package main) is the github persona binary — it links only its
handler + the polling dispatcher and mirrors `cmd/manual/main.go` (06A §7); the handler
is also registered in the `cmd/bbrunner` superset (all personas by subcommand). No
argv[0] multiplexing. Only the deltas are listed:

- Identity `meshapi.Identity{Name: "github-block-runner"}` (registered in the
  `cmd/bbrunner` superset).
- Polling: `dispatch.NewLoop(LoopConfig{PollInterval: 10s, ClaimBackoff: 0,
  MaxConcurrent: cfg.MaxConcurrentRuns}, …)` + `dispatch.NewInProcess(map[…]{
  meshapi.RunnerTypeGitHubWorkflow: handler})`. The type key is the enum constant
  (`meshapi/dtos.go:278`; GITHUB_WORKFLOW maps identity through `ToRunnerType`,
  `:284-296` — umbrella §7.12). Shared `ClaimClassifier`, no 60s backoff.
- Decryptor wiring: cert-based `crypto.MeshCertBasedCrypto` from the resolved private
  key in polling mode; `meshapi.NoOpDecryptor` in single-run mode (controller
  pre-decrypted `appPem` + inputs — D7). Decrypt placement is handler-side (plan 05
  §16.2), so this is purely which `Decryptor` is injected.
- `mgmt.NewServer` on `config.ManagementPort(log, 8102, PORT-alias)` + `mgmt.RunMetrics`.
  Metrics classification per umbrella §7.2: async handover (IN_PROGRESS + nil return)
  counts **succeeded**; sync terminal keyed on reported status.
- Self-registration off by default; opt-in `registration:` with default capability
  `GITHUB_WORKFLOW`.
- Single-run mode: `config.SingleRunMode` → `RUN_JSON_FILE_PATH` → `ClaimedRun`
  (UseNumber, D8) → `handler.Execute` once, no listener, R12 exit rule (exit 0 iff a
  terminal or IN_PROGRESS-handover update was reported — covers async single-run,
  where the handover *is* the job's success; pre-report fetch/parse failures exit
  non-zero, the sanctioned §7.9 delta anchored by G-P11): Go single-run pods exit
  non-zero on pre-report fetch/parse failures where Kotlin exited 0; the old exit-0
  behavior stays pinned (G-P11) for audit.
- Node id = plain runner uuid; header set = shared-client set (umbrella §7.7, verified
  once in 06A).

## 8. Dockerfile & image switch

The 06A §8 template stage, instantiated:

- `containers/github-block-runner/Dockerfile` builds `./runner/cmd/github` as its own
  image: alpine (same digest pin), `ca-certificates bash`
  only (HTTP-only), uid 2000, the persona binary at `/app/github-block-runner`,
  `ENV PORT=8080`, `EXPOSE 8080`, **direct entrypoint**
  `ENTRYPOINT ["/app/github-block-runner"]` (no symlink, no argv[0] `entrypoint.sh`).
- `containers/github-block-runner/runner-config.yml`: effect-equivalent to §2.7's
  classpath defaults in flat Go keys — uuid `606f54c8-…`, api url
  `http://localhost:8302`, `bb-api`/`guest`, **plus the baked dev private key**
  placed consciously per umbrella §10.5 (local-dev pair of the meshfed magic-runner
  key; never a fallback when `RUNNER_PRIVATE_KEY_FILE` is set — `ResolvePrivateKey`
  order guarantees that).
- Published name/tags unchanged (`ghcr.io/meshcloud/github-block-runner:main` +
  release tags); deployed controller configs keep working via the baked
  `SPRING_PROFILES_ACTIVE: kubernetes` honor (§6.2).
- CI flip in the same PR as removal (§12): `ci.yml` github entries out of the JVM
  matrices, a `github-block-runner` image leg into `go-runners-image`. The leg builds
  `./runner/cmd/github` via `containers/github-block-runner/Dockerfile`;
  `build-images.yml:35-37` flips to `dockerfile: containers/github-block-runner/Dockerfile`
  (no `target:`), paired with a `go build ./runner/cmd/...` build-matrix leg
  `./runner/cmd/github`.
  Because 06D also deletes the now-empty JVM jobs, the flip and the §12 teardown are
  one motion here.
- JVM `command:`-override incompatibility: same wording as 06A §16.9 (accepted,
  umbrella §5.6).

## 9. Migration sequence

Always-green steps for one reviewable single-commit PR; after every step `task test` +
`task lint` + `task coverage` green, and `./gradlew check` green until step 10.

| # | Step | What changes | What proves it |
|---|---|---|---|
| 0 | **Preflight.** Umbrella A1–A12 + D1–D11 verifications on the phase-6c branch; branch `phase-6d-github`. Re-run the §4.6 fit-check mechanically; record `DecryptInputs`/`ExternalCallError` shapes vs §4.4/§4.3 needs. | nothing | STOP-A / STOP-D gate |
| 1 | **Kotlin pins (tests only).** §3.2 G-P1–G-P11 in `github-block-runner`; verify C-P1–C-P7 present (D9). | Kotlin test files only | `./gradlew :github-block-runner:check :block-runner-core:check` green; tests-only diff |
| 2 | **JWT + PEM.** `parseAppPem` + `appToken` (§4.2) with the G-P1/G-P2 unit twins (real test key fixture; verify with `rsa.VerifyPKCS1v15`). | `internal/github` | unit tables green; `-race` |
| 3 | **GitHub client.** `githubClient` (§4.3) + fake-GitHub transcript tests: the `GithubClientTest` twins (headers, paths, payload bytes, 422 heuristic table, permission gate). | `internal/github` | transcript suite green |
| 4 | **Inputs builder.** Outbound payload struct + `dispatchInputs` (§4.4): `BuildingBlockWorkflowInputsBuilderTest` twins + Mode-A parsed-JSON parity against the Kotlin byte fixture (`GithubBlockRunnerServiceTest.kt:155-260` expected JSON) + G-P10 leak test. | `internal/github` | unit + fixture-parity tests |
| 5 | **Handler + poller.** `NewHandler`/`Execute` + `pollWorkflow` (§4.1/§4.5): the scenario suite (§10.1) — run JSON in → fake meshStack + fake GitHub transcripts out, async/sync/error paths, fake clock driving find window and 30-min timeout. | `internal/github` | scenario suite matches the Kotlin pins |
| 6 | **Persona wiring.** `cmd/github/main.go` + register the handler in the `cmd/bbrunner` superset; mgmt 8102; single-run tail. | `runner/cmd/github/main.go`, `runner/cmd/bbrunner` | loop-wiring scenario; alias precedence (`MANAGEMENT_PORT`>`PORT`>8102); single-run scenario incl. R12/G-P11 twins |
| 7 | **Gate + tooling.** `thresholds.txt` += `internal/github 90` (no exclusions); depguard: `github` imports `dispatch`/`meshapi`/`report`/`config` + stdlib only. | `tools/coverage/*`, `.golangci.yml` | induced-failure check; `task coverage` |
| 8 | **Image.** Dockerfile stage + `containers/github-block-runner/runner-config.yml` (§8). | containers/ | `docker build --target github-block-runner` + healthz/claim-loop smoke |
| 9 | **Acceptance gate (§11).** Side-by-side transcripts + real-GitHub smoke. | — | STOP-E; evidence in the PR description |
| 10 | **Module removal.** Delete `github-block-runner/` + its CI legs per the 06A recipe (§12 steps 1–4). | module dir, workflows | CI green with the flipped image leg; `./gradlew :block-runner-core:check` still green |
| 11 | **JVM endgame.** Delete `block-runner-core/` and all Gradle/JVM machinery (§12 inventory); README/flake/.claude/.gitignore layout-forced edits; grep gates. | see §12 | full CI green (no JVM jobs left); `git grep -il gradle -- ':!PLAN_*' ':!CHANGELOG*'` clean per §12 gate |

Steps 10 and 11 are separate commits-in-PR so the module removal and the machinery
teardown are individually revertable during review, but they land in the same squashed
PR (umbrella §5.8).

## 10. Test plan & gate (D16)

### 10.1 Pin → Go mapping — N:1 into scenarios by design

Umbrella §5.2's worked consolidation rule was written for this suite; it is applied
row by row, not litigated per test.

| Kotlin pin/test (37 existing + G-P1–11) | Go destination | Kind |
|---|---|---|
| ServiceTest no-run / fetch-exception | loop scenario (shared classifier) — same test shape as 06A–C | scenario (consolidated) |
| ServiceTest happy trigger + async + `SensitiveSystemInputsIntegrationScenario` "…omitRunObjectInput is true/false" (2 of 6) | `Scenario_Github_AsyncRun_TriggersAndHandsOver` (Mode A) + `…_ModeB`: register → JWT'd installation calls → dispatch (payload bytes asserted) → one IN_PROGRESS update with SUCCEEDED `gh-trigger` step → no polling calls | scenario (≈6 mockk/wiremock tests → 2 scenarios) |
| ServiceTest decrypted-inputs+mixin + G-P10 | fixture assertions inside the Mode-A scenario (sensitive + typed inputs in one run JSON; leak assertions) | scenario |
| Sensitive scenario remaining 4 (token-only, none, endpoint ×2) + `BuildingBlockWorkflowInputsBuilderTest` (7) | `Test_DispatchInputs` table (Mode B rules incl. endpoint conditionality) — the scenario covers one row end-to-end | **keep-as-unit** (pure input→output table) + scenario spot-check |
| ServiceTest sync-poll + G-P3/G-P4/G-P5/G-P6 | `Scenario_Github_SyncRun_PollsJobsToCompletion` (+ `_FindRunTimeout`, `_PollTimeout`, `_AlreadyCompletedRun` variants): fake clock, fake GitHub returning staged run/job sequences; asserts every PATCH body (job-step ids, first-batch trigger step, re-report quirk, terminal update) | scenario |
| ServiceTest UnsupportedInput ×2 + Error + `GithubClientTest` 422/500/401 classification (5) | `Test_ClassifyDispatchResponse` table (status × body → outcome) + one FAILED-update scenario asserting the joined guidance message bytes | keep-as-unit (heuristic table) + scenario |
| ServiceTest wrong-impl ("deserializing") + G-P8 + G-P9 | `Scenario_Github_FailsBeforeTrigger` table-driven: wrong impl type / null destroyWorkflow / bad base URL / bad PEM — each asserts register-then-FAILED order and the §2.6 message | scenario (consolidated) |
| `GithubClientTest` wire-shape tests (dispatch/run/jobs/installation happy paths, 6) | fake-GitHub transcript assertions inside the scenarios + `githubClient` request-shape tests where a scenario doesn't reach the call | scenario/transcript |
| G-P7 permission gate | FAILED-update scenario variant + the message-format unit row (§16.5 delta) | scenario |
| G-P1/G-P2 JWT + PEM | `Test_AppToken` / `Test_ParseAppPem` tables | **keep-as-unit** (crypto/parsing — the umbrella's named candidate) |
| `GitHubClientFactoryTest` + `UrlSanitizerServiceTest` (core) | `Test_SanitizeBaseUrl` table | keep-as-unit |
| k8s startup scenario + G-P11 | `Scenario_Github_SingleRun_FileSource` (pre-decrypted run JSON, NoOp decryptor, captured wire) + exit-condition tests (report-failure ⇒ non-zero; fetch-failure ⇒ non-zero, the flagged §7.9 delta) | scenario |
| boot smoke | persona boot smoke (config-read stage) | smoke |
| C-P1–C-P7 (block-runner-core) | already ported in 06A (`meshapi`/`report` twins) — deleted with the core in step 11 | inherited |

Net: 48 Kotlin pins (37 existing + 11 G-pins) collapse into ~8 scenario families + 5
unit tables — the D16 consolidation outcome. No assertion changes shape beyond the two sanctioned deltas
(exit tightening G-P11; permission-map rendering §16.5) — anything further is STOP-B.

### 10.2 New Go-only tests

Ctx-cancellation mid-poll (reports a terminal `ABORTED` update, fallback `FAILED`, then
returns the ctx error — §4.5), unknown job/run status retry path, `json.Number`
fidelity through the Mode-A payload, single-run async handover exit 0, mgmt-on-8102
smoke. All hermetic (fake transport, fake clock).

### 10.3 Gate

`thresholds.txt` += `github.com/meshcloud/building-block-runner/runner/internal/github 90`;
**no exclusion entries** (whole package hermetic — fake HTTP for both meshStack and
GitHub). Shortfall ⇒ STOP-C (add scenario cases). `-race` on; the fake clock keeps the
poller deterministic. Keep-as-unit list is exactly: `Test_AppToken`, `Test_ParseAppPem`,
`Test_DispatchInputs`, `Test_ClassifyDispatchResponse`, `Test_SanitizeBaseUrl` — each a
real decision surface; nothing else is added to move the number.

## 11. Acceptance validation

No automated per-type acceptance exists for github (umbrella §5.7 finding) — the gate
before removal (step 9) is:

1. **Side-by-side transcript equivalence** (06A §11.3 procedure + delta allowlist
   verbatim): the same run JSONs (Mode A with sensitive/typed inputs; Mode B with both
   tokens + endpoint; sync with a multi-job workflow; DESTROY) driven through the
   Kotlin runner (wiremock GitHub + captured meshStack updates — the pin suite) and the
   Go handler (fake twins); diff empty modulo the sanctioned deltas (§13). The GitHub-
   side transcript comparison includes the dispatch payload **decoded** (base64 →
   parsed JSON) so Mode-A parity is checked semantically, not byte-wise.
2. **Manual smoke against real GitHub** (documented in the PR description): one
   GitHub-App-authenticated trigger each for async and sync against a real repo
   workflow, one `omitRunObjectInput: true` run, one GHE-style base-URL sanity check if
   an instance is available (else flagged as not exercised). This is the only leg that
   exercises real App-JWT acceptance by GitHub — the reason it is mandatory here and
   was optional-ish for gitlab/ado. GitHub has real coverage in the sibling
   `meshstack-smoke-tests` repo, so the port validates there before Kotlin module removal
   (in addition to the in-repo integration tests); real-GitHub / GHE coverage lives in
   `meshstack-smoke-tests`.
3. **k8s single-run smoke** against the built image (docker run, pre-decrypted run
   JSON + `SPRING_PROFILES_ACTIVE=kubernetes`): captured wire equal to the Kotlin k8s
   scenario behavior; exit 0.
4. **Outer net:** meshfed-release local-dev-stack + acceptance suite green (github
   claims via mux `:8302`, wire frozen — umbrella A11).

STOP-E: only after this gate do steps 10–11 land.

## 12. Kotlin module removal + Gradle shrink (the JVM endgame)

### 12.1 Module removal (umbrella §5.8 recipe, step 10)

1. `git rm -r github-block-runner/` (the §3.2 G-pins die with it; Go twins survive).
2. `settings.gradle`: drop `include 'github-block-runner'` (`settings.gradle:5`).
3. `ci.yml`: drop the github entries from `jvm-runners-ci` and `jvm-runners-image`;
   add the go image leg (§8).
4. `build-images.yml`: github leg → `dockerfile: containers/github-block-runner/Dockerfile`,
   build-matrix leg `./runner/cmd/github` (no `target:`, drop `runner-module:`).

### 12.2 JVM machinery teardown (step 11 — phase exit "Gradle build gone")

Complete inventory, verified against the tree @ `c3fce61` (paths already shrunk by
06A–C where noted):

| Delete | Evidence / note |
|---|---|
| `block-runner-core/` (main + tests incl. the C-P pins) | last consumer gone; C-pin twins live in `meshapi`/`report` since 06A (D9) |
| root `build.gradle` | plugin versions, spring-boot BOM, okhttp/auth0/bouncycastle constraints, ktlint DSL — all JVM-only |
| `settings.gradle` | after step 12.1 it holds only `rootProject.name` + the core include |
| `gradle.properties` | gradle daemon/caching flags |
| `gradle/wrapper/gradle-wrapper.jar`, `gradle/wrapper/gradle-wrapper.properties`, `gradlew`, `gradlew.bat` | the wrapper |
| `containers/jvm.Dockerfile`, `containers/entrypoint-jvm.sh` | no JVM image left to build |
| `.github/workflows/ci.yml`: the **whole** `jvm-runners-ci` and `jvm-runners-image` jobs (both matrices are empty after 12.1) | D14: Go job *structure* untouched — deleting jobs whose subject no longer exists is the plan-04 §10.8 layout-forced precedent |
| `flake.nix`: `pkgs.jdk21_headless`, `pkgsUnstable.ktlint` + the sync comment (`flake.nix:25,29-33`) | dev-shell JVM toolchain |
| `.claude/settings.json`: the `ktlint --format` hook (`settings.json:9`) | fires on `*.kt` writes — dead and warning-prone once no Kotlin exists |
| `.gitignore`: `.gradle`, `.kotlin/` entries and the JVM-era `build/` block (keep whatever the Go tree still needs — verify the tf/controller `!…/build/` negations are already gone with plan 04's module collapse) | hygiene, layout-forced |
| `README.md` **minimal factual edits only**: the "two separate build systems" bullet (`README.md:48-51`), the `./gradlew bootRun` port example (`:83`), the "Build and test (JVM)" section (`:106-110`), the `block-runner-core` module bullet | stale-path removal is layout-forced (grep gate below); the full docs overhaul stays **phase 7** (umbrella §2) |

**Grep gates (last commit):**
`grep -rn "gradle\|gradlew\|ktlint\|block-runner-core" --exclude-dir=.git .` — hits
allowed only in `PLAN_*` docs and CHANGELOG; `grep -rn "github-block-runner/" …` — only
image/persona *names* remain (workflows, containers/, run-controller sample, docs), no
module paths. `./gradlew` no longer exists to run — CI proves the world is Go-only.

**Explicitly not deleted here:** the Go CI job layout, release workflows, `cliff.toml`
(no JVM references found), meshfed docs beyond §15 — phase 7 (D14).

## 13. Frozen contracts touched

Umbrella §8 instantiated for GITHUB_WORKFLOW. **Preserved (pins → ported tests):**

- meshStack wire: claim/register/update per umbrella §8 (inherited via the 06A wire
  seam); step id `gh-trigger` + `"Trigger GitHub Action"`; job step ids
  `gh-workflow-job-<id>` + `"GitHub Job: <name>"`; every §2.6 message string; the
  IN_PROGRESS handover for `async: true` (D9) and the sync terminal mapping.
- **The workflow-inputs contract (§2.4)** — customer workflows parse it: input names
  `buildingBlockRun`/`buildingBlockRunUrl`/`MESHSTACK_API_TOKEN`/`MESHSTACK_RUN_TOKEN`/
  `MESHSTACK_ENDPOINT`, their presence rules, the base64-JSON encoding + field set +
  implementation-stripped-to-type, `ref` = implementation branch, APPLY/DETECT→apply,
  DESTROY→destroy selection, the 422 guidance UX.
- GitHub API usage: the five endpoints, `X-GitHub-Api-Version: 2022-11-28`,
  `application/vnd.github+json`, Bearer auth, JWT claims `{iat: now−10, exp: now+300,
  iss: appId}` RS256/PKCS#1, the `actions=write` gate, per-run `githubBaseUrl` (GHE),
  find-window (5 newest, −30s, 12×10s) and 10s/30min poll constants.
- k8s single-run contract incl. `SPRING_PROFILES_ACTIVE: kubernetes`; image name/tags;
  `ENV PORT=8080`/`EXPOSE 8080`; healthz `OK` on resolved legacy port 8102; all §6.2
  env/yaml names; mux GITHUB_WORKFLOW `:8302`.

**Sanctioned, flagged deltas (uniform umbrella wording + 06D-specific):** additive
client headers (§7.7); single-run exit tightening (§7.9, G-P11); no single-run listener
(§7.10); additive metrics/config; JVM `command:` override (§5.6/§8); null ≡ absent JSON
(06A §16.4 — applies to the meshStack PATCH bodies *and* the Mode-A payload, asserted
at parsed level); dropped stray dispatch `Content-Type` header (§16.2); deterministic
permission-map rendering in one system message (§16.5); slog format.

## 14. Rollback story

One squash commit ⇒ one `git revert` — but 06D's blast radius is the largest of phase
6: the revert restores the github module **and** `block-runner-core`, the root Gradle
build, wrapper, `jvm.Dockerfile`/`entrypoint-jvm.sh`, both JVM CI jobs (whose matrices
then again contain only core+github — consistent, since 06A–C's reverts are not
implied), the flake/ktlint/.gitignore/README edits, and deletes `internal/github`, `cmd/github/` +
its `cmd/bbrunner` registration, `containers/github-block-runner/` (Dockerfile + config),
the thresholds line and depguard rules. Wire/k8s/image contracts frozen (§13) ⇒ `:main`
floats back to a JVM-built github image on the next CI run; operator configs unchanged
in either direction (`SPRING_PROFILES_ACTIVE` honored by both generations,
`EXECUTION_MODE` never required). Release tags immutable. Lost on revert (documented
cost): the phase-6 additive config/metrics for this persona and the exit tightening.
Because the JVM teardown is the phase exit, a 06D revert also reverts the "Gradle build
gone" milestone — phase 7 (Go-only CI reshape) must not start while a 06D revert is on
the table (§5 phase-ordering rule).

## 15. Cross-repo touch points

Umbrella §9 subset for 06D:

- **meshfed-release `local-dev-stack/SKILL.md`:** no github-runner start block exists
  (umbrella §9 — the skill starts manual + terraform only); verify by grep at step 0
  and state "no edit" in the PR, or add an optional Go start snippet if the maintainers
  want one (sub-plan decision, not gate-relevant). The mux GITHUB_WORKFLOW `:8302`
  fan-out is read-only.
- **meshfed-release `how-to-run-building-block-runners.md`:** doc-truth check — if it
  documents `SPRING_PROFILES_ACTIVE` or `./gradlew` semantics for the github image, the
  minimal correction rides a lock-step PR; full pass is phase 7.
- **meshfed-release acceptance tests:** read-only outer net (§11.4); no per-type github
  acceptance exists (umbrella §10.2).
- **This repo, `run-controller/runner-config.yml` sample:** valid unchanged; optional
  `EXECUTION_MODE` comment as in 06A §15 (flip deferred to phase 7).
- **customer-facing GitHub workflow templates** (meshcloud `actions-register-source`,
  referenced verbatim in the §2.6 guidance text): read-only — the §2.4 contract freeze
  exists precisely so those workflows keep working; no edits anywhere.
- **terraform-provider-meshstack:** none (pattern source only, D3).

## 16. Flags + Open questions

Findings the umbrella / prior plans did not anticipate, plus reviewer-vetoable calls:

1. **Test-count erratum:** the github Kotlin suite has **37** tests (10+11+7+6+1+1+1),
   not "~30" (umbrella §3.3). No content impact; the §10.1 map covers all of them.
2. **The dispatch request carries a stray second `Content-Type` header** — the
   meshBuildingBlockRun HAL media type added next to the JSON body type
   (`GithubClient.kt:277`). GitHub ignores it; the Go port sends a single
   `application/json`. Sanctioned delta, pinned loosely (the wiremock pin matches the
   json content-type either way). Not a customer-visible surface.
3. **A mislabeled Kotlin test:** `processBlock reports error when deserializing a
   GitHub response fails` actually exercises the wrong-implementation-type path (the
   fixture carries a *Gitlab* implementation; the stubbed GitHub throw is unreachable).
   Ported as the wrong-impl scenario row; G-P9 adds the ordering assertion the name
   promised.
4. **The legacy Mode-A payload deliberately contains `runToken` and `_links`** — at
   first sight a secret leak, actually the legacy callback mechanism (workflows PATCH
   the run with it). The §7.6 leak pin (G-P10) therefore asserts the *impl* secrets'
   absence, not the runToken's. Recorded so nobody "fixes" it.
5. **Permission-gate message rendering:** the Kotlin message embeds a JVM
   `Map.toString()` (`{actions=read, …}` in JSON order); Go renders sorted
   deterministically. One byte-level delta inside one system message — flagged rather
   than silently absorbed by §7.11's byte-identical rule.
6. **Completed jobs are re-reported on every poll batch** (`:341-348`) — extra PATCH
   traffic, coordinator-tolerated. Pinned as-is (G-P4); fixing the dedup is a
   post-refactor follow-up, never this PR (D13 discipline).
7. **Correlation is heuristic and can mis-associate:** the find-run window picks the
   newest run created after trigger−30s — concurrent dispatches of the same workflow
   file (e.g. two runs of the same building block definition) can cross-track. Faithful
   port of an existing limitation; `maxConcurrentRuns > 1` makes it *more* likely, so
   the shipped default stays 1 and the config docs note it. No prior plan mentions it.
8. **Kotlin's PEM handling is not strict PEM** — single-line/whitespace-mangled PKCS#1
   keys parse today; a `pem.Decode`-only Go port would reject working customer keys.
   §4.2 replicates the tolerant normalization (G-P2 pins it). PKCS#8 stays unsupported
   (fails with the generic-error UX, as today).
9. **The JWT is minted from `Instant.now()`, not the injected clock**
   (`AppTokenFactory.kt:31` vs the service's `clock` param) — invisible in production,
   awkward for pinning. The Go port routes both through the one injected `Clock`;
   G-P1's Kotlin pin uses claim-window tolerance instead of exact equality. Test-only
   semantic difference, flagged.
10. **CRT-validation delta:** Kotlin rebuilds the RSA key from modulus+privateExponent
    (ignoring CRT params); `x509.ParsePKCS1PrivateKey` validates the full key. A PEM
    with corrupted CRT components signs fine on the JVM and errors in Go (same FAILED
    UX, different trigger point). Theoretical; recorded, not worked around.
11. **No token caching exists to port** — the umbrella's "installation token exchange +
    caching if any" resolves to *none* (fresh JWT + token per run). The Go port stays
    cache-free (parity + simplicity); adding caching would be a feature (out of scope,
    high-level §8).
12. **The JVM-endgame inventory is larger than the umbrella's list** (§5.8 names the
    Gradle/CI/Dockerfile items): also `flake.nix` jdk21/ktlint, the `.claude/settings.json`
    ktlint hook, `.gitignore` gradle entries, and minimal README factual edits — all
    layout-forced (§12.2); the README *overhaul* stays phase 7.
13. **Stdlib-JWT decision** (§4.2) — the App JWT is hand-rolled RS256 via `crypto/rsa`
    `SignPKCS1v15` over PKCS#1 parsing (`x509.ParsePKCS1PrivateKey`), with **no
    `golang-jwt/jwt` dependency**. The library only ever *signs* (it never verifies
    untrusted tokens; the G-P1/G-P2 pins cross-check the claims and the signature), so a
    claims/validation framework buys nothing.
14. **DETECT uses `applyWorkflow`** (`:97-101`) — worth stating because tf treats
    DETECT specially (saved-plan rules); for github it is simply "run the apply
    workflow". Frozen in §13.

**Open questions:** none.
