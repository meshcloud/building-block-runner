# Architecture

This is the maintained record of the Building Block Runner repository's architecture, as it stands after the
2026 single-Go-binary refactor. It targets maintainers and contributors (operators should start with the root
[`README.md`](../README.md)). The historical design plans that drove the refactor live in
[`docs/plans/`](plans/) — they are not maintained as the codebase evolves; this file is.

## 1. One binary, six personas

Every published image is a **direct entrypoint** for exactly one binary — there is no shared/multiplexed binary and
no `argv[0]`-based persona switch. Each persona is its own fit `main` package under `cmd/`, linking only the
dependencies it actually needs:

| Persona           | `cmd/` package    | Published image                | Links                                   |
|-------------------|-------------------|----------------------------------|------------------------------------------|
| tf                | `cmd/tf`          | `tf-block-runner`                | go-git, OpenTofu exec, no Kubernetes      |
| manual            | `cmd/manual`      | `manual-block-runner`            | meshapi/report/config only               |
| gitlab            | `cmd/gitlab`      | `gitlab-block-runner`            | meshapi/report/config only               |
| azdevops          | `cmd/azdevops`    | `azure-devops-block-runner`      | meshapi/report/config only               |
| github            | `cmd/github`      | `github-block-runner`            | meshapi/report/config only               |
| controller/superset | `cmd/bbrunner`  | `run-controller`                 | all five handlers + both dispatchers (below) |

`cmd/bbrunner` is the one deliberately fat image: it links every persona's handler plus the Kubernetes-Job dispatcher,
so that the same binary can act as the in-cluster controller *and* force any single persona to run standalone.
Invocation:

- `bbrunner` (no subcommand) — the **controller**: polls meshStack, decrypts run details, and dispatches each claimed
  run as a Kubernetes Job running the corresponding fit persona image (`kubernetes.go`/`internal/k8sjob`). This is
  the default and only mode of the published `run-controller` image today.
- `bbrunner <persona>` (`tf`, `manual`, `gitlab`, `azdevops`, `github`) — forces that one persona in-process, the same
  wiring its standalone `cmd/<persona>` binary runs. Used for local development and as the multiplexing-block-runner
  (mux) replacement.
- `bbrunner <unknown>` / extra arguments — a non-zero-exit usage error (never a silent default).

Each persona bootstrap stamps its own identity name on the meshStack runner headers and keeps its own log-line
identity regardless of whether it runs standalone or forced in-process via `bbrunner <persona>`.

**Design intent vs. current implementation — a partially-closed gap.** The design that motivated this refactor
(`docs/plans/PLAN_HIGH_LEVEL.md` D1/D2) specifies that `bbrunner`'s default (no-subcommand) invocation should
*auto-detect* whether it is running inside a Kubernetes cluster (`rest.InClusterConfig()`/
`KUBERNETES_SERVICE_HOST`) and pick its dispatcher accordingly — `KubernetesJobDispatcher` in-cluster,
an all-types `InProcessDispatcher` (registering every linked handler) out of cluster — overridable with
`RUNNER_DISPATCHER`. This is the property meant to make the standalone `multiplexing-block-runner` in
meshfed-release obsolete (§1 downstream goal). **As shipped, the detection mechanism and the `RUNNER_DISPATCHER`
override now exist** (`cmd/bbrunner/dispatcher.go`: `KUBERNETES_SERVICE_HOST` present ⇒ `kubernetes`, else
`inprocess`; `RUNNER_DISPATCHER` overrides; unit-tested), and the standalone per-persona in-process paths
(`bbrunner <persona>` / `cmd/<persona>`) use it. **What is still missing is the controller *superset***: the
default no-subcommand `bbrunner` still always builds a `KubernetesJobDispatcher` (`cmd/bbrunner/controller.go`),
and an explicit `RUNNER_DISPATCHER=inprocess` on the controller **fails fast** with an actionable message rather
than running all five persona handlers in one process — that needs each persona's config loaded into the
controller bootstrap (not yet wired). So running the `run-controller` image out of cluster as an all-types,
mux-replacing runner is still not possible; closing this remains the largest design-vs-shipped gap and is required
before meshfed-release's `multiplexing-block-runner` can be retired. Tracked in [`FOLLOW_UP.md`](../FOLLOW_UP.md) and §8.

