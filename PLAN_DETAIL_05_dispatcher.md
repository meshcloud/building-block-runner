# Detail Plan 05 — Dispatcher Abstraction & In-Process Concurrency (Phase 5)

**Phase:** 5 · **Branch:** `refactor/single-go-binary/phase-5-dispatcher` (stacked on
`refactor/single-go-binary/phase-4-single-binary`) · **Delivery:** one single-commit PR
(§5 high-level plan) · **Binding:** §3 P1–P8, D4 (ports), D5 (dispatcher = capability
registry, claim-and-fail-fast), D6 (gate growth), D9/D10 (frozen contracts incl. the k8s
single-run contract and async IN_PROGRESS handover), D11 (`internal/dispatch`,
`internal/k8sjob`), D12 (metrics) of `PLAN_HIGH_LEVEL.md`; risks #4 (in-process
concurrency) and #5 (secret hygiene) drive §7 and §8.

Phase character: **behavior-preserving for the run-controller persona** — the k8s dispatch
path stays **bit-identical** (claim wire, decryption order, Job/Secret/ServiceAccount
manifests per the plan-03 goldens, registration PUT, metrics names), **with one sanctioned
exception**: the compiled-in `maxConcurrentJobs` default changes 20 → 10 (§12, §16). The tf persona's
polling loop is restructured onto the same dispatch loop with in-process execution; its
wire pins stay green, with a short list of sanctioned, flagged deltas (§12). All code
references are `main` @ `c3fce61` unless marked *post-N* (shape promised by plan N).

---

## 1. Assumptions from prior phases

Plans 00–04 are **not implemented yet**; everything below is a promise, not a fact.
Implementation of phase 5 **begins by running every verification step**. Any material
failure is a **STOP**: update this plan (and cascading plans) first, get the revision
reviewed, then resume.

