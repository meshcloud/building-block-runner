# Detail Plan 06 — Kotlin Ports Umbrella (Phase 6 consistency contract)

**Phase:** 6 (umbrella over 4 stacked PRs) · **Branches:** §6 · **Binding:** §3 P1–P8,
D5, D6 (Kotlin corollary), D7, D8, D9 (async IN_PROGRESS handover, k8s contract), D10,
D11 (`internal/manual`, `internal/gitlab`, `internal/azdevops`, `internal/github`),
D12 (ports 8101–8104), D14, **D15 (translation, not transliteration — §7.13)**, **D16
(scenario-first coverage — §5.2)** of `PLAN_HIGH_LEVEL.md`; plan 05 §17 promise set.

This umbrella owns **consistency** across the four per-runner ports. It contains the
cross-runner behavior inventory, the shared machinery map, and the template contract the
four sub-plans (`PLAN_DETAIL_06A_manual.md`, `06B_gitlab`, `06C_azdevops`, `06D_github`)
must satisfy. It deliberately contains **no per-runner porting detail** beyond what is
needed to check the sub-plans against each other. Kotlin references are `main` @
`c3fce61`; Go references marked *post-N* are shapes promised by plan N.

## 1. Assumptions from prior phases

Plans 00–05 are **not implemented yet**. Every sub-plan implementation begins by running
the verification steps of this table (umbrella assumptions) **plus its own**. Any
material failure is a **STOP**: update this umbrella and the affected sub-plans first,
get the revision reviewed, then resume.