## 2. Package map

The module lives at the repo root; every non-`cmd` package sits one level below the root under `internal/` (a
visibility mechanism, not counted as nesting depth) — no deeper subdirectories, no hyphenated directory names,
package name always equal to its last path element:

| Package | Concept |
|---|---|
| `internal/meshapi` | The meshStack API client: claim/register/patch-status/artifact-download, DTOs, retry/backoff, media types, runner headers |
| `internal/meshapitest` | A reusable `httptest`-backed fake meshStack server, shared by every runner's integration tests |
| `internal/crypto` | RSA/AES hybrid decrypt (the meshStack runner key exchange) |
| `internal/config` | Config loading mechanics: two-layer YAML deep-merge, `${VAR}` interpolation, env bindings, deprecated-alias helpers, the legacy-env fail-fast guard |
| `internal/report` | The shared status/log reporting facility: `Reporter`, `Progress`/`Observer` (tf's 10s ticker), `RunLog` |
| `internal/mgmt` | The unified `/healthz` + `/metrics` listener and the generic `runner_*` metrics every standalone persona wires in |
| `internal/dispatch` | Backend-agnostic claim/dispatch: `Loop`, the `Dispatcher`/`RunHandler` interfaces, `InProcess` dispatcher, capability parsing, claim-and-fail-fast (`UnhandledTypeError`) |
| `internal/k8sjob` | The Kubernetes-specific `Dispatcher`: Job/Secret/ServiceAccount manifest building, WIF/OIDC discovery, capacity guard |
| `internal/tf` | The tf-block-runner domain: run engine, git/OpenTofu adapters, backend/workspace handling |
| `internal/manual`, `internal/gitlab`, `internal/azdevops`, `internal/github` | One package per ported runner persona, each a `dispatch.RunHandler` |
| `internal/build` | Compile-time version stamping (`-ldflags -X`) |
| `internal/resources` | Shared test fixtures |

**Dependency direction** (domain must not import adapters; only `cmd/<persona>` mains wire dependencies together) is
enforced by `depguard` rules in [`.golangci.yml`](../.golangci.yml), keyed on file globs per package — not by tree
shape alone. Only `internal/mgmt`, `internal/dispatch`, `internal/k8sjob` and the `cmd/bbrunner`/persona `main`
packages may import `prometheus/*`; only `internal/meshapi`, `internal/crypto`, `internal/config`, `internal/report`
and `internal/meshapitest` may import outside the module's own packages plus `gopkg.in/yaml.v2` (`internal-client`
depguard group) — every other package is stdlib-plus-siblings only.

> **D11 is linter-enforced (P1.1 closed).** The per-package `depguard` file-globs in `.golangci.yml` are anchored as
> `**/internal/X/*.go` (and `**/cmd/X/*.go`), so they match the absolute file paths depguard sees at lint time and
> the layering rules are live — a domain package importing `prometheus/*` outside the allowed set, or an adapter,
> now fails `task lint`. Two glob details matter and must be kept: the leading `**/` anchors the absolute path, and
> there is **no** middle `/**/ ` segment (these packages are flat — every `.go` file sits directly under
> `internal/X/`, and gobwas-glob's `/**/*.go` will not match a file with zero intermediate directories, which is why
> the earlier `internal/X/**/*.go` form silently matched **zero files**). The `internal/build` logging-stack AST
> test remains a complementary check.