| # | Assumption | Promised by | Verification step |
|---|---|---|---|
| A1 | Phase-4 promise set holds: module `github.com/meshcloud/building-block-runner` at the **repo root** (no `./runner` subdir), no go.work/workspace, personas as **per-persona binaries** (the phase-4 fit binary `cmd/tf/main.go` — linking only its persona's deps — plus the `cmd/bbrunner` superset binary that links all handlers + both dispatchers; the four further runner mains `manual`/`gitlab`/`github`/`azdevops` arrive in phase 6; no argv[0] multiplexing / persona registry; no separate `cmd/controller` — `cmd/bbrunner` **is** both the controller and the superset (= the `run-controller` image) and auto-detects its dispatcher at startup (`rest.InClusterConfig()`/`KUBERNETES_SERVICE_HOST` ⇒ k8sjob; else ⇒ InProcess; `RUNNER_DISPATCHER` overrides)); packages `internal/{tf,gitsource,tofu,meshapi,crypto,config,report,mgmt,controller,build}`; binaries present at phase 4 `cmd/{tf,bbrunner}` (phase 6 adds `manual`/`gitlab`/`github`/`azdevops`); task targets `test`/`lint`/`coverage`/`fmt`/`tidy`/`start:*`. | Plan 04 §11 | `git checkout refactor/single-go-binary/phase-4-single-binary && ls internal && task test && task lint && task coverage` |
| A2 | `internal/controller` is the verbatim post-phase-3 controller code (drain loop `drainRuns`/`availableCapacity`/`processNextRun`/`reportRunFailure`, `JobManager` seam, `KubernetesClient`, de-globaled config/metrics), moved not rewritten, and declared transitional for this phase. | Plan 04 §5 step 1, §10.3 | `git diff --find-renames phase-3..phase-4 -- '**/controller/'` shows moves; read `internal/controller/controller.go` against §3.1 |
| A3 | Controller wire-characterization tests + k8s Job manifest goldens (fake clientset) exist and are green: claim transcript incl. node-id `run-controller-<uuid>`, registration PUT + v1-preview media type + WIF golden, `StatusUpdateDTO` PATCH shape, Job/Secret/SA goldens incl. `/var/run/secrets/meshstack` mount, `RUN_JSON_FILE_PATH`/`RUNNER_UUID`/`RUNNER_API_URL` env, no `EXECUTION_MODE`, `CountActiveJobs` finished-job filtering. | Plan 03 §6 step 1 | run the controller test files; `grep -rn "golden" internal/controller` |
| A4 | tf code has the plan-02/03 shape: one `tf.Engine.Execute(ctx, run)`; ports `StatusReporter`(→`report.Reporter`)/`ArtifactSource`/`Decryptor`(shared in `meshapi`)/`TfProvider`/`Source`/`Clock`; `Manager` polling loop with token protocol `work/done/norun/failed/stop/stopped`, delays 10s/60s, `atomic.Bool` shutdown; `handleFetchRunError` semantics verbatim incl. the chunked-transport string match. | Plan 02 §5.3/§5.4, Plan 03 §5.2.5 | `grep -rn "func (e Engine) Execute\|norun\|chunked" internal/tf` |
| A5 | Shared client is split: `meshapi.RunClient` (claim/register/patch/artifact), `meshapi.RunnerClient.Update` (registration PUT, 404 ⇒ actionable error), `HttpError{StatusCode,ResponseBody}` with `IsNotFound/IsConflict`, `meshapi.DecryptRunDetails(runJsonBase64, dec Decryptor)` with all five impl-type branches, `NodeId` typed. Claim POST and status PATCH are **never retried** by the retry transport. | Plan 03 §5.2 | `grep -rn "RunnerClient\|DecryptRunDetails\|WhitelistedPosts" internal/meshapi` |
| A6 | `report` package: `Progress` (mutate-under-lock, deep-copy `Snapshot()`), `RunLog`, `Observer` (10s ticker, abort-cancel, async `SUCCEEDED→IN_PROGRESS`, no-final-after-cancel), value-typed `Steps []StepStatus` (B10 fixed). `-race` is ON; zero package-level mutable state anywhere. | Plan 02 §5.5/2b R1, Plan 03 §5.4 | read `internal/report`; `grep -rn '\-race' Taskfile.yml .github/workflows/`; `grep -rn "^var [A-Z]" internal --include='*.go' \| grep -v _test` |
| A7 | `mgmt.RunMetrics` (`runner_runs_claimed_total`, `runner_runs_succeeded_total`, `runner_runs_failed_total`, `runner_run_duration_seconds`, `runner_poll_errors_total`, all labeled `runner_uuid`) exists; the tf polling loop drives it via a consumer-side meter interface declared in `internal/tf`; success/failure classification is keyed on `Engine.Execute`'s error return. **Record the exact classification** — this plan's handler metrics (§10) must reproduce it. | Plan 04 §4.3 step 4 | read `internal/mgmt` + the meter interface + its polling-loop hooks |
| A8 | Coverage thresholds are per-package lines at 90 for `tf,gitsource,tofu,meshapi,crypto,config,report,mgmt`; `internal/controller` is deliberately **ungated** (its gating was deferred to this phase); exclusions name `gitsource/git.go`, `tofu/tfbinaries.go`. | Plan 04 §7.1, Plan 03 §9 | `cat tools/coverage/thresholds.txt tools/coverage/exclusions.txt && task coverage` — record numbers |
| A9 | Single-run exit semantics are the 2b-R12 conditional form (non-zero only when no terminal status was reported), and plan 02 R12 deferred the k8s `BackoffLimit: 1` alignment question **to this phase** (§16.3 resolves it). | Plan 02 §7 R12 | read `persona_tf.go` single-run tail; note the exact condition |
| A10 | `TfBinaries` (post-04 `internal/tofu`) still guards install with a struct-embedded `sync.Mutex` around the stat/remove/install sequence, and `testMode` bypasses the mutex early. | unchanged since `main` (`tfbinaries.go:33,92-93,83-90`) | read `internal/tofu/tfbinaries.go` |
| A11 | `ClearRunToken`/`SetRunToken` pins exist in their phase-2 retargeted form (polling adapter clears after execution; single-run sets from the run spec) — the **single shared mutable token** design is still in place. | Plan 02 §5.4, `tfrun/runapi.go:73-80` today | `grep -rn "ClearRunToken\|SetRunToken" internal/tf` |
| A12 | The depguard rules use `…/internal/…` prefixes (`github.com/meshcloud/building-block-runner/internal/...`); only `mgmt` + `controller` (+ main) may import prometheus; only main wires adapters. | Plan 04 §4.6 | read `.golangci.yml` |

**STOP-A (before any coding):** any of A1–A12 materially false ⇒ update this plan first.
**STOP-B (any time):** a characterization/golden/transcript assertion must change beyond
the declared retargets in §11.2 — stop, record, review, resume. The k8s Job goldens (A3)
changing **at all** is an automatic STOP-B.
**STOP-C (any time):** a step drops any gated package below 90 without a planned
compensating test step (§11) — do not touch `exclusions.txt`; replan.
**STOP-D (step 7):** the concurrent scenario suite or `-race` reveals a shared-state
hazard **not** in the §7 inventory — a new inventory entry, not a drive-by fix: stop,
extend §7 with a named test, review, resume.
**STOP-E (step 9):** the meshfed-release local-dev-stack acceptance flow or the
N-concurrent smoke fails — D10's outer net; diagnose/replan before merging.

---

## 2. Scope

**In:**

- New package `internal/dispatch`: the generalized claim/drain loop (`Loop`,
  extracted from `controller.Controller`), the `Dispatcher` and `RunHandler` interfaces,
  `InProcess` dispatcher (go-func per run, in-flight counter, completion wake, drain on
  shutdown), typed failure errors (`UnhandledTypeError`), claim-error classification
  ports, the loop-side fail-fast reporting (today's `reportRunFailure` pattern).
- New package `internal/k8sjob`: today's `KubernetesClient` as the
  `KubernetesJobDispatcher` (manifest building, Secret/SA handling, `RunTooLargeError`,
  `CountActiveJobs`, per-run decryption before Job creation — moved, not changed),
  `JobSpecTemplate`+volume/toleration config types, `DiscoverOIDCIssuer`, the WIF
  registration-DTO builder.
- **`internal/controller` is dissolved** into the two packages above + persona
  wiring (§5); the transitional package named by plan 04 §10.3 ceases to exist.
- tf persona cutover: `Manager` + poll worker + polling `RunSource` adapter are replaced
  by `dispatch.Loop` + `dispatch.InProcess` + a `tf` run handler; per-run
  reporter/artifact clients with runToken-only auth (kills the shared mutable
  `SetRunToken`/`ClearRunToken` slot, §8); per-run logger prefixes.
- New config, all **additive**: tf persona `maxConcurrentRuns` (**default 3** — the
  standalone tf runner executes up to 3 runs concurrently by default, an intentional
  throughput improvement, **not** byte-identical to today's single serial worker; env
  `RUNNER_MAX_CONCURRENT_RUNS` overrides; negative = unlimited with the 10-per-cycle
  backstop; `maxConcurrentRuns=1` reproduces today's exact serial cadence for operators
  who want it — sanctioned behavior change §12), optional tf persona
  `registration:` section (displayName, ownedByWorkspace, publicKey, capability) enabling
  standalone self-registration incl. `ALL`; controller `capability:` key (default `ALL`,
  preserving today's hardcoded value, `controller/dtos.go:23`).
- Claim-and-fail-fast for unhandled types (D5) on both dispatchers, with the failure-path
  spec of §10.
- Concurrency-hazard test suite (§7), one named test per inventory entry; `-race` stays on.
- D6 gate extension: `dispatch` and `k8sjob` join the gate (§13); depguard rules for the
  new packages; thresholds/exclusions updated in lock-step.
- Small metrics additions sanctioned by D12 (additive names only, §10.3).

**Out (deferred, with destination):**

- **Mixed dispatch in one process** (some types in-process, others as k8s Jobs from the
  same loop). The `Dispatcher` seam makes a composite trivial, but no persona needs it in
  phase 5 and building it now is speculative (P3). Deliberate narrowing of D5's
  per-run-routing sentence — flagged §16.1, not silent. Destination: phase 6 revisits
  when the first non-tf handler exists.
- New `RunHandler` implementations beyond TERRAFORM → **phase 6** (this plan fixes the
  interface the phase-6 template PR reviews against all four runner inventories, §4.3).
- Any change to the single-run (k8s Job) execution path of the tf persona — frozen
  contract; it keeps its own glue (`executeSingleRun` shape) untouched. A possible
  later unification with the tf handler is noted for phase 7 (§16.7).
- k8s Job `BackoffLimit` change — resolved as "keep 1" (§16.3), no code change.
- CI reshape, docs beyond truthful config/README updates → phase 7.
- Cross-repo SDK extraction (high-level §8).

---

## 3. Research evidence — current state

### 3.1 The controller loop that gets the Dispatcher seam

- `run-controller/controller/controller.go:18-21` — `JobManager` is already a
  consumer-side seam: `CreateRunnerJob(runInfo, runJsonBase64, implType, jobSpec,
  metrics) error` + `CountActiveJobs() (int, error)`. This phase widens it into
  `Dispatcher` (§4.1).
- Drain loop: 10s ticker (`controller.go:99-105`, `pollingIntervalSeconds` default 10),
  `drainRuns` computes capacity **once per cycle** then claims back-to-back until
  no-run/failure/capacity/shutdown (`controller.go:122-142`); `availableCapacity` =
  `maxJobs - CountActiveJobs()`, negative `MaxConcurrentJobs` ⇒ unlimited with the
  `maxDrainPerCycleUnlimited = 10` backstop (`controller.go:44-48,148-165`); count error
  ⇒ return 0, skip cycle (`controller.go:154-158`). Shutdown = bare bool checked per
  iteration (`controller.go:90-93,108-114`) — running Jobs are out-of-process and
  unaffected.
- `processNextRun` (`controller.go:167-228`): claim → decrypt (`decryptRunDetails`) →
  `GetImplementationType` → `ToRunnerType` → template lookup → `CreateRunnerJob`. Error
  taxonomy: decrypt failure ⇒ log + `decryptionErrors` metric + `processFailed`
  **without any status report** (`controller.go:180-184` — the run is left to the
  coordinator's timeout, a load-bearing quirk, §10); missing template ⇒
  `reportRunFailure` with `"no implementation handler configured for type '%s'"`
  (`controller.go:199-204`); `RunTooLargeError` ⇒ the 1MiB-secret message; other job
  errors ⇒ `"Failed to create job for run: "+err` (`controller.go:211-224`).
- The D5 fail-fast pattern to reuse, verbatim (`controller.go:230-242`):

  ```go
  // reportRunFailure registers the controller as a status source and marks the run as FAILED.
  func (c *Controller) reportRunFailure(runId string, errorMessage string) {
      if regErr := c.runApi.RegisterSource(runId); regErr != nil { …return }
      if statusErr := c.runApi.UpdateRunStatus(runId, "FAILED", errorMessage, errorMessage); statusErr != nil { … }
  }
  ```

  `RegisterSource` registers a single `validation` step (`runapi.go:69-79`);
  `UpdateRunStatus` PATCHes `StatusUpdateDTO` (`runapi.go:95-107`) — both frozen shapes.
- `kubernetes.go`: Job/Secret/SA construction (`:61-207`), `BackoffLimit: 1` /
  `TTLSecondsAfterFinished: 120` (`:135,140`), size guard + `RunTooLargeError`
  (`:84-99,274-287`), WIF projected-token volumes (`:485-542`), env contract
  `RUN_JSON_FILE_PATH=/var/run/secrets/meshstack/run.json`, `RUNNER_UUID`,
  `RUNNER_API_URL` and **no** `EXECUTION_MODE` (`:613-645`), operator `command:`/`args:`
  passthrough (`:358-376`), `CountActiveJobs` label-selector + finished-job filter
  (`:209-241`). `decryption.go:13-128` (five impl-type branches) moved to
  `meshapi.DecryptRunDetails` by plan 03 §5.2.5 — its second consumer named there is
  **this phase's in-process path**.
- `controller_capacity_test.go:12-34` (`fakeJobManager`), `:71-162` (drain back-to-back,
  stop-at-capacity, skip-at-capacity, stop-on-failure), `:164-207` (capacity math) — the
  behavior net this phase retargets mechanically (§11.2).
- Metrics: `run_controller_*` names with `controller_uuid`/`error_type` labels
  (`metrics.go:68-159`) — frozen; fetch-duration/fetch-error live in the claim adapter
  (`runapi.go:42-57`; note a 409 **does** increment `runsFetchErrors` there, unlike tf).

### 3.2 The tf polling machinery this phase absorbs

- Single worker via token protocol: `manager.go:60-80` starts exactly one `Worker`;
  delays `NORUN=10s`, `FAILED=60s` (`manager.go:13-14`); `done` ⇒ immediate new token
  (`manager.go:93-95`). Post-02 this survives as the de-globaled `Manager` (A4).
- Claim node-id `"<uuid>-worker-N"` — `worker.go:50` fetches with `worker-%d`,
  `tfrun/runapi.go:87-90` builds a per-fetch client with that requester. N is always 1.
- `handleFetchRunError` (`worker.go:66-91`): 404/409 ⇒ `norun` (409 logs "Conflict at
  coordinator-api."), the chunked-transport string match (`worker.go:84`) ⇒ `norun`,
  else ⇒ `failed` (60s). This is per-persona claim-error **policy**, distinct from the
  controller's (§3.1) — it moves into a classifier port (§4.2).
- **Run-token concurrency finding:** the polling adapter holds **one** mutable token slot
  (`runapi.go:16-31,73-80` — `runApiAuth.runToken` pointer, Bearer wins over base auth)
  set on fetch (`runapi.go:98`) and cleared after execution (`worker.go:55-57`). With N
  concurrent runs this is both a data race and a secret-hygiene violation (run A reports
  with run B's token). Phase 5 replaces it with per-run clients (§8) — the single-run
  path already models this: `main.go:141-143` builds a fresh api + `SetRunToken(
  runDetails.Spec.RunToken)`; `runapi.go:73-75`.
- Per-run isolation that **already exists**: per-run working dir via
  `os.MkdirTemp(workerDir, "block-<bbId>-*")` + `defer os.RemoveAll` (`worker.go:97-109`);
  per-run log file under `<cmdDir>/logs`. What does **not** exist per-run: the logger
  prefix (`[WORKER-001]`, `manager.go:74`) and the token slot.
- `TfBinaries`: shared install dir, `sync.Mutex` serializing stat/remove/install
  (`tfbinaries.go:33,92-93`); `testMode` returns the mock **before** taking the mutex
  (`tfbinaries.go:83-90`); tofu download uses `context.Background()` (B8, fixed in 2b).
- Standalone tf **never self-registers**: grep over `tf-block-runner/` finds no
  `meshbuildingblockrunners` usage; the runner object is pre-created in meshStack and
  claim-only (`runapi.go:92` fetches by `RunnerUuid`).

### 3.3 Registration & capability today

- Controller: PUT `…/meshbuildingblockrunners/<uuid>` with
  `ImplementationType: string(meshapi.RunnerTypeAll)` hardcoded (`dtos.go:12-25`), WIF
  block from discovered OIDC issuer + namespace (`dtos.go:27-49`, aligned with the SA
  subject pattern `kubernetes.go:101-106`); 404 ⇒ "create it via the meshStack UI"
  (`registration.go:63-65`); 10-min retry loop in `main.go:48-64`. Post-03 the transport
  is `meshapi.RunnerClient.Update` (A5).
- The backend enum is single-valued: `RunnerImplementationType` with 5 concrete values +
  `ALL` (`meshapi/dtos.go:273-282`); `"ALL" is a registration concept only and cannot be
  used as a handler key` (`controller/config.go:320-333`). `ToRunnerType` maps run
  impl-types to handler keys (`meshapi/dtos.go:284-295`).
- `maxConcurrentJobs`: field + semantics `controller/config.go:22`, today's code default
  20 (`config.go:139-143`), negative = unlimited. **This phase changes the compiled-in
  default to 10** (field/semantics/backstop otherwise unchanged; sanctioned §12/§16).

### 3.4 What the phase-6 handlers will need (interface-sizing input, not code)

From `go-meshapi-client/meshapi/dtos.go`: `TerraformImplementation` (`:111-122`),
`GithubImplementation` (`:131-143` — App JWT from `AppPem`, async, workflows),
`GitlabImplementation` (`:146-152` — `PipelineTriggerToken`),
`AzureDevOpsImplementation` (`:155-164` — PAT, async). Common needs: the parsed
`RunDetailsDTO` (inputs, raw `Implementation` JSON they unmarshal themselves,
`Links.MeshstackBaseUrl`), the run's `runToken` for reporting, async-handover reporting
via `report.Observer` (D9: final `IN_PROGRESS` on successful handover), and a decryptor
for their impl secret. **No** per-runner parameter appears that `ClaimedRun` +
constructor-injected deps cannot carry — §4.3 shows the fit runner by runner.

---

## 4. Interface designs (illustrative signatures only, P3/P8)

**slog-native:** the dispatcher packages (`dispatch`, `k8sjob`, and the per-run handler
loggers) use `log/slog` from the start — no `*log.Logger` seam and no `slog.NewLogLogger`
bridge (consistent with plans 03/04). Every logger parameter below is a `*slog.Logger`.

### 4.1 `dispatch.Loop`, `Dispatcher`, `ClaimedRun`

All declared in `internal/dispatch` — the loop is the consumer (P3). The loop owns
what is generic (ticker, capacity math, claim, type extraction, fail-fast reporting);
dispatchers own what is backend-specific (routing to a template/handler, decryption
placement, in-flight tracking).

**Capacity guard is k8s-independent.** Capacity math (`MaxConcurrent`, available-slot
computation, back-to-back claim-until-full) lives on the generic `Loop`; each dispatcher
only reports its `InFlight()` count. So the `InProcessDispatcher` (go-func mode) is
capacity-guarded by the same code path as k8s — `maxConcurrentRuns` mirrors
`maxConcurrentJobs` (§ intro), and a standalone polling runner honors it too. No k8s-only
capacity logic remains.

```go
type RunId string   // typed: cannot be swapped with source ids / uuids (P8)

// ClaimedRun is one claimed run as fetched — sensitive values still encrypted.
// RawJson carries the base64 of the claimed bytes (today's controller shape,
// runapi.go:59): the k8s dispatcher decrypts it for the Secret; the in-process
// dispatcher hands it to the handler, which decrypts per run (§8).
type ClaimedRun struct {
    Id      RunId
    Type    meshapi.RunnerImplementationType // via GetImplementationType + ToRunnerType
    Details meshapi.RunDetailsDTO
    RawJson string
}

// Dispatcher places one claimed run for execution. Dispatch is non-blocking for
// in-process execution (the run proceeds in its own goroutine) and synchronous-but-fast
// for k8s (Job creation). Typed errors drive the loop's failure paths (§10).
type Dispatcher interface {
    InFlight() (int, error)      // k8s: CountActiveJobs; in-process: counter (§6)
    Dispatch(run ClaimedRun) error
}

// UnhandledTypeError is the D5 claim-and-fail-fast signal; the message is
// dispatcher-authored (§10.1) and becomes the reported run failure verbatim.
type UnhandledTypeError struct { Type meshapi.RunnerImplementationType; Message string }

// Claimer + claim-error policy (per persona, §3.1 vs §3.2 differ and both are pinned):
type Claimer interface { Claim() (ClaimedRun, error) }
type ClaimOutcome int // OutcomeNoRun | OutcomeNoRunLogged | OutcomeBackoff
type ClaimClassifier func(error) ClaimOutcome

// StatusApi is the fail-fast backchannel — exactly today's shape (runapi.go:65-115).
type StatusApi interface {
    RegisterSource(runId RunId) error
    UpdateRunStatus(runId RunId, status, summary, stepMessage string) error
}

type LoopConfig struct {
    PollInterval  time.Duration // controller: pollingIntervalSeconds·s; tf: 10s (heir of NORUN_WORKER_DELAY)
    ClaimBackoff  time.Duration // suppress claiming after OutcomeBackoff; tf: 60s (FAILED_WORKER_DELAY); controller: 0 (next tick)
    MaxConcurrent int           // maxConcurrentJobs / maxConcurrentRuns; <0 = unlimited (10-per-cycle backstop)
}

func NewLoop(cfg LoopConfig, deps LoopDeps) *Loop // deps: Claimer, Dispatcher, StatusApi, ClaimClassifier, Metrics, Wake <-chan struct{}, *slog.Logger
func (l *Loop) Start(wg *sync.WaitGroup)
func (l *Loop) Stop()
```

`Loop.run` reproduces `controller.go:95-142` generalized: tick (or `Wake`, §6) →
capacity → claim/dispatch back-to-back. `reportRunFailure` moves onto `Loop` verbatim
(§3.1 quote). `Wake` is a plain nil-able channel dep instead of a `Dispatcher` method so
the interface stays two methods (P3); main wires it from `InProcess.Done()`.

### 4.2 `dispatch.InProcess` and `RunHandler`

```go
// RunHandler executes exactly one claimed run to completion. Contract:
//  - run.RawJson is still encrypted; the handler decrypts per run (§8).
//  - all run-scoped reporting MUST use the run's runToken, never process credentials.
//  - the handler owns its execution timeout (tf: TfCommandTimeoutMins, worker.go:116).
//  - a non-nil error means infrastructure failure *around* execution; run-level FAILED
//    is reported by the handler itself and returns nil (mirrors Engine.Execute, A7).
type RunHandler interface {
    Execute(ctx context.Context, run ClaimedRun) error
}

// NewInProcess: one handler per concrete type; registering RunnerTypeAll is a
// constructor error (ALL is a registration concept, config.go:320-333).
func NewInProcess(handlers map[meshapi.RunnerImplementationType]RunHandler, log *slog.Logger, m HandlerMetrics) *InProcess
func (d *InProcess) Dispatch(run ClaimedRun) error // UnhandledTypeError if no handler; increments in-flight *before* spawning (§6)
func (d *InProcess) InFlight() (int, error)        // never errors; satisfies Dispatcher
// InProcess + this registry are SHARED internal/dispatch code, not a binary. Which
// handlers a process can run is fixed at LINK TIME by which handler packages its binary
// imports: cmd/tf registers its one handler (single type); the cmd/bbrunner SUPERSET
// links every handler + both dispatchers and is the only build that registers ALL / runs
// every run type in one process. k8sjob.KubernetesJobDispatcher is likewise shared code,
// linked ONLY by cmd/bbrunner (= the run-controller image, the superset) — the sole
// binary that pulls in k8s. internal/dispatch.InProcess is linked by every persona
// binary AND by cmd/bbrunner.
func (d *InProcess) Done() <-chan struct{}         // signaled on each run completion (loop wake)
func (d *InProcess) Wait()                         // shutdown: drain in-flight within the configurable grace period, then cancel remaining sync-polling runs → ABORTED (§7 H7)
```

**Handler purity — no shared/`core` module, clean interfaces.** D11 forbids a
`core`/`shared` package. A `RunHandler` is as close to *pure domain logic over an injected
run context* as its runner allows — the framework (loop + dispatcher + reporting facility)
owns claim, decryption placement, run-token wiring and HTTP; the handler implements only
its behavior. The **manual** handler is the litmus test: it must be expressible as "copy
inputs → outputs" against injected ports (a run-scoped reporter, decrypted inputs,
`*slog.Logger`) with **no direct import of `meshapi`/HTTP or any other shared client
package** (logging excepted) — prefer handing the handler an already-run-scoped
reporter/output sink over having it build its own `meshapi.RunClient`. The tf and async
(gitlab/azdevops/github) handlers legitimately need more (streaming step logs,
external-pipeline calls, async handover), so the interface still permits injected clients,
but the default is minimalism; 06A must prove the manual handler stays HTTP-free.

The purity boundary: a `RunHandler` MAY read the meshapi client's DTOs (`RunDetailsDTO`,
inputs, links) and consume its use-case/domain API (potentially a wrapper around the
tf-provider client one day); purity means it **NEVER assembles its own HTTP
transport/auth**. The reporter is injected as a **use-case-level port**, not a raw client
the handler builds — and that port is ONE unified interface consumed by all five runners
(covering both manual and async needs):

```go
type Reporter interface {
    Register(RunStatus) error
    Report(RunStatus) (abort bool, err error)
}
```

`Report(RunStatus)` sends only the steps present in `RunStatus.Steps` (changed/new since
the last send); the meshfed endpoint **upserts steps by id**, and each included step
carries its **FULL current message text** (the backend overwrites, does not append). The
four ported runners (manual/gitlab/azdevops/github) consume this same `Reporter`, call
`Report` on state changes only, run **NO Observer/ticker**, own their own step dedup, and
**DISCARD the `abort` return**. **tf is the only Observer/ticker/abort consumer** (it
keeps `report.Progress` + `report.Observer`). One unified `Reporter`; ports discard abort
and run no Observer; tf alone uses Progress+Observer.

The tf handler lives in `internal/tf` and implements `dispatch.RunHandler`
directly (imports `dispatch` for the parameter types — the same
consumer-declares/adapter-imports relationship `JobManager` has today; depguard §5.3):

```go
// tf package. Shared deps injected once (TfProvider, Config, Clock, Identity, base URL,
// cert Decryptor); per run it: maps DTO→Run with the cert Decryptor (polling semantics
// today — pins intact), builds a run-scoped meshapi.RunClient with runToken-only auth,
// constructs Engine with that run-scoped reporter/artifact source, and executes.
func NewHandler(cfg Config, deps HandlerDeps) Handler
func (h Handler) Execute(ctx context.Context, run dispatch.ClaimedRun) error
```

`NewEngine` per run is a cheap struct literal — the reporter/artifact ports become
per-run values instead of handler-lifetime values (declared retarget §11.2.4/.5).

### 4.3 Fit check against the phase-6 runners (anticipation without speculation)

| Runner (impl struct, §3.4) | Needs | Carried by |
|---|---|---|
| manual | run id, register+report SUCCEEDED | `ClaimedRun.Id/.Details`, per-run `RunClient` + `report` |
| gitlab (`PipelineTriggerToken`) | impl secret decrypt, trigger POST, async `IN_PROGRESS` handover | handler-injected `Decryptor` (via `meshapi.DecryptRunDetails` or field-level), `report.Observer` async mapping (A6) |
| azure-devops (PAT, async) | same shape as gitlab | same |
| github (`AppPem` App JWT, workflows) | impl secret decrypt, JWT mint, workflow dispatch, async | same + handler-local HTTP client (constructor dep) |

Nothing needs a new `Execute` parameter: inputs/impl/links/runToken are in
`Details`/`RawJson`; per-runner config and secrets arrive via constructors (main wires,
D11). The phase-6 `manual` template PR reviews this table against the Kotlin inventories
before porting (high-level §5 phase 6).

### 4.4 Capability config (D5)

```go
// dispatch (used by persona wiring): one concrete type or ALL — the backend enum is
// single-valued; subsets are unrepresentable, so the config type doesn't pretend.
type Capability meshapi.RunnerImplementationType
func ParseCapability(s string) (Capability, error) // validates against the 5 types + ALL
```

Capability feeds **registration only** (§9): what a runner claims is decided server-side
by the registered type of its runner object. Fail-fast (§10) is therefore unconditional —
any claimed run without a handler/template fails fast regardless of configured capability.

---

## 5. Package fate of `internal/controller` (dissolved per D11)

Plan 04 §10.3 moved the controller verbatim into a transitional package and handed its
fate to this phase. Decision: **dissolve into `internal/dispatch` + `internal/k8sjob`**,
exactly the D11 names — justified per file:

The destinations below are **shared `internal/*` packages**; the *binaries* that link them
are `cmd/<persona>/main.go` (one per persona) plus the `cmd/bbrunner`
superset. Wherever a row (or a §11 migration step — "persona_controller re-wired",
"persona_tf.go") says wiring goes to `persona_<name>.go`, read
`cmd/<persona>/main.go`. `internal/k8sjob` (`KubernetesJobDispatcher`) is linked
**only** by `cmd/bbrunner` (= the `run-controller` image, the superset);
`internal/dispatch.InProcess` by every persona binary and by `cmd/bbrunner`.

| Today (`internal/controller/…`) | Destination | Why |
|---|---|---|
| `controller.go` loop (`run`,`drainRuns`,`availableCapacity`,`processNextRun`,`reportRunFailure`) | `dispatch/loop.go` (`Loop`) | backend-agnostic claim/capacity/fail-fast — the piece both personas share (D5) |
| `runapi.go` (`RunApiClient`: claim + base64, `RegisterSource` validation step, `UpdateRunStatus` `StatusUpdateDTO`, fetch metrics) | `dispatch/claim.go` (implements `Claimer`+`StatusApi` over `meshapi.RunClient`; `NodeId` and metrics are constructor params) | it is the loop's own meshapi adapter; node-ids `run-controller-<uuid>` vs `<uuid>-worker-1` become injected `NodeId`s (frozen headers, §16.5) |
| `kubernetes.go` (client, Job/Secret/SA, `CountActiveJobs`, `RunTooLargeError`, size guard) | `k8sjob/` (`Dispatcher` impl) | the k8s adapter; `CreateRunnerJob` becomes `Dispatch(ClaimedRun)` — **decryption moves inside it** (`meshapi.DecryptRunDetails`, order preserved: decrypt → template lookup → size → SA/Secret/Job, §10.2) |
| `getKubernetesConfig`, `DiscoverOIDCIssuer` | `k8sjob/cluster.go` | real cluster I/O isolated in one file for the D6 exclusion list (§13) |
| `dtos.go` (`BuildRunnerRegistrationDTO`, WIF block) | `k8sjob/registration.go` | WIF audiences/token paths are literally coupled to the Job volume mounts (`kubernetes.go:101-106` comment: "should align with subject pattern in dtos.go") |
| `registration.go` residue (404-message mapping, retry policy) | `persona_controller.go` (wiring) + `meshapi.RunnerClient` (transport, already there post-03) | startup orchestration = main's job (D11: only main wires) |
| `config.go` (`ControllerConfig`) | split: `dispatch.LoopConfig` fields (polling, maxConcurrent) · `k8sjob.Config` (namespace, implementations `map[string]JobSpecTemplate`, tolerations, nodeSelector, imagePullSecrets, incl. their validation) · registration/crypto/api fields stay in the persona yaml struct, which moves to `persona_controller.go` and **embeds the package configs `yaml:",inline"`** so every existing yaml key parses byte-identically (D7) | config follows the code it configures (P3); validation lives in the gated packages, the persona struct only composes |
| `metrics.go` (`MetricsCollector`, `run_controller_*`) | `dispatch/metrics.go` | it instruments the loop + claim + dispatch events; names/labels frozen. `k8sjob` declares its own small consumer-side meter interface (`JobMetrics`: job created/error/duration, SA created/error) satisfied structurally by `MetricsCollector` — no `k8sjob→dispatch` metric coupling and prometheus stays out of `k8sjob` |
| `decryption.go` | already gone (plan 03 → `meshapi.DecryptRunDetails`) | — |
| `controller_test.go`, `controller_capacity_test.go`, transcript/golden tests | move with their code (harness retargets only, §11.2) | assertions untouched |

Dependency/depguard rules added: `dispatch` imports only `meshapi` + stdlib (+ prometheus
for the frozen collector); `k8sjob` imports `dispatch` (parameter types), `meshapi`,
`k8s.io/*`; `tf` may import `dispatch` (handler param types) but **not** `k8sjob`;
`dispatch` imports neither `tf` nor `k8sjob` (only main wires dispatchers/handlers);
prometheus allowance moves `controller` → `dispatch` (+`mgmt`, main). The plan-04
`internal/controller` prometheus rule is deleted with the package.

---

## 6. Capacity semantics per dispatcher & drain-loop unification

One `Loop.drainRuns` serves both personas; all of today's controller math moves verbatim
(`available = MaxConcurrent − InFlight()`, floor 0; `MaxConcurrent < 0` ⇒ the
`maxDrainPerCycleUnlimited = 10` backstop; capacity computed **once per cycle**, then
claim-before-dispatch up to it — `controller.go:122-165`).

| Aspect | `k8sjob` (controller persona) | `InProcess` (standalone persona) |
|---|---|---|
| `MaxConcurrent` source | `maxConcurrentJobs` (**new default 10**; today's code default is 20 at `config.go:139-143`; negative = unlimited with backstop) — sanctioned default change §12/§16 | **new** `maxConcurrentRuns` (**default 3** — up to 3 concurrent runs, an intentional throughput improvement, **not** today's single serial worker; negative = unlimited with backstop; env `RUNNER_MAX_CONCURRENT_RUNS`; `=1` reproduces today's exact serial cadence) |
| `InFlight()` | `CountActiveJobs` — label selector `meshcloud.io/runner-id=<uuid>`, finished Jobs excluded (`kubernetes.go:209-241`) | atomic in-flight counter; **incremented synchronously inside `Dispatch` before the goroutine spawns**, decremented on run completion |
| `InFlight()` error | count error ⇒ capacity 0, skip cycle, log (`controller.go:154-158`) — preserved | cannot error |
| Oversubscription safety | best-effort (k8s eventual consistency, as today) | exact: the loop is single-goroutine, so claim→`Dispatch`→increment is sequential; concurrent completions only *increase* capacity (H6 test) |
| Refetch after completion | none — next tick re-counts (today's behavior, unchanged) | `Done()` wake channel: the loop drains again immediately, reproducing today's `done ⇒ handoutWorkerToken(0)` immediate refetch (`manager.go:93-95`) |
| No-run cadence | next tick (10s `pollingIntervalSeconds`) | next tick (10s `PollInterval` = heir of `NORUN_WORKER_DELAY`, `manager.go:13-14`) |
| Claim-error cadence | next tick (10s); non-404 logged + `runsFetchErrors` metric (`runapi.go:51-57`) | 60s `ClaimBackoff` (heir of `FAILED_WORKER_DELAY`); classification per `handleFetchRunError` incl. 409⇒no-run-logged and the chunked-transport quirk (`worker.go:66-91`) — injected `ClaimClassifier`, both policies preserved verbatim |
| Shutdown | stop claiming; loop exits after current cycle; Jobs continue out-of-process (unchanged) | stop claiming; persona main drains a **configurable grace period (default 120s; new additive knob)** via `InProcess.Wait()` — short in-flight runs finish and report their own terminal status; **sync-polling runs still active at grace expiry get their run context cancelled** and MUST report a **terminal** `ABORTED` status (fallback `FAILED`, **never** `SUCCEEDED`) so the coordinator never sees a stale `IN_PROGRESS`; logs flushed during shutdown. This deliberately **diverges** from today's unbounded manager/worker drain: k8s/docker grace periods make a ~30-min poll drain illusory (Kotlin parity — a JVM SIGKILL mid-`Thread.sleep` orphaned the run identically). SIGKILL semantics unchanged |

**Claim-rate consequence:** **by default (`maxConcurrentRuns = 3`)** the standalone tf
persona drains up to 3 claims back-to-back per cycle and executes them concurrently —
**not** byte-for-byte today's single serial worker, but an intentional throughput
improvement (sanctioned §12). Setting `maxConcurrentRuns = 1` restores the exact cadence
of today's token loop (one run at a time, refetch immediately after completion, 10s
no-run, 60s fetch-error, immediate after done). With `maxConcurrentRuns = N` it drains up
to N claims back-to-back per cycle, exactly like the controller's job draining; the
10-per-cycle backstop bounds the unlimited case. Because the default is now 3, the
concurrency paths (H1–H8, §7) are **default-on** rather than opt-in. The `Manager`/token
protocol (`work/done/norun/failed/stop/stopped`) is **deleted**; its pinned observables
map onto `Loop` as declared retargets (§11.2.3).

---

## 7. Concurrency-hazards inventory (risk #4) — one named test each

All tests run under `-race` (on since 2b, A6). H-tests live next to their subject; the
concurrent scenario tests drive **two+ simultaneous runs end-to-end** through
`Loop`+`InProcess`+tf handler with fake transport + `MockedTfFacade` (the phase-1 style).

| # | Hazard | Mitigation (design) | Named test |
|---|---|---|---|
| H1 | Shared `TfBinaries` install dir: two runs requesting the same/different tofu versions race the stat/remove/install sequence | existing struct mutex (`tfbinaries.go:33,92-93`) serializes; **verify, don't rebuild**. Real downloads stay gate-excluded; the hermetic test exercises the mutex over the existing-binary fast path (pre-created fake binary files) with 2×2 concurrent `GetTF` calls | `Test_TfBinaries_ConcurrentGetTF_IsRaceFree` (in `tofu`; `-race`); real-download dedup remains the opt-in e2e note from plan 01 §7 |
| H2 | Per-run working dirs: concurrent runs writing tfvars/backend files into each other's dirs | `os.MkdirTemp("block-<bbId>-*")` per run already guarantees distinct dirs (`worker.go:97`); engine never touches a dir it didn't create; cleanup per run (`worker.go:109`) | `Test_InProcess_ConcurrentRuns_UseIsolatedWorkingDirs` — two concurrent runs, `MockedTfFacade` hooks capture each working dir: distinct, own files only, both removed after |
| H3 | Per-run logger prefixes & log files: interleaved lines under one `[WORKER-001]` prefix; two runs sharing a `RunLog` | per-run `*slog.Logger` with prefix `[RUN-<short-id>] ` derived in the handler; `RunLog` is per-run already (file lives in the run's dir) | `Test_InProcess_ConcurrentRuns_LogsAreIsolated` — captured PATCH `SystemMessage` log segments contain only the own run's lines; local log lines carry the run-scoped prefix |
| H4 | Status-struct sharing: observer of run A marshaling structs run B mutates | plan 02 §5.5 already made `report.Progress` per-run with deep-copy `Snapshot()` (B10 unrepresentable) — **verify the engine instantiates one `Progress` per `Execute`**, no handler-lifetime status state | `Test_InProcess_ConcurrentRuns_StatusUpdatesDoNotInterleave` — concurrent APPLY+DETECT; every captured PATCH body contains only its own run's id/steps |
| H5 | Run-token sharing: the single `runApiAuth.runToken` slot (§3.2 finding) races and cross-authenticates concurrent runs | structurally eliminated: claim client carries base auth **only**, each run gets its own `RunClient` with runToken-only auth (§8); `SetRunToken`/`ClearRunToken` deleted | `Test_InProcess_ConcurrentRuns_UseOwnRunTokens` — fake transport asserts every register/PATCH/artifact request of run X carries `Bearer <tokenX>`, claims carry base auth, no request ever mixes |
| H6 | Claim-loop vs in-flight accounting: loop claims run N+1 while run N's completion decrement is in flight, oversubscribing `maxConcurrentRuns` | increment inside `Dispatch` on the loop goroutine (§6); decrement+wake on completion; capacity re-read per drain iteration is monotone-safe | `Test_Loop_NeverExceedsMaxConcurrentRuns` — slow fake handler, queue of 5 claims, `MaxConcurrent=2`: peak in-flight ≤ 2, 3rd claim only after a completion wake; plus `Test_InProcess_InFlightCountsSynchronously` (Dispatch returns ⇒ `InFlight` already incremented) |
| H7 | Graceful shutdown vs in-flight runs: process exit while N runs report status | `Loop.Stop()` stops claiming; shutdown drains a **configurable grace period (default 120s; new additive knob)** during which `InProcess.Wait()` lets in-flight runs finish. Short in-process handlers drain normally; **sync-polling handlers (which may block up to ~30 min polling an external pipeline) DO get their run context cancelled** at grace expiry — k8s/docker grace periods make a 30-min drain illusory (Kotlin parity: a JVM SIGKILL mid-`Thread.sleep` orphaned the run identically). A cancelled run MUST report a **terminal** status so the coordinator never sees a stale `IN_PROGRESS`: report **`ABORTED`** (added to `report.ExecutionStatus`; meshStack's status source already defines it as terminal), falling back to `FAILED` if the endpoint rejects `ABORTED`, **never `SUCCEEDED`**; logs are cleared/flushed while shutdown is in progress. The meshfed runner-facing `PATCH …/status/source/{sourceId}` endpoint accepts inbound `ABORTED` and persists it terminal via the coordinator; the accepted transition is `IN_PROGRESS → ABORTED` (a cancelled in-flight run is IN_PROGRESS, so this holds), and an already-aborted run returns `409 {runAborted:true}` which the shutdown reporter treats as success/no-op (same abort channel D9 pins) — so the `FAILED` fallback is belt-and-suspenders, not the expected path. | `Test_Loop_ShutdownWaitsForInFlightRuns` — Stop during 2 in-flight runs: no further claims; short handlers drain and report their own terminal status within the grace period; a long/sync handler still running at grace-period expiry has its context cancelled and reports `ABORTED` (or `FAILED` on endpoint rejection, never `SUCCEEDED`); a `409 {runAborted:true}` on the `ABORTED` PATCH is treated as success; `Wait` returns; loop goroutine exits |
| H8 | Wake-channel vs ticker vs Stop races in the loop select | single loop goroutine; `Done()` is a buffered/coalescing signal channel; Stop via context/atomic per plan-02 house pattern | `Test_Loop_WakeAndTickerRace` — hammer completions + ticks + Stop under `-race`; loop neither deadlocks nor claims after Stop |

Non-hazards, recorded so nobody "fixes" them: `http.Client`/`meshapi` clients are
concurrency-safe and shared deliberately; prometheus collectors are thread-safe; the
`mgmt` listener is untouched. **STOP-D** governs any hazard discovered beyond H1–H8.

---

## 8. In-process secret & auth model (risk #5)

The trust model mirrors the k8s Secret handover as closely as one process can:

| Concern | k8s dispatch (today, frozen) | In-process dispatch (this phase) |
|---|---|---|
| Claim auth | process credentials (Basic/ApiKey from config) | same — claim client is built once with base auth **only**; it can never hold a run token |
| Decryption | controller decrypts once per run before handover (`controller.go:180`, now inside `k8sjob.Dispatch`) | **per-run decrypt inside the handler goroutine**: the tf handler maps DTO→`Run` with the cert `Decryptor` exactly as the polling worker does today — plaintext exists only in that run's structures and working dir. (High-level phase-5 line "per-run decrypt then runToken-only reporting" ✓; the loop never sees plaintext.) |
| Run-scoped reporting | Job pod uses runToken-only auth (`main.go:141-143` + `runapi.go:73-75`; no base creds in the pod) | per-run `meshapi.RunClient` constructed with `run.Details.Spec.RunToken` and **no base-auth fallback** — the runner's main credentials are never used for run-scoped register/PATCH/artifact calls (H5 test) |
| Token lifetime | pod lifetime | run lifetime: the run-scoped client is unreachable after `Execute` returns (garbage-collected); nothing to "clear" — `SetRunToken`/`ClearRunToken` and their single mutable slot are deleted (declared retarget §11.2.4) |
| Fail-fast reporting (no handler/template) | controller reports with **process credentials** (`reportRunFailure` → `RunApiClient` with config auth) | process credentials — the fail-fast FAILED report uses the runner's **process** credentials (controller parity), **not** the claimed run's runToken. Fail-fast happens before any handler owns the run; using that run's token would carve an exception into the "runToken = executing handler only" invariant (risk #5). Identical to the controller pattern (D5) |
| Plaintext at rest | Secret object (namespace-scoped, owner-ref GC, `kubernetes.go:452-480`) | run working dir (tfvars/backend files), removed on completion (`worker.go:109` semantics preserved); log files likewise per-run |
| Residual risk | pod isolation | decrypted values of N runs coexist in one address space — the documented, accepted delta vs k8s isolation (high-level risk #5: "as closely as a single process can"); no cross-run references exist by construction (H2–H5) |

Decrypt-failure UX is **unchanged in both modes**: k8s keeps the controller's
log+metric+no-report quirk (`controller.go:180-184`, §10.2); in-process keeps the tf
engine's FAILED-with-key-mismatch-guidance (D9 pin) because decryption still happens
during DTO→Run mapping inside the handler. This placement (handler-side, not loop-side)
is *the* reason the pinned decrypt UX survives — the alternative (loop-level
`DecryptRunDetails` for all paths) was evaluated and rejected because it would re-shape a
D9-pinned failure surface (flag §16.2).

---

## 9. Registration & capability matrix (persona × dispatcher)

Registered capability is explicit config (D5): one concrete
`RunnerImplementationType` or `ALL` — the backend enum is single-valued
(`meshapi/dtos.go:273-282`), subsets are not representable and not pretended.
Transport is always `meshapi.RunnerClient.Update` (PUT; **a pre-created runner object is
required** — the 404 ⇒ "create it via the meshStack UI or API" contract,
`registration.go:63-65`, is untouched; no API changes, D10).

| Persona | Dispatcher | Self-registers? | Capability | WIF | Notes |
|---|---|---|---|---|---|
| `run-controller` | `k8sjob` | **yes** (unchanged): startup PUT with 10-min retry (`main.go:48-64`) | new `capability:` key, **default `ALL`** = today's hardcoded value (`dtos.go:23`) — default behavior byte-identical | yes (discovered OIDC issuer + namespace, `k8sjob/registration.go`) | bit-identical by default; a concrete capability is an operator opt-in |
| `tf-block-runner`, polling (default config) | `InProcess{TERRAFORM}` | **no** (unchanged — the standalone never self-registers today, §3.2) | effective capability = whatever the pre-created runner object carries (normally TERRAFORM) | n/a | zero wire/registration change (still never self-registers); the one sanctioned behavior change is default concurrency **`maxConcurrentRuns = 3`** (§6/§12), not today's single serial worker. Operator may flip the runner object to `ALL` server-side today already — fail-fast (§10) then covers unported types with **no runner config at all** |
| `tf-block-runner`, polling + new `registration:` section | `InProcess{TERRAFORM}` | **yes, opt-in**: startup PUT (reusing the controller's retry shape) when the section is present | `registration.capability`, required in the section: concrete type or `ALL` | **no** WIF block (no k8s, no projected tokens — a WIF-less `MeshBuildingBlockRunnerSpecDTO`, the field is `omitempty`, `dtos.go:314-319`) | the D5 scenario "standalone runner registers ALL before all Kotlin ports exist"; requires `displayName`, `ownedByWorkspace`, `publicKey` in the section (same keys as the controller yaml); still requires the pre-created runner (404 contract) |
| `tf-block-runner`, single-run (k8s Job) | none (direct engine) | **never** (frozen k8s contract) | n/a | n/a | untouched |
| Kotlin personas | — | untouched until phase 6 | — | — | phase 6 rows reuse the `registration:` section shape |

Consequences spelled out (the D5 documented cost): a standalone registered `ALL` claims
runs of all five types and **fails** every type without an in-process handler, with the
actionable message of §10.1 — operators who don't accept that keep/configure a concrete
capability. The `capability` value never gates claiming or dispatching locally (§4.4);
it only shapes the registration DTO.

"A standalone runner registers `ALL` / one container runs every run type in-process" is
specifically the **`cmd/bbrunner` superset** build (the only binary that links all
handlers). A default per-persona binary (`cmd/tf`) links only its own handler, so it can
in-process-dispatch only its own type no matter what capability its runner object carries —
a `cmd/tf` whose runner object was flipped to `ALL` fails fast (§10.1) on every
non-TERRAFORM claim. Registering `ALL` and serving all five types in one process requires
the superset. That superset build IS the `run-controller` image and IS the controller —
`cmd/bbrunner` links BOTH dispatchers (`KubernetesJobDispatcher` + `InProcess`) and
auto-detects the in-cluster k8s API at startup (in-cluster ⇒ k8sjob; else ⇒ InProcess;
`RUNNER_DISPATCHER` overrides).

---

## 10. Failure-path spec (exact messages, metrics)

### 10.1 Unhandled type — claim-and-fail-fast (D5)

Both dispatchers return `UnhandledTypeError`; the loop runs the `reportRunFailure`
pattern (§3.1 quote): `RegisterSource` (single `validation` step) + `UpdateRunStatus
(runId, "FAILED", msg, msg)` with `StatusUpdateDTO` — both frozen wire shapes.
The **message** is dispatcher-authored:

- `k8sjob` (controller persona), **byte-identical to today** (`controller.go:201`):
  `no implementation handler configured for type '<T>'`
- `InProcess` (standalone persona), new and actionable per D5:
  `this runner does not handle run type '<T>': no in-process handler is registered for
  it. The run was claimed because the runner's registered capability covers this type —
  register the runner with the concrete capability it supports, or run it on a runner
  that implements '<T>'.`

One error type, two messages: unifying the string would change controller-visible bytes
(forbidden) or ship the frozen-but-vague text to new users (defeats D5). Flagged §16.4.

D5 fail-fast also has a **compile-time dimension**. A fit per-persona binary literally
cannot dispatch a type whose handler it did not link, so the runtime unhandled-type path
is reachable only in a binary that registered a capability it can serve none/only-some of
— i.e. the `cmd/bbrunner` superset (= the `run-controller` image; it may register `ALL`,
or a k8s template it lacks a handler/template for). The runtime fail-fast (claim →
register → `FAILED` with **process** credentials) applies to `cmd/bbrunner`; the
compile-time dimension covers the fit binaries (`cmd/tf` + the four runner mains). The
`ALL` capability is meaningful only for a binary that links all handlers (the superset).

### 10.2 Full loop taxonomy (behavior column is normative)

| Event | Controller persona (must stay bit-identical) | Standalone persona |
|---|---|---|
| claim 404 | silent, no metric, wait next tick (`controller.go:171-177`) | silent, wait next tick |
| claim 409 | `Error fetching run` log + `runsFetchErrors` metric, wait next tick (`runapi.go:51-57`) | `Conflict at coordinator-api.` log, **no** backoff (no-run class, `worker.go:73-75`) |
| claim transport error, chunked-transfer quirk | (n/a today — generic error path) | no-run class, silent (`worker.go:81-86` verbatim) |
| claim other error | log + metric, wait next tick | log + `runner_poll_errors_total`, **60s** `ClaimBackoff` |
| impl-type parse failure | `reportRunFailure("Failed to determine implementation type: "+err)` (`controller.go:188-193`) — frozen | same message (loop-level, shared) |
| decrypt failure | log + `run_controller_decryption_errors_total`, `processFailed`, **no status report** — the run times out coordinator-side (`controller.go:180-184`; a latent-bug quirk kept for bit-identity, flagged §16.8) | n/a at loop level — decryption is handler-side; a mismatch fails the run through the engine with the pinned key-mismatch guidance (§8) |
| unhandled type | §10.1 frozen message; no dedicated metric (none exists today) | §10.1 new message; `runner_runs_unhandled_total{runner_uuid,type}` (new, additive) — **not** counted as `runner_runs_failed_total` (that series means "executed and failed", A7) |
| `RunTooLargeError` | frozen 1MiB message (`controller.go:218-221`) + `run_too_large` error metric | n/a |
| other dispatch/job error | `"Failed to create job for run: "+err` + `reportRunFailure` (`controller.go:211-224`) | handler `Execute` returned error ⇒ log + `runner_runs_failed_total`; the run's own status reporting already happened (engine init-fail / FAILED paths, unchanged) |
| drain stop conditions | first no-run / first `processFailed` / capacity / shutdown (`controller.go:130-141`) — preserved verbatim | same loop code |

### 10.3 Metrics summary

- Controller persona: `run_controller_*` set unchanged in names, labels, and firing sites
  (moved with the code, §5). `controllerLoopIterations` keeps firing per tick.
- Standalone persona: plan-04 `runner_*` set rewired from the deleted polling loop onto
  the new path (claimed on successful claim; succeeded/failed + duration around
  `RunHandler.Execute`, classification identical to A7's recorded semantics); **new,
  additive**: `runner_runs_unhandled_total{runner_uuid,type}` and
  `runner_at_capacity_skips_total{runner_uuid}` (the in-process twin of
  `run_controller_jobs_at_capacity_skips_total`). D12 additive-names rule satisfied; no
  renames, no alias duty.

---

## 11. Migration sequence — always-green steps

Rules: after every step `task test` + `task lint` green, `task coverage` ≥ gate; numbers
recorded per working commit (squashed on merge). Characterize before restructuring.

### 11.1 Steps

| # | Step | What changes | What proves it |
|---|---|---|---|
| 0 | **Preflight.** Run all §1 verifications on the phase-4 branch; branch `phase-5-dispatcher`. Record: coverage numbers (A8), the A7 metric classification, the A9 exit condition. | nothing | A1–A12 verified (STOP-A) |
| 1 | **Characterization top-up (tests only).** Pin what §10.2 relies on and A3 may not cover: the exact unhandled-type message string, the decrypt-failure no-report quirk (transcript shows *no* register/PATCH), drain-stop-on-failure ordering, 409-claim metric increment. | `_test.go` only in `internal/controller` | `git diff -- ':!*_test.go'` empty for this step |
| 2 | **Dispatcher seam inside `internal/controller`.** Widen `JobManager` → `Dispatcher` (`InFlight`/`Dispatch(ClaimedRun)`); move decryption + template lookup + `RunTooLargeError` taxonomy from `processNextRun` into the k8s implementation (order preserved: decrypt → template → size → SA/Secret/Job); introduce `ClaimedRun`, `UnhandledTypeError`, `ClaimClassifier`; loop consumes only the seam. | `internal/controller/*.go` | step-1 pins + A3 transcripts/goldens green **with zero assertion changes** (STOP-B); capacity tests retargeted per §11.2.1 |
| 3 | **Package split.** `git mv` loop+claim+metrics → `internal/dispatch`; k8s parts → `internal/k8sjob` (cluster I/O isolated in `k8sjob/cluster.go`); config split per §5; delete `internal/controller`; persona_controller re-wired; thresholds/exclusions/depguard updated in lock-step. | moves + import rewrites, no semantic edits | full suite green; `git diff --find-renames` shows moves; induced-failure check on the two new thresholds lines; `go run . run-controller` boots |
| 4 | **`InProcess` dispatcher.** In-flight counter, per-run goroutine, `Done()` wake, `Wait()`, handler registry with ALL-rejection, `runner_*` metric hooks + the two new counters. | `internal/dispatch` | new unit tests: unhandled type ⇒ typed error; H6/H8 tests; fake-handler lifecycle |
| 5 | **tf run handler.** `tf.NewHandler` implementing `dispatch.RunHandler`: per-run DTO→Run mapping (cert `Decryptor`), per-run runToken-only `RunClient` → run-scoped reporter/artifact ports, per-run `[RUN-<id>]` logger, engine constructed per run; `SetRunToken`/`ClearRunToken` and the shared token slot deleted (retargets §11.2.4/.5). `Manager` still runs production — handler exercised by tests only. | `internal/tf` | scenario suite green via the handler entry; H5 test; token-pin retargets |
| 6 | **Cutover persona_tf polling.** `dispatch.Loop` + `InProcess{TERRAFORM: handler}` replace `Manager`+poll worker (deleted, incl. the token protocol); tf claim adapter with `NodeId` `<uuid>-worker-1` + `handleFetchRunError` classifier verbatim; `maxConcurrentRuns` config (+env alias); mgmt metrics rewired. | `internal/tf`, `internal/dispatch`, `persona_tf.go`, config | **entire phase-1 characterization suite green** driven through the loop (same inputs, same captured HTTP transcripts); §11.2.3 loop-cadence tests replace the CP12 heirs; single-run path diff-empty |
| 7 | **Concurrency hazard suite.** H1–H8 named tests (§7); N-concurrent scenario tests; `-race` across all of it. | tests (+ fixes only if a §7 mitigation was mis-implemented) | all H-tests green; STOP-D live here |
| 8 | **Capability & registration (additive config).** `Capability` parsing; controller `capability:` key (default ALL); tf `registration:` section + opt-in startup PUT (no WIF); config validation + docs in `containers/*/runner-config.yml` comments. | `dispatch`, `k8sjob`, persona files, config samples | registration transcript tests: controller DTO byte-identical by default; standalone PUT golden (no WIF, chosen capability); section-absent ⇒ zero registration traffic |
| 9 | **Acceptance + self-review gate.** local-dev-stack flow with the tf persona on the new loop (≥1 TERRAFORM + ≥1 MANUAL run); **N-concurrent smoke**: `maxConcurrentRuns=2`, two TERRAFORM runs verifiably overlapping (log timestamps); controller evidence = A3 goldens + container smoke (inherited gap, plan 03 §12.3); P1–P8 walk; PR lists flags (§16) + retargets (§11.2). | — | evidence in PR description (STOP-E) |

### 11.2 Declared test retargets (assertions never change; beyond this list = STOP-B)

1. `controller_capacity_test.go`: `fakeJobManager{CreateRunnerJob,CountActiveJobs}` →
   fake `Dispatcher{Dispatch,InFlight}`; the asserted counts/claim-stops are unchanged.
2. Controller harness: `NewController()` wiring → `NewLoop` + `k8sjob` construction;
   config struct composition per §5 (yaml fixtures byte-identical).
3. CP12-heir manager-protocol tests: deleted with `Manager`; replaced by named `Loop`
   tests asserting the same observables — 10s no-run cadence, 60s claim-error backoff
   (constructor-constant pins, as CP12 pinned the delays), immediate refetch after
   completion, graceful-stop ordering.
4. `Test_ClearRunToken_*` pins: retargeted to H5's observable — claims always carry base
   auth, run-scoped calls always carry only their run's Bearer token (the *pinned
   behavior* — token never leaks across runs/fetches — is identical; the mechanism is
   per-run clients instead of a cleared slot).
5. Scenario-suite entry: plan-02's "polling-loop entry" → `Loop`+`InProcess`+handler
   entry; run-JSON in, captured HTTP transcript out — assertions untouched.
6. Constructor-default pins (engine 10s interval/timeout-from-minutes): re-anchored on
   the per-run engine construction in the handler (values unchanged).

---

## 12. Frozen contracts touched (D9/D10) — see also §16 flags

**Preserved byte-identically (proven by moved-not-changed tests):** the **entire k8s Job
contract** — manifests (labels, `BackoffLimit:1`, TTL 120s, WIF volumes/mounts,
Secret+SA shapes, owner refs), env (`RUN_JSON_FILE_PATH`, `RUNNER_UUID`,
`RUNNER_API_URL`, no `EXECUTION_MODE`), `command:`/`args:` passthrough — plan-03 goldens
green with **zero assertion edits** (STOP-B); the tf single-run path (persona glue,
runToken-only auth, R12 exit semantics) — diff-empty; controller claim wire (node-id
`run-controller-<uuid>`, media types, base64 handover), registration PUT + v1-preview
media type + WIF DTO (default `capability` = ALL), `StatusUpdateDTO` fail-fast body and
the frozen `no implementation handler configured for type '%s'` message,
`run_controller_*` metric names/labels, `maxConcurrentJobs` field/semantics and the
unlimited backstop (**default excepted** — the compiled-in default changes 20 → 10, the
one run-controller-persona behavior change, sanctioned-delta 6 below); tf claim node-id `<uuid>-worker-1`, `RunStatusUpdateDTO`
PATCH bodies, 10s status ticker, abort-cancel, async `IN_PROGRESS` handover, decrypt-
failure guidance, workspace naming, backend fallback, pre-run script contract; healthz/
`MANAGEMENT_PORT`/image names/entrypoints (untouched this phase); mux claim contract;
all existing env vars and yaml keys (new keys are additive only).

**Sanctioned, flagged deltas (standalone-persona, additive, or the one noted controller default):**
1. The `Manager` token loop is replaced by `dispatch.Loop` — external cadence is
   preserved by construction (§6); the `[WORKER-001]` log prefix becomes per-run
   `[RUN-<id>]` (operator-visible log format, not a wire contract — §16.9).
2. Fail-fast reporting from the tf persona (register + `StatusUpdateDTO` FAILED) is
   **new wire behavior** for that persona — reachable only when its runner object claims
   non-TERRAFORM runs, impossible in today's default deployments (inert by default).
3. New additive config keys (§2) and additive metric names (§10.3).
4. `SetRunToken`/`ClearRunToken` deleted — the pinned auth observables hold via per-run
   clients (§11.2.4).
5. **Standalone `maxConcurrentRuns` defaults to 3** (was effectively 1 = today's single
   serial worker): the standalone tf runner executes up to 3 runs concurrently by
   default — an intentional throughput improvement, **not** byte-identical to today's
   serial cadence. Rationale: safe concurrency is the whole point of phase 5; the hazards
   are covered by the §7 H1–H8 suite under `-race`. `RUNNER_MAX_CONCURRENT_RUNS`
   overrides; negative = unlimited with the 10-per-cycle backstop; `maxConcurrentRuns=1`
   reproduces today's exact serial cadence for operators who want it. This makes the
   concurrency paths (H1–H8) **default-on** rather than opt-in.
6. **Controller `maxConcurrentJobs` compiled-in default changes 20 → 10** — the sole
   exception to the run-controller persona's bit-identical/behavior-preserving claim
   (top-of-file, §3.3, §6). Field/semantics and the unlimited backstop are otherwise
   unchanged; today's code value is 20 (`config.go:139-143`) and operators can still set
   any value. Rationale: a more conservative default for cluster job pressure; no
   wire/manifest/metric-shape changes.

---

## 13. Test plan & D6 gate extension

**Gate (`tools/coverage/thresholds.txt`) after this phase** — two new lines:

```
github.com/meshcloud/building-block-runner/internal/dispatch  90   (new)
github.com/meshcloud/building-block-runner/internal/k8sjob    90   (new)
```

`exclusions.txt` gains `internal/k8sjob/cluster.go` (real cluster I/O:
kubeconfig loading via clientcmd, OIDC discovery HTTP against the API server —
`kubernetes.go:49-59,650-708` today; isolated into one file in §5 precisely so the
exclusion stays per-file honest). Everything else in `k8sjob` is hermetically testable
via `kubernetes/fake` (the A3 goldens already prove it); everything in `dispatch` via
fakes/channels/fake clock. This closes plan 03 §9's deferred decision: the controller's
application logic joins the gate now that it is restructured (`dispatch`), with the
declared `k8sjob` exclusion.

**What proves each piece:**

- **Loop/dispatch:** retargeted capacity suite (§11.2.1) + new `Loop` cadence tests
  (§11.2.3) + failure-taxonomy table tests driving every §10.2 row through a fake
  transport (message strings asserted byte-exactly, incl. the frozen k8s one).
- **k8sjob:** A3 goldens unchanged; new tests only for the moved decryption placement
  (decrypt-failure ⇒ no report, transcript-empty pin from step 1) and config validation
  relocation.
- **In-process execution:** the phase-1 characterization suite re-driven through
  `Loop`+`InProcess`+handler (step 6) — the run-JSON-in/transcript-out assertions are
  the real behavior-preservation proof; plus H1–H8 (§7) under `-race`.
- **Registration/capability:** transcript tests per §11.1 step 8 (controller DTO
  byte-identical by default; standalone PUT golden without WIF; absent section ⇒ no
  registration request).
- **Metrics:** prometheus `testutil` asserts for the rewired `runner_*` hooks and the
  two new counters; controller collector untouched (names pinned by plan-03/04 tests).
- **End-to-end (step 9):** local-dev-stack acceptance + the 2-concurrent-runs smoke —
  the first-ever live proof of the phase's headline capability.

Coverage arithmetic note: `dispatch` inherits the well-tested loop (capacity suite +
step-1 pins) and gains loop/in-process tests — projected comfortably ≥90; `k8sjob` is
~55% manifest-building code already golden-covered post-03 (plan 03 §9), minus the
excluded `cluster.go`. If either lands <90 at step 3 ⇒ STOP-C (add tests, never
exclusions).

---

## 14. Rollback story

One squash commit on a stacked branch: `git revert` restores `internal/controller`, the
`Manager`/token loop, the shared-token adapter, thresholds/exclusions/depguard state —
all in-repo. No wire shape, image name, port, metric name, env var, yaml key, or k8s
contract changes in the default configurations (§12), so **published images stay correct
under rollback**; `:main` floats back on the next CI run, release tags are immutable.
The additive surfaces (`maxConcurrentRuns`, `registration:`, `capability:`, the two new
`runner_*` counters, N-concurrency itself) disappear with the revert — operators who
adopted them within the window lose them; documented in the PR as the rollback cost.
No cross-repo edits exist to co-revert (§15).

---

## 15. Cross-repo touch points

- **meshfed-release — read-only, verify only.** `local-dev-stack/SKILL.md` starts the tf
  runner via `go run . tf-block-runner` (post-04 wording, plan 04 §9) — command
  unchanged; the `[TF RUNNER]` readiness prefix is untouched (persona logger, plan 04
  §4.1). Verify the skill greps nothing for `[WORKER-` (plan-04 reading found only
  pgrep/process hints — re-check at step 0; a hit would make the log-prefix delta §16.9
  a lock-step doc edit instead). Acceptance flow is the step-9 outer net. Note to
  maintainers in the PR: a standalone runner registered `ALL` now fail-fasts unported
  types — relevant to local-dev setups that flip runner objects to ALL.
- **terraform-provider-meshstack:** pattern source only (D3); no edit.
- **meshStack/meshfed API:** untouched. Claims, PATCH shapes, registration PUT and its
  pre-created-runner requirement are all frozen; capability values are existing enum
  members (`meshapi/dtos.go:275-282`). The controller-side decrypt-failure quirk
  (§10.2) continues to rely on the coordinator's run timeout — unchanged.

---

## 16. Flags — findings the high-level/prior plans did not anticipate

1. **Mixed dispatch in one process is deferred.** D5's per-run routing sentence
   ("in-process where a handler is registered, k8s Job where a template is configured")
   describes a composite no phase-5 persona needs; each persona gets exactly one
   dispatcher. The seam makes the composite a later 30-line change. Flagged, not silent.
2. **"Per-run decrypt" is handler-side, not loop-side.** The obvious unification
   (loop-level `meshapi.DecryptRunDetails` for all paths) would move the tf persona's
   decrypt failure out of the engine and break the D9-pinned key-mismatch UX, and would
   force a report where the controller today reports nothing. Handler-side decryption
   preserves both pinned surfaces and still confines plaintext per run (§8).
3. **`BackoffLimit: 1` stays** (closes plan 02 R12's deferred note): reconciled with the
   plan-02 R12 narrowing — a tf single-run exits non-zero **only for pre-mutation
   failures** (i.e. only when *no terminal status was reported*); a run that **began
   applying** reports a terminal status (`FAILED`, or `ABORTED` on cancellation, §7 H7)
   and is therefore **not auto-re-run**. So the k8s `BackoffLimit: 1` retry re-runs only
   runs meshStack never heard about — desirable, not double execution. No code change;
   recorded as the reviewed resolution.
4. **Two fail-fast messages for one error type** (§10.1): the controller's string is
   frozen (bit-identical mandate), D5 demands a more actionable one for the new
   capability-ALL scenario. Unification is a reviewer option — at the cost of one of
   the two constraints.
5. **The tf claim node-id `<uuid>-worker-1` is vestigial** once workers die, but it is
   an observable frozen header (D9) — kept as a constant `NodeId`, documented as
   historical.
6. **Fail-fast reporting uses process credentials** on both personas (controller parity).
   Even though the claimed run's token is available in the DTO, it
   is **not** used: fail-fast fires before any handler owns the run, so using that token
   would carve an exception into the "runToken = executing handler only" invariant
   (risk #5). Process-cred parity with the controller is confirmed.
7. **Single-run could reuse the handler** (NoopDecryptor + provided token) and delete
   more glue — deliberately not done: the k8s single-run path is frozen and untouched
   beats unified-but-touched. Phase-7 cleanup candidate.
8. **The controller never reports decrypt failures** (`controller.go:180-184`) — claimed
   runs with undecryptable payloads hang until the coordinator times out. Kept for
   bit-identity; recorded as a latent-bug candidate for a post-refactor fix (it predates
   this refactor; D13's inventory covered only `tfrun`).
9. **Per-run log prefix replaces `[WORKER-001]`** — an operator-visible log-format change
   no prior plan mentions; required because one shared prefix is meaningless with N
   concurrent runs (§7 H3).
10. **The shared run-token slot is a pre-existing latent race** (`runapi.go:16-31,73-80`
    + `worker.go:55-57`): harmless at 1 worker, fatal at N. Its structural removal (§8)
    is this phase's equivalent of plan 02's B6/B10 exception — eliminated by
    construction, verified by H5 + `-race`, called out for review rather than preserved.
11. **Plan 02's promised "in-memory `RunSource`" materializes differently:** the run
    arrives as a `ClaimedRun` handed to the handler, not as a third `RunSource`
    implementation; the polling `RunSource` impl dies with the loop cutover and the
    port's remaining consumers are the single-run file path and tests. Reconciled here
    so nobody hunts for the missing impl.

---

## 17. Promise set for phase 6

- Interfaces: `dispatch.RunHandler` (`Execute(ctx, ClaimedRun) error`, contract §4.2),
  `dispatch.ClaimedRun`, `dispatch.InProcess` handler registry (concrete types only),
  `Capability`/`registration:` config section shape (§9) — the `manual` template PR
  builds on these and reviews them against the four Kotlin inventories (§4.3) **before**
  porting; a needed interface change there is a reviewed revision of this plan's §4,
  not a workaround.
- Adding a handler = one package implementing `RunHandler` + one registry entry in the
  persona wiring + one `registration:`-capable persona config — no dispatch/loop edits.
- Per-run reporting pattern: run-scoped `meshapi.RunClient` (runToken-only) +
  `report.Progress`/`Observer` (async `IN_PROGRESS` mapping) — the template for every
  async handover port.
- Packages: `internal/{dispatch,k8sjob}` exist, gated at 90 (with the
  `k8sjob/cluster.go` exclusion); `internal/controller` is gone.
- Loop policy knobs available to phase-6 personas: `LoopConfig{PollInterval,
  ClaimBackoff, MaxConcurrent}`, `ClaimClassifier`, wake channel.
- Metrics: `runner_*` set + `runner_runs_unhandled_total` +
  `runner_at_capacity_skips_total` are the standard standalone-persona instrumentation.

---

## 18. Open questions

All decision branches were walked and resolved from the codebase; the judgment calls a
reviewer may veto are encoded as flags/STOPs, not questions: mixed-dispatch deferral
(§16.1), handler-side decrypt placement (§16.2), BackoffLimit resolution (§16.3), the
two-message fail-fast split (§16.4), process-credential fail-fast (§16.6), the shared-
token structural removal (§16.10), and the `k8sjob/cluster.go` exclusion (§13).
*(empty otherwise)*
