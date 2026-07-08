# High-Level Plan: Single Go Binary for all Building Block Runners

**Status:** draft for review · **Branch:** `refactor/single-go-binary` · **Owner:** @agrub

## 1. Goal

All apps in this repo become **one Go binary** (one `main.go`), selecting behavior from
`argv[0]` (busybox-style symlinks — the pattern of `multiplexing-block-runner` in
meshfed-release, tf-block-runner and run-controller are the in-repo starting points):

- `tf-block-runner`, `run-controller`, and (eventually) `manual-`, `github-`, `gitlab-`,
  `azure-devops-block-runner` are **personas** of the same binary.
- Docker images share the binary; they differ only in entrypoint symlink and base toolchain.
- **run-controller mode is just dispatching.** Dispatch targets are pluggable:
  - `KubernetesJobDispatcher` — today's behavior (k8s Job per run).
  - `InProcessDispatcher` — `go func` per run inside one process; this makes a standalone
    Docker runner able to execute **multiple runs of any type concurrently**.
  - A polling standalone runner is then the degenerate case: in-process dispatch with a
    single handler type.
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
- **P5 — Fail fast, never suppress silently.** Errors carry context (`fmt.Errorf` with
  `%w`), are handled or escalated, never swallowed. Validation at startup (config) fails
  the process with an actionable message, as the config packages already do.
- **P6 — Naming:** acronyms of 2+ letters keep only the first letter uppercase (`Id`,
  `Uuid`, `Api`) — consistent with both sibling repos and this repo's DTOs.
- **P7 — Tests are part of every step,** not a follow-up (meshfed-release build-and-test
  rule); the coverage gate (D6) never dips below threshold once enabled.
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

## 4. Design decisions (self-grilled; override in review if wrong)

- **D1 — argv[0] selects the persona; env/config selects the mode.** `EXECUTION_MODE=single-run`
  etc. stay env/config concerns inside a persona. Fallback: `bbrunner <persona> [...]` when
  `argv[0]` is unrecognized (needed for `go run .` and debugging).
- **D2 — one Go module for the binary.** New root `main.go` + `cmd/` persona registry; the
  existing three modules collapse into it stepwise (workspace stays during migration).
  Accepted cost: k8s client-go is linked into every persona (binary size, not runtime cost).
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
- **D5 — dispatcher = capability registry.** The controller loop depends on a `Dispatcher`
  interface. Capability (which implementation types it claims/registers for) derives from
  what's registered: k8s job templates (any type) and/or in-process Go handlers (initially
  only `TERRAFORM`, later each ported runner). This makes the Kotlin ports incremental —
  no big-bang: un-ported types keep running via k8s Jobs with the existing Kotlin images.
- **D6 — coverage gate: ≥90% lines on the refactored domain + application packages**, from
  hermetic integration-style tests (fake HTTP API + `TfFacade`/`GitFacade` mocks — the
  existing scenario-test style, extended). Real-tofu/real-git e2e is a separate opt-in make
  target, not part of the gate (network + nondeterminism). Gate enforced in CI via
  `go test -coverprofile` + threshold script.
- **D7 — config precedence: defaults < YAML file < env.** One config package; file path via
  `RUNNER_CONFIG_FILE` (default `runner-config.yml`). Nested structures (e.g. controller
  `implementations` map) remain file-only — env-first ≠ env-only. All existing env var
  names and file keys keep working (aliases + deprecation warnings where renamed).
- **D8 — one binary, several thin images.** Same binary copied into per-persona images
  (tf needs git/tofu/nix/aws-cli; controller is slim), entrypoint = persona symlink.
  Published image names stay (`tf-block-runner`, `run-controller`, …) — customers reference
  them.
- **D9 — behavior pins (characterization tests must cover these before refactor):**
  async runs report `IN_PROGRESS` on successful handover; abort flag via status PATCH
  response cancels the run context; 10s status ticker; run-token > base-auth precedence and
  `ClearRunToken` after execution; 409-on-register = success; 404/409-on-claim = no run;
  media types + `X-Block-Runner-Node-Id`/runner headers; plan-artifact download (only
  same-origin URL, 128MiB cap); meshStack HTTP backend fallback incl. `TF_HTTP_USERNAME/
  PASSWORD` ephemeral auth; pre-run script contract (`$MESHSTACK_USER_MESSAGE`, run JSON on
  stdin); `aaaaaa_…auto.tfvars` + `meshStack_run_vars.tf` generation rules (run-scoped vars
  omitted on DETECT/saved-plan APPLY); FILE inputs as data-URLs; env whitelist
  (`cleanSystemEnv`); decrypt-failure UX (key-mismatch guidance); workspace select/create/
  delete naming logic; k8s single-run contract (`RUN_JSON_FILE_PATH`,
  `/var/run/secrets/meshstack/run.json`, `RUNNER_UUID`, `RUNNER_API_URL`, runToken-only auth).