**Coverage-excluded, real-I/O adapter files** (one file, one justification, in
[`tools/coverage/exclusions.txt`](../tools/coverage/exclusions.txt)): `internal/tf/git.go` (real git/SSH clone —
auth transports and remote quirks need a live server), `internal/tf/tfbinaries.go` (real HashiCorp/OpenTofu release
downloads), `internal/k8sjob/cluster.go` (real in-cluster/kubeconfig discovery and OIDC discovery HTTP against a
live API server). Everything else in those packages is hermetically tested.

## 3. Execution model

A **persona bootstrap** (`cmd/<persona>/main.go`, or the equivalent `cmd/bbrunner/<persona>.go` wiring) builds:

1. A **handler** — a `dispatch.RunHandler` that knows how to execute one run type (the tf `Engine`, or one of the
   four ported handlers).
2. A **dispatcher** — either `dispatch.InProcess` (a `go func` per run, in-flight counter, drains on shutdown; the
   standalone/local-dev/mux-replacement path, default concurrency `RUNNER_MAX_CONCURRENT_RUNS=3`, negative = 
   unlimited with a 10-per-cycle backstop) or, for `run-controller`, `k8sjob.KubernetesJobDispatcher` (dispatches a
   Kubernetes Job running the matching fit persona image, `EXECUTION_MODE=single-run`, default
   `maxConcurrentJobs=10`).
3. A **`dispatch.Loop`** — claims runs from meshStack on a fixed poll interval (10s for the four ported personas,
   matching their Kotlin `@Scheduled(fixedRate=10000)` precedent) and hands each claimed run to the dispatcher.

**Claim-and-fail-fast (D5):** a runner's registered *capability* (one concrete `RunnerImplementationType`, or `ALL`
— the backend enum is single-valued, subsets are not representable) only shapes what meshStack lets it claim. A
claimed run whose type has neither an in-process handler nor a Job template is immediately registered and reported
`FAILED`, using the runner's own process credentials (never the claimed run's token — reaching for that run's token
before any handler owns it would carve an exception into the "runToken = executing handler only" invariant). There
are deliberately **two different messages** for this one error type: the Kubernetes-dispatcher message is
byte-identical to the pre-refactor controller's wording (a frozen, customer-visible run-status string) even though
it reads as vague ("no implementation handler configured for type '%s'"); the in-process-dispatcher message is new
and actionable, telling a standalone operator to either register a narrower capability or run the type elsewhere.
Unifying them would either change frozen wire bytes or ship the vague text to new standalone users — see
`internal/dispatch/unhandledtype.go`.

**Single-run mode:** the tf persona (and, via the shared `config` alias, every ported persona) also supports
`EXECUTION_MODE=single-run` (alias: `SPRING_PROFILES_ACTIVE` containing `kubernetes`, the pre-existing Kotlin
trigger every operator's Job template already sets) — read one run from `RUN_JSON_FILE_PATH` (default
`/var/run/secrets/meshstack/run.json`), execute it with runToken-only auth, and exit. This is the shape a
`KubernetesJobDispatcher`-dispatched Job runs; it deliberately stays separate glue from the in-process handler path
in `cmd/tf/main.go` today (unifying the two, so the k8s Job path reuses the exact same handler the in-process
dispatcher calls, is a documented near-term cleanup — see §8).

## 4. Frozen contracts register

The following are the customer-facing, wire-visible contracts this refactor preserves byte-for-byte (see
`docs/plans/PLAN_HIGH_LEVEL.md` D9/D10 for the full historical pin list; this section is the living summary):

- **Wire shapes:** the claim/register/status-PATCH bodies, media types, the `X-Block-Runner-Node-Id`/runner headers,
  plan-artifact download (128 MiB cap — the former same-origin check was intentionally removed before this refactor
  and is not reintroduced).
- **The Kubernetes Job contract:** env vars (`EXECUTION_MODE=single-run`, `RUN_JSON_FILE_PATH`,
  `RUNNER_UUID`, `RUNNER_API_URL`), the run-JSON mount path, `BackoffLimit: 1`/`RestartPolicy: Never`, runToken-only
  auth for the dispatched Job.