| # | Assumption | Promised by | Verification step |
|---|---|---|---|
| A1 | `dispatch.RunHandler` is `Execute(ctx context.Context, run ClaimedRun) error` with `ClaimedRun{Id RunId, Type meshapi.RunnerImplementationType, Details meshapi.RunDetailsDTO, RawJson string}`; contract: handler decrypts per run, run-scoped reporting is runToken-only, handler owns its timeout, non-nil error = infrastructure failure (run-level FAILED is reported by the handler, which then returns nil). `InProcess` registry rejects `ALL`. | Plan 05 §4.2, §17 | read `internal/dispatch`; `grep -rn "Execute(ctx" internal/dispatch` |
| A2 | Loop policy knobs exist: `LoopConfig{PollInterval, ClaimBackoff, MaxConcurrent}`, injectable `ClaimClassifier`, `Done()` wake channel, `maxConcurrentRuns` config + `RUNNER_MAX_CONCURRENT_RUNS`. | Plan 05 §4.1/§6 | read `dispatch/loop.go`; run the loop cadence tests |
| A3 | The opt-in `registration:` config section (displayName, ownedByWorkspace, publicKey, capability) + startup PUT without WIF works for standalone personas; absent section ⇒ no registration traffic. | Plan 05 §9 | run the plan-05 step-8 registration transcript tests |
| A4 | `meshapi`: `RunClient` (claim POST `…/meshbuildingblockruns/create?forRunnerUuid=`, register-source, PATCH, artifact), per-run construction with runToken-only auth, `HttpError.IsNotFound/IsConflict`, `Identity`, `DecryptRunDetails(runJsonBase64, Decryptor)` with all five impl-type branches (input decryption + per-type impl secrets: `SshPrivateKey`, `AppPem`, `PipelineTriggerToken`, `PersonalAccessToken` — today `run-controller/controller/decryption.go:27-118`), `RunDetailsDTO.Links{Self, RegisterSource, UpdateSource, MeshstackBaseUrl}` (`go-meshapi-client/meshapi/dtos.go:19-24`). Claim POST and status PATCH are never retried. | Plan 03 §5.2, Plan 05 §5 | `grep -rn "DecryptRunDetails\|LinksDTO\|WhitelistedPosts" internal/meshapi` |
| A5 | `report`: `Progress`/`RunStatus`/`StepStatus` (value `Steps`), `Reporter{Register, Report}`, `ToStatusUpdate(s, source, type)`, `Observer` (10s ticker — **not used by these ports**, §4 row 7). | Plan 03 §5.4 | read `internal/report` |
| A6 | `config`: `Path`/`LoadFile`/`Env` mechanics, `Api` struct with `user`/`username` alias + `NewAuthProvider` (API key wins, `/api/login` exchange), `ManagementPort(log, def, aliases…)` with `MANAGEMENT_PORT > alias > default` precedence. | Plan 03 §5.3, Plan 04 §4.3 | read `internal/config`; run the alias-precedence tests |
| A7 | Persona wiring: adding a persona = add `cmd/<persona>/main.go` (wiring, linking only its deps) + register the handler in the `cmd/bbrunner` superset + one per-persona `containers/<persona>-block-runner/Dockerfile` (direct entrypoint to the persona binary) + one build-matrix leg (`./cmd/<persona>`); no `main.go` registry or argv[0] multiplexing. `mgmt.NewServer` (healthz `OK` + `/metrics`) and `mgmt.RunMetrics` (`runner_runs_claimed_total` etc., labeled `runner_uuid`) are reusable per persona; plan-05 additions `runner_runs_unhandled_total`, `runner_at_capacity_skips_total`. | Plan 04 §11, Plan 05 §10.3 | read `cmd/tf/main.go`, `cmd/bbrunner/main.go`, a per-persona `containers/*/Dockerfile`, `internal/mgmt` |
| A8 | Coverage gate mechanics: per-package lines in `tools/coverage/thresholds.txt` at 90, `exclusions.txt` per-file with justification, induced-failure check procedure. `-race` is ON. | Plan 00 §5.4, plan 02 §7, plan 04 §7.1 | `cat tools/coverage/thresholds.txt tools/coverage/exclusions.txt && task coverage` |
| A9 | `crypto.MeshCertBasedCrypto.DecryptMeshCertBased` implements RSA/OAEP-SHA1-MGF1 + AES-128-GCM with 4-byte IV-length prefix — the same algorithm as `MeshCertDecryptionService` (`block-runner-core/...security/MeshCertDecryptionService.kt:32-120`); parity is already proven in production (the controller decrypts what meshStack encrypts for all five types). | Current `main`, unchanged by 00–05 | run `crypto` tests; cross-decrypt one Kotlin-test fixture ciphertext (`MeshCertDecryptionServiceTest.kt`) with the Go crypto in a scratch test |
| A10 | The Kotlin modules, `containers/jvm.Dockerfile`, `entrypoint-jvm.sh`, the `jvm-runners-ci`/`jvm-runners-image` matrix legs (`.github/workflows/ci.yml:19-90`) and the four JVM legs in `build-images.yml:32-43` are untouched by phases 0–5. | Plans 00–05 scope | `git diff main..phase-5-dispatcher -- '*.gradle' containers/jvm.Dockerfile .github/workflows/` — empty for these paths |
| A11 | meshfed-release `local-dev-stack` still starts the **manual** runner via `./gradlew :manual-block-runner:bootRun` (SKILL.md:64-71) and the tf runner the post-04 way; mux ports MANUAL `:8301`, GITHUB_WORKFLOW `:8302`, GITLAB_PIPELINE `:8303`, AZURE_DEVOPS_PIPELINE `:8304` (SKILL.md:56; matching the modules' `runner-config.yml` defaults). | Plan 04 §9 + meshfed-release | read the SKILL.md sections |
| A12 | The shipped controller config dispatches the Kotlin images with `env: SPRING_PROFILES_ACTIVE: kubernetes` per type (`run-controller/runner-config.yml:138-157`) and the k8s Job env contract injects `RUN_JSON_FILE_PATH`, `RUNNER_UUID`, `RUNNER_API_URL` (frozen). | D9/D10, plan 03 goldens | read the config; controller Job-manifest goldens green |

**STOP-A (per sub-plan, before any coding):** any umbrella or sub-plan assumption
materially false ⇒ update the plans first.
**STOP-B (any time):** a Kotlin pin test written in the pinning step (§5.2) cannot be
ported truthfully to Go without changing its assertion ⇒ stop; either the pin found a
sanctioned delta (record it in the sub-plan's flag list, umbrella §7/§8 decide) or the
port is wrong.
**STOP-C (any time):** a gated package drops below 90 ⇒ add tests, never exclusions.
**STOP-D (06A only):** the template review against the §3 inventories (see §6) finds a
`RunHandler`/`ClaimedRun`/`registration:` shape that does not fit a later runner ⇒ the
fix is a reviewed revision of plan 05 §4 + this umbrella, never a 06A-local workaround.
**STOP-E (per sub-plan, last step):** the §5.7 validation gate fails ⇒ diagnose/replan
before merging; the Kotlin module is not removed until it passes.

## 2. Scope — umbrella vs sub-plan responsibilities

**The umbrella owns (and sub-plans must not re-decide):**

- The cross-runner behavior inventory (§3) and the block-runner-core→Go map (§4) —
  sub-plans cite them and add depth, they do not contradict them silently.
- The template contract (§5): section list, pinning workflow, handler shape, config
  alias rules, persona/port/Dockerfile pattern, removal sequence.
- Port order, branch names, the 06A-establishes/06B–D-review protocol (§6).
- Cross-runner consistency rules (§7): naming, metrics, error-UX parity, reporting
  cadence, secret hygiene, node-id policy, single-run activation and exit semantics.
- The frozen-contract register (§8) and the cross-repo map (§9).
- Resolution authority: a conflict between a sub-plan and this umbrella is resolved by
  revising the umbrella (reviewed), never by a sub-plan diverging quietly.

**Each sub-plan owns:**

- The full per-runner Kotlin behavior study (every branch of its service classes) and
  the concrete **Kotlin pin-test list** closing its §3.3 gaps.
- The Go handler design in its D11 package, its external-API client (GitLab/ADO/GitHub
  HTTP), its config struct and validation, its hermetic test suite (ported pins + the
  external-API fake), its Dockerfile stage content, its migration steps, rollback, and
  its module-removal diff.
- 06A additionally: the concrete template artifacts (§6) that B–D copy.

**Out of scope for all of phase 6 (destination):**

- Go-only CI reshape, deletion of Gradle *tooling* left after 06D, README/docs overhaul
  → **phase 7** (D14). Phase 6 deletes modules and their CI matrix legs (layout-forced,
  the plan-04 §10.8 precedent); the workflow *structure* stays.
- Mixed in-process/k8s dispatch in one process (plan 05 §16.1 deferral) — no phase-6
  persona needs it; revisit only if a sub-plan surfaces a need (STOP-D).
- Any meshStack/meshfed API change; the mux; new runner features (high-level §8).
- Changing tf/run-controller behavior in any way.

## 3. Cross-runner behavior inventory (from Kotlin sources)

### 3.1 Per-runner summary table

All four runners share the block-runner-core skeleton (§4): claim → `registerAsSource`
(one step) → do work → `updateBlockRun` (`SourceUpdate{status, steps}`) via a
runToken-authenticated client against the run's HAL links. Differences:

| | manual | gitlab | azure-devops | github |
|---|---|---|---|---|
| Module / image / persona | `manual-block-runner` | `gitlab-block-runner` | `azure-devops-block-runner` | `github-block-runner` |
| D11 package | `internal/manual` | `internal/gitlab` | `internal/azdevops` | `internal/github` |
| Impl type / runner capability | `MANUAL` | `GITLAB_CICD` / `GITLAB_PIPELINE` | `AZURE_DEVOPS` / `AZURE_DEVOPS_PIPELINE` | `GITHUB_WORKFLOW` |
| Service class(es) | `NoOpBlockRunnerService` (+`DebugBlockRunnerService`) | `GitLabBlockRunnerService`, `GitLabClient` | `AzureDevOpsBlockRunnerService`, `~Client`, `~PipelinePoller`, `~StatusUpdater`, `~StatusMapper` | `GithubBlockRunnerService`, `GithubClient`, `AppTokenFactory`, `BuildingBlockWorkflowInputsBuilder` |
| Step id / display name | `manual` / "Manual Block Run" (`NoOpBlockRunnerService.kt:26-28,71`) | `gl-trigger` / "Trigger GitLab CI/CD" (`GitLabBlockRunnerService.kt:30-33,130`) | `azure-devops-trigger` / "Trigger Azure DevOps Pipeline" (`AzureDevOpsBlockRunnerService.kt:30-33,75`) | `gh-trigger` / "Trigger GitHub Action" (`GitHubBlockRunnerService.kt:38-41,593`) |
| External calls | none | POST `{base}/api/v4/projects/{id}/trigger/pipeline` (multipart) (`GitLabClient.kt:52-63`) | POST `…/_apis/pipelines/{id}/runs?api-version=7.1`; GET run; GET `…/build/builds/{id}/timeline` (`AzureDevOpsClient.kt:60,91,118`) | GET installation, POST installation token, POST `workflow_dispatch`, GET runs/run/jobs (`GitHubClient.kt:157ff`) |
| External auth | — | trigger token as form field `token` | PAT (basic auth header) | App JWT (RS256, `iat=now-10`, `exp=now+300`, `iss=appId`, PKCS#1 PEM via BouncyCastle, `AppTokenFactory.kt:23-68`) → installation token |
| Async semantics | terminal `SUCCEEDED` immediately (sync; no handover) | **always async**: final update `status=IN_PROGRESS`, trigger step `SUCCEEDED` (`GitLabBlockRunnerService.kt:109-126`); no `async` field in the impl DTO | `impl.async`: true ⇒ IN_PROGRESS handover; false ⇒ poll ADO ≤30min @10s, stage steps `ado-stage-<id>`, then terminal (`AzureDevOpsPipelinePoller.kt:96-156`) | `impl.async`: true ⇒ IN_PROGRESS handover; false ⇒ find run (≤12×10s, 30s buffer) then poll ≤30min @10s, job steps `gh-workflow-job-<id>`, then terminal (`GitHubBlockRunnerService.kt:215-333,595-597`) |
| Secrets decrypted by runner (standalone) | none | `pipelineTriggerToken` + sensitive inputs (`GitLabBlockRunnerService.kt:53-56`) | `personalAccessToken` + sensitive inputs (`client/AzureDevOpsClientFactory.kt:19,23`) | `appPem` + sensitive inputs (`GitHubBlockRunnerService.kt:60,173`) |
| Decrypt fields in `decryption.go` (cross-check) | none (`:113-114`) | `PipelineTriggerToken` (`:86-92`) | `PersonalAccessToken` (`:102-108`) | `AppPem` (`:70-76`) |
| Runner-specific config keys | `blockrunner.debugMode` (`ManualRunnerConfig.kt:5-7`) | none beyond core | none beyond core | none beyond core |
| Spring `PORT` default / D12 `MANAGEMENT_PORT` default | 8104 | 8103 | 8101 | 8102 |
| Shipped runner uuid / mux port defaults | `d943b032…` / `:8301` | `bfe76555…` / `:8303` | `a9786b14…` / `:8304` | `606f54c8…` / `:8302` |
| Extra JVM deps to replace | — | — | — | `com.auth0:java-jwt`, `org.bouncycastle` (`github-block-runner/build.gradle:12-13`) |

### 3.2 Per-runner behavior detail (pin lists)

The sub-plans expand these into concrete test names; the umbrella records what must not
be lost. Everything here is coordinator- or external-system-visible.

**manual (06A):** outputs echo the run's inputs 1:1, keyed by input key, with type
mapping `FILE→STRING`, `LIST→CODE`, `SINGLE_SELECT→STRING`, `MULTI_SELECT→CODE`,
identity otherwise (`NoOpBlockRunnerService.kt:50-56,77-88`); sensitivity flag is
echoed; **no decryption happens** (the crypto placeholder provides an empty key,
`BlockRunnerApplication.kt:22-32`) — in standalone mode sensitive inputs are echoed as
*ciphertext* outputs, in k8s mode the controller has already decrypted them (pin both);
one update, terminal `SUCCEEDED`, single step `manual` `SUCCEEDED`; `debugMode` swaps in
`DebugBlockRunnerService` (3×5s IN_PROGRESS updates then random SUCCEEDED/FAILED —
dev-only; ported as behavior-equivalent debug mode, exact sleep cadence is not a
contract); fetch errors are caught ⇒ no run processed, no status reported
(`NoOpBlockRunnerService.kt:16-23`).

**gitlab (06B):** multipart trigger payload fields (`GitLabClient.kt:111-175`):
`token` (decrypted trigger token), `ref`, `variables[MESHSTACK_BEHAVIOR]`,
`variables[MESHSTACK_RUN]` = the full run JSON **with inputs decrypted but the
implementation's `pipelineTriggerToken` still encrypted** (only
`decryptBlockRunInputs` is applied to the payload, `GitLabBlockRunnerService.kt:56` —
secret-hygiene pin, §7.6), `variables[<key>]` for `isEnvironment` inputs,
`inputs[<key>]` for non-environment inputs, and callback URLs
`variables[MESHSTACK_SELF_URL|MESHSTACK_REGISTER_SOURCE_URL|MESHSTACK_UPDATE_SOURCE_URL|
MESHSTACK_BASE_URL]` from the run's HAL links (missing link ⇒ warn + omit). Error UX
(`GitLabClient.kt:69-107`, `GitLabBlockRunnerService.kt:73-107`): 404 ⇒ user "GitLab
pipeline could not be triggered successfully. Please contact support." + system "GitLab
reported 404, which can happen if you have entered a wrong projectId."; the
identity-verification error body ⇒ the dedicated token/verification message pair;
undeserializable error body ⇒ "There was a problem while communicating with GitLab.";
all failures reported as run `FAILED` + step `gl-trigger` `FAILED` with user "Could not
trigger the GitLab pipeline". Success: run `IN_PROGRESS`, step `SUCCEEDED`, user
"Triggered the configured GitLab pipeline", system "Triggered pipeline in project
'<id>'". Base URL is trailing-slash-sanitized (`UrlSanitizerService.kt:8-20`).

**azure-devops (06C):** trigger body `{templateParameters, resources?}` where
`templateParameters` = non-environment inputs stringified + `MESHSTACK_BEHAVIOR`, and
`resources.repositories.self.refName` only when `refName` set
(`AzureDevOpsClient.kt:59-74`); PAT decrypted, inputs decrypted for the client's run
copy (`AzureDevOpsClientFactory.kt:19,23`). Trigger success update: run `IN_PROGRESS`,
step `SUCCEEDED`, messages incl. "Polling for completion status..." (sync) vs "Will
wait for API updates on status..." (async) and the web URL
(`AzureDevOpsStatusUpdater.kt:72-97`). Sync polling: 10s interval, 30min timeout ⇒
FAILED "Pipeline polling timeout after 30 minutes"; per-poll timeline stages become
steps `ado-stage-<id>` ("Stage: <name>") with the `AzureDevOpsStatusMapper` state/result
mapping (`AzureDevOpsStatusMapper.kt:182-238`); stages re-reported only when new or
COMPLETED (`AzureDevOpsStatusUpdater.kt:118-171`); timeline fetch failure falls back to
run-state-only updates on state change; poll errors are retried, not fatal
(`AzureDevOpsPipelinePoller.kt:112-147`). Final: result SUCCEEDED ⇒ SUCCEEDED, anything
else (FAILED/CANCELED/unknown) ⇒ FAILED, with the mapper's user messages. Failure UX:
user "Could not trigger the Azure DevOps Pipeline" + system with request URL/status/body
(MeshHttpException) or "There was an internal error while trying to contact Azure
DevOps: <msg>".

**github (06D):** App-JWT mint (parameters in §3.1) → `GET /repos/{owner}/{repo}/installation`
→ `POST /app/installations/{id}/access_tokens` → `POST …/actions/workflows/{file}/dispatches`.
Workflow selection: APPLY/DETECT ⇒ `applyWorkflow`, DESTROY ⇒ `destroyWorkflow`; null ⇒
FAILED "Workflow file name must not be null" (`GitHubBlockRunnerService.kt:97-109`).
Dispatch inputs (`BuildingBlockWorkflowInputsBuilder.kt`): `omitRunObjectInput=true` ⇒
`buildingBlockRunUrl` (self link) + `MESHSTACK_API_TOKEN`/`MESHSTACK_RUN_TOKEN` if
present as inputs (decrypted) + `MESHSTACK_ENDPOINT` only when the API token is present;
`false` ⇒ `buildingBlockRun` = base64 JSON of the run **with the GitHub implementation
object stripped** via mixin (`IgnoreBuildingBlockGithubImplementationMixin`, secret
hygiene §7.6). The 422 unsupported-input heuristic and its four long guidance messages
(`GitHubBlockRunnerService.kt:196-201,505-556`) are user-facing UX — ported verbatim.
Sync polling: find the run created after `trigger−30s` among the 5 most recent (≤12
attempts @10s), else FAILED "Could not find the triggered workflow run after 12
attempts"; then poll run+jobs @10s ≤30min; job steps `gh-workflow-job-<id>`
("GitHub Job: <name>") with the status/conclusion mapping and message formats
(`GitHubBlockRunnerService.kt:335-417`); trigger step included only with the first job
batch; final status from conclusion (`success` ⇒ SUCCEEDED, else FAILED) with run-level
message (`:419-454`). Async: IN_PROGRESS handover after trigger.

### 3.3 Kotlin test coverage state (the D6 gap statement)

Per D6's corollary, each port **first** adds Kotlin tests where behavior is unpinned,
then ports pins + code together. Current state (test files/methods verified on `main`):

| Runner | Exists (keep + port) | Gaps (Kotlin pin tests to ADD first) |
|---|---|---|
| manual | `NoOpBlockRunnerServiceTest` (12 tests: no-run, fetch-exception, happy path, all 8 `toOutputType` mappings); `ManualRunnerKubernetesStartupScenario` (full k8s single-shot API interaction incl. captured register/update); `ManualRunnerStartupScenario` (context boot) | sensitive-input echo (ciphertext passthrough, standalone); `DebugBlockRunnerService` (untested entirely — pin update sequence/final statuses, not sleeps); k8s-mode exit codes (uncaught update error ⇒ exit 1; fetch error swallowed ⇒ exit 0, the §7.9 quirk) |
| gitlab | `GitLabBlockRunnerServiceTest` (5: error-deserialize, no-run, fetch-exception, happy trigger, decrypted-inputs+impl-asymmetry — `:128` is the secret-hygiene pin); `GitLabClientTest` (1: happy trigger); factory test; startup + k8s startup scenarios | multipart payload **field-by-field pin** (env vs non-env inputs, all 4 callback URLs, missing-link omission); 404 / identity-verification / generic error message pins against a fake GitLab; final-update `IN_PROGRESS` wire pin (the always-async handover, D9) |
| azure-devops | `AzureDevOpsBlockRunnerServiceTest` (3: fetch-throws, no-run, sync trigger+poll — thin); `AzureDevOpsClientTest` (4: trigger POST shape, refName resources, get run, get timeline); k8s startup scenario | **`AzureDevOpsPipelinePoller`, `AzureDevOpsStatusUpdater`, `AzureDevOpsStatusMapper` have zero direct tests** — pin: async handover update, stage-step emission/dedup (`ado-stage-*`), state/result→status mapping table, 30-min timeout message, poll-error resilience, timeline-fallback path, final result mapping; PAT decryption path; failure-UX message pins |
| github | Richest suite: `GithubBlockRunnerServiceTest` (10 incl. async, sync-poll, all 3 `TriggerWorkflowResult` outcomes), `GithubClientTest` (unsupported-input heuristics, error classes), `BuildingBlockWorkflowInputsBuilderTest` (7), `SensitiveSystemInputsIntegrationScenario` (6, wiremock end-to-end), k8s startup scenario | job-step emission details (`gh-workflow-job-<id>` ids, first-batch trigger-step inclusion, seen-job dedup); find-workflow-run timeout/buffer behavior; JWT claim pin (`iat/exp/iss`, RS256, PKCS#1 parsing) — currently only exercised indirectly via `TestAppTokenFactory` fixture |
| block-runner-core | Auth config scenarios, `ApiKeyAuthInterceptorTest` (8), `MeshCertDecryptionServiceTest` (8), `RunFileJsonBlockRunClientFetcherTest` (6), `ImmediateRetryDecoratorTest` (4), `UrlSanitizerServiceTest` (5), `AuthHttpClientFactoryTest` (8), k8s run-token config scenario | **`HttpBlockRunClient` and `MeshObjectApiBlockRunClientFetcher` have zero direct tests** — the claim/register/update *wire* (endpoints, media type, 404/409 handling, `{sourceId}` substitution, 409-register-tolerated, PATCH body JSON) is unpinned in Kotlin. Owner: **06A** adds these core pins (mockwebserver transcript style) since every port relies on them; 06B–D inherit |

## 4. block-runner-core mechanics map (Kotlin → Go shared packages)

Every row names the Go destination and, where the shared packages have a **gap**, the
owning sub-plan that must fill it (design agreed here, implementation there).

| # | Kotlin mechanism (evidence) | Go destination | Gap / owner |
|---|---|---|---|
| 1 | `BlockRunRequestScheduler` — Spring `@Scheduled(fixedRate=10000)` (`BlockRunRequestScheduler.kt:14`) + `ImmediateRetryDecorator` (immediately re-claims after a processed run, `ImmediateRetryDecorator.kt:16-25`) | `dispatch.Loop` with `PollInterval: 10s` + the `Done()` wake (immediate re-drain after completion) — cadence-equivalent by construction | none; pin in 06A loop-wiring tests |
| 2 | Claim-error policy: each service **catches all fetch exceptions, logs, treats as no-run** (`NoOpBlockRunnerService.kt:16-23` and twins) ⇒ next 10s tick, never a longer backoff; 404 and 409 ⇒ no-run (409 logs a warn, `MeshObjectApiBlockRunClientFetcher.kt:57-66`) | injected `ClaimClassifier`: 404 ⇒ no-run, 409 ⇒ no-run-logged, **any other error ⇒ no-run-logged + `runner_poll_errors_total`** with `ClaimBackoff: 0` (next tick) — deliberately *not* tf's 60s backoff (§7.3) | none; the classifier is a per-persona constructor arg (plan 05 A2) |
| 3 | `SingleShotRunner` + `RunFileJsonBlockRunClientFetcher`: profile `kubernetes` ⇒ one run from `RUN_JSON_FILE_PATH`, then exit; exit 0 on normal return, exit 1 on uncaught exception (`SingleShotRunner.kt:38-49`) | persona single-run mode reusing the handler directly (no loop), file source, NoOp decryptor, runToken-only reporting — the tf single-run pattern | **activation delta**: Kotlin single-run is triggered by `SPRING_PROFILES_ACTIVE=kubernetes` (operator config, A12), **not** `EXECUTION_MODE` — §7.8 rules; exit semantics §7.9. Owner: 06A |
| 4 | `MeshObjectApiBlockRunClientFetcher` — POST `api/meshobjects/meshbuildingblockruns/create?forRunnerUuid=<uuid>`, media type v1 both headers (`:35-45`) | `meshapi.RunClient.FetchRun` — **identical endpoint and media types** (`go-meshapi-client/meshapi/client.go:89-91,235-243`) | header delta: Kotlin sends only `X-Meshcloud-Runner-Version` (`AuthHttpClientFactory.kt:70-82`); Go adds `X-Block-Runner-Node-Id`, `X-Meshcloud-Runner-Name`, `User-Agent` — sanctioned additive (§7.7) |
| 5 | `HttpRunTokenRunClientFactory`/`HttpBlockRunClient` — per-run OkHttp with Bearer runToken **only**, URLs from HAL links, `updateSource` `{sourceId}`→runner uuid (`ActiveRunBasedUrlProvider.kt:15-25`) | per-run `meshapi.RunClient` with runToken-only auth (plan 05 §8) — same trust model, already the template pattern | URL derivation: Go constructs from base URL + run id (`EPRunSourceRegistration/Update`), Kotlin follows links. Path shapes are identical today (link hrefs = the EP templates, see the manual k8s scenario fixture `ManualRunnerKubernetesStartupScenario.kt:199-205`). Decision: **handlers use `Details.Links`** where the payload needs URLs (gitlab/github callbacks) and the shared client for API calls; 06A pins that both yield the same requests |
| 6 | `registerAsSource` — one step `{id, displayName, status: PENDING}`, source id = runner uuid, 409 ⇒ already-registered OK (`HttpBlockRunClient.kt:27-60`) | `RunClient.RegisterSource` with a one-step `RegistrationDTO` (409-tolerant, D9 pin) | none |
| 7 | `updateBlockRun` — PATCH `SourceUpdate{status, steps[{id, displayName?, userMessage?, systemMessage?, outputs?, status?}]}`; response body **ignored — abort flag deliberately not honored** (`HttpBlockRunClient.kt:62-88`); updates are **event-driven** (one per state change), no ticker | `report.Progress` + `Reporter` + `ToStatusUpdate` for the body mapping, **without `report.Observer`**: handlers report on events, never on a 10s ticker, and do not react to the abort flag | **gap: event-driven reporting seam** (a thin per-run "step reporter" over `Reporter`, no ticker) + the PATCH-body superset decision §7.4 — owner: 06A |
| 8 | `DecryptionService`: `decrypt(secret)` + `decryptBlockRunInputs(run)` (inputs only — STRING/CODE/FILE, others logged + left as-is, `MeshCertDecryptionService.kt:58-97`); k8s profile ⇒ `NoOpDecryptionService` | `crypto.MeshCertBasedCrypto` (algorithm parity A9) behind the shared `meshapi.Decryptor` port; single-run mode uses the NoOp decryptor (controller already decrypted) | **gap: input-only decryption helper** — `meshapi.DecryptRunDetails` decrypts inputs *and* impl secrets; gitlab/github payload construction needs the Kotlin asymmetry (§7.6). Add `meshapi.DecryptInputs` (same branch rules incl. the non-STRING/CODE/FILE skip) — owner: **06B** (first consumer), signature reviewed against 06D's needs |
| 9 | `PrivateKeyLoader`: env `RUNNER_PRIVATE_KEY_FILE` > yaml `privateKeyFile` > default `/app/runner-private.pem`, falling back to inline `privateKey` (`PrivateKeyLoader.kt:8-24`) | `config` key + env binding (tf already binds `RUNNER_PRIVATE_KEY_FILE`, plan 03 §5.3) | gap: the `/app/runner-private.pem` default-path fallback and inline-vs-file precedence differ from tf's — 06A specifies one resolution order covering both (alias-compat per D7) |
| 10 | `AuthHttpClientFactory` + `ApiKeyAuthInterceptor` (POST `/api/login`, cached Bearer, 30s expiry buffer, `ApiKeyAuthInterceptor.kt:71-147`) / `BasicAuthInterceptor` (deprecated) | `meshapi.ApiKeyAuth` (same login flow) / `BasicAuth`; `config.Api.NewAuthProvider` precedence (API key wins) matches `AuthHttpClientFactory.kt:46-68` | parity check only (expiry-buffer semantics) — 06A verification step |
| 11 | `StandaloneBlockRunnerApiConfig`/`BlockRunnerApiConfig`/`BlockRunnerPrivateKeyProperties` — Spring props under `blockrunner.*` with kebab-case `api-key.client-id` (`runner-config.yml` of each module) | persona config structs over `config` mechanics | **gap: `blockrunner:`-prefixed yaml compat** (customers mount their own runner-config.yml against the published images) — §5.4 alias table; owner: 06A |
| 12 | `HealthController` `/healthz` → "OK" on Spring `PORT` (8101–8104) | `mgmt.NewServer` on `MANAGEMENT_PORT`, per-persona defaults 8104/8103/8101/8102, `PORT` alias (plan 04 §4.3 mechanics) | none; note single-run listener delta §7.10 |
| 13 | `UrlSanitizerService` (trim + drop trailing slash, error on empty) | a tiny unexported helper where consumed (gitlab/github/azdevops packages) — no shared package for 6 lines (P3) | sub-plan-local; behavior pinned by existing Kotlin tests |
| 14 | `MeshHttpException{userMessage, systemMessage?, statusCode, requestUrl, responseBody}` — carries the user/system split into step updates | per-package typed error with the same fields (external-API error), mapped into step `userMessage`/`systemMessage` exactly as today | 06A defines the shape (gitlab/azdevops/github reuse); messages themselves are per-runner pins (§3.2) |
| 15 | `ImmediateRetryDecorator`, `RequestLoggingUtility` (MDC request ids), Spring profiles/scheduling config, `MeshException`, `MeshObjectApiObjectMapper` | dissolved: loop wake (row 1), per-run slog attrs (plan 05 H3 observable, §10.12), persona wiring, plain Go errors, `encoding/json` | none — deletions, no port |
| 16 | `AppTokenFactory` (GitHub App JWT via auth0-jwt + BouncyCastle PKCS#1) | `internal/github`: stdlib `x509.ParsePKCS1PrivateKey` + hand-rolled RS256 JWT via `crypto/rsa` `SignPKCS1v15` (header/payload/sign ≈ 40 lines) — **no new JWT dependency** (P2; the meshfed mux stdlib bar; the code only signs, never verifies untrusted tokens, so `golang-jwt/jwt` buys nothing) | owner: 06D |

## 5. The template contract (every sub-plan must satisfy this)

### 5.1 Mandatory section list per sub-plan

Each sub-plan is one stacked single-commit PR and must contain exactly these sections
(order fixed, so cross-review is mechanical):

1. **Assumptions from prior phases** — umbrella §1 verifications + 06A-template
   verifications (for B–D) + its own; STOP markers per §1.
2. **Kotlin behavior inventory** — the full study of its module (deepening §3.2), every
   coordinator/external-visible behavior listed with file:line.
3. **Kotlin pin tests (tests-first step)** — closing its §3.3 gap column (§5.2).
4. **Go handler design** — package, types, deps (§5.3); illustrative signatures only.
5. **Kotlin-isms → idiomatic Go (D15)** — the runner's transformation table: every
   Kotlin-ism its code uses (exceptions, Spring annotations/profiles, Jackson
   mixins/mappers, OkHttp interceptors, `Thread.sleep` loops, companion objects, MDC
   logging, …) with the idiomatic Go replacement per the §7.13 rules. Semantic-parity
   note per row where the translation is not mechanical.
6. **Config** — persona struct + the full §5.4 alias table instantiated for the runner.
7. **Persona wiring & modes** — `cmd/<persona>/main.go` wiring + `cmd/bbrunner` superset
   registration (not a `main.go` registry entry), `MANAGEMENT_PORT`, registration
   section, single-run activation (§5.5).
8. **Dockerfile & image switch** (§5.6).
9. **Migration sequence** — always-green steps sized for one reviewable PR, each with
   "what proves it"; Gradle CI stays green until the removal step.
10. **Test plan & gate (D16)** — scenario-first: the pin→Go mapping per §5.2 (N:1
    consolidation into scenario transcripts is the norm), the keep-as-unit list with
    its decision-surface justification per §5.2, the external-API fake suite, package
    joins `thresholds.txt` at 90 **via scenario coverage** — a unit test added solely
    to reach the number is a review reject.
11. **Acceptance validation** (§5.7) — the gate before Kotlin removal.
12. **Kotlin module removal + Gradle shrink** (§5.8).
13. **Frozen contracts touched** — instantiating §8 for the runner.
14. **Rollback story** (§5.9).
15. **Cross-repo touch points** (§9 subset).
16. **Flags** + **Open questions** (empty is the goal).

### 5.2 Kotlin-tests-first pinning & scenario-first porting (D6 corollary + D16)

**Pinning (Kotlin side):**

- The pinning step is **tests-only in the Kotlin module** (`git diff -- ':!*_test*'`
  empty for it) and lands as the first commits of the same PR; the existing
  `jvm-runners-ci` leg proves them green before any Go code exists.
- Pins target **observable behavior** and are authored **at scenario level where
  possible** (D16): captured meshStack API interactions (the
  `ManualRunnerKubernetesStartupScenario` capture style or mockwebserver/wiremock
  transcripts) and captured external-system requests (wiremock, already used by
  gitlab/github/azdevops). No pins on Kotlin internals that have no Go counterpart.
- Bugs/quirks found while pinning are pinned as-is and listed in the sub-plan's flag
  section (the D13 discipline; phase 6 has no bug-fix pass — fixes are follow-ups after
  the port, never inside it).
- 06A additionally writes the **block-runner-core wire pins** (§3.3 last row); B–D
  verify they exist instead of re-writing them.

**Porting (Go side, D16 — scenario-first):**

- Parity is **semantic, not structural** (D15): every pin carries a stable name and the
  sub-plan's test plan maps each Kotlin pin to the Go test that preserves its asserted
  *behavior* (same inputs, same observable bytes/fields on the wire) — but the mapping
  is **N:1 by design**: Kotlin unit tests that merely restate structure through mocks
  ("register called once with step id X", "update called with SUCCEEDED") consolidate
  into one Go **scenario test** in the house harness style (run JSON in → fake meshStack
  + fake external-API transcript out, black-box through `Handler.Execute`). An
  assertion whose *behavior* cannot be preserved is STOP-B; a test whose *shape*
  disappears into a scenario is the intended outcome, not a loss.
- **Keep-as-unit criterion:** a ported test stays (or becomes) a unit test only where
  the unit has real decision surface — pure input→output tables and parsers: the manual
  `toOutputType` mapping, the ADO `StatusMapper` state/result table, the github
  `InputsBuilder` variants and unsupported-input classification, JWT/PKCS#1 parsing,
  crypto, config alias resolution. Rule of thumb for the github suite (~30 tests): the
  wiremock scenarios (`SensitiveSystemInputsIntegrationScenario`, the service-level
  trigger/poll tests) map to Go scenario transcripts; `GithubClientTest`'s
  error-classification cases and `BuildingBlockWorkflowInputsBuilderTest` stay
  unit-level; `GithubBlockRunnerServiceTest`'s mockk-verification tests consolidate
  into the scenarios. Sub-plans apply this rule, not per-test litigation.
- Existing **meaningful** tests (Kotlin or Go) are kept or transformed, never
  discarded; nobody adds unit tests just to move the coverage number — the 90% gate is
  reached through the scenario suites (STOP-C's "add tests" means scenario cases
  first).

### 5.3 RunHandler implementation shape

Uniform across all four (deviations = STOP-D):

```go
// package internal/<runner> (D11); consumer-side deps, main wires (P3).
func NewHandler(cfg Config, deps HandlerDeps) Handler   // value type, P4
func (h Handler) Execute(ctx context.Context, run dispatch.ClaimedRun) error
```

- `HandlerDeps` carries: a per-run `RunClient` factory (base URL + runToken from
  `run.Details.Spec.RunToken`; claim credentials never reach the handler), the
  `meshapi.Decryptor` (cert-based in polling mode, NoOp in single-run mode — decrypt
  placement is handler-side, plan 05 §16.2), the external-API HTTP client seam
  (fakeable), a `Clock`, and a `*slog.Logger` (D15) — per-run identification via
  `logger.With("run", run.Id)`, satisfying the plan-05 H3 per-run-isolation observable
  as an attribute rather than a `[RUN-<id>]` prefix (reconciliation flag §10.12).
- Execution skeleton = the Kotlin skeleton: register one step → do work → event-driven
  updates (§4 row 7) → terminal or IN_PROGRESS-handover update. A failure that was
  reported as run `FAILED` returns `nil` (A1 contract); only claim/report transport
  failures return errors.
- Sync pollers (azdevops, github) respect `ctx` cancellation between poll sleeps (the
  Kotlin `Thread.sleep` loops become clock/ticker waits) — same 10s/30min constants,
  pinned as constructor defaults like the tf engine's.
- External-API clients live in the same package as unexported types unless the package
  grows past cohesion (D11: sibling split only if seams prove real).
- Coverage: the whole package is hermetically testable (fake meshStack transport + fake
  external API) and is covered **scenario-first** (D16, §5.2); **no exclusion-list
  entries** are expected for any phase-6 package.

### 5.4 Config section + env/yaml alias table

Persona config = shared `config.Api` + `uuid` + persona extras. **Every existing env
var and yaml key keeps working (D7).** The compat matrix each sub-plan instantiates:

| Existing name (Kotlin) | Evidence | Phase-6 handling |
|---|---|---|
| env `RUNNER_UUID`, `RUNNER_API_URL`, `RUNNER_API_USERNAME`, `RUNNER_API_PASSWORD`, `RUNNER_API_CLIENT_ID`, `RUNNER_API_CLIENT_SECRET`, `VERSION` | `*/src/main/resources/runner-config.yml` placeholders | same names, bound via `config.Env` — identical to the tf persona's bindings |
| env `RUNNER_PRIVATE_KEY_FILE`; yaml `blockrunner.privateKey` / `blockrunner.privateKeyFile`; default `/app/runner-private.pem` | `PrivateKeyLoader.kt:8-24` | env > file key > default path > inline key — one resolution order defined in 06A (§4 row 9), deprecation-logged where it diverges from tf's key names |
| env `PORT` (Spring `server.port`, defaults 8101–8104; images bake `PORT=8080`) | `*/application.yml:8`, `jvm.Dockerfile:19` | `MANAGEMENT_PORT` with `PORT` alias (deprecation-logged once) — plan-04 tf mechanics reused verbatim; images keep `ENV PORT=8080`, never bake `MANAGEMENT_PORT` (plan 04 §10.7 lesson) |
| env `SPRING_PROFILES_ACTIVE=kubernetes` | operator job templates, `run-controller/runner-config.yml:142-157` | honored as single-run trigger (§7.8), deprecation-logged; `EXECUTION_MODE=single-run` also accepted (Go convention) |
| yaml `blockrunner.uuid`, `blockrunner.api.url`, `blockrunner.auth.username/password`, `blockrunner.auth.api-key.client-id/client-secret` (kebab-case), `blockrunner.debugMode` (manual only), `blockrunner.version` | module `runner-config.yml`s, `StandaloneBlockRunnerApiConfig.kt`, `ManualRunnerConfig.kt` | the persona loader accepts **both** the Go-native flat keys (`api:`, `uuid:` — tf/controller style) and a `blockrunner:` compat block normalized after load (deprecation-logged). Customers mounting their existing yaml onto the published image keep working. Spring *relaxed-binding* variants beyond these literal spellings (e.g. `BLOCKRUNNER_UUID`) are **not** carried; startup fails fast when an unconsumed `BLOCKRUNNER_*`-prefixed env var is present (§10.4 RULED) |
| yaml `logging.*`, `server.*`, `spring.*` blocks | Spring framework config | ignored-with-warning when present in a mounted file (never an error — a mounted Kotlin-era file must still boot the persona) |

New config (additive only): `maxConcurrentRuns` + `registration:` (plan 05 shapes),
`RUNNER_CONFIG_FILE` (shared loader). Config layering: a shared
top-level base `runner-config.yml` holds the cross-persona defaults (incl. the
gitlab/azdevops/github well-known dev private key — §10.5), and each per-impl
`containers/<persona>/runner-config.yml` **deep-merges over** it, env last
(base < per-impl < env). Defaults stay byte-equivalent in effect to the shipped module
`runner-config.yml`s.

### 5.5 Persona wiring + MANAGEMENT_PORT + single-run mode

Per persona (names are the published image names, D8):

| Persona | Registry entry / Identity name | `MANAGEMENT_PORT` default (D12) | Polling mode | Single-run mode |
|---|---|---|---|---|
| `manual-block-runner` | `manual-block-runner` | **8104** | `dispatch.Loop` + `InProcess{MANUAL: handler}`, `PollInterval` 10s, `ClaimBackoff` 0, `maxConcurrentRuns` default 3 | handler direct, NoOp decryptor |
| `gitlab-block-runner` | `gitlab-block-runner` | **8103** | same, `{GITLAB_PIPELINE: handler}` | same |
| `azure-devops-block-runner` | `azure-devops-block-runner` | **8101** | same, `{AZURE_DEVOPS_PIPELINE: handler}` | same |
| `github-block-runner` | `github-block-runner` | **8102** | same, `{GITHUB_WORKFLOW: handler}` | same |

- Wiring lives in `cmd/<persona>/main.go` (package main) — one **binary** per
  persona, mirroring `cmd/tf`; the handler is **also** registered in the
  `cmd/bbrunner` superset; only main wires adapters (D11 depguard). The "Registry
  entry" column above = the persona's own `cmd/<persona>/main.go` binary (no
  `main.go` registry / argv[0] switch).
- Node id (`X-Block-Runner-Node-Id`): the plain runner uuid (no `-worker-N` suffix —
  that is tf history, plan 05 §16.5). New header for these runner types (§7.7).
- `mgmt.NewServer` + `mgmt.RunMetrics` wired exactly as the tf persona (plan 04 §4.3);
  single-run mode runs **no listener** (§7.10).
- Self-registration: off by default (parity — Kotlin runners never self-register; the
  runner object is pre-created); the plan-05 `registration:` section is available
  opt-in, capability = the persona's concrete type by default.
- Single-run activation: `EXECUTION_MODE=single-run` **or** `SPRING_PROFILES_ACTIVE`
  containing `kubernetes` (§7.8); reads `RUN_JSON_FILE_PATH`; runToken-only reporting;
  exit semantics §7.9.

### 5.6 Dockerfile + image switch

- Each port adds one per-persona `containers/<persona>-block-runner/Dockerfile` building
  only its own binary (`go build ./cmd/<persona>`): alpine base (same digest pin),
  `ca-certificates bash` only (these runners are HTTP-only — no git/tofu/nix), meshcloud
  uid 2000, the built `./cmd/<persona>` binary copied in directly at
  `/app/<persona-name>` (its own binary — no shared `bbrunner`, no symlink), a **direct**
  `ENTRYPOINT ["/app/entrypoint.sh", "/app/<persona-name>"]` (no argv[0] multiplexing),
  config at `/app/runner-config.yml` from `containers/<persona>-block-runner/`,
  `ENV PORT=8080`, `EXPOSE 8080` (parity with `jvm.Dockerfile:19-20`).
- Published image name and tag scheme unchanged (`ghcr.io/meshcloud/<module>:main` +
  release tags) — operators' controller configs keep working without edits because the
  new image honors their baked `SPRING_PROFILES_ACTIVE` env (§7.8).
- CI: the runner's `jvm-runners-image`/`build-images.yml` leg flips from
  `containers/jvm.Dockerfile` + `RUNNER_MODULE` to
  `dockerfile: containers/<persona>-block-runner/Dockerfile` (a per-persona Dockerfile,
  no shared `target:` stage) **in the same PR** that removes the module (§5.8) — image
  builds stay green at every commit.
- Explicit non-goal: no `java`-shaped compat. The JVM entrypoint was
  `["/app/entrypoint.sh","java","-jar","/app/executable"]` (`jvm.Dockerfile:28`);
  operators overriding `command:` with java arguments break — documented in the
  sub-plan's flag list (the entrypoint targets the persona binary directly, and no
  per-persona binary aliases `java`), judged acceptable because the shipped controller
  config uses the default entrypoint (A12).

### 5.7 Acceptance validation

Research finding the high-level plan glossed over: meshfed-release has **no per-type
runner acceptance tests** for gitlab/azure-devops/github. The acceptance-testing skill
(`meshfed-release/.agents/skills/acceptance-testing/SKILL.md`) covers coordinator/
meshObject API tests; the `local-dev-stack` skill starts only the **manual + terraform**
runners (SKILL.md description + lines 43-83), and the meshObjects acceptance scenarios
exercise the run lifecycle through whatever runner answers the mux. Therefore the
per-port validation gate is:

| Runner | Gate before Kotlin removal |
|---|---|
| manual | local-dev-stack flow with the **Go** manual persona replacing the gradle bootRun (lock-step SKILL edit, §9) + ≥1 MANUAL acceptance run green + the k8s single-run smoke (run JSON file → captured wire identical to the `ManualRunnerKubernetesStartupScenario` transcript) |
| github | `github` (like `manual` and `tf`) has real end-to-end coverage via the sibling **`meshstack-smoke-test`** harness, which runs `tofu test` e2e modules against a live meshStack: `terraform` (tf) is covered by a module in the smoke-test repo itself, while the **`github_workflows`** (github) and `manual` e2e modules live in the `meshstack-hub` repo and are discovered and executed by the harness (both repos are on its discovery path). github/tf/manual coverage is thus real but split across `meshstack-smoke-test` + `meshstack-hub`; the github port validates there before Kotlin module removal. Plus the hermetic **side-by-side transcript comparison**: the same run JSON driven through the Kotlin runner (wiremock external API + captured meshStack updates — the pin suite) and through the Go handler (fake transport twins); transcripts must match modulo the sanctioned deltas of §7 |
| gitlab / azure-devops | These two have **no smoke tests** (accepted shortcoming; commissioning new meshfed-release acceptance tests for them is out of scope). Deletion leans entirely on the in-repo integration/transcript tests: the hermetic **side-by-side transcript comparison** (same run JSON through the Kotlin pin suite vs the Go handler, matching modulo the §7 deltas). A documented manual smoke against a real GitLab/ADO target (one trigger each, async + sync where applicable) is best-effort PR evidence, not a gate (§10.2) |
| all | `local-dev-stack` + acceptance suite still green as the outer regression net (the runner under port claims from its mux port per A11) |

### 5.8 Kotlin module removal + Gradle shrink

The per-PR removal recipe (each sub-plan repeats it; steps are the *last* commits of
the PR, after §5.7 passes):

1. Delete the module directory (`<module>/`).
2. `settings.gradle`: drop its `include` line (`settings.gradle:3-7`).
3. `.github/workflows/ci.yml`: drop its `jvm-runners-ci` matrix entry (`:26-37`) and
   its `jvm-runners-image` matrix entry (`:64-73`).
4. `.github/workflows/build-images.yml`: replace its JVM leg (`:32-43`) with the
   per-persona `containers/<persona>-block-runner/Dockerfile` leg (§5.6).
5. meshfed-release lock-step doc edits where named (§9).
6. Grep gate: no reference to the module path remains outside CHANGELOG/plan docs.

**06D additionally** (phase exit "Gradle build gone", high-level §5):
`block-runner-core/`, root `build.gradle`, `settings.gradle`, `gradle/`, `gradlew`,
`gradlew.bat`, `gradle.properties`, `containers/jvm.Dockerfile`,
`containers/entrypoint-jvm.sh`, the whole `jvm-runners-ci`/`jvm-runners-image` jobs and
any ktlint wiring. CI job *structure* for Go stays untouched (D14 — the phase-7
boundary); deleting jobs whose subject no longer exists is layout-forced, the plan-04
§10.8 precedent.

### 5.9 Rollback story requirements

Each sub-plan documents: one squash commit ⇒ one `git revert` restores the Kotlin
module, its Gradle/CI legs, and the JVM image build; the persona's `cmd/<persona>/main.go`
binary + its `cmd/bbrunner` superset registration (not a `main.go` registry entry),
handler package, per-persona Dockerfile and thresholds lines disappear. Because image names
and the k8s/wire contracts are frozen (§8), `:main` floats back to a JVM-built image on
the next CI run and **deployed operator configs need no change in either direction**
(the `SPRING_PROFILES_ACTIVE` env is honored by both generations; `EXECUTION_MODE` must
therefore never become *required*). Release tags are immutable. Additive config
(`maxConcurrentRuns`, `registration:`, `MANAGEMENT_PORT`) is lost on revert — the
documented rollback cost. Cross-repo doc edits (§9) revert in the same motion, linked
from the PR.

## 6. Port order, branch names, template establishment & review protocol

**Order (by complexity, confirmed against §3):** manual → gitlab → azure-devops →
github. Rationale re-verified from the sources: manual has no external system, no
secrets, no async (≈90 lines of service code); gitlab adds one external POST + secret
decrypt + the always-async handover but **no polling**; azure-devops adds the sync
polling loop + stage-step fan-out; github adds the App-JWT auth chain, two token
exchanges, the dual input modes and the unsupported-input heuristics on top of the
azdevops-shaped polling. The high-level §5 order stands.

**Branches (stacked, one squash-merge PR each, §5 delivery model):**

| PR | Plan | Branch | Base |
|---|---|---|---|
| 1 | 06A manual | `refactor/single-go-binary/phase-6a-manual` | `refactor/single-go-binary/phase-5-dispatcher` |
| 2 | 06B gitlab | `refactor/single-go-binary/phase-6b-gitlab` | `…/phase-6a-manual` |
| 3 | 06C azure-devops | `refactor/single-go-binary/phase-6c-azdevops` | `…/phase-6b-gitlab` |
| 4 | 06D github | `refactor/single-go-binary/phase-6d-github` | `…/phase-6c-azdevops` |

(`azdevops` in the branch name matches the D11 package name — no hyphens inside the
discriminating token, consistent with the package rule's spirit.)

**What 06A must additionally establish (the template PR, high-level §5):**

1. The block-runner-core **wire pins** (§3.3 last row) — claim/register/update
   transcript tests in Kotlin that all four ports inherit.
2. The **event-driven reporting seam** over `report` (§4 row 7) and the PATCH-body
   decision §7.4 — designed against gitlab/azdevops/github needs (multi-step updates,
   step dedup, IN_PROGRESS handover) even though manual sends exactly one update.
3. The **config compat mechanics**: `blockrunner:` yaml block normalization, private-key
   resolution order, `SPRING_PROFILES_ACTIVE` single-run alias (§5.4/§7.8) — as shared
   `config`/persona helpers the other three reuse without new code.
4. The `MeshHttpException`-equivalent external-API error shape (§4 row 14) — even
   though manual performs no external calls, the type is where B–D converge; it ships
   with its first consumer in 06B if review prefers no dead type in 06A (06A must state
   the choice explicitly).
5. The Dockerfile stage pattern, Gradle-shrink recipe instantiation, per-persona
   `containers/<persona>/runner-config.yml` layout, and the local-dev-stack SKILL edit
   pattern.
6. The **fit review** (STOP-D): before 06A's handler is implemented, walk plan 05
   §4.3's table against §3 of this umbrella and 06B–D's inventories; record the result
   in 06A. Interface changes found there are plan-05/umbrella revisions.

**Review protocol for 06B–D:** authored in parallel against umbrella + 06A (the
high-level §7 instruction). Each must carry a **"Template fit-check"** subsection in
its section 4: a table of every point where the runner deviates from the 06A artifacts
(new deps, extra config keys, new report usage, polling loops), each row either mapped
to an umbrella rule that anticipates it or escalated as an umbrella revision. B–D do
not merge before 06A is merged (stacked bases enforce this), but their *plans* are
reviewed together so 06A's interfaces are checked against real needs, not guesses.

## 7. Consistency rules

1. **Naming (P6/P8/D11).** Packages `internal/{manual,gitlab,azdevops,github}`; the
   handler type is `Handler`, constructor `NewHandler`, config `Config` — package name
   provides the qualifier (`gitlab.Handler`). Persona/Identity/image names are the
   Kotlin module names. Step ids (`manual`, `gl-trigger`, `azure-devops-trigger`,
   `gh-trigger`, `ado-stage-<id>`, `gh-workflow-job-<id>`) and display names are frozen
   strings (typed constants). Acronym casing per P6 (`Id`, `Api`, `Pem`).
2. **Metrics (D12).** Each persona gets exactly the standard `runner_*` set
   (`mgmt.RunMetrics` + the plan-05 counters), labeled `runner_uuid` — **no per-runner
   metric names**. Classification: claimed on successful claim; succeeded/failed +
   duration keyed on the run's *reported terminal status* (an IN_PROGRESS async
   handover with `Execute` returning nil counts as **succeeded** — the handover is the
   runner's whole job; recorded here so all four agree). Kotlin runners had zero
   metrics, so everything is additive — no alias duty.
3. **Claim cadence.** All four personas: 10s poll, immediate re-drain after a processed
   run (Kotlin `@Scheduled(10s)` + `ImmediateRetryDecorator`), claim errors ⇒ next tick
   (**no 60s backoff** — that is tf policy; §4 row 2). One `ClaimClassifier` shared by
   the four personas, defined in 06A.
4. **PATCH body.** Kotlin sends the lean `SourceUpdate{status, steps}`
   (`MeshBuildingBlockRun.kt:56-79`); tf historically sent the richer `RunStatusUpdateDTO`
   (blockRunId/source/type/createdOn/…, `tf-block-runner/tfrun/dtos.go:165-174`) to the
   same endpoint. All five runners emit the **lean shape**: it is exactly what the unified
   `Reporter.Report(RunStatus)` emits when `RunStatus` carries only the changed/new steps,
   marshaled by the event-reporting seam (§6 item 2) as a DTO with all fields
   optional/omitempty. tf joins this changed-steps-only send in phase 3 (reduced from
   full-snapshot to only-what-changed), a deliberate flagged wire change that is
   backend-result-identical: the meshfed endpoint **upserts steps by id**, so sending only
   the present steps yields the same coordinator state as a full snapshot. Each included
   step carries its **FULL current message text** (the backend overwrites by assignment,
   not append). The shared rule is only *what* fills `Steps` — the changed/new steps since
   the last send — matching the partial-step pattern the ports use (ado `ado-stage-*`,
   github `gh-workflow-job-*`, gitlab trigger step). Coordinator-visible bytes stay what
   meshfed sees today (D10).
5. **No ticker, no abort.** The ports consume the **same** unified `Reporter` interface as
   tf — `type Reporter interface { Register(RunStatus) error; Report(RunStatus) (abort
   bool, err error) }` — but use it differently: they call `Report` on state changes only
   (sending the changed steps only, §7.4), own their own step dedup (ado `ado-stage-*`,
   github `gh-workflow-job-*`, gitlab trigger step), never run the 10s `report.Observer`
   ticker, and **discard the abort return** (matching Kotlin, which never honored abort —
   `HttpBlockRunClient.kt:62-66`). tf is the only consumer that drives the
   `Progress`+`Observer` 10s ticker and honors the abort flag. Introducing abort support
   is a post-refactor feature, not part of a truthful port. The D9 pin "async runs report
   IN_PROGRESS on successful handover" applies as inventoried per runner (§3.1 row "Async
   semantics").
6. **Secret hygiene of outbound payloads.** Whatever leaves the runner toward the
   external system must reproduce the Kotlin asymmetry: inputs decrypted, impl secrets
   **not** embedded — gitlab's `MESHSTACK_RUN` keeps `pipelineTriggerToken` encrypted
   (`GitLabBlockRunnerService.kt:53-56`), github's `buildingBlockRun` strips the whole
   implementation object (mixin). Concretely: **never** build outbound payloads from
   `meshapi.DecryptRunDetails` output (it decrypts impl secrets); use
   `meshapi.DecryptInputs` (§4 row 8) and strip per runner. Each sub-plan pins this
   with a leak test (payload must not contain the decrypted secret).
7. **Header deltas are uniform.** All four ports adopt the shared client's headers
   (`User-Agent`, `X-Meshcloud-Runner-Name/-Version`, `X-Block-Runner-Node-Id` = plain
   runner uuid) — additive vs Kotlin's version-only surface (§4 row 4); one flagged
   delta, identical wording in every sub-plan, verified once against the mux +
   coordinator in 06A (they already accept the tf/controller header set).
8. **Single-run activation.** `EXECUTION_MODE=single-run` (Go convention) **or**
   `SPRING_PROFILES_ACTIVE` containing `kubernetes` (the deployed operator contract,
   A12) — the latter deprecation-logged, supported until phase 7 at the earliest, and
   honored by all four personas identically. Nothing ever *requires* the new variable
   while rollback to JVM images remains possible (§5.9).
9. **Single-run exit semantics.** Adopt the tf 2b-R12 rule: non-zero exit **only when
   no terminal (or handover) status was reported**. This matches Kotlin where it
   matters (uncaught register/update exception ⇒ exit 1 ⇒ `BackoffLimit: 1` retry;
   reported FAILED ⇒ exit 0) and deliberately diverges where Kotlin swallows: a fetch/
   parse failure before any report exited 0 in Kotlin (`NoOpBlockRunnerService.kt:16-23`
   catch + `SingleShotRunner.kt:38-49`), leaving the run to time out coordinator-side.
   The Go ports exit non-zero there so k8s retries a run meshStack never heard about —
   sanctioned, flagged delta (§10.3), identical in all four sub-plans. The old exit-0
   behavior is pinned per-runner (M-P7, G-P13, …) for audit.
10. **Single-run listener.** Like tf (plan 04 §10.4): no mgmt listener in single-run
    mode. Delta vs Kotlin: the Spring Job pods served an unprobed `/healthz`; the
    controller's Job template sets no probes (plan-03 goldens) — inert, flagged once.
11. **Error-UX parity.** User/system message strings of §3.2 are ported byte-identically
    (they render in the meshStack UI). New Go-side failure modes (e.g. fail-fast for
    unhandled types) use the plan-05 §10.1 wording — never per-runner improvisation.
12. **Per-runner RunType/capability naming** comes only from
    `meshapi/dtos.go:276-295` (`ToRunnerType` mapping GITLAB_CICD→GITLAB_PIPELINE,
    AZURE_DEVOPS→AZURE_DEVOPS_PIPELINE) — no new string literals in handler packages.
13. **Kotlin→Go idiom rules (D15) — translation, not transliteration.** Behavior parity
    is *semantic*, defined by the pinned tests (§5.2); the Go code is idiomatic Go, and
    a Go file that mirrors the Kotlin class structure 1:1 fails review the same way a
    P8 violation does. Uniform transformation table (each sub-plan instantiates it for
    its own Kotlin-isms — skeleton section 5):

    | Kotlin-ism | Idiomatic Go replacement |
    |---|---|
    | Exceptions + `try/catch` fan-out (`updateFailedBlockStatusWith…` per exception type) | returned error chains: `fmt.Errorf("triggering pipeline %s: %w", id, err)` — succinct, lowercase, context formatted in, chained with `:`, no stacktrace-style prose; the user/system *message strings* stay byte-identical (§7.11) — only the plumbing changes. Panics only for programmer errors |
    | JVM logging (kotlin-logging, logback patterns, MDC `requestId`) | `log/slog`, default human-readable text handler on stdout/stderr, kept simple — no handler ceremony; per-run context as attrs (`slog.With("run", id)`), not string prefixes (§10.12) |
    | Spring DI (`@Component`/`@Bean`/`@Profile`), `@ConfigurationProperties` | constructor injection wired in `persona_<name>.go` (P3/D11); profiles become the persona's mode switch (§5.5); properties become the persona config struct over the shared `config` package (D7) |
    | Jackson (`@JsonProperty`, mixins, custom deserializers, `ObjectMapper` modules) | plain structs + `encoding/json` tags (the `meshapi` house style); the github sanitizing mixin becomes an explicit payload struct that simply omits the implementation field (§7.6) — structural omission over annotation magic |
    | OkHttp interceptors (`BearerAuthInterceptor`, `ApiKeyAuthInterceptor`, version-header interceptor) | the existing `AuthProvider`/client composition in `meshapi` — no new middleware layer |
    | `@Scheduled` + `ImmediateRetryDecorator`; `Thread.sleep` polling loops | `dispatch.Loop` ticker/wake (§4 row 1); poll loops as `select` on ticker/`ctx.Done()` with injected `Clock` (§5.3) |
    | Sealed classes / `when` results (`TriggerWorkflowResult`) | small typed result values or typed errors with `errors.As` — whichever reads better at the call site; exhaustiveness via the P8 named-type pattern |
    | Companion-object constants, `object` singletons (`AppTokenFactory`, `StatusMapper`) | package-level `const`/pure functions — no singleton state (P3) |
    | Kotlin data-class `copy()` chains (decrypt-and-rebuild) | value-semantics structs mutated on a local copy (P4) |

## 8. Frozen contracts

**Frozen byte-identically (proven by pins → ported tests):**

- meshStack wire: claim `POST …/meshbuildingblockruns/create?forRunnerUuid=<uuid>` +
  `application/vnd.meshcloud.api.meshbuildingblockrun.v1.hal+json` both headers;
  404/409-claim = no run; register-source body (one step, PENDING) with 409 = success;
  the lean `SourceUpdate` PATCH body (§7.4); runToken-only auth for run-scoped calls;
  `{sourceId}` = runner uuid.
- External-system payloads (customer pipelines parse these!): the GitLab multipart
  field set incl. `MESHSTACK_*` variable names and `inputs[…]`/`variables[…]` split;
  the ADO `templateParameters` + `MESHSTACK_BEHAVIOR` + `resources.refName` shape; the
  GitHub dispatch input names (`buildingBlockRun`, `buildingBlockRunUrl`,
  `MESHSTACK_API_TOKEN`, `MESHSTACK_RUN_TOKEN`, `MESHSTACK_ENDPOINT`) and the
  behavior→workflow selection.
- Step ids/display names and the §3.2 message strings (UI-visible).
- k8s single-run contract: `RUN_JSON_FILE_PATH` (+ the mounted-secret path the
  controller provides), `RUNNER_UUID`, `RUNNER_API_URL`, and the operator-config env
  `SPRING_PROFILES_ACTIVE: kubernetes` as an accepted single-run trigger (D10 both
  directions: old controller config → new image, and rollback).
- Published image names + tags; `ENV PORT=8080`/`EXPOSE 8080`; healthz body `OK`;
  standalone healthz reachable on today's resolved port (alias precedence).
- All Kotlin env vars and yaml keys per §5.4; mux claim contract (per-type ports A11);
  no meshStack/meshfed API change.

**Sanctioned, flagged deltas (uniform across sub-plans):** additive client headers
(§7.7); single-run exit-code tightening (§7.9); no listener in single-run pods (§7.10);
new additive metrics/config (§7.2, §5.4); JVM `command:`-override incompatibility
(§5.6); the slog text format with per-run attrs replacing Spring's log format
(operator-visible log *format* was never a wire contract; readiness markers in §9 are
updated in lock-step; §10.12). For GitLab `variables[k]`/`inputs[k]` and Azure DevOps
`templateParameters`, composite/exotic values render as **compact JSON** (not Java
`toString()`) — a deliberate, flagged byte change recorded in migration/release notes;
pins assert JSON.

## 9. Cross-repo touch points

- **meshfed-release `local-dev-stack/SKILL.md` — must change in 06A (lock-step PR):**
  the manual-runner block (lines 64-71, `./gradlew :manual-block-runner:bootRun`)
  becomes the Go persona start (`go run . manual-block-runner` in the repo root, env
  `RUNNER_API_URL=http://localhost:8301` + config path); readiness table line ~103
  (`Started BlockRunnerApplication` marker in `/tmp/manual-runner.log`) gets the Go
  readiness marker; the pgrep hint (`BlockRunnerApplication`, lines 88-91) is updated.
  06B–D: no local-dev-stack entries exist for their runners — verify (grep) and state
  "no edit" or add optional start snippets if the maintainers want them (sub-plan
  decision, not required for the gate).
- **meshfed-release acceptance tests / mux:** read-only. The mux fans out per type
  (MANUAL `:8301`, GITHUB_WORKFLOW `:8302`, GITLAB_PIPELINE `:8303`,
  AZURE_DEVOPS_PIPELINE `:8304`, TERRAFORM `:8300`, SKILL.md:56) — wire frozen; the
  acceptance suite is the outer net per §5.7. No per-type acceptance tests exist for
  gitlab/azdevops/github (§5.7 finding) — nothing to update there.
- **meshfed-release `how-to-run-building-block-runners.md`:** references images by
  registry name only — names unchanged, but the page documents JVM-era env
  (`SPRING_PROFILES_ACTIVE`) semantics; 06A adds a doc-truth check, edits (if any) ride
  the same lock-step PR. Full docs pass remains phase 7.
- **This repo, `run-controller/runner-config.yml` (shipped sample):** stays valid
  unchanged (the new images honor the profile env). Optionally each port adds a comment
  noting `EXECUTION_MODE: single-run` as the preferred form; flipping the sample's env
  is deferred to phase 7 so rollback stays symmetric (§5.9).
- **terraform-provider-meshstack:** no dependency on the Kotlin runners (pattern source
  only, D3) — no edits; 06A verifies by grep over its skills.

## 10. Flags — findings the high-level/prior plans did not anticipate

1. **The Kotlin single-run trigger is `SPRING_PROFILES_ACTIVE=kubernetes`, not
   `EXECUTION_MODE`.** D9 documents the tf contract only; the operator-facing job
   templates for the four Kotlin types bake the Spring profile
   (`run-controller/runner-config.yml:142-157`). The ported personas must honor it or
   every existing controller deployment breaks on image update — hence §7.8. No prior
   plan mentions this.
2. **No uniform acceptance coverage exists for gitlab/azdevops/github.** `github`, `tf`
   and `manual` have real end-to-end coverage via the sibling `meshstack-smoke-test`
   harness (tf in that repo; github/manual e2e modules in `meshstack-hub`, discovered and
   run by it) and validate there before Kotlin module removal; `gitlab` and `azure-devops`
   have **no** smoke tests (accepted shortcoming) and lean on the in-repo side-by-side
   transcript equivalence (Kotlin pin suite vs Go). Commissioning new meshfed-release
   acceptance tests for those two is out of scope. The §5.7 gate reads honestly per runner
   rather than promising a uniform manual smoke.
3. **Kotlin swallows pre-report failures in k8s mode** (fetch/parse errors caught ⇒
   exit 0, no status ever reported — the run hangs until coordinator timeout), the twin
   of the controller's decrypt-failure quirk (plan 05 §16.8). §7.9 tightens this to the
   R12 rule instead of pinning the swallow — a deliberate, flagged behavior change fixed
   in phase 6; the old exit-0 behavior is pinned (M-P7, G-P13, …) for audit.
4. **Spring relaxed binding is an unownable compat surface.** `blockrunner.uuid` can be
   spelled a dozen ways in Spring (env `BLOCKRUNNER_UUID`, `blockrunner.api-key…`).
   §5.4 carries the literal spellings that appear in shipped files and docs; anything
   else is unsupported. D7's "all existing env var names keep working" is read as "all
   names we ever shipped or documented": support only the literal shipped/documented
   spellings — do **not** reimplement Spring relaxed binding. Startup **fails fast** (not
   just warns) with an actionable message when an env var matching a known legacy prefix
   (e.g. `BLOCKRUNNER_*`) is present but consumed by no config key.
5. **The gitlab/azdevops/github modules ship a baked-in dev private key** inside the
   classpath `runner-config.yml` (e.g. `gitlab-block-runner/src/main/resources/
   runner-config.yml:12`). Keep the well-known dev private key **verbatim**
   (byte-equivalent defaults), with a one-line comment marking it the well-known dev key
   so scanner hits self-answer. It lives **once** in a shared top-level base
   `runner-config.yml` that the per-impl
   `containers/<persona>/runner-config.yml` files deep-merge over (base < per-impl <
   env) — not duplicated per persona, never a silent fallback when
   `RUNNER_PRIVATE_KEY_FILE` is set (it is the local-dev pair of meshfed's magic-runner
   public key). Removing it from published-image defaults is an explicit **phase-7**
   ledger item.
6. **The Kotlin runners never send `X-Block-Runner-Node-Id`** (and no runner-name
   header) — D9 lists the node-id header among frozen pins, but for these four types it
   is *new* wire surface, not preserved surface (§7.7).
7. **`report.Observer` is the wrong tool for these ports** — the shared reporting
   facility was generalized from tf (10s ticker, abort-cancel); Kotlin runners are
   event-driven and abort-blind. Plan 05 §4.3's "report.Observer async mapping" row is
   corrected by §7.5: only `Progress`/DTO mapping and the async-handover *rule* carry
   over. Recorded so 06A doesn't force the ticker to "reuse shared machinery".
8. **The `SourceUpdate` PATCH body is a third wire shape** (leaner than both tf's and
   the controller's DTOs) — no prior plan lists it; §7.4 freezes it for the ports.
9. **`meshapi.DecryptRunDetails` is a leak hazard for outbound payloads** (it decrypts
   impl secrets; Kotlin's payload path decrypts inputs only) — the `DecryptInputs`
   split (§4 row 8) exists precisely to make the §7.6 rule structural.
10. **gitlab is always-async** — the impl DTO has no `async` field
    (`MeshBuildingBlockRun.kt:221-226`); the D9 "async handover" pin applies
    unconditionally there, unlike azdevops/github. Prior plans treated async as a flag
    on all pipeline runners.
11. **`GithubImplementation`/dtos.go cross-check passed** with one nuance: the Go DTO
    already models all fields the services read (incl. `omitRunObjectInput`,
    `MeshstackBaseUrl` link); no DTO gaps found — but `PipelineRun`/`Timeline`/
    `WorkflowRun` DTOs are runner-package-local ports (not `meshapi`).
12. **D15's `log/slog` conflicts with the plans-02–05 logging baseline.** Every shared
    package and both existing personas standardize on `*log.Logger` (e.g.
    `dispatch.NewLoop` deps, `mgmt.NewServer(log *log.Logger, …)`, `config.Path/Env`
    signatures — plans 03 §5.3, 04 §4.3, 05 §4.1), and plan 05 H3/§16.9 specifies the
    per-run `[RUN-<id>]` *prefix*. Umbrella ruling: phase-6 handler packages use
    `slog` per D15 (run id as an attr, H3's real observable — per-run log isolation —
    retargeted onto it); persona wiring bridges where a shared-package signature
    demands `*log.Logger` (`slog.NewLogLogger`). Migrating the shared packages and the
    tf/controller personas to slog is **not** phase-6 work — it lands in phase 7 (or a
    reviewed revision of plans 03–05 if the reviewer prefers one logging stack sooner).
    Until then the binary has two logging styles — flagged, not hidden.

## 11. Open questions

None open. The judgment calls carrying the most weight are encoded as flags/rules: the
acceptance-gap reading (§10.2), the exit-code tightening (§10.3/§7.9), the lean-PATCH-body
choice (§7.4), the stdlib JWT decision (§4 row 16), the relaxed-binding boundary (§10.4),
the dev-private-key placement (§10.5), and the two-logging-stacks interim state
(§10.12/D15).
