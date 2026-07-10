# Detail Plan 03 — Shared Runner-Core & Client Consolidation (Phase 3)

**Phase:** 3 · **Branch:** `refactor/single-go-binary/phase-3-shared-core` (stacked on
`refactor/single-go-binary/phase-2b-bugfixes`) · **Delivery:** one single-commit PR
(§5 high-level plan) · **Binding:** §3 P1–P8, D3 (client patterns), D4 (reporting
facility), D5 (registration outlook), D6 (gate growth), D7 (config precedence), D9/D10
(frozen contracts), D11 (package layout), D12 (observability split) of
`PLAN_HIGH_LEVEL.md`.

Phase character: **behavior-preserving for both runtimes** — the tf runner's phase-1
characterization suite and the run-controller's existing test suite stay green with
assertions untouched, and the controller's wire behavior (claim, dispatch, registration,
status reporting, k8s Job contract) is byte-identical, **with one sanctioned exception**:
retry/backoff on the shared HTTP client, which the high-level phase-3 mandate explicitly
orders (D3) and which is inert on every happy path (§5.2.3).

---

## 1. Assumptions from prior phases

Plans 00–02 are **not implemented yet**; everything below is a promise, not a fact.
Implementation of phase 3 **begins by running every verification step**. Any material
failure is a **STOP**: update this plan (and cascading plans) first, get the revision
reviewed, then resume.

| # | Assumption | Promised by | Verification step |
|---|---|---|---|
| A1 | Task targets exist and are green on the phase-2b branch: `task test`, `task lint`, `task coverage`, `task test:go-meshapi-client` etc. | Plan 00 §12 | `git checkout refactor/single-go-binary/phase-2b-bugfixes && task test && task lint && task coverage` |
| A2 | The tf runner has the plan-02 shape: `tf-block-runner/internal/tf` (engine + ports `RunSource`/`StatusReporter`/`ArtifactSource`/`Decryptor`/`TfProvider`/`Source`/`Clock`), siblings `internal/gitsource` + `internal/tofu`, `progress` tracker with `Snapshot()`, `RunLog`, one `Engine.Execute`, no `Worker`/`SingleRunWorker`/`TfCmd` types, `util/` dissolved. | Plan 02 §5, §6 | `ls tf-block-runner/internal/{tf,gitsource,tofu}`; grep for the port names and for `SingleRunWorker` (must be gone). |
| A3 | Package-level mutable state in tf-block-runner is gone (`tfrun.AppConfig`, `runInfoContextKey`); `crypto.Crypto` in go-meshapi-client was deleted in phase 2 step 2 (flagged cross-module edit, plan 02 §10). The **only surviving mutable globals in the three modules** are `meshapi.runnerName`/`runnerVersion` (`go-meshapi-client/meshapi/client.go:26-40`) plus the run-controller's set (§3.3 below), all explicitly deferred to this phase. | Plan 02 §2, §3.1 | `grep -rn "var AppConfig\|var Crypto\|runnerName\s*=" tf-block-runner go-meshapi-client` — hits only in `meshapi/client.go`. If `crypto.Crypto` still exists (review chose the plan-02 §10 alternative), its deletion moves into step 4 here — not a STOP, a recorded scope addition. |
| A4 | Coverage gate is ON with prefix line `github.com/meshcloud/building-block-runner/tf-block-runner/internal 90` and exclusions `internal/gitsource/git.go`, `internal/tofu/tfbinaries.go` (paths post-move). Measured number has ≥1pp buffer over 90. | Plan 01 CP13, Plan 02 §5.1/step 11 | `cat tools/coverage/thresholds.txt tools/coverage/exclusions.txt && task coverage`; record the number. |
| A5 | `-race` is ON (`task test:tf-block-runner` + CI tf leg) since phase 2b R1. Everything phase 3 moves into shared packages (notably `progress`) must stay race-clean. | Plan 02 §7 R1 | `grep -rn "\-race" Taskfile.yml .github/workflows/` |
| A6 | `.golangci.yml` still carries the **temporary `controller` and `meshapi` exclusion blocks** whose removal plan 00 assigned to phase 3 (errcheck-class production pins: `meshapi/client.go`×4, `meshapi/auth.go`×1, controller errcheck×2 / staticcheck×3 / unparam×1). | Plan 00 §5.3, §12 | Read `.golangci.yml` exclusion comments; confirm the removal-owner notes name phase 3. |
| A7 | `run-controller/` production code is untouched since `main` except plan-00 category-1 lint fixes (comments/format/mechanical). No behavior drift to re-research. | Plans 00–02 scope | `git diff main..refactor/single-go-binary/phase-2b-bugfixes -- run-controller/ ':!*_test.go'` — only inert hunks. |
| A8 | `go-meshapi-client` is tested in CI (`go-runners-ci` matrix leg), per plan 00 §11.1's flagged recommendation. | Plan 00 §5.5 | grep `go-meshapi-client` in `.github/workflows/ci.yml`. **If the reviewer rejected that leg**, phase 3 adds it here — a shared-core module cannot stay CI-untested once it gates (STOP only if review again refuses). |
| A9 | Phase-1 pin tests exist in `go-meshapi-client/meshapi/client_test.go` beyond today's two `TestDownloadArtifact_*` (at minimum the 128MiB-cap pin, plan 01 CP4). (`TestRegister_409Conflict_ReturnsNil` lives in `tf-block-runner/tfrun/runapi_status_test.go:83`, not here — see flag §12.8.) | Plan 01 CP4/§5 | `grep -n "func Test" go-meshapi-client/meshapi/client_test.go` |
| A10 | Bug fixes from 2b are in (no `FIXME(bug)` markers); in particular B12 (`ExecutionStatus.str` no longer panics the process via `Behavior`; stringers are move-safe) and B10/B6 (value-typed `Steps []StepStatus`, snapshot-under-lock) — the shapes this phase relocates. | Plan 02 §7 | `grep -rc "FIXME(bug)" tf-block-runner/ \| grep -v ':0'` (empty). |

**STOP-A (before any coding):** any of A1–A10 materially false ⇒ update this plan first.
**STOP-B (any time):** a tf phase-1 characterization test or a controller test can only be
kept green by changing its **assertions** beyond the declared retargets in §6.1 —
stop, record, review, resume.
**STOP-C (any time):** a code move drops the gated tf coverage below 90 without a
compensating test step already planned (§9) — do not touch the exclusion list; replan.
**STOP-D (step 3):** the retry transport changes the observable transcript of **any**
phase-1 pinned scenario (request counts on non-5xx paths, header contents, bodies) —
the retry policy is then wrong; replan the policy, do not weaken the pin.

---

## 2. Scope

**In:**