- **Image names, tags and entrypoints** (`tf-block-runner`, `run-controller`, `manual-block-runner`,
  `gitlab-block-runner`, `azure-devops-block-runner`, `github-block-runner`) — customers reference these directly.
  The `tf-block-runner` image additionally ships a `/app/tfrunner` copy of its binary as a legacy `command:` override
  target.
- **`/healthz`** body `OK` and the resolved management port precedence `MANAGEMENT_PORT > PORT > per-persona default`.
- **Every metric name and label** — Prometheus metric names are a de-facto public surface (operator dashboards scrape
  them); no metric has been renamed by this refactor, only new series added (`runner_*` for standalone personas;
  `run_controller_*`, byte-identical to the pre-refactor controller's own series, for `run-controller`). See §6.
- **All config keys and environment variables** — nothing has been removed, only new aliases added (§5).

**Sanctioned behavior change (L14):** the pre-refactor controller decrypt-failure quirk — where a run whose
details could not be decrypted was left to sit silently until the coordinator's own timeout reclaimed it — has
been fixed in this cleanup phase. The controller now reports a terminal `FAILED` status with actionable
key-mismatch guidance (wording aligned with the tf runner's, `internal/tf/run.go`) via the already-accepted
`reportRunFailure` wire shape, while still incrementing `run_controller_decryption_errors_total` (P5: never
suppress silently). This is the one error-path wire-behavior change in this phase; every happy path stays
byte-identical. The former `SilentDispatchFailure` seam is removed.

## 5. Configuration

Precedence: **compiled-in defaults < shared base YAML < per-persona YAML < environment variables** (env always
wins). `${VAR}` interpolation inside either YAML layer is how legacy env-var spellings are honored declaratively,
rather than via a growing alias table in code. A config loader that sees a `BLOCKRUNNER_*`-prefixed (or otherwise
known-legacy) environment variable that no key or interpolation ever consumes fails startup with an actionable
error — a stale Spring relaxed-binding-style override must never silently boot the runner on wrong defaults.

**Two-layer deep-merge — available, and used by one persona today.** The shared
`config.Loader.Load(basePath, perImplPath, into)` deep-merges an optional shared top-level base
(`containers/runner-config.yml`) *under* a per-impl file (base < per-impl, key-wise, nested maps merged). It is a
live, exercised mechanism — but only **gitlab** wires a non-empty base layer: its `gitlab-block-runner` image bakes
both `containers/runner-config.yml` → `/app/containers/runner-config.yml` and its own file → `/app/runner-config.yml`
(WORKDIR `/app`), so at runtime the base layer *is* read to supply the well-known local-dev private key, which
gitlab's per-impl file intentionally does not duplicate (verified in `internal/gitlab/containerconfig_test.go` and
`internal/config/basekey_test.go`). The other three ported personas call `Load` with an **empty** base path and load
a single self-contained `containers/<persona>/runner-config.yml` (`manual` needs no key; `github` and `azdevops`
each bake the dev key directly into their own per-impl file). The `tf` runner and the `run-controller` do not use
`config.Loader` at all — each decodes its own single self-contained `runner-config.yml` via `os.ReadFile`+
`yaml.Unmarshal`. So the base layer is neither dead code nor universally wired: it is the deliberate seam that keeps
the one cross-persona default (the dev key) DRY for the persona that opted into it.

### 5.1 Alias inventory

| Deprecated | Canonical | Warned? |
|---|---|---|
| `PORT` | `MANAGEMENT_PORT` | yes, on every persona but `run-controller` (which never read `PORT`) |
| `RUNCONTROLLER_CONFIG_FILE` | `RUNNER_CONFIG_FILE` | yes |
| `SPRING_PROFILES_ACTIVE` containing `kubernetes` | `EXECUTION_MODE=single-run` | yes |
| `blockrunner:` YAML block (`uuid`, `version`, `api.url`, `auth.*` incl. kebab-case `api-key.client-id`, `debugMode`, `privateKey`/`privateKeyFile`) | flat, persona-level YAML keys | yes, per field applied |
| `logging.*` / `server.*` / `spring.*` YAML blocks | — (ignored) | **yes** — a top-level `logging:`/`server:`/`spring:` block in a config file loaded through the shared `config.Loader` (the four ported personas) logs one warn-and-ignore line (`config.Loader.WarnIgnoredLegacyYAMLBlocks`); warn-only, never fatal (see `docs/DEPRECATIONS.md` §4) |
| `api.user` (tf) vs `api.username` (other personas) | both accepted | **no** — neither spelling was ever renamed; not a deprecation |
| metric names | — | **none renamed** — no alias duty applies |

**Timeline:** every alias above is supported for the lifetime of the current major version, and at minimum 12
months from this refactor's general availability, whichever is longer. Config keys and environment variables are
customer-facing API (a public, self-hosted repository) — removal, if it ever happens, is scheduled no earlier than
the next major release and always called out in release notes ahead of time.

### 5.2 Concurrency

Standalone in-process personas default to `RUNNER_MAX_CONCURRENT_RUNS=3` concurrent runs (negative = unlimited, with
a 10-per-cycle backstop) — an intentional throughput improvement over the historical one-run-at-a-time cadence.
`run-controller`'s Kubernetes dispatch defaults `maxConcurrentJobs` to 10 (file-only key, down from a historical 20
— the one sanctioned capacity retune of this refactor).