- **D10 — compatibility commitments during rollout:** old controller must be able to
  dispatch to new runner images and vice versa (the k8s Job contract in D9 is frozen);
  mux claim contract unchanged; healthz ports unchanged; meshfed-release `local-dev-stack`
  + acceptance tests keep working (update that repo's docs in lock-step when layout changes).
- **D11 — package layout: flat concept packages, one conceptual level deep.** The single
  binary lives in a module at `./runner` (coexists with legacy module dirs during
  migration; they are deleted phase by phase). Packages sit at exactly one level below the
  module root, under `internal/` (visibility mechanism, exempt from the depth count — the
  repo is public and these packages are not API): `internal/meshapi`, `internal/crypto`,
  `internal/config`, `internal/report`, `internal/dispatch`, `internal/k8sjob`,
  `internal/tf`, `internal/manual`, `internal/gitlab`, `internal/azdevops`,
  `internal/github`. Rules: package name = last path element, named for a domain concept —
  never `api`/`util`/`common`/`core` (the existing `tf-block-runner/util` dissolves into
  its callers); no hyphenated directories (package identifier ≠ dir name is a permanent
  papercut); no deeper nesting — a parent dir earns its place only by discriminating, and
  call sites only ever see the last element anyway. Dependency direction (domain must not
  import adapters; only `main.go` wires) is enforced by `depguard` in golangci-lint — the
  same mechanism both sibling repos use — not by tree shape. The tf handler may split into
  sibling packages (`tf` + e.g. `gitsource`, `tofu`) in Phase 2 only if the seams prove
  real; one cohesive package is acceptable otherwise.

## 5. Phases (order matters)

**Delivery model: one phase = one single-commit PR, stacked.** All detail plans are
written up-front, then the phases are implemented by running through them in order; each
phase is one squash-merged PR whose base is the previous phase's branch (stacked PRs,
merged sequentially into `main`). Each lands green, behavior-compatible, and reviewable on
its own; this feature branch carries only the plan documents. Phase 6 is the exception:
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
yet gating); inventory of untested behaviors against the D9 pin list; verify the
meshfed-release local-dev-stack + acceptance suite runs as the outer safety net.
**Exit:** baseline numbers documented; CI publishes coverage.
→ Detail plan: `PLAN_DETAIL_00_guardrails.md`

### Phase 1 — Characterization tests to ≥90% (tf runner, pre-refactor)
Extend the existing scenario-suite style (fake HTTP transport, mocked facades) to cover
every D9 pin and every use case (APPLY/DETECT/DESTROY × polling/single-run × async ×
artifact-replay × failure paths). Tests are written against *current* behavior at
use-case level (black-box through `Worker`/`SingleRunWorker`/`Manager`), so they survive
the restructuring. **Exit:** ≥90% lines on `tfrun` (excluding declared adapter files),
gate ON in CI.
→ `PLAN_DETAIL_01_tf_characterization_tests.md`

### Phase 2 — DDD refactor of the tf runner (under green tests)
Extract domain (run, steps, status), application (execution engine unifying
`Worker`/`SingleRunWorker`, observer/reporting), ports & adapters (D4). Eliminate globals
(`AppConfig`, `meshcrypto.Crypto`) via injection. Small, always-green steps; coverage gate
stays ≥90%. **Exit:** one execution engine; polling and single-run are `RunSource`
configurations; no package-level mutable state.
→ `PLAN_DETAIL_02_tf_ddd_refactor.md`

### Phase 3 — Shared runner-core & client consolidation
Move runner-agnostic pieces to shared packages: config loader (D7), reporting facility,
polling/claim engine, crypto, registration, retry/backoff (adopted from the provider
client's design, D3). Re-base run-controller onto them (its `runapi.go` duplication
disappears; `controller.AppConfig` global goes). **Exit:** tf runner and controller share
client, config, reporting; behavior unchanged (controller tests + acceptance suite).
→ `PLAN_DETAIL_03_shared_core.md`

