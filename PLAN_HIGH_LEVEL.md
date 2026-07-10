# High-Level Plan: Single Go Module for all Building Block Runners

<!-- Branch names (`refactor/single-go-binary/*`) and the `PLAN_DETAIL_04_single_binary.md` filename are retained as-is. -->

**Status:** draft for review · **Branch:** `refactor/single-go-binary/plan` · **Owner:** @agrub

## 1. Goal

All apps in this repo become **one Go module** (rooted at the repo root, `go.mod` at root)
with shared `internal/`
packages and **one fit-for-purpose binary per runner persona** (`cmd/<persona>/main.go`),
plus a `bbrunner` **superset** binary that links all handlers + both dispatchers and *is*
the run-controller (see D1/D2/D8). tf-block-runner and run-controller are the in-repo
starting points:

- `tf-block-runner`, `run-controller`, and (eventually) `manual-`, `github-`, `gitlab-`,
  `azure-devops-block-runner` are **personas** sharing one Go module — each a fit binary,
  except `run-controller` which is the adaptive `bbrunner` superset.
- Docker images each ship one binary as a direct entrypoint; they differ by binary and base
  toolchain (no shared binary, no symlink multiplexing).
- **run-controller mode is just dispatching**, and the superset **auto-detects** which
  dispatcher to use (D1/D2):
  - `KubernetesJobDispatcher` — today's behavior (k8s Job per run), selected when the
    in-cluster k8s API is detected.
  - `InProcessDispatcher` — `go func` per run inside one process (selected out of cluster);
    this makes a standalone Docker runner able to execute **multiple runs of any type
    concurrently**.
  - A polling standalone runner is then the degenerate case: a fit persona binary doing
    in-process dispatch of its single handler type. "Standalone" also covers the case where
    the controller dispatches a run *as a k8s Job*; that Job runs the fit persona binary in
    single-run mode — standalone/in-process and k8s-Job are not exclusive.
- **Downstream goal — make the `multiplexing-block-runner` (mux) obsolete.** Once
  the `bbrunner` superset build (D2) can register `ALL` and dispatch every run type
  in-process (D5), the mux's per-type fan-out in meshfed-release has no remaining job. Its actual removal from
  meshfed-release is cross-repo and tracked as a follow-up (see §8), but the refactor is
  designed to reach that end state, not to preserve the mux indefinitely.
- Configuration: **env-first, YAML file second** (default file locations, path overridable
  via env). Per-run input arrives as a run JSON (API claim, mounted file, or in-memory
  handover), including sensitive values decrypted with the runner's private key.
- Shared building blocks: a **meshStack API client** (aligned with, and possibly later
  merged with, `terraform-provider-meshstack/client`) and a **reporting/logging facility**
  serving both sync (tf: streaming step logs) and async (pipeline handover) runner modes.

**Prerequisite for everything:** a domain-driven refactor of the tf runner, secured by
integration-style tests with **≥90% line coverage** on the refactored domain/application
code, written *before* restructuring (characterization tests).

## 2. Current state (research summary)

| Component | State |
|---|---|
| `tf-block-runner` (Go) | Two near-duplicate execution paths: `tfrun/worker.go` (polling) and `tfrun/singlerunworker.go` (single-run, `EXECUTION_MODE=single-run` + `RUN_JSON_FILE_PATH`). Globals: `tfrun.AppConfig`, `meshcrypto.Crypto`. Domain logic (steps, status, vars, backend fallback) interleaved with I/O (`tfcmd.go` ~850 lines). Good facades already exist (`TfFacade`, `GitFacade` + mocks) and scenario-test suites (`worker_scenario_test.go`, `tfplan_scenario_test.go`) with fake HTTP transport. |
| `run-controller` (Go) | Polls, decrypts run JSON (`decryption.go`), creates k8s Job + Secret (`kubernetes.go`), capacity guard, Prometheus metrics, self-registration with WIF/OIDC discovery. Own thin API wrapper (`controller/runapi.go`) duplicating parts of the shared client. Global `controller.AppConfig`. |
| `go-meshapi-client` (Go) | Claim/register/patch-status/artifact-download client with runner headers + media types; `ApiKeyAuth` (login → cached Bearer), `BasicAuth`; RSA-4096+AES-128 hybrid crypto (`MeshCertBasedCrypto`). No retry/backoff. |
| Kotlin runners | Spring Boot apps on `block-runner-core` (scheduler, run client, decryption, single-shot mode). Runner-specific logic is HTTP-only (trigger pipeline / GitHub App JWT / no-op). |
| `terraform-provider-meshstack/client` | Package inside the provider module (no own `go.mod`). Rich meshObject clients, retry/backoff, `MinMeshStackVersion` startup check. Does **not** implement the runner-facing claim/status-source endpoints. |
| meshfed-release | `multiplexing-block-runner` (env-only config, stdlib Go) fans out the magic `ALL` runner UUID by type for local dev; `local-dev-stack` skill starts tf runner via `go run .` — cross-repo docs depend on this repo's layout and claim contract. |

## 3. Prime directives (apply to every phase and every detail plan)

These are binding for all implementation work in this refactor. Sources: meshfed-release
`PRINCIPLES.md` ("Code Comments", error handling, dependency minimalism) and
terraform-provider-meshstack `AGENTS.md` + `modern-go` skill — applied to this repo.

- **P1 — Comments explain *why*, not *what*.** A comment earns its place only by saying
  something the code cannot: intent, trade-off, non-obvious constraint, or a link that
  justifies a choice the reader would otherwise question. If a name/signature already says
  it, write nothing. One sharp line over a paragraph. Wrong comments are worse than none —
  update or delete them with the code they describe. (The existing `tfcmd.go` comments on
  saved-plan/backend-auth pitfalls are the house style to preserve.)