- Evolve `go-meshapi-client` into the shared runner core (module decision §5.1):
  - `meshapi`: typed error (`HttpError`), retry/backoff transport, client-per-resource
    split (`RunClient` + `RunnerClient` incl. the controller's registration PUT),
    de-globaled client identity (kills `runnerName`/`runnerVersion` — the plan-02
    deferred global), shared run-JSON decryption (`DecryptRunDetails` + `Decryptor`).
  - new `config`: D7 loader mechanics (defaults < base YAML < per-impl YAML < env), shared `Api` section with
    the `user`/`username` yaml alias, env-binding helper, full compat alias table (§5.3).
  - new `report`: runner-agnostic reporting facility (D4) — `RunStatus`/`StepStatus`/
    `ExecutionStatus`, `Progress` tracker, `RunLog`, `Observer` (10s ticker, abort-cancel,
    async `SUCCEEDED→IN_PROGRESS` mapping), `Reporter` port, DTO mapping.
- Re-base `internal/tf` onto `config` mechanics and `report` (moves code out; tf keys and
  behavior unchanged).
- Re-base `run-controller` onto the shared packages; eliminate its package-level mutable
  state (`AppConfig`, `DiscoveredOidcIssuer`, `UseTestClient`, metrics singleton);
  characterize its 0%-covered wire adapters (`runapi.go`, `registration.go`,
  `kubernetes.go` manifest shape) **before** touching them.
- D12 down payment (explicit split, §5.6): registry-injected `MetricsCollector`
  construction; ports, listeners, healthz and the new generic runner metrics stay in
  phase 4.
- D6 gate extension: thresholds lines for the shared packages; new hermetic tests to earn
  them (§9).
- Pay off the phase-0 lint debt owned by this phase (A6): delete the `controller`/`meshapi`
  exclusion blocks and fix the pinned findings under the new tests; extend depguard for
  the new packages.

**Out (deferred, with destination):**

- Unifying the tf polling loop and the controller drain loop into one shared
  "polling/claim engine" → **phase 5** (`Dispatcher`). This deliberately narrows the
  high-level phase-3 sentence "polling/claim engine [moves to shared packages]" — see
  flag §12.1; what *is* shared here is the claim client, error classification and the
  reporting engine.
- `MANAGEMENT_PORT`, controller healthz, per-persona port defaults, generic
  runs-claimed/succeeded/failed metrics → **phase 4** (D12, §5.6).
- Capability-config registration API, claim-and-fail-fast → **phase 5** (D5).
- Any DTO unification of the two PATCH body shapes (`StatusUpdateDTO` vs
  `RunStatusUpdateDTO`) — both are frozen wire shapes (§8).
- Root Go module consolidation, per-persona `cmd/<persona>` mains + `cmd/bbrunner` superset (D2) → **phase 4**.
- k8s dispatch restructuring (`kubernetes.go` stays a monolithic adapter; only its
  `AppConfig` reads become parameters) → **phase 5** (`KubernetesJobDispatcher`).
- Cross-repo SDK extraction (explicitly out per high-level §8).

---

## 3. Research evidence — current state

All references at `refactor/single-go-binary/plan` (= `main` @ c3fce61) unless noted;
run-controller code is assumed unchanged through phase 2b (A7).

### 3.1 Duplication map: `controller/runapi.go` + `registration.go` vs `meshapi.Client`

`run-controller/controller/runapi.go` (115 lines) wraps the shared client but duplicates
its construction plumbing; `registration.go` (128 lines) bypasses the shared client
entirely with its own `http.Client`:

| Concern | Shared client (`go-meshapi-client/meshapi`) | Controller duplication | Delta |
|---|---|---|---|
| Claim | `Client.FetchRun` (`client.go:89-122`): POST, media type V1, returns `(dto, rawBytes, err)` | `RunApiClient.FetchRunDetails` (`runapi.go:39-61`) wraps it, then **base64-encodes rawBytes** (`runapi.go:59`) for the k8s Secret path | tf's twin (`tfrun/runapi.go:86-101`) instead maps to `*Run` and stores the runToken. Node-id differs: controller sends `"run-controller-<postfix>"` (`runapi.go:15,40`) and `processNextRun` passes `AppConfig.Uuid` as postfix (`controller.go:169`); tf sends `"<uuid>-worker-N"` (`tfrun/runapi.go:87`). Both formats are observable headers — **frozen**. |
| Per-call client | `NewClient`/`NewClientWithHTTP` (`client.go:66-84`) | `newMeshClient` builds a fresh client **per call** with a fresh `AuthProvider` (`runapi.go:35-37`) | tf builds one client at construction + a per-fetch client for the node-id (`tfrun/runapi.go:62-69,90`). |
| Register source | `Client.RegisterSource`, 409 ⇒ success (`client.go:187-189`) | `RegisterSource` registers a single hardcoded `validation` step (`runapi.go:69-79`) | tf registers the full step list (`tfrun/runapi.go:103-127`). Same endpoint/media type. |
| Status PATCH | `Client.PatchStatus` returns raw body (`client.go:202-233`) | `UpdateRunStatus` sends **`StatusUpdateDTO`** (status/summary/steps only, `runapi.go:95-107`) and ignores the response body (no abort handling) | tf sends **`RunStatusUpdateDTO`** (blockRunId/source/type/createdOn/artifact, `tfrun/dtos.go:164-174`) and parses `runAborted`. Two distinct frozen body shapes. |
| Runner registration | **absent from the shared client** | `registration.go:73-102`: own `http.Client{}` (`:42`), own `setHeaders` with `MeshBuildingBlockRunner_MediaType_V1Preview` (`:14-18,105-109`), PUT `EP_RunnerWithUuid`, treats 404 as "controller not found — create it via UI" (`:63-65`), all other non-200 as error | This is the piece that **moves into the shared client** (`RunnerClient`). The WIF/namespace DTO construction (`dtos.go:12-52`) is k8s-persona logic and stays in the controller. |
| Error type | `StatusError{Status}` (`client.go:47-53`) — status only, no body | `isNoRunError` type-asserts on it, 404 only (`controller.go:244-250`); metrics classification re-asserts (`runapi.go:51-55`) | tf's `handleFetchRunError` treats 404 **and** 409 as no-run plus a chunked-transport string match (plan 02 §3.3). Provider pattern: `HttpError{StatusCode, ResponseBody}` + `IsNotFound/IsConflict` (`terraform-provider-meshstack/client/internal/http_error.go:10-32`). |
| Identity headers | package globals `runnerName`/`runnerVersion` via `SetClientMetadata` (`client.go:26-40`), stamped in `setHeaders` (`client.go:235-243`) | both mains call `SetClientMetadata` once (`run-controller/main.go:21`, `tf-block-runner/main.go:26`) | The deferred third global (plan 02 §11.4) — dies here. |

### 3.2 Config today: `tfrun/config.go` vs `controller/config.go`

| Aspect | tf (`tf-block-runner/tfrun/config.go`) | controller (`run-controller/controller/config.go`) |
|---|---|---|
| Global | `var AppConfig TfRunnerConfig` (`:14`) — removed by phase 2 | `var AppConfig *ControllerConfig` (`:12`) — **removed here** |
| File path env | `RUNNER_CONFIG_FILE` (`:56`), default `runner-config.yml` (`:53`) | `RUNCONTROLLER_CONFIG_FILE` (`:132`), same default (`:130`) |
| Missing file | tolerated: defaults + env (`:76-77`) | **fatal**: `ReadInYmlConfig` error ⇒ `logger.Fatalf` (`:152-155`) — sensible, since the file-only `implementations` map is mandatory (`:262-264`) |
| Env overrides | `RUNNER_UUID`, `RUNNER_API_URL`, `RUNNER_API_USERNAME`, `RUNNER_API_PASSWORD`, `RUNNER_API_CLIENT_ID`, `RUNNER_API_CLIENT_SECRET`, `RUNNER_PRIVATE_KEY_FILE` (`:57-63,126-164`) | only `RUNNER_API_CLIENT_ID` / `RUNNER_API_CLIENT_SECRET` (`:136-137,186-204`), with a loud half-configured warning (`:192-194`) |
| Auth yaml keys | `api.user` / `api.password` (`:34`) | `api.username` / `api.password` (`:37`) — **key divergence** |
| AuthProvider | nil when nothing configured (single-run legit, `:42-50`) | `BasicAuth` fallback even when empty; takes a `fallbackURL` (`:46-55`) |
| Auth validation | single-run mode (`EXECUTION_MODE`+`RUN_JSON_FILE_PATH`) exempts auth (`:190-213`) | `validateApiAuth` with per-field messages (`:212-235`) |
| Extra validation | runnerUuid required (`:216-221`) | namespace, api.url, uuid, ownedByWorkspace, displayName, crypto keys, implementations non-empty + valid keys + image (`:237-274`, `:320-333`) |
| Other globals | — | `DiscoveredOidcIssuer` (`:15`, written by `main.go:39`), `UseTestClient` (`runapi.go:13` — **no writer anywhere in the repo**, verified by grep; a dead switch read at `registration.go:113` and `config.go:284`) |

### 3.3 Controller mutable-state & metrics inventory

1. `AppConfig` — read at ~25 sites: `controller.go:51-77,100,125-126,149,169,199`,
   `runapi.go:30,36,45,49,52-54,66,92,109`, `registration.go:38-40,53,64,68,74,76,121`,
   `dtos.go:17-23`, `kubernetes.go:67,73,82,130,146,154-155,204,215-216,347-352,386,425,626,632`.
2. `DiscoveredOidcIssuer` — write `main.go:39`, read `registration.go:41`.
3. `UseTestClient` — dead switch (§3.2).
4. `metricsInstance`/`metricsOnce` singleton (`metrics.go:10-13,65-162`):
   `NewMetricsCollector()` is called **three times** (`controller.go:68`, `runapi.go:31`,
   `registration.go:119`) and only works because of the `sync.Once`; tests depend on it
   too (`controller_test.go:98`). Metric names `run_controller_*` with
   `controller_uuid`/`error_type` labels (`metrics.go:68-159`) are scrape-visible —
   frozen in this phase.
5. Metrics endpoint: hardcoded `:2112`, `/metrics` only — **the controller has no healthz
   at all today** (`main.go:26-35`; D12's "finally gains a healthz" lands in phase 4).

### 3.4 Coverage & tests today (controller + client)

- `controller/runapi.go`, `registration.go`, `kubernetes.go`: **0.0%** (plan 00 §3/§8).
  Existing tests cover the loop/capacity via `RunApi` + `JobManager` fakes
  (`controller_test.go:16-42`, `controller_capacity_test.go:13-33`), config validation
  (`config_test.go`), decryption (`decryption_test.go`), registration DTO shape
  (`dtos_test.go`). Nothing pins the wire: media types, node-ids, PUT semantics, Job
  manifest. Phase 3 must **characterize before refactoring** (§6 step 1).
- `meshapi` 39.3%, `crypto` 71.4% (plan 00 §3) — both must reach ≥90 to join the gate (§9).

### 3.5 Provider patterns to adopt (D3 — patterns only, no import)

- **Retry transport** (`client/internal/retry.go:19-78,154-198`): a `RoundTripper`
  wrapper; retries idempotent methods (GET/PUT/DELETE) by default, POST only when
  whitelisted; retryable responses: transport error, 429/503 (honoring `Retry-After`,
  capped 5min, `:110-143`), 502/504; exponential backoff `MinWait*2^(n-1)` capped
  (`:96-106`); request-body replay via `GetBody`/tee (`:200-243`); response drain for
  connection reuse (`:259-270`). Budget sized to ride out a backend restart
  (`client/client.go:62-69`: 12 retries, 1–30s, ~4min).
- **Error type** (`http_error.go`): `HttpError{StatusCode, ResponseBody}` + `IsNotFound`/
  `IsConflict`/`IsForbidden`.
- **Client layout** (`client/client.go:20-44`): one aggregate struct of per-resource
  clients, each thin over one shared `HttpClient`.
- Deliberately **not** adopted (alignment notes §7): `MinMeshStackVersion` startup check,
  the 5-minute whole-client timeout (`http_client.go:18` — would kill legitimate 128MiB
  artifact streams), generic `DoRequest[R]`/`RequestOption` machinery (P2: our four
  endpoints don't earn it yet), `Authorization.Header(ctx, client) (string, error)` in
  place of `AuthProvider.AuthHeader() string`.

### 3.6 Reporting facility: what plan 02 leaves in `internal/tf` that is runner-agnostic

Post-plan-02 shapes (A2): `progress` (mutex + `Snapshot()` deep copy, plan 02 §5.5),
`RunLog` (file-backed, `LogStartIdx` segmentation), the observer goroutine (10s ticker →
`Snapshot()` → `Report` → abort ⇒ `cancel()`; final mapping async+SUCCEEDED ⇒
`IN_PROGRESS`, cancelled ctx ⇒ no final update — D9 pins), `RunStatus`/`StepStatus`
(values, `runstatus.go` today) and `ExecutionStatus` (`executionstatus.go`), plus the
`RunStatus → RunStatusUpdateDTO` mapping which stamps `Source` (runner uuid) and
`Type: TERRAFORM` (`dtos.go:164-174` today). Runner-specific and staying in `internal/tf`:
step tables/ids, `TfOutput` production, the `EP_State` backend URL (`tfrun/runapi.go:42`),
`NodeSuffix`, run-token lifecycle, `RunSource`/`ArtifactSource`/`TfProvider`/`Source`/
`Clock` ports.

---

## 4. Objective & exit criteria (from the high-level plan)

tf runner and controller share client, config, reporting; the controller's `runapi.go`
duplication (registration HTTP guts) is gone; `controller.AppConfig` (and every other
package-level mutable in all three modules) is gone; behavior unchanged (controller
tests + acceptance checks, §6/§9); shared packages join the D6 gate at ≥90.

---

## 5. Target design

**Logging (slog-native):** the shared-core packages (`meshapi`, `crypto`, `config`,
`report`) are authored slog-native (`log/slog`) from the start: no `*log.Logger` seam, no
`slog.NewLogLogger` bridge; every logger parameter below is a `*slog.Logger`. The
`tf`/`tfrun` package migrates to slog in phase 7 (its phase-1/2 pins predate this).

### 5.1 Module decision: shared packages live in `go-meshapi-client` during this phase

D11's destination (`internal/{meshapi,crypto,config,report}` at repo root) is **physically
unreachable in phase 3**: Go's `internal/` rule means packages under `internal/…`
can only be imported from within the root module — but the tf runner and controller
mains live in their own modules until phase 4. The alternatives:

- **(rejected) new root module now with temporarily-exported packages:** creates a
  public importable API surface in a public repo that phase 4 immediately breaks; also
  contradicts D2 (the module is the binary, phase 4).
- **(chosen) grow `go-meshapi-client`:** it is already the shared module both binaries
  import via `replace` + `go.work` (`run-controller/go.mod`, `tf-block-runner/go.mod`,
  `go.work`), it is dependency-light, and phase 4's consolidation becomes a mechanical
  `git mv go-meshapi-client/<pkg> internal/<pkg>` + import rewrite, with the `go.work`
  workspace removed entirely (the root module needs no `use` directive) (same trick as
  plan 02 §5.1). Package names are chosen now exactly as their D11 destinations:

| Phase 3 (module `…/go-meshapi-client`) | Phase 4+ (module `…/building-block-runner`) |
|---|---|
| `meshapi` (client, DTOs, errors, retry, run-JSON decryption) | `internal/meshapi` |
| `crypto` (unchanged algorithms) | `internal/crypto` |
| `config` (new) | `internal/config` |
| `report` (new) | `internal/report` |

Dependency cost: `config` adds `gopkg.in/yaml.v2` to the module (already used by both
consumers); `report` adds nothing (imports `meshapi` for the DTO mapping). Prometheus
stays **out** of the shared module (§5.6). Depguard gets the corresponding per-package
rules (`report` may import `meshapi`; `config` may import `meshapi` for `AuthProvider`;
`meshapi` may import nothing but stdlib; `crypto` stdlib only).

### 5.2 `meshapi` — client consolidation (D3)

Illustrative signatures only.

**5.2.1 Error type** (replaces `StatusError`, provider-aligned):

```go
type HttpError struct {
    StatusCode   int
    ResponseBody []byte // capped at maxErrorBodyBytes, as today
}
func (e HttpError) Error() string
func (e HttpError) IsNotFound() bool  // claim 404 = no run (D9)
func (e HttpError) IsConflict() bool  // register 409 = success / claim 409 = no run (D9)
```

Every non-2xx path returns it (today `FetchRun` returns `StatusError` without the body,
`client.go:104-105`, while other methods return `fmt.Errorf` with the body — the error
*messages* unify; the classified statuses and semantics do not change). Call-site
migration: `controller.isNoRunError` (`controller.go:244-250`), metrics classification
(`runapi.go:51-55`), tf fetch-error classification (404/409/other — plan 02 keeps
`handleFetchRunError` semantics verbatim; only the type assert changes, assertions in
tests unchanged).

**5.2.2 Identity** (kills the deferred global):

```go
type Identity struct{ Name, Version string } // e.g. {"run-controller", build.Version}
func (id Identity) UserAgent() string        // "meshcloud-<name>/<version>"
```

`SetClientMetadata`, `runnerName`, `runnerVersion` (`client.go:26-44`) are deleted;
constructors take `Identity`; both mains pass it where they construct clients. Header
output (`User-Agent`, `X-Meshcloud-Runner-Name/-Version`) byte-identical.

**5.2.3 Retry/backoff** (provider design, runner-tuned policy):

```go
// retry.go (package meshapi, unexported roundtripper — same design as the provider's
// client/internal/retry.go: idempotent-by-method + POST whitelist, Retry-After-aware).
type RetryOptions struct {
    MaxRetries       int
    Backoff          Backoff            // ExponentialBackoff{MinWait, MaxWait}
    WhitelistedPosts []string           // exact request paths
}
```

Policy (constructor default, one place):

- Retried: GET/PUT/DELETE always; POST only `/api/login` (ApiKeyAuth) and the
  register-source path — safe because 409-on-replay is already success
  (`client.go:187-189`), making the POST effectively idempotent.
- **Never retried: the claim POST** (`FetchRun`). A replay after an ambiguous failure
  would claim a *second* run while the first sits claimed — the polling loops' own
  cadence (10s/60s delays; controller ticker) is the retry.
- **Not retried: status PATCH.** The 10s observer ticker re-sends the full status anyway;
  retrying inside the transport would only delay abort detection.
- Retryable responses: transport error, 429/503 (honor `Retry-After`, capped), 502/504.
  Plain 500 is **not** retried (matches the provider) — so the phase-1 pins
  "registration 500 ⇒ FAILED" and all 404/409 pins see identical transcripts (STOP-D
  guards this).
- Budget: `MaxRetries: 4`, `ExponentialBackoff{1s, 8s}` (~15s total). Rationale: runners
  sit in retry loops already (manager delays, controller polling ticker, the controller
  main's 10-minute registration loop `main.go:48-64`); the provider's 4-minute
  restart-riding budget belongs to a CLI-invoked client, not a daemon that will poll
  again anyway. The 503-riding *mechanism* is what D3 asks for; the budget is reviewable.

**5.2.4 Client-per-resource layout** (provider `client.go:20-44` in miniature):

```go
// RunClient: the runner-facing run endpoints, media type BlockRunMediaTypeV1.
func NewRunClient(baseURL string, nodeId NodeId, id Identity, auth AuthProvider) RunClient
func (c RunClient) FetchRun(runnerUuid string) (*RunDetailsDTO, []byte, error)
func (c RunClient) RegisterSource(runId string, reg RegistrationDTO) error
func (c RunClient) PatchStatus(runId, sourceId string, payload any) ([]byte, error)
func (c RunClient) DownloadArtifact(url string, w io.Writer) error

// RunnerClient: the meshBuildingBlockRunner meshObject endpoint,
// media type MeshBuildingBlockRunnerMediaTypeV1Preview (moved verbatim from
// run-controller/controller/registration.go:15-17).
func NewRunnerClient(baseURL string, id Identity, auth AuthProvider) RunnerClient
// Update PUTs the registration; the runner must already exist (404 => HttpError,
// caller maps it to the actionable "create it via the meshStack UI" message).
func (c RunnerClient) Update(uuid string, dto MeshBuildingBlockRunnerDTO) error

type NodeId string // X-Block-Runner-Node-Id; typed so uuid vs "uuid-worker-N" vs
                   // "run-controller-uuid" cannot be silently swapped (P8)
```

`Client` (the current god-struct) dissolves into the two. tf's per-fetch node-id override
keeps working by constructing a `RunClient` with the fetch `NodeId` per claim, exactly as
today (`tfrun/runapi.go:87-90`); the underlying `http.Client` (with retry transport) is
shared via an optional `WithHTTP`-style constructor variant, as now (`client.go:77-84`).

**5.2.5 Shared run-JSON decryption** (from `controller/decryption.go`):

```go
// Decryptor matches the phase-2 tf port (plan 02 §5.4) so one interface serves both.
type Decryptor interface{ Decrypt(ciphertext string) (string, error) }

// DecryptRunDetails decrypts sensitive inputs + per-implementation secrets and
// re-encodes; signature identical to today's controller helper (base64 in/out).
func DecryptRunDetails(runJsonBase64 string, dec Decryptor) (string, error)
```

Moves verbatim (all five impl-type branches, `decryption.go:13-128`) together with its
tests. Justified consumers: controller (now) and phase 5's `InProcessDispatcher`
("per-run decrypt then runToken-only reporting", high-level §5 phase 5) — a named, non-
speculative second consumer. `crypto.MeshCertBasedCrypto` gets a small
`Decrypt(string) (string, error)` method delegating to `DecryptMeshCertBased` so it
satisfies the port without renaming the existing API.

### 5.3 `config` — one shared loader (D7)

Persona config **structs stay in their persona packages** (`internal/tf` keeps its keys;
`controller` keeps `ControllerConfig` incl. the file-only `implementations` map — D7).
The shared package owns the mechanics and the genuinely shared sections.

**Config deep-merge:** the loader deep-merges **two** YAML file layers — a shared
top-level base `runner-config.yml` (common keys) overlaid by an optional
per-impl/per-persona `runner-config.yml` (overrides). Effective precedence: **compiled-in
defaults < base YAML < per-impl YAML < env**. The merge is key-wise: a key present in the
per-impl layer wins over the base; absent keys inherit the base value.

```go
// Path resolves a config file path: primary env var RUNNER_CONFIG_FILE, then
// persona-specific aliases (deprecation-warned), then the default runner-config.yml.
// Called once for the base layer and once for the per-impl layer.
func Path(log *slog.Logger, aliases ...EnvAlias) string
type EnvAlias struct{ Var string; Deprecated bool }

// Load unmarshals the base YAML then deep-merges the per-impl/per-persona YAML on top
// (per-impl keys override), decoding both into the persona struct; found=false when
// neither layer exists (personas decide whether that is fatal — the controller's
// implementations map makes it so, the tf runner runs on defaults+env).
func Load(basePath, perImplPath string, into any) (found bool, err error)

// Env applies explicit env-var bindings (highest precedence), logging each use —
// preserving today's "Using X from environment" lines verbatim. No reflection (P2).
func Env(log *slog.Logger, bindings ...EnvBinding)
type EnvBinding struct{ Var string; Target *string }

// Api is the shared API section. Both yaml spellings keep working:
// tf uses `user` (tfrun/config.go:34), controller uses `username` (config.go:37).
type Api struct {
    Url          string `yaml:"url"`
    Username     string `yaml:"username"`
    User         string `yaml:"user"` // alias, normalized into Username after load
    Password     string `yaml:"password"`
    ClientId     string `yaml:"clientId"`
    ClientSecret string `yaml:"clientSecret"`
}
// NewAuthProvider: API key wins when complete; else Basic when complete; else nil.
// (tf semantics — the controller's unconditional-BasicAuth variant is unreachable
// post-validation, verified against validateApiAuth, controller/config.go:212-235.)
func (a Api) NewAuthProvider(fallbackURL string) meshapi.AuthProvider
// Validate reproduces the per-field error messages of controller/config.go:212-235,
// with a mode flag for the tf single-run exemption (tfrun/config.go:190-213).
func (a Api) Validate(context string, required bool) error
```

**Compatibility alias table (every existing env var and yaml key keeps working):**

| Existing name | Persona | Phase-3 handling |
|---|---|---|
| `RUNNER_CONFIG_FILE` | tf | primary, unchanged |
| `RUNCONTROLLER_CONFIG_FILE` | controller | alias of `RUNNER_CONFIG_FILE`; keeps working, logs a deprecation warning when used (D7) |
| `RUNNER_UUID`, `RUNNER_API_URL`, `RUNNER_API_USERNAME`, `RUNNER_API_PASSWORD`, `RUNNER_API_CLIENT_ID`, `RUNNER_API_CLIENT_SECRET`, `RUNNER_PRIVATE_KEY_FILE` | tf | unchanged bindings via `Env` |
| `RUNNER_API_CLIENT_ID`, `RUNNER_API_CLIENT_SECRET` | controller | unchanged (incl. the half-configured warning, `config.go:186-204`) |
| `EXECUTION_MODE`, `RUN_JSON_FILE_PATH` | tf | unchanged (frozen k8s contract, D9) |
| `PORT` (tf healthz 8100) | tf | untouched this phase (D12 → phase 4) |
| yaml `api.user` vs `api.username` | both | both accepted via the `Api` alias, normalized after load |
| all other yaml keys (`timeoutMins`, `namespace`, `implementations`, …) | both | unchanged, persona-side |

The controller gains nothing new (no new env overrides beyond aliases): env-first
*growth* (e.g. `RUNNER_UUID` for the controller) is deferred to phase 4 where the persona
config surfaces are documented together — this phase only unifies mechanics without
changing any persona's effective configuration behavior.

### 5.4 `report` — the shared reporting facility (D4)

Everything below moves from `internal/tf` (post-plan-02 shapes, §3.6). Each seam's
second consumer is named (P3):

```go
type ExecutionStatus int // PENDING, IN_PROGRESS, SUCCEEDED, FAILED, ABORTED — moved as-is
// ABORTED (terminal): reported when an in-flight run is cancelled on shutdown so the
// coordinator never sees a stale IN_PROGRESS (D9 graceful-shutdown, plan-05 H7). Verified
// against meshfed-release: the runner-facing PATCH .../status/source/{sourceId} endpoint
// accepts inbound ABORTED and persists it terminal (block-runner-core
// ExecutionStatus.ABORTED); accepted transition is IN_PROGRESS->ABORTED, and an
// already-aborted run returns 409 {runAborted:true} (treat as success). The tf runner never
// emitted ABORTED before.
type RunStatus struct {
    RunId            string
    Status           ExecutionStatus
    Steps            []StepStatus // values (plan 02 B10 fix)
    CurrentStepIndex int
    Summary          *string
    Artifact         []byte
}
type StepStatus struct {
    Name, DisplayName string
    Status            ExecutionStatus
    Outputs           map[string]Output // generalized from map[string]*TfOutput
    UserMessage       *string
    SystemMessage     *string
    LogStartIdx       int64
}
type Output struct { Value any; Type string; Sensitive bool } // mirrors meshapi.OutputDTO

// Progress: the phase-2 tracker, verbatim (mutate under lock / deep-copy snapshot).
type Progress struct{ /* mu, status */ }
func (p *Progress) Mutate(f func(*RunStatus))
func (p *Progress) Snapshot() RunStatus

// RunLog: file-backed live log with size tracking + segment reads (phase-2 shape).
func NewRunLog(logger *slog.Logger, path string) *RunLog

// Reporter is the ONE unified status backchannel port, consumed by ALL five runners
// (tf plus the four phase-6 ports) — identical to plan 02's StatusReporter, relocated
// here. The tf meshapi adapter, the phase-6 handlers, and phase-5's dispatcher all
// implement/consume this SAME interface: there is no separate "event-driven" reporter
// for the ports — they use this Reporter, simply passing changed steps and discarding
// the abort return (see the Reporter rules below).
type Reporter interface {
    Register(RunStatus) error
    Report(RunStatus) (abort bool, err error)
}

// Observer runs the status ticker for one run — TF-ONLY (the four ports run no Observer).
// Every Interval it computes the per-send diff (the running step whose log grew, plus any
// steps that changed status; the fail-not-finished-steps event includes the current step
// plus all later steps) and Report()s ONLY those changed steps; the abort response cancels
// the run context; on completion it sends the final update with the async mapping
// (SUCCEEDED => IN_PROGRESS) and skips it when ctx is cancelled.
// D9 pins: 10s ticker, abort-cancel, async IN_PROGRESS, no-final-after-cancel.
type Observer struct { Interval time.Duration; Reporter Reporter; Async bool; Log *slog.Logger }
func (o Observer) Run(ctx context.Context, cancel context.CancelFunc, p *Progress)

// ToStatusUpdate parametrizes today's tf-only DTO mapping (dtos.go:164-174) by source
// and run type instead of hardcoding TERRAFORM.
func ToStatusUpdate(s RunStatus, source string, t meshapi.RunType) (meshapi.RunStatusUpdateDTO, error)
```

**Unified `Reporter`** — one interface (declared above), consumed by all five runners:

- `Report(RunStatus)` transmits **only the steps present** in `RunStatus.Steps` (the
  changed/new steps since the last send). The meshfed runner-facing status endpoint
  **UPSERTS steps by id** (verified: `BlockRunSourceUpdateService` merges by id, never
  replaces the collection), so sending a subset is safe — it is exactly how the ported
  runners already report (ado `ado-stage-*`, github `gh-workflow-job-*`).
- Messages are **cumulative-replace**: each step included in a `Report` carries its FULL
  current message text, and the backend overwrites `step.userMessage`/`systemMessage` by
  **assignment (not append)**. So a diff never sends incremental log chunks — it sends the
  changed step(s) with full current message. (Switching to append-deltas would require a
  coordinated backend change and is explicitly out of scope.)
- **tf is the only consumer of the `Progress`+`Observer` 10s ticker:** the Observer
  computes the per-send diff (§5.4 above) and honors the returned `abort` flag. The four
  ported runners run **no Observer** — they call `Report` on state changes only, own their
  dedup, and **discard** the abort return.
- **Phase-3 sequencing:** the unified interface lands in this phase (§6 step 9). tf is
  reduced from full-snapshot sends to changed-steps-only (diff) sends **in phase 3 too** —
  a deliberate, **flagged** wire change that is backend-result-identical (upsert +
  cumulative-replace make the subset send persist the same state), so the acceptance suite
  stays green while tf's phase-1 transcript pins are updated for the diff shape.

Second consumers, named from the high-level plan: **phase 6** `manual`/`gitlab`/
`azdevops`/`github` handlers ("plugged into the engine + reporting facility (async
handover semantics from D9)", §5 phase 6) need `Progress`+`Reporter`+`ToStatusUpdate`
(but **no** `Observer` — they call `Report` on state changes); **phase 5**
`InProcessDispatcher` needs per-run `Progress`/`RunLog` isolation (risk #4). What does **not** move (single consumer, tf): step tables and
`StepId` constants, `TfOutput` collection (tf maps into `report.Output` at the boundary),
log-segment *content* rules tied to tf steps, `EP_State`, run-token lifecycle, the
engine and manager. The controller's `reportRunFailure` (single `validation` step,
`StatusUpdateDTO` body) intentionally does **not** adopt `report` — its PATCH body shape
is a different frozen contract (§3.1) and a one-step status needs no ticker.

### 5.5 Registration outlook (D5) — what this phase does and does not build

This phase moves the **transport** (PUT + v1-preview media type) into `RunnerClient` and
keeps the **content** persona-side: `BuildRunnerRegistrationDTO` (WIF subject pattern,
namespace coupling, `ImplementationType: ALL` — `controller/dtos.go:12-52`) stays a
controller concern, as does the 404→actionable-message mapping and `main.go`'s 10-minute
outer retry loop. No capability-config API is introduced now: D5's "explicit config
(one concrete type or `ALL`)" arrives with the dispatcher in phase 5, and the DTO already
carries the field (`MeshBuildingBlockRunnerSpecDTO.ImplementationType`,
`meshapi/dtos.go:314-319`) — nothing speculative is needed (P3).

### 5.6 D12 split — what lands here vs phase 4 (explicit, as instructed)

**Phase 3 lands exactly one thing:** metrics construction becomes injectable.

```go
// controller package (prometheus dependency stays out of the shared module)
func NewMetricsCollector(reg prometheus.Registerer) *MetricsCollector
```

The `metricsInstance`/`metricsOnce` singleton (`metrics.go:10-13,65-66`) dies; `main.go`
constructs one collector against `prometheus.DefaultRegisterer` and injects it into
`Controller`, `RunApiClient` and the registration path (today three `NewMetricsCollector()`
call sites alias the singleton, §3.3.4); tests use fresh `prometheus.NewRegistry()`
instances instead of relying on the `sync.Once`. Metric names, labels, and the `:2112`
`/metrics` endpoint are **byte-identical** (scrape-visible surface; operators' dashboards
depend on it).

**Phase 4 lands the rest of D12:** the `MANAGEMENT_PORT` listener serving `/healthz` +
`/metrics` on one port per persona (controller 2112 gaining healthz, tf 8100, …), the
generic runner metrics for standalone personas (runs claimed/succeeded/failed, run
duration, poll errors), and any shared metrics package. Rationale: those metrics have no
consumer until the persona binaries exist (phase 4), and a shared prometheus-dependent
package created now would be speculative (P3) and would bloat `go-meshapi-client` for
nothing. The injectable-`Registerer` seam is the only plumbing phase 4 needs from us —
stated here so phase 4's plan can assume it (its instruction already owns
"MANAGEMENT_PORT unification incl. per-persona defaults and the new standalone-runner
metrics", high-level §7).

---

## 6. Controller re-base & migration sequence (always-green)

Rules: after every step `task test` + `task lint` green, `task coverage` ≥ gate; record
the number per working commit (squashed on merge). Characterize before refactoring.

| # | Step | What changes | What proves it |
|---|---|---|---|
| 0 | **Preflight.** Run all §1 verifications on the phase-2b branch; branch `phase-3-shared-core`. | nothing | A1–A10 verified (STOP-A); tf coverage number recorded |
| 1 | **Controller characterization (tests only).** (a) Fake-`RoundTripper` transcript tests for `RunApiClient`: fetch node-id `run-controller-<uuid>`, V1 media types, base64 passthrough of raw claim bytes, register body (single `validation` step), `StatusUpdateDTO` PATCH shape; (b) same for `RegistrationApiClient`: PUT URL, v1-preview media type both headers, WIF-body golden (extends `dtos_test.go`), 404 ⇒ "create it via the meshStack UI" error, non-200 error path; (c) **k8s Job manifest golden** via `client-go`'s `kubernetes/fake` clientset (already an indirect dep — no new module): labels, `BackoffLimit:1`, `TTLSecondsAfterFinished:120`, volumes/mounts incl. `/var/run/secrets/meshstack`, env (`RUN_JSON_FILE_PATH`/`RUNNER_UUID`/`RUNNER_API_URL` and **no** `EXECUTION_MODE` — plan 02 §11.3), Secret+ServiceAccount shapes, secret-owner-reference, `CountActiveJobs` finished-job filtering. | `_test.go` only | `runapi.go`/`registration.go`/`kubernetes.go` leave 0%; `git diff -- ':!*_test.go'` empty for this step |
| 2 | **`HttpError`.** Introduce in `meshapi`, migrate every producer/consumer, delete `StatusError`. | `meshapi/client.go`, `controller/controller.go:244-250`, `controller/runapi.go:51-55`, tf fetch classification, tests' error construction (declared retarget §6.1.1) | full suites green; step-1 transcripts unchanged |
| 3 | **Retry transport.** `meshapi/retry.go` per §5.2.3 + constructor default policy; ApiKeyAuth's login client gets the same transport (login POST whitelisted). New tests: attempt-counting fake transport (503×N then 200; Retry-After honored; 500 not retried; claim POST never retried; PATCH never retried). | `meshapi` | new retry tests green; **all** phase-1 tf pins + step-1 transcripts byte-identical (STOP-D) |
| 4 | **Identity de-global.** `Identity` into constructors; delete `SetClientMetadata`/`runnerName`/`runnerVersion`; wire both mains + tf adapter. (If A3 found `crypto.Crypto` still alive, delete it here too.) | `meshapi/client.go:26-44`, both `main.go`s, tf meshapi adapter | grep: zero package-level `var` with mutable state in `meshapi`; header pins (phase-1 CP2, step-1) green |
| 5 | **Client split + registration move.** `RunClient`/`RunnerClient` per §5.2.4; move `EP_RunnerWithUuid` + v1-preview media type + PUT semantics out of `controller/registration.go`; controller re-based: `registration.go` keeps DTO build, 404 mapping, logging; its `http.Client`, `setHeaders`, `putController` die. | `meshapi`, `controller/registration.go`, `controller/runapi.go:35-37` | step-1 transcript pins prove wire-identical; `RegistrationApi` unit tests green |
| 6 | **`config` package.** Extract mechanics + `Api` per §5.3; re-base `internal/tf` config (phase-1 CP9 tests green unchanged); re-base controller: `LoadControllerConfig(logger) (ControllerConfig, error)` returns a value, fatal-on-missing-file preserved at the `main.go` call site. | new `config`; `internal/tf` config file; `controller/config.go` | CP9 + `config_test.go` assertions unchanged; new `config` package tests (§9) |
| 7 | **Controller de-global.** Delete `AppConfig` (inject `ControllerConfig` into `Controller`, `RunApiClient`, registration, `KubernetesClient` — its ~15 read sites become fields/params), `DiscoveredOidcIssuer` (main passes it as a parameter), `UseTestClient` (dead switch, deleted — flag §12.2), metrics singleton (§5.6). | `controller/*.go`, `run-controller/main.go` | all controller tests green with harness-only changes (§6.1.2); grep: zero package-level mutable state in `controller` |
| 8 | **Decryption move.** `DecryptRunDetails` + `Decryptor` into `meshapi` (§5.2.5); `crypto` gains the `Decrypt` method; controller wraps its instance; tests move; tf's phase-2 `Decryptor` port aliases/retargets to the shared interface (same shape — no assertion change). | `controller/decryption.go` (dies), `meshapi`, `crypto`, `internal/tf` port decl | moved `decryption_test.go` green; tf CP4 crypto scenarios green |
| 9 | **`report` extraction.** Move `Progress`/`RunLog`/`RunStatus`/`StepStatus`/`ExecutionStatus`/observer/`ToStatusUpdate` per §5.4; `internal/tf` re-imports; `Output` generalization at the tf boundary; **new direct `report` tests** (coverage is per-package — tf's scenario suite stops counting for the moved lines, §9). | `internal/tf` shrinks; new `report` | full tf phase-1 suite green (PATCH bodies byte-identical); `report` ≥90 on its own tests; tf gate still ≥90 (STOP-C) |
| 10 | **Gate + lint debt.** thresholds.txt gains the shared-package lines (§9); delete the `.golangci.yml` `controller`/`meshapi` exclusion blocks and fix the ~11 pinned errcheck/staticcheck/unparam findings under the new tests (A6); depguard rules for `config`/`report` (§5.1). | tooling + small prod fixes | `task lint` green with blocks gone; induced-failure check on each new thresholds line (temporarily set 99 → fails → revert) |
| 11 | **Acceptance + self-review gate.** Run the meshfed-release local-dev-stack flow (tf runner from this branch in polling mode: claim/status/artifact wire + registration-free path) and at least one MANUAL + one TERRAFORM acceptance run; P1–P8 walk; PR description lists the retry-policy decision (§5.2.3), the D12 split (§5.6), and the §12 flags. | — | evidence in PR description; k8s contract proven by the step-1 manifest goldens (the controller has no e2e in-repo — flag §12.3) |

11 steps + preflight. Riskiest step: 9 (coverage relocation; the compensating `report`
tests are planned, STOP-C guards the residue).

### 6.1 Declared test retargets (assertions never change; beyond this list = STOP-B)

1. Error-type construction in tests: `&meshapi.StatusError{Status: 404}`
   (`controller_test.go:107`, `controller_capacity_test.go:47` etc.) becomes
   `meshapi.HttpError{StatusCode: 404}` — the asserted behavior (no-run classification)
   is unchanged.
2. Controller test harness: `AppConfig = &ControllerConfig{…}` + save/restore
   (`controller_test.go:76-103`) becomes value construction + injection; fresh
   `prometheus.NewRegistry()` per test instead of the singleton.
3. tf harness: whatever plan-02 helper builds the reporter/config now builds them from
   `report`/`config` types (one import path change in the helper; scenario assertions
   untouched).
4. `decryption_test.go` moves with its code (package changes, assertions identical).

---

## 7. Alignment notes vs the provider client (for the future SDK merge, D3)

| Concern | Provider (`terraform-provider-meshstack/client`) | This repo after phase 3 | Merge cost later |
|---|---|---|---|
| Error type | `HttpError{StatusCode, ResponseBody}` + `Is*` | identical shape & name | none |
| Retry | `retryRoundTripper` + `RetryOptions{MaxRetries, Backoff, WhitelistedPaths}` | same design, unexported, smaller budget, `WhitelistedPosts` | trivial (merge = keep provider's, port budget) |
| Auth | `Authorization.Header(ctx, c) (string, error)` | `AuthProvider.AuthHeader() string` kept (phase-1 pins cover the empty-header branch; changing the failure surface is not behavior-preserving) | small; adapter or signature migration at merge time — recorded delta |
| Client layout | aggregate of per-resource clients over one `HttpClient` | `RunClient`/`RunnerClient` over one `http.Client` | naming maps 1:1 (`RunnerClient` ≈ `MeshBuildingBlockRunnerClient`) |
| Media types | computed `application/vnd.meshcloud.api.<kind>.<version>.hal+json` (`mesh_object_client.go:72-74`) | constants (two endpoints) | mechanical |
| Request plumbing | generic `DoRequest[R]` + `RequestOption` | hand-rolled per endpoint (4 endpoints; P2 — generics don't pay yet) | isolated to method bodies |
| Version check | `MinMeshStackVersion` at startup | deliberately absent (wrong for runners, D3) | n/a |
| Client timeout | 5 min whole-client | none (128MiB artifact streams; contexts arrive with later phases) | recorded delta |

---

## 8. Frozen contracts touched (D9/D10)

**Preserved byte-identically and now pinned by tests:** claim POST endpoint + V1 media
type + `X-Block-Runner-Node-Id` formats (`run-controller-<uuid>`, `<uuid>-worker-N`) +
`User-Agent`/`X-Meshcloud-Runner-*`; 404/409-claim = no run; 409-register = success;
runner-registration **PUT with `…meshbuildingblockrunner.v1-preview.hal+json`** (moved,
not changed — the media-type constant relocates into `meshapi`); both PATCH body shapes
(`StatusUpdateDTO` controller / `RunStatusUpdateDTO` tf) — no unification; 128MiB
artifact cap; abort flag semantics; 10s ticker + async `IN_PROGRESS` mapping (relocated
into `report`, pinned by moved+new tests); the **entire k8s Job contract**
(`RUN_JSON_FILE_PATH`, `/var/run/secrets/meshstack/run.json`, `RUNNER_UUID`,
`RUNNER_API_URL`, no `EXECUTION_MODE` injection, labels, WIF volumes, secret handling) —
untouched and newly golden-pinned (step 1c); metrics names/labels + `:2112`; tf healthz
`PORT`/8100; `go run .` layouts of both mains (D10); mux claim contract (headers pass
through unchanged).

**Changed with sanction:** retry behavior on 429/502/503/504/transport errors for
idempotent + whitelisted calls (high-level phase-3 mandate, D3; §5.2.3 policy; STOP-D
guards transcript neutrality everywhere else). Old-controller/new-runner and
new-controller/old-runner mixes (D10) are unaffected: the wire shapes are identical and
retries are invisible to the peer beyond duplicate idempotent requests.

---

## 9. Test plan & D6 gate extension

**Gate (thresholds.txt) after this phase:**

```
github.com/meshcloud/building-block-runner/tf-block-runner/internal   90   (existing)
github.com/meshcloud/building-block-runner/go-meshapi-client/meshapi  90   (new)
github.com/meshcloud/building-block-runner/go-meshapi-client/crypto   90   (new)
github.com/meshcloud/building-block-runner/go-meshapi-client/config   90   (new)
github.com/meshcloud/building-block-runner/go-meshapi-client/report   90   (new)
```

No exclusion-list entries for the shared packages: all are hermetically testable (fake
transports, repo test keys `tf-block-runner/resources/test.pem`/`test.key`, temp files,
fake clock/reporter). Baselines to lift: `meshapi` 39.3% → ≥90 (steps 1–5, 8: transcript,
retry, error, registration, decryption tests), `crypto` 71.4% → ≥90 (key-parsing branch
tests + the new `Decrypt` method), `config`/`report` born ≥90 (steps 6/9).

**`report` needs direct tests** (step 9): coverage is per-package, so the tf scenario
suite exercising it no longer counts (the `util.SortedByKeys` lesson, plan 01 §3).
Specified: `Progress` mutate/snapshot isolation (plus `-race`, A5), `Observer` with fake
`Reporter` + short interval — ticks carry snapshots, abort cancels, async final mapping,
cancelled-ctx skips final, report-error path; `RunLog` write/segment/nil-safety
(post-2b `(nil, error)` shape); `ToStatusUpdate` golden vs today's tf PATCH JSON.

**The `controller` package does not join the gate in this phase** — interpretation of
D6 ("gate extends to every new domain/application package"): `controller` is not a new
package, and ~55% of its statements are the real-I/O k8s adapter. Its **application
logic** joins the gate in phase 5 when it is restructured into `dispatch` (+`k8sjob`
exclusions). Phase 3 still raises it materially (step 1 characterization: `runapi.go`,
`registration.go` and the manifest-shape parts of `kubernetes.go` leave 0%). Flagged for
review (§12.4) rather than silently decided.

**Behavior-unchanged proof for the controller:** existing suites with assertions
untouched (§6.1) + the step-1 transcript/golden pins written against the *old* code and
kept green through steps 2–10.

## 10. Rollback story

One squash commit on a stacked branch: `git revert` restores `StatusError`, the
controller globals and its private registration HTTP path, deletes `config`/`report`,
and reinstates the thresholds/lint state of phase 2b. No image names, ports, env vars,
config keys, metric names, wire shapes, or k8s contracts change (§8), so rollback is
purely local; the retry transport disappears with the revert. The controller keeps
working against any meshfed throughout (D10) because the wire is frozen.

## 11. Cross-repo touch points

- **meshfed-release:** read-only. local-dev-stack starts the tf runner via `go run .` in
  `tf-block-runner/` — path unchanged; the mux claim contract sees identical headers.
  Step 11 uses the acceptance flow as the outer net. No doc updates required this phase.
- **terraform-provider-meshstack:** pattern source only (retry/error/layout; §7 records
  the deliberate deltas for the later `meshstack-go-sdk` merge). No import, no edit.
- **meshStack/meshfed API:** untouched; the registration PUT keeps requiring a
  pre-created runner object (404 message contract).

## 12. Flags — findings the high-level/prior plans did not anticipate

1. **Go's `internal/` visibility rule forecloses the "create the root module early" option**:
   `internal/*` cannot be imported from the legacy modules, so D11's target layout
   is unreachable until phase 4 moves the mains. Shared packages therefore transit
   through `go-meshapi-client` (§5.1) — the high-level plan never says where phase-3
   packages live; this plan decides it.
2. **`controller.UseTestClient` is a dead switch** — declared at `runapi.go:13`, read at
   `registration.go:113` and `config.go:284`, **written nowhere** in the repo. Deleted in
   step 7 (removing a package-level mutable global that cannot even be enabled).
3. **The controller has no automated end-to-end anywhere** (local-dev-stack exercises the
   polling runners, not k8s dispatch), and its wire adapters have 0% coverage. "Behavior
   unchanged (controller tests + acceptance suite)" from the high-level plan therefore
   *requires* the step-1 characterization layer this plan adds, incl. `client-go` fake-
   clientset manifest goldens — a bigger test investment than the phase description
   implies.
4. **D6 wording vs the `controller` package**: not gated this phase (justification §9);
   reviewer may override.
5. **"Polling/claim engine" does not move** (§2 out-scope): only the claim client,
   error classification and reporting engine are shareable today without a speculative
   abstraction (P3); loop unification is phase 5's dispatcher. Deviation from the
   phase-3 sentence in §5 of the high-level plan — flagged, not silent.
6. **yaml key split `api.user` vs `api.username`** between the two personas — D7's
   "all existing file keys keep working" forces the alias in the shared `Api` (§5.3);
   neither prior plan noticed the divergence.
7. **Retry must exclude the claim POST and the status PATCH** (§5.2.3) — D3's
   "retry/backoff with 503-riding budget" applied naively would double-claim runs and
   delay abort detection. The policy, not the mechanism, is the real design work.
8. **`TestRegister_409Conflict_ReturnsNil` lives in
   `tf-block-runner/tfrun/runapi_status_test.go:83`** (not in
   `go-meshapi-client/meshapi/client_test.go`); plan 01's citation is correct. A9 verifies
   what phase 1 adds to the `go-meshapi-client` test files this phase inherits.
9. **Metric names are a de-facto public surface** (operator dashboards scrape
   `run_controller_*` on :2112) — frozen here; D12's phase-4 work must treat renames
   like env-var renames (alias/deprecation thinking), which D12 does not currently say.

## 13. Open questions

None open. Reviewable decisions are recorded at their sites: the retry budget and
whitelist (§5.2.3, STOP-D), the `controller`-not-gated interpretation (§9, flag §12.4),
the `UseTestClient` deletion (§12.2), and the D12 split (§5.6).