### Phase 4 — Single binary & argv[0] personas
Root `main.go` + persona registry (D1, D2); Dockerfiles switch to symlink entrypoints
(D8); CI/release matrix builds one binary, N images; module consolidation; update
meshfed-release local-dev-stack docs. **Exit:** `tf-block-runner` and `run-controller`
images ship from the single binary; old per-module mains deleted.
→ `PLAN_DETAIL_04_single_binary.md`

### Phase 5 — Dispatcher abstraction & in-process concurrency
`Dispatcher` interface behind the controller loop; extract `KubernetesJobDispatcher`;
add `InProcessDispatcher` (go-func per run, per-run decrypt then runToken-only reporting —
same trust model as the k8s Secret handover; `maxConcurrentRuns` mirrors
`maxConcurrentJobs`; per-run working dirs; version-download locking in `TfBinaries`).
Standalone polling runner becomes in-process dispatch with one handler. Registration
capability derives from the registry (D5). **Exit:** one Docker container executes N
concurrent TERRAFORM runs; k8s mode bit-identical to today.
→ `PLAN_DETAIL_05_dispatcher.md`

### Phase 6 — Port Kotlin runners as Go handlers (one PR per runner)
Order by complexity: `manual` → `gitlab` → `azure-devops` → `github` (App JWT). Each is a
`RunHandler` plugged into the engine + reporting facility (async handover semantics from
D9). **The `manual` port is the template PR:** it must introduce the persona wiring,
handler registration, config section shape, acceptance-test harness and Dockerfile pattern
in the exact form the other three reuse — anticipate their needs (async handover, external
pipeline polling, per-runner secrets) in the interfaces even though `manual` itself needs
none of them. Each port is validated against the meshfed-release acceptance tests before
the corresponding Kotlin module is deprecated. **Exit (per runner/PR):** Go handler passes
acceptance; image switched; Kotlin module removed. **Exit (phase):** Gradle build gone.
→ `PLAN_DETAIL_06_kotlin_ports.md`

### Phase 7 — Cleanup & docs
READMEs, public docs pointers, release workflow simplification, config deprecation
warnings, memory of final architecture. → `PLAN_DETAIL_07_cleanup.md`

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
  CI wiring); design the threshold gate (fail-under, excluded files policy); verify
  local-dev-stack acceptance flow against this branch; document baseline numbers.
- **01 tf characterization tests**: map every D9 pin + every `tfrun` use case to an
  existing or missing test; specify new scenario tests (fixtures: run JSONs incl.
  encrypted inputs, fake API transcript assertions); define the coverage exclusion list
  (adapter files exercised only by opt-in e2e) and get `tfrun` to ≥90%.
- **02 tf DDD refactor**: propose the package layout (domain/application/ports/adapters)
  with a migration sequence of ≤~15 always-compiling steps; explicit inventory of global
  state and its injection replacement; show how `Worker`+`SingleRunWorker` collapse; how
  `RunContextInfo`-in-context.Value is replaced.
- **03 shared core**: define the shared packages' API surface (config, reporting, polling,
  client+retry, crypto, registration); diff `controller/runapi.go` vs shared client and
  the merge path; controller re-base steps; alignment notes vs provider-client naming.
- **04 single binary**: persona registry design (argv[0] + fallback arg), module/layout
  migration incl. `go.work` endgame, Dockerfile matrix, release workflow changes,
  meshfed-release doc updates, healthz/metrics port unification table.
- **05 dispatcher**: `Dispatcher`/`RunHandler` interfaces, capacity semantics per
  dispatcher, in-process secret/auth model, concurrency hazards inventory (from risk #4)
  each with a test, registration capability derivation.
- **06 kotlin ports**: per-runner behavior inventory from Kotlin sources (endpoints,
  auth, async semantics, config), Go handler design, acceptance-test validation plan,
  deprecation/removal sequence. One sub-plan per runner (= one PR each); the `manual`
  sub-plan is written first and must define the template every later port follows —
  its interfaces are reviewed against the *other three* runners' inventoried needs
  before any port is implemented.
- **07 cleanup**: docs/README/release audit checklist; deprecation timeline; final
  architecture record.

## 8. Explicitly out of scope

- Extracting a cross-repo `meshstack-go-sdk` shared with terraform-provider-meshstack
  (confirmed: provider-client convergence happens later; D3 keeps the door open).
- Changing the meshStack/meshfed API surface or the mux.
- New runner features (this is a refactor; feature freeze per phase where possible).