- **P2 — Modern Go (1.26), idioms over ceremony.** Use `new(expression)` for inline
  pointers (already in use: `kubernetes.go`'s `new(false)`); no `ptrTo` helpers. Use
  generics **only** where they push misuse to compile time (typed clients, handler/store
  registries) — never reflection-replacement for its own sake, never `any`-laundering.
  Prefer stdlib; every new dependency must be justified (the meshfed-release mux is
  stdlib-only production code — that's the bar for new code paths).
- **P3 — Package structure is the architecture.** Packages map to domain concepts, not
  layers-for-layers'-sake; interfaces are small, defined on the **consumer** side, and
  exist because there are ≥2 implementations or a test seam — not speculatively. Data flow
  must be visible and consistent: dependencies passed in constructors, no package-level
  mutable state, no values smuggled through `context.Value` (the current
  `RunContextInfo`-in-context is the anti-example to remove).
- **P4 — Value semantics by default.** Immutable objects are non-pointer values — easy to
  reason about, safe to share across goroutines. Pointer receivers only where necessary
  (actual mutation, identity, or embedded locks) — never as premature optimization that
  sacrifices clarity. Pointers + `omitempty` only for fields genuinely nullable in the
  API; non-nullable fields are value types.
  The pointer-for-nullable rule is scoped to **composite** types (structs/slices/maps). A
  nullable *simple* field (`int`, `string`) stays a
  non-pointer value when the zero value (`""`, `0`) already means "not present" and is not
  itself a valid domain value — reach for a pointer only when zero is a legitimate value
  that must be distinguished from absent.
- **P5 — Fail fast, never suppress silently.** Errors carry context (`fmt.Errorf` with
  `%w`), are handled or escalated, never swallowed. Validation at startup (config) fails
  the process with an actionable message, as the config packages already do.
- **P6 — Naming:** acronyms of 2+ letters keep only the first letter uppercase (`Id`,
  `Uuid`, `Api`) — consistent with both sibling repos and this repo's DTOs.
- **P7 — Tests are part of every step,** not a follow-up (meshfed-release build-and-test
  rule); the coverage gate (D6) never dips below threshold once enabled.
  Coverage is never a reason to add a unit test. Before writing one, reconsider whether a scenario/integration test belongs there instead;
  structure related scenarios as Go subtests (`t.Run`) under one test rather than many
  near-duplicate functions. Add unit tests **sparingly, if at all** — only for units with
  real decision surface (see D16).
- **P8 — Code-quality gate: types that make misuse hard.** Every reviewable unit (each
  step's checkpoint, and the PR as a whole) passes this gate before it counts as done:
  functions and methods are small, single-purpose, and hang off **well-defined data
  types** — not free-floating over primitives. Push misuse to compile time as far as Go's
  type system allows: named types instead of bare `string`/`int` where the value has
  meaning (the existing `Behavior`, `ExecutionStatus`, `RunnerImplementationType` are the
  house pattern — extend it, e.g. run IDs, tokens, workspace names crossing package
  boundaries); constructors that validate so a constructed value is always usable
  (make invalid states unrepresentable rather than checked-everywhere); parameter lists
  where two same-typed arguments can't be silently swapped (introduce a type or a params
  struct); enums with a defined zero value or an explicit "unset" sentinel. Modern idioms
  (P2) are part of the same gate — code that compiles but reads like Go 1.13 fails review.
  Data and the methods that interpret it must be **cohesive** — a domain string becomes a
  named type carrying its own parsing/manipulation/interpretation
  methods, rather than free functions over a bare `string`. The counter-weight still holds:
  do not introduce a type purely for ceremony — it earns its place by owning behavior or
  preventing a misuse.

## 4. Design decisions

- **D1 — persona = binary; env/config selects the mode; the superset auto-detects its
  dispatcher.** Each runner persona is its **own fit binary**, not a runtime `argv[0]` selection of one
  shared binary. `EXECUTION_MODE=single-run` etc. stay env/config concerns *inside* a persona
  binary (unchanged). The controller is the **`bbrunner` superset** (D2), shipped as the
  `run-controller` image: with no subcommand it runs as the controller/superset and
  **auto-detects the in-cluster k8s API** to pick its dispatcher (in-cluster ⇒ dispatch k8s
  Jobs; else ⇒ in-process, all types), overridable by `RUNNER_DISPATCHER`. Optional
  `bbrunner <persona>` subcommands force a single persona in-process for local-dev / the mux
  replacement. Unlike the runner personas, the superset is a published image (it *is*
  run-controller), not an opt-in extra.
- **D2 — one Go module, fit per-persona binaries + one adaptive superset (= run-controller).**
  New module at the **repo root** (`go.mod` at root, no `./runner` subdir); shared code in `internal/*` (D11). Entrypoints are **one `main` package per runner persona**
  under `cmd/<persona>/main.go` — `cmd/tf`, `cmd/manual`, `cmd/gitlab`, `cmd/github`,
  `cmd/azdevops` — each a fit binary linking **only what its persona needs** (`cmd/tf` links
  go-git+terraform-exec but not k8s; the four runner binaries link neither k8s nor go-git/
  tofu; verified today the heavy trees are disjoint). **There is no separate `cmd/controller`
  binary:** the controller collapses into `cmd/bbrunner` — one **environment-adaptive
  superset** that links all handlers + both dispatchers and, at startup, **auto-detects the
  in-cluster k8s API** (client-go `rest.InClusterConfig()`, which keys off the kubelet-injected
  `KUBERNETES_SERVICE_HOST`/`KUBERNETES_SERVICE_PORT` — the official documented in-cluster
  signal — plus the SA token/CA under `/var/run/secrets/kubernetes.io/serviceaccount/`): in
  cluster ⇒ `KubernetesJobDispatcher` (dispatch Jobs that run the fit per-persona images), out
  of cluster ⇒ `InProcessDispatcher` (go-func per run, all types in one process), with an
  explicit config override (`RUNNER_DISPATCHER=kubernetes|in-process`) that bypasses detection.
  The **`run-controller` published image ships `cmd/bbrunner`** as its direct entrypoint — the
  same image is the k8s controller in-cluster and the all-in-process standalone runner out of
  cluster. Optional `bbrunner <persona>` subcommands force a single persona in-process for
  local-dev (equivalent to the fit binary); the default (no subcommand) is the auto-detecting
  controller/superset. **Sequencing:** `cmd/bbrunner` is introduced in phase 4 as the
  controller (k8s dispatch, behavior-preserving); the `InProcessDispatcher` + k8s/in-process
  auto-detect land in phase 5; handlers accrete through phase 6, at which point bbrunner is the
  full superset. `cmd/*` holds entrypoint *wiring only* (package main, deps assembled per P3),
  no domain logic, so it does not violate D11 (which governs concept packages under
  `internal/`). The existing three modules collapse into this one module (atomically, not via shims — Go's
  module-scoped `internal/` blocks cross-module imports; rollout compat is carried by the image
  and wire contracts per D10, see plan 04).
  **Rationale:** fit per-persona binaries keep each dispatched-Job / slim-runner
  image's dependency / SBOM / CVE surface minimal and drop the argv[0]+symlink machinery, at
  the cost of N build targets (`go build ./cmd/...`). Merging the controller into the
  adaptive superset saves a whole persona/binary and gives one image that
  adapts to its environment; the accepted price is that the `run-controller` image carries all
  handler code even though in k8s mode it only dispatches Jobs — the bloat is concentrated in
  that one adaptive image while the numerous dispatched-Job images stay lean.
- **D3 — keep the runner API client in this repo** (evolve `go-meshapi-client`), do **not**
  import `terraform-provider-meshstack/client`: it lives in another module, targets the
  user-facing meshObject API (not claim/status-source/artifact), and its startup version
  check + dependency weight are wrong for runners. **Do:** adopt its proven patterns
  (retry/backoff with 503-riding budget, client-per-resource layout, error type) and align
  naming/DTOs so a later extraction into one shared `meshstack-go-sdk` module remains cheap.
  Cross-repo extraction is explicitly out of scope for this refactor.
- **D4 — unify the duplicated workers via ports.** Domain core (run lifecycle, steps,
  status transitions) + application service (execution engine with observer/status ticker)
  with ports: `RunSource` (API poll | mounted file | in-memory), `Decryptor` (cert-based |
  no-op — kills the `meshcrypto.Crypto` global and `ToInternalWithoutDecryption` fork),
  `StatusReporter`, `GitPort`, `TfPort`, clock. The observer loop + `logwrap` +
  `RunStatus`/`StepStatus` generalize into the **shared reporting facility** (runner-agnostic).
  The shared reporting facility exposes exactly ONE unified interface consumed by all five
  runners —
  `type Reporter interface { Register(RunStatus) error; Report(RunStatus) (abort bool, err error) }`.
  `Report` transmits only the steps present in `RunStatus.Steps` (changed/new since the last
  send); the meshfed endpoint upserts steps by id, so a subset is safe, and each included
  step carries its FULL current message text (backend overwrites by assignment, never
  appends) — never incremental chunks. `tf` is the **only** runner using the Progress+Observer
  10s ticker: its Observer computes the per-send diff and honors the returned `abort` flag.
  The four ported runners (manual/gitlab/azdevops/github) run **no** Observer, call `Report`
  on state changes only, own their own step dedup, and **discard** the `abort` return. Handler
  purity boundary: a `RunHandler` may read the meshapi client's DTOs and consume its
  use-case/domain API — purity means it never assembles its own HTTP transport/auth; the
  `Reporter` is injected as a use-case-level port.
- **D5 — dispatcher = capability registry; claim-and-fail-fast for unhandled types.**
  The controller loop depends on a `Dispatcher` interface. The registered capability is
  **explicit config** (one concrete type or `ALL` — the backend's
  `BuildingBlockRunnerCapabilityType` is a single enum value; subsets are not
  representable). Dispatch per run: in-process where a Go handler is registered, k8s Job
  where a job template is configured; a claimed run with **neither** is immediately
  registered + reported `FAILED` with an actionable "this runner does not handle type X"
  message (the pattern of `controller.go` for unconfigured implementations). This lets a
  standalone runner register `ALL` before all Kotlin ports exist, at the documented cost
  of failing runs of unported types — operators who don't accept that configure a
  concrete capability. Kotlin ports stay incremental — no big-bang.
  The fail-fast FAILED report uses the **runner's process credentials** (controller parity), not the claimed run's runToken — fail-fast happens
  before any handler owns the run, so reaching for that run's token would carve an
  exception into the "runToken = executing handler only" invariant (risk #5). This is the
  one run-scoped call deliberately made with process creds; examined here, not accidental.
- **D6 — coverage gate: ≥90% Go statement coverage** (the toolchain's measure; "lines" in
  conversation means this) **on domain + application packages, with growing scope**: the
  gate starts on `tfrun` (phase 1) and automatically extends to every new
  domain/application package (shared core in phase 3, each ported runner in phase 6).
  Source: hermetic integration-style tests. Prefer a reusable **meshfed-API server mock
  package** built on the
  stdlib `net/http/httptest` server (a real HTTP server the client dials) over a
  hand-rolled fake `http.RoundTripper` — it exercises the client's real transport and is
  shared across every runner type's integration tests. Built in phase 3 as the `meshapitest`
  package (plan 03 §5.7) and reused by the phase-5 concurrency suite, the phase-6 per-persona
  tests, and the phase-7 opt-in controller e2e. Where feasible, git-clone steps
  pull from a **bare git repo in `testdata`** with per-testcase remote branches (the
  pattern `terraform-provider-meshstack` already uses, copyable here) instead of a mocked
  `GitFacade`, so cloning is exercised end to end. The `TfFacade`/`GitFacade` mocks remain
  only where driving the real tool is impractical. The adapter exclusion list (real I/O:
  git exec, tofu exec, k8s client) lives in one visible place with a justification per file;
  real-tofu/real-git e2e is a separate opt-in task, not part of the gate. Enforced in CI
  via `go test -coverprofile` + threshold script. **Kotlin corollary:** before each phase-6
  port, the Kotlin runner's behavior is pinned by Kotlin tests (added where missing),
  which are then ported truthfully to Go together with the code.
- **D7 — config precedence: defaults < shared base YAML < per-impl YAML < env.** One config
  package; file path via `RUNNER_CONFIG_FILE` (default `runner-config.yml`). Nested
  structures (e.g. controller `implementations` map) remain file-only — env-first ≠
  env-only. All existing env var names and file keys keep working (aliases + deprecation
  warnings where renamed).
  The YAML layer is itself **two files deep-merged** — a shared top-level base
  `runner-config.yml` (keys common to all personas,
  including the well-known dev private key, per D8/§6) overlaid by an optional per-impl
  `runner-config.yml` (persona-specific overrides). Effective precedence: compiled-in
  defaults < base YAML < per-impl YAML < env. One place owns each shared key; per-impl
  files carry only their deltas. Plan 03 defines the deep-merge loader; plan 04 lays out
  the `containers/*` file tree.
  The loader is a well-designed Go package that exposes configuration as **typed struct
  fields** (`bool`/`int`/`string`/…),
  parsed identically whether the value arrives from env or YAML (typed coercion in one
  place, not stringly-typed lookups scattered at call sites). **Env-var naming is kept
  as-is:** the existing `RUNNER_*` and `TF_*` spellings stay — no new
  `BB_RUNNER_`/`RUNNER_TF_` canonical prefix scheme is introduced. `TF_*` in particular is
  **passed straight through to the tofu process** and must not be touched; `RUNNER_*` is
  runner config; a future `RUNNER_TF_*`-style key is admissible only if the tf runner
  itself needs a runner-specific knob, but is not a mandated rename. Backwards-compatible
  env vars are supported by **`${VAR}` interpolation inside the YAML defaults** (e.g.
  `mode: ${SPRING_PROFILE}` resolving `SPRING_PROFILE=kubernetes`) rather than a growing
  alias table in code — the base `runner-config.yml` maps legacy env vars onto struct keys
  declaratively. This composes with the fail-fast guard below (a legacy-prefixed env var
  that no interpolation or key consumes is still an error). Plan 03 owns the concrete
  struct layout, the env→field mapping, and the interpolation syntax.
  "Existing env var names keep working" means the **literal spellings actually shipped or
  documented** — the Go ports do NOT reimplement
  Spring's relaxed-binding matrix (`BLOCKRUNNER_UUID`, `runner.uuid`, …). To make silent
  misconfiguration impossible, startup **fails fast** (P5) with an actionable message when
  an env var matching a known legacy prefix (e.g. `BLOCKRUNNER_*`) is present but consumed
  by no config key — a relaxed-binding holdover must surface as a hard error, never as a
  runner that boots on wrong defaults and polls forever.
- **D8 — one binary per image, direct entrypoint; fit-for-purpose.** Each published image
  ships **one binary** (D2) as a
  direct entrypoint — **no shared binary, no symlink multiplexing**. `run-controller` = the
  **`bbrunner` superset binary** (all handlers + both dispatchers, auto-detecting its
  dispatcher, D1/D2) — the one adaptive/fat image, deliberately so; `tf-block-runner` =
  tofu/git/nix/aws-cli base + fit `cmd/tf` binary (go-git+tofu, no k8s); `manual`/`gitlab`/
  `github`/`azure-devops-block-runner` = minimal base + their small fit binary (shared
  client/report/config + own handler; no k8s, no go-git, no tofu). No separate slim
  controller-only image — the run-controller image *is* the superset. Published image names
  stay (`tf-block-runner`, `run-controller`, …) — customers reference them.
- **D9 — behavior pins (characterization tests must cover these before refactor):**
  async runs report `IN_PROGRESS` on successful handover; abort flag via status PATCH
  response cancels the run context; 10s status ticker; run-token > base-auth precedence and
  `ClearRunToken` after execution; 409-on-register = success; 404/409-on-claim = no run;
  media types + `X-Block-Runner-Node-Id`/runner headers; plan-artifact download (128MiB
  cap; NOTE: the former same-origin check was deliberately reverted in `88d67d4` — do not
  reintroduce or pin it); meshStack HTTP backend fallback incl. `TF_HTTP_USERNAME/
  PASSWORD` ephemeral auth; pre-run script contract (`$MESHSTACK_USER_MESSAGE`, run JSON on
  stdin); `aaaaaa_…auto.tfvars` + `meshStack_run_vars.tf` generation rules (run-scoped vars
  omitted on DETECT/saved-plan APPLY); FILE inputs as data-URLs; env whitelist
  (`cleanSystemEnv`); decrypt-failure UX (key-mismatch guidance); workspace select/create/
  delete naming logic; k8s single-run contract (`RUN_JSON_FILE_PATH`,
  `/var/run/secrets/meshstack/run.json`, `RUNNER_UUID`, `RUNNER_API_URL`, runToken-only
  auth; NOTE: `EXECUTION_MODE=single-run` is NOT injected by the controller's code — it
  comes from the job template's `env` map in the operator's `runner-config.yml`, i.e. it
  is deployment config and part of the customer-facing contract. The KOTLIN runners'
  single-run mode is triggered by `SPRING_PROFILES_ACTIVE=kubernetes` in those same
  operator configs — the phase-6 Go images must honor that variable as an alias or
  deployed controller configs break).
  On graceful shutdown, a persona that cancels an in-flight run (see the plan-05 H7
  amendment) must leave the coordinator with a
  **terminal** status — never a stale `IN_PROGRESS` that only clears after the coordinator's
  long timeout. Report `ABORTED` (the Go `ExecutionStatus` enum, today only
  `PENDING/IN_PROGRESS/SUCCEEDED/FAILED`, gains it), falling back to `FAILED` if the
  endpoint rejects `ABORTED` — **never `SUCCEEDED`**. Shutdown drains a **configurable grace
  period, default 120s** (deliberately longer than a typical graceful shutdown, far below a
  ~30-min external poll), and logs clearly while it is in progress.
  `ABORTED` is a real inbound runner status (verified against meshfed-release): the
  runner-facing `PATCH …/status/source/{sourceId}`
  endpoint (`MeshBuildingBlockRunWriteController.updateSourceSteps`) deserializes
  `MeshBuildingBlockRun.ExecutionStatus`, which includes `ABORTED(isTerminal=true)`;
  `BlockRunService.processUpdateFromSource` forwards it to the coordinator, which persists
  terminal `ABORTED` and reflects it back as `BuildingBlockStatus.ABORTED`. Two constraints
  this pins for the implementation: (1) the accepted transition is **`IN_PROGRESS →
  ABORTED`** (the endpoint's `ensureRunIsInProgress` rejects updates to a non-`IN_PROGRESS`
  run; `PENDING` is the *only* status a runner is outright forbidden to send) — which is
  exactly the graceful-shutdown case, an in-flight (IN_PROGRESS) run being cancelled; (2) if
  the run was **already** aborted (e.g. a user `DELETE`d it), the endpoint returns **`409
  {runAborted:true}`** — the shutdown reporter must treat that 409 as success/no-op, not an
  error (it is the same abort-flag channel D9 already pins). The `FAILED` fallback stays
  valid (also terminal, also accepted from `IN_PROGRESS`).
- **D10 — compatibility commitments during rollout:** old controller must be able to
  dispatch to new runner images and vice versa (the k8s Job contract in D9 is frozen);
  mux claim contract unchanged; healthz ports unchanged; meshfed-release `local-dev-stack`
  + acceptance tests keep working (update that repo's docs in lock-step when layout changes).
- **D11 — package layout: flat concept packages, one conceptual level deep.** The single Go
  module lives at the **repo root** (`go.mod` at root, NOT in a `./runner` subdir; coexists
  with the legacy module dirs during migration — which are deleted phase by phase, the
  `go.work`/`go.work.sum` workspace that bridged them removed outright at phase 4);
  its per-persona entrypoints are `cmd/<persona>/main.go` + `cmd/bbrunner`
  (D2 — package `main`, wiring only, exempt from the concept-package rules below). Packages
  sit at exactly one level below the
  module root, under `internal/` (visibility mechanism, exempt from the depth count — the
  repo is public and these packages are not API): `internal/meshapi`, `internal/crypto`,
  `internal/config`, `internal/report`, `internal/dispatch`, `internal/k8sjob`,
  `internal/tf`, `internal/manual`, `internal/gitlab`, `internal/azdevops`,
  `internal/github`. Rules: package name = last path element, named for a domain concept —
  never `api`/`util`/`common`/`core` (the existing `tf-block-runner/util` dissolves into
  its callers); no hyphenated directories (package identifier ≠ dir name is a permanent
  papercut); no deeper nesting — a parent dir earns its place only by discriminating, and
  call sites only ever see the last element anyway. Dependency direction (domain must not
  import adapters; only the `cmd/<persona>` mains wire) is enforced by `depguard` in golangci-lint — the
  same mechanism both sibling repos use — not by tree shape. The tf handler may split into
  sibling packages (`tf` + e.g. `gitsource`, `tofu`) in Phase 2 only if the seams prove
  real; one cohesive package is acceptable otherwise.
- **D12 — unified observability on `MANAGEMENT_PORT`.** Every persona serves `/healthz`
  **and** Prometheus `/metrics` on a single management listener, configured via
  `MANAGEMENT_PORT` with per-persona defaults preserving today's values (run-controller
  2112 — which finally gains a healthz; tf 8100; manual 8104; github 8102; gitlab 8103;
  azure-devops 8101; container default 8080 where PORT did that job before). Nothing is
  served twice. All personas — including standalone runners, which have zero metrics
  today — get basic Prometheus metrics (runs claimed/succeeded/failed, run duration,
  poll errors), reusing the controller's `MetricsCollector` approach. Existing metric
  *names* are a de-facto public surface (operator dashboards scrape them): renames get
  the same alias/deprecation treatment as env vars (D7). Single-run (k8s Job) executions
  run no management listener — a Job has no liveness/scrape lifecycle (plan 04).
- **D13 — bug policy during characterization: pin everything, fix after the refactor.**
  Phase 1 pins *current* behavior verbatim — including behavior identified as buggy (e.g.
  the swallowed workspace-select error, `tfcmd.go:229-233`). Each such pin is marked
  `// FIXME(bug):` in the test and recorded in a bug inventory in the phase-1 detail
  plan. A **dedicated bug-fix PR (phase 2b)** directly after the DDD refactor works
  through the inventory: flip each pinned test to assert correct behavior, fix the code,
  one inventory = one PR. No bug fixes sneak into phase 1 (tests-only) or phase 2
  (behavior-preserving refactor).
  The two *data races* in the phase-1 inventory (B6 mutable `progress` struct, B10 abort
  flag) are exempted from "pin verbatim, fix in
  2b" and are fixed **structurally in phase 2** (mutex-snapshot + `atomic.Bool`). A data
  race is undefined behavior — it cannot be meaningfully "preserved," and `go test -race`
  would flag it the moment the DDD refactor touches that code. The `-race` gate turns on in
  phase 2 and stays on; phase 3's shared reporting package inherits the correct shapes.
  This is the *only* sanctioned in-phase-2 behavior change; every other inventory bug still
  waits for 2b. (Plan 02 §5.5 STOP-D records the mechanical fallback if a reviewer later
  disagrees.)
- **D15 — Kotlin→Go ports are translations, not transliterations.** Behavior parity is
  defined by the pinned Kotlin tests at the *semantic* level; the Go code itself takes
  the freedom to be idiomatic Go. Concretely: exceptions/stacktraces → returned error
  chains (`fmt.Errorf("fetching pipeline %s: %w", id, err)` — succinct, lowercase,
  context formatted in, chained with `:`; panics only for programmer errors); JVM
  logging frameworks → `log/slog` with the default human-readable text handler on
  stdout/stderr, kept simple (no logging ceremony; the shared-core, dispatcher and
  single-binary packages — plans 03/04/05 — are authored **slog-native from the start**.
  Only the `tf`/`tfrun` package, whose phase-1/2 characterization pins predate this
  decision, migrates to slog in phase 7 (see umbrella §10.12) so those pins aren't disturbed
  mid-refactor. New phase-6 packages use slog natively, run id as attribute). A `LOG_LEVEL` env
  (`debug|info|warn|error`, default `info`) sets the slog handler level for every persona;
  at `debug` the shared `meshapi` HTTP transport logs full request/response headers **and
  bodies including sensitive values — deliberately unredacted** (opt-in diagnostic;
  artifact-download bodies excepted, metadata only). The client's pluggable `Logger` seam is
  copied from the terraform-provider-meshstack client (interface + body/header helpers, a
  slog adapter) so the future shared meshfed-api go-client merges with no logging delta (D3).
  See plan 03 §5.2.6 / §5.3 / §7; Spring
  DI/annotations/properties →
  constructor injection (P3) + the shared config package (D7); Jackson DTOs → plain
  structs with `encoding/json` (existing `meshapi` house style); OkHttp interceptors →
  the existing `AuthProvider`/client composition; schedulers → ticker/goroutine loops
  (the dispatch package). Sub-plans list their runner's Kotlin-isms and the idiomatic
  Go replacement for each.
- **D16 — coverage comes from scenario tests, not unit-test armadas.** The D6 gate is
  reached with use-case/scenario tests in the house harness style (fake HTTP
  transcripts, black-box through the engine/handlers) — the same style phase 1 extends.
  Unit tests are written only where a unit has real decision surface (e.g. parsers,
  crypto, type conversions); existing meaningful unit tests (Kotlin and Go) are kept or
  transformed, not discarded — but nobody adds unit tests just to move the number.
- **D14 — tooling: golangci-lint v2 + Taskfile in phase 0; CI reshaped only in the last
  phase.** Phase 0 adopts golangci-lint v2 (`.golangci.yml` mirroring the provider repo:
  gci import ordering, govet *inside* lint — the separate `go vet` target is dropped —
  and depguard rules that grow as D11 packages appear) and replaces the Makefile with a
  `Taskfile.yml`. GitHub Actions CI is left functionally as-is until the **cleanup phase
  (7)**, which turns it into a Go-only CI with the docker image builds.

## 5. Phases (order matters)

**Delivery model: one phase = one single-commit PR, stacked.** All detail plans are
written up-front, then the phases are implemented by running through them in order; each
phase is one squash-merged PR whose base is the previous phase's branch (stacked PRs,
merged sequentially into `main`). Branch naming:
`refactor/single-go-binary/phase-<N>-<short-description>` (e.g.
`refactor/single-go-binary/phase-1-characterization-tests`); the plan documents live on
`refactor/single-go-binary/plan` (note: a bare `refactor/single-go-binary` branch cannot
coexist with these — git refs cannot be both file and directory). Each PR lands green,
behavior-compatible, and reviewable on its own; the plan branch carries only the plan
documents. Phase 6 is the exception:
**one PR per ported runner**, where the first PR (simplest runner) deliberately
establishes the handler template, registration and test patterns the later ports fill in.
Phase N+1 must not start before phase N's exit criteria hold.

**Plans stack on assumptions, not facts — stop markers are mandatory.** Because detail
plan N+1 is authored before plan N is implemented, it necessarily builds on N's *planned*
outcome. Therefore every detail plan (01+) must carry:

- an **"Assumptions from prior phases"** section: each assumption states what it presumes
  exists (interface shape, package, coverage level, contract), which prior plan promised
  it, and a concrete *verification step* (a command, a file to read, a test to run).
- **STOP markers** in the implementation sequence: implementation of a phase begins by
  running all verification steps; any materially failed assumption — and any mid-phase
  discovery that invalidates a later step — is a **STOP: do not code around it.** Update
  the affected detail plan(s) first (including cascading corrections to later plans),
  get the revision reviewed, then resume. A drive-by workaround that "makes it fit" is
  the failure mode this rule exists to prevent.

### Phase 0 — Guardrails & baseline
Coverage baseline measurement per package; CI coverage report + threshold plumbing (not
yet gating); adopt golangci-lint v2 and replace the Makefile with a Taskfile, dropping
the separate vet target (D14 — GitHub Actions CI itself stays functionally untouched
until phase 7); inventory of untested behaviors against the D9 pin list; verify the
meshfed-release local-dev-stack + acceptance suite runs as the outer safety net.
**Exit:** baseline numbers documented; CI publishes coverage; `task lint`/`task test` work.
→ Detail plan: `PLAN_DETAIL_00_guardrails.md`

### Phase 1 — Characterization tests to ≥90% (tf runner, pre-refactor)
Extend the existing scenario-suite style (fake HTTP transport, mocked facades) to cover
every D9 pin and every use case (APPLY/DETECT/DESTROY × polling/single-run × async ×
artifact-replay × failure paths). Tests are written against *current* behavior at
use-case level (black-box through `Worker`/`SingleRunWorker`/`Manager`), so they survive
the restructuring. Bugs found are pinned, not fixed (D13): `// FIXME(bug):` markers + a
bug inventory in the detail plan. **Exit:** ≥90% statement coverage on `tfrun` (excluding
declared adapter files), gate ON in CI; bug inventory complete.
→ `PLAN_DETAIL_01_tf_characterization_tests.md`

### Phase 2 — DDD refactor of the tf runner (under green tests)
Extract domain (run, steps, status), application (execution engine unifying
`Worker`/`SingleRunWorker`, observer/reporting), ports & adapters (D4). Eliminate globals
(`AppConfig`, `meshcrypto.Crypto`) via injection. Small, always-green steps; coverage gate
stays ≥90%. **Exit:** one execution engine; polling and single-run are `RunSource`
configurations; no package-level mutable state; **plus two manual runtime smokes** (the coverage gate can't
reach `main.go` wiring and local-dev-stack only exercises polling): a local-dev-stack
acceptance run (polling) and a single-run smoke (binary with
`EXECUTION_MODE=single-run` + `RUN_JSON_FILE_PATH` against a fixture run JSON), so a
single-run wiring regression can't ride to `main` invisibly until phase 4.

**Phase 2b — bug-fix pass (own stacked PR):** work through the phase-1 bug inventory
(D13) — flip each `FIXME(bug)` pin to assert correct behavior, fix the code.
**Exit:** inventory empty; no `FIXME(bug)` markers remain.
→ `PLAN_DETAIL_02_tf_ddd_refactor.md` (covers 2 and 2b)

### Phase 3 — Shared runner-core & client consolidation
Move runner-agnostic pieces to shared packages: config loader (D7), reporting facility,
polling/claim engine, crypto, registration, retry/backoff (adopted from the provider
client's design, D3). Re-base run-controller onto them (its `runapi.go` duplication
disappears; `controller.AppConfig` global goes). **Exit:** tf runner and controller share
client, config, reporting; behavior unchanged (controller tests + acceptance suite).
Phase 3 carries exactly ONE deliberate, flagged wire change — tf's status send goes
full-snapshot → changed-steps-only (diff). It is
backend-result-identical (the endpoint upserts steps by id), so the acceptance suite stays
green; only tf's phase-1 HTTP transcript pins are updated in this phase to match the
reduced request bodies.
→ `PLAN_DETAIL_03_shared_core.md`

### Phase 4 — Per-persona binaries & module consolidation
`cmd/tf` (+ `cmd/bbrunner`, the superset that *is* run-controller) created this phase; the
four runner personas' `cmd/<persona>/main.go` arrive in phase 6 (D1, D2); each fit binary
links only its persona's deps; Dockerfiles get direct entrypoints, no symlinks (D8);
CI/release matrix builds the binaries via `go build ./cmd/...` → N images; module
consolidation (the single `go.mod` moves to the repo root, the `./runner` subdir is
dropped, and `go.work`+`go.work.sum` are deleted outright — the workspace only bridged the
phase 0–3 multi-module period); update meshfed-release local-dev-stack docs (which use `bbrunner`). The
dispatcher auto-detect + `InProcessDispatcher` land in phase 5. **Exit:** `tf-block-runner`
ships its fit binary and `run-controller` ships the `bbrunner` superset; old per-module
mains deleted.
→ `PLAN_DETAIL_04_single_binary.md` (retains its filename; content is the per-persona-binary plan)

### Phase 5 — Dispatcher abstraction & in-process concurrency
`Dispatcher` interface behind the controller loop; extract `KubernetesJobDispatcher`;
add `InProcessDispatcher` (go-func per run, per-run decrypt then runToken-only reporting —
same trust model as the k8s Secret handover; standalone in-process runs default to
**`maxConcurrentRuns`=3** — an intentional throughput improvement over the former serial
single-worker cadence — overridable via `RUNNER_MAX_CONCURRENT_RUNS` (negative = unlimited,
with a 10-per-cycle backstop), playing the role `maxConcurrentJobs` does for the k8s
dispatcher; per-run working dirs; version-download locking in `TfBinaries`).
Standalone polling runner becomes in-process dispatch with one handler. Registered
capability is explicit config (concrete type or `ALL`); claimed runs without a matching
handler or job template fail fast with an actionable message (D5). **Exit:** one Docker
container executes N concurrent TERRAFORM runs; `ALL` registration with fail-fast works;
k8s mode bit-identical to today, with the **one sanctioned exception** that the compiled-in
`maxConcurrentJobs` default drops 20 → 10 (an intentional capacity retune).
→ `PLAN_DETAIL_05_dispatcher.md`

### Phase 6 — Port Kotlin runners as Go handlers (one PR per runner)
Order by complexity: `manual` → `gitlab` → `azure-devops` → `github` (App JWT). Each is a
`RunHandler` plugged into the engine + reporting facility (async handover semantics from
D9). **The `manual` port is the template PR:** it must introduce the persona wiring,
handler registration, config section shape, acceptance-test harness and Dockerfile pattern
in the exact form the other three reuse — anticipate their needs (async handover, external
pipeline polling, per-runner secrets) in the interfaces even though `manual` itself needs
none of them. Per D6, each port starts by pinning the Kotlin runner's behavior with
**Kotlin tests** (added where missing), which are then ported truthfully to Go with the
code; the Go domain packages join the coverage gate. The deletion gate is honest about what real
coverage exists — `github`,
`tf` and `manual` have real end-to-end coverage in the sibling `meshstack-smoke-tests`
repo, so their ports are validated there before the Kotlin module is removed. `gitlab` and
`azure-devops` have **no** smoke tests (accepted shortcoming) — their deletion leans on the
in-repo integration/transcript tests (hermetic side-by-side equivalence, Kotlin pin suite
vs Go, against the same fake/local meshStack). Commissioning new meshfed-release acceptance
tests for these two is explicitly out of scope.
**Exit (per runner/PR):** Kotlin behavior pinned; Go handler validated per the gate above;
image switched; Kotlin module removed. **Exit (phase):** Gradle build gone.
→ `PLAN_DETAIL_06_kotlin_ports_umbrella.md` (consistency contract for the
Kotlin→Go migration) + one sub-plan per runner: `PLAN_DETAIL_06A_manual.md`,
`PLAN_DETAIL_06B_gitlab.md`, `PLAN_DETAIL_06C_azdevops.md`, `PLAN_DETAIL_06D_github.md`.

### Phase 7 — Cleanup & docs
READMEs, public docs pointers, config deprecation warnings, memory of final architecture;
reshape GitHub Actions into a Go-only CI including the docker image builds (D14 — the
JVM/Gradle CI legs die here with the last Kotlin module). → `PLAN_DETAIL_07_cleanup.md`

## 6. Key risks & caveats (watch-list)

1. **Refactoring without the test net** — hence Phase 1 strictly before Phase 2; any
   "quick" restructuring found necessary during Phase 1 must be deferred.
2. **Test brittleness vs. refactor** — characterization tests must target use-case
   boundaries (claim → execute → reported statuses), not internals, or Phase 2 churns them.
3. **Hidden contracts**: mux claim forwarding, k8s Job env/volume contract, meshfed
   coordinator status-machine expectations (e.g. first status must be `IN_PROGRESS`, 409
   semantics), OpenTofu backend-config-in-saved-plan pitfall (auth must stay in env, not
   files). These are pinned in D9 but easy to lose in review — every detail plan carries a
   "contract" section.
4. **Concurrency correctness in one process** (Phase 5): shared `TfBinaries` install dir,
   working-dir isolation, per-run logger prefixes, no shared mutable status structs (the
   current `reportStatus` copy pattern is subtle — see `runcontextinfo.go`).
5. **Secret hygiene in in-process mode**: decrypted run data lives in process memory next
   to other runs; runner main creds must not be used for run-scoped reporting (runToken
   only), matching the k8s isolation model as closely as a single process can.
6. **Cross-repo coupling**: meshfed-release (local-dev-stack, acceptance tests, mux) and
   terraform-provider-meshstack (patterns only, D3). Changes here ripple into docs/skills
   there; each phase's detail plan lists the cross-repo touch points.
7. **Public repo**: images, config keys and env vars are customer-facing API. Nothing is
   renamed without an alias + deprecation period (D7, D8).
8. **JVM runner parity** unknowns (e.g. GitHub App token flow edge cases) — Phase 6 detail
   plans start with a behavior inventory of the Kotlin sources, not with Go code.

## 7. Detail plans & subagent instructions

Each `PLAN_DETAIL_*.md` is authored by a subagent that receives:
(a) this file, (b) the phase-specific instruction below, (c) the standing rules:

> **Standing rules for detail-plan subagents.** Research the referenced code first; quote
> file:line evidence for every claim. The prime directives (§3) bind every proposed
> design — a plan that violates P1–P8 (e.g. speculative interfaces, pointer-happy structs,
> layer-cake packages, stringly-typed APIs) is wrong even if it works; package layout
> follows D11. List: scope
> (in/out), step-by-step implementation order with always-green checkpoints (sized so the
> phase can land as one reviewable single-commit PR, stacked on the previous phase's
> branch), the frozen contracts touched (from D9/D10), test plan (what proves each step),
> rollback story, and cross-repo touch points. Plans 01+ must open with an **"Assumptions
> from prior phases"** section (assumption → promising plan → concrete verification step)
> and place **STOP markers** at every point where a failed assumption or mid-phase
> discovery requires replanning instead of coding around it (see §5). Flag any finding
> that contradicts PLAN_HIGH_LEVEL.md instead of silently deviating. No code in the plan
> beyond illustrative signatures. Grill your own plan before returning: walk every
> decision branch, resolve it from the codebase, and record unresolved questions in an
> explicit "Open questions" section (empty is the goal).

- **00 guardrails**: measure per-package coverage for all 3 Go modules (command lines +
  CI wiring); design the threshold gate (fail-under, excluded files policy); golangci-lint
  v2 setup mirroring the provider repo (gci, govet-in-lint, depguard skeleton) and the
  Makefile→Taskfile migration (D14; CI workflows functionally untouched); verify
  local-dev-stack acceptance flow against this branch; document baseline numbers.
- **01 tf characterization tests**: map every D9 pin + every `tfrun` use case to an
  existing or missing test; specify new scenario tests (fixtures: run JSONs incl.
  encrypted inputs, fake API transcript assertions); define the coverage exclusion list
  (adapter files exercised only by opt-in e2e) and get `tfrun` to ≥90% statement
  coverage; maintain the D13 bug inventory (pin with `FIXME(bug)`, never fix here).
- **02 tf DDD refactor**: propose the package layout (domain/application/ports/adapters)
  with a migration sequence of ≤~15 always-compiling steps; explicit inventory of global
  state and its injection replacement; show how `Worker`+`SingleRunWorker` collapse; how
  `RunContextInfo`-in-context.Value is replaced. Includes **phase 2b**: the bug-fix pass
  over the phase-1 inventory (D13) as its own stacked PR.
- **03 shared core**: define the shared packages' API surface (config, reporting, polling,
  client+retry, crypto, registration); diff `controller/runapi.go` vs shared client and
  the merge path; controller re-base steps; alignment notes vs provider-client naming.
- **04 per-persona binaries**: `cmd/tf` + `cmd/bbrunner` (the superset = run-controller,
  auto-detecting its dispatcher; no separate `cmd/controller`); the runner personas' mains
  arrive in phase 6 (D1/D2); module/layout migration (go.mod to repo root, `./runner` dropped,
  `go.work`+`go.work.sum` deleted), Dockerfile
  matrix with direct entrypoints (no symlinks, D8), release workflow building the binaries
  via `./cmd/...`, meshfed-release doc updates (local-dev-stack uses `bbrunner`),
  `MANAGEMENT_PORT` unification incl. per-persona defaults and the new standalone-runner
  metrics (D12).
- **05 dispatcher**: `Dispatcher`/`RunHandler` interfaces, capacity semantics per
  dispatcher, in-process secret/auth model, concurrency hazards inventory (from risk #4)
  each with a test, explicit capability config + claim-and-fail-fast for unhandled
  types (D5).
- **06 kotlin ports**: split into an **umbrella plan + one sub-plan per runner** (= one
  PR each). The umbrella (`PLAN_DETAIL_06_kotlin_ports_umbrella.md`) owns consistency:
  the cross-runner behavior inventory from Kotlin sources (endpoints, auth, async
  semantics, config, block-runner-core mechanics), the shared template contract every
  sub-plan must satisfy (handler wiring, config section shape, Kotlin-tests-first
  pinning step per D6, acceptance validation, deprecation/removal sequence), and the
  port order. Sub-plans are authored umbrella-first, then `06A_manual` (defines the
  concrete template, its interfaces reviewed against the other three runners'
  inventoried needs before any port is implemented), then `06B_gitlab`/`06C_azdevops`/
  `06D_github` (may be authored in parallel against umbrella + 06A).
- **07 cleanup**: docs/README/release audit checklist; deprecation timeline; Go-only CI
  reshape incl. docker builds (D14); final architecture record.

## 8. Explicitly out of scope

- Extracting a cross-repo `meshstack-go-sdk` shared with terraform-provider-meshstack
  (confirmed: provider-client convergence happens later; D3 keeps the door open).
  The provider client's retry/backoff **is in scope** — this repo adopts it for the runner
  client (D3, plan 03) as an important robustness improvement. Only *merging* the two clients
  into one package/repo is deferred, not the retry feature.
- Changing the meshStack/meshfed API surface. The **mux** (`multiplexing-block-runner`)
  is not modified here, but this refactor is designed to make it obsolete (§1 downstream
  goal). Its **removal from meshfed-release** is **tracked as a separate
  meshfed-release work item**, not scheduled as a phase-7 task in this plan — this keeps
  this repo's task list from bloating further. Phase 7 only records the end state and the
  hand-off note pointing at that separate item.
- New runner features (this is a refactor; feature freeze per phase where possible).