## 6. Observability

Every persona serves `GET /healthz` (`200 OK`, body `OK`) and `GET /metrics` on one `MANAGEMENT_PORT`-configured
listener — nothing is ever served twice on separate ports. Default ports (unchanged from before this refactor,
preserved per persona): `tf-block-runner` 8100, `azure-devops-block-runner` 8101, `github-block-runner` 8102,
`gitlab-block-runner` 8103, `manual-block-runner` 8104, `run-controller` 2112 (which gained `/healthz` for the first
time in this refactor).

Metric series:

- **`run_controller_*`** (run-controller only, byte-identical names/labels to the pre-refactor controller):
  `run_controller_runs_fetch_errors_total`, `run_controller_runs_fetch_duration_seconds`,
  `run_controller_jobs_created_total`, `run_controller_job_creation_errors_total`,
  `run_controller_job_creation_duration_seconds`, `run_controller_jobs_at_capacity_skips_total`,
  `run_controller_service_accounts_created_total`, `run_controller_service_account_creation_errors_total`,
  `run_controller_decryption_errors_total`, `run_controller_runner_registration_success_total`,
  `run_controller_runner_registration_errors_total`, `run_controller_loop_iterations_total`,
  `run_controller_active_runners`.
  
- **`runner_*`** (every other persona, new in this refactor — standalone runners previously shipped zero metrics):
  `runner_runs_claimed_total`, `runner_runs_succeeded_total`, `runner_runs_failed_total`,
  `runner_run_duration_seconds`, `runner_poll_errors_total`, each labeled by `runner_uuid`.

Metric names are a de-facto public surface (operator dashboards scrape them) — any future rename needs the same
alias/deprecation treatment as an environment variable (§5).

## 7. Testing & gates

**Scenario tests over unit-test armadas.** The statement-coverage gate is reached with use-case/scenario tests that
drive the engine/handlers black-box, against a real HTTP server (`internal/meshapitest`, an `httptest`-backed fake
meshStack, shared by every runner's tests) rather than a hand-rolled `RoundTripper`. Unit tests are added only where
a unit has real decision surface of its own (parsers, crypto, type conversions) — coverage percentage is never, by
itself, a reason to add a test.

**Gate mechanics:** `task coverage` runs `go test -coverprofile=... ./...` then
[`tools/coverage/check.sh`](../tools/coverage/check.sh), which recomputes statement coverage per package-prefix
against [`tools/coverage/thresholds.txt`](../tools/coverage/thresholds.txt) (currently 90% on every domain/shared
package: `tf`, `config`, `meshapi`, `report`, `mgmt`, `dispatch`, `k8sjob`, `manual`, `gitlab`, `azdevops`, `github`)
after dropping the lines listed in [`tools/coverage/exclusions.txt`](../tools/coverage/exclusions.txt) (§2). A
package with zero matching statements in the profile (a stale prefix, a typo, or a fully-excluded file) fails the
gate rather than passing vacuously.

`task test` always runs with `-race` — the concurrency hazards of the in-process dispatcher (shared `TfBinaries`
install-dir locking, per-run working-directory isolation, no shared mutable status structs) are exercised, not just
hoped away.

## 8. Follow-up register

Items consciously deferred past this refactor, so the next engineer finds every one of them in a single place
instead of re-discovering them:

- **Dispatcher auto-detection is implemented; the controller in-process *superset* is not** (§1). The
  `RUNNER_DISPATCHER` override and in-cluster/out-of-cluster detection now exist (`cmd/bbrunner/dispatcher.go`) and
  drive the standalone per-persona in-process paths, but `bbrunner`'s default (no-subcommand) controller mode still
  always uses the Kubernetes-Job dispatcher, and `RUNNER_DISPATCHER=inprocess` on the controller fails fast rather
  than running all five handlers in one process (each persona's config must first be wired into the controller
  bootstrap). Until that lands, running the `run-controller` image outside a cluster as an all-types, in-process,
  mux-replacing runner is **not possible**. This is the single largest gap between the design record in
  `docs/plans/` and the shipped binary; closing it is required before the `multiplexing-block-runner` in
  meshfed-release can actually be retired (the refactor's stated downstream goal).
- **tf single-run/handler unification (deferred).** The Kubernetes-Job single-run path (`cmd/tf/main.go`) still
  duplicates logic that the in-process tf handler also has, rather than calling through the same handler. This
  cleanup phase deliberately did **not** unify them (L15 was a guarded should-have, abandonable on any pinned-
  observable drift; the parallel glue was kept to hold the frozen single-run wire/exit contract byte-identical).
  A future unification remains a should-have — abandon rather than force it through if any pinned observable moves.
- **`meshstack-go-sdk` extraction.** `internal/meshapi` and `terraform-provider-meshstack/client` remain separate
  packages in separate repos; a future extraction into one shared module is explicitly out of this refactor's
  scope, though `internal/meshapi` already adopted the provider client's retry/backoff design so the eventual merge
  stays cheap.
- **Abort-flag support for the four ported runners** (manual/gitlab/azdevops/github) — only the tf runner honors an
  abort signal from the status-PATCH response today; adding it to the ported runners is a feature, not part of a
  faithful port.
- **Per-type meshfed-release acceptance tests for gitlab and azure-devops** — these two ports have no smoke-test
  coverage in the sibling `meshstack-smoke-tests` repo (github, tf and manual do); their correctness rests on
  in-repo scenario/transcript tests instead. Commissioning new acceptance tests for them is out of this repo's
  scope.
- **Mixed dispatch in one process** (some run types dispatched in-process, others as Kubernetes Jobs, from the same
  `bbrunner` instance) — the `Dispatcher` seam makes this a small change if a future persona needs it; none does
  today.
- **Alias removals** (§5.1) — none are scheduled; any future removal follows the documented timeline and always
  ships with release-note warning ahead of time, never inside a routine release.

See `docs/plans/PLAN_DETAIL_07_cleanup.md` §4 for the exhaustive, phase-numbered ledger this register was
distilled from, and `docs/plans/ERRATA.md` for known factual corrections to the archived plan text.
