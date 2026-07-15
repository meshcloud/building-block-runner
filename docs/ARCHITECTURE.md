# Architecture

This is the maintained record of the Building Block Runner repository's architecture. It targets maintainers and
contributors (operators should start with the root [`README.md`](../README.md)). Keep it current as the codebase
evolves — describe what the code *is* and why it is shaped that way, not how it got here.

## 1. One binary per runner type

Every published image is a **direct entrypoint** for exactly one binary. Each runner type has its own `main` package
under `cmd/`, linking only the dependencies it actually needs:

| Type         | `cmd/` package | Published image             | Links                                        |
|--------------|----------------|-----------------------------|----------------------------------------------|
| Terraform    | `cmd/tf`       | `tf-block-runner`           | go-git, OpenTofu exec — no Kubernetes        |
| Manual       | `cmd/manual`   | `manual-block-runner`       | meshapi/report/config only                   |
| GitLab       | `cmd/gitlab`   | `gitlab-block-runner`       | meshapi/report/config only                   |
| Azure DevOps | `cmd/azdevops` | `azure-devops-block-runner` | meshapi/report/config only                   |
| GitHub       | `cmd/github`   | `github-block-runner`       | meshapi/report/config only                   |
| `ALL`        | `cmd/bbrunner` | `run-controller`            | all five handlers + the Kubernetes dispatcher |

`cmd/bbrunner` is the one image that links every type's handler plus the Kubernetes-Job dispatcher, so the same binary
can act as the in-cluster controller *and* run any single type in-process. Invocation:

- `bbrunner` (no subcommand) — the **controller** (`ALL` type). It polls meshStack, decrypts run details, and
  dispatches each claimed run. Its dispatcher is auto-detected (`cmd/bbrunner/dispatcher.go`, overridable with
  `RUNNER_DISPATCHER`):
  - **in-cluster** (`KUBERNETES_SERVICE_HOST` present) — dispatches each claimed run as a Kubernetes Job running the
    image configured for that run's type (`internal/k8sjob`). This is the `run-controller` image's production mode.
  - **out of cluster** (or `RUNNER_DISPATCHER=inprocess`) — runs every linked handler in one `dispatch.InProcess`
    dispatcher, claiming under the controller's single `ALL` identity and routing each claimed run to the matching
    in-process handler (`cmd/bbrunner/superset.go`).
- `bbrunner <type>` (`tf`, `manual`, `gitlab`, `azdevops`, `github`) — runs that one type in-process, the same wiring
  its standalone `cmd/<type>` binary runs. Used for local development.
- `bbrunner <unknown>` or extra arguments — a non-zero-exit usage error (never a silent default).

Each type's bootstrap stamps its own identity on the meshStack runner headers and keeps its own log-line identity,
whether it runs standalone or in-process via `bbrunner <type>`.

The `ALL` type is single-valued in the backend enum — a runner registers exactly one concrete type or `ALL`; subsets
are not representable. Only the controller registers `ALL`; it claims every run type and fans them out (to per-type
Jobs in-cluster, or to per-type in-process handlers out of cluster).

## 2. Package map

The module lives at the repo root. Every non-`cmd` package sits under `internal/` (a visibility mechanism), one
concept per package, with no `util`/`common`/`core` catch-alls. The package name always equals its last path element.
Packages are flat today; a domain package with real internal structure (e.g. `internal/tf`) may grow subpackages as
it earns them — the rule is one-concept-per-package, not a hard ban on depth.

The most important packages:

| Package | Concept |
|---|---|
| `internal/meshapi` | The meshStack API client: claim/register/patch-status/artifact-download, DTOs, retry/backoff, media types, runner headers |
| `internal/crypto` | RSA/AES hybrid decrypt (the meshStack runner key exchange) |
| `internal/secret` | The shared decryptor seam (`Decryptor`, `NoopDecryptor`, `CertDecryptor`) and the sensitive-input type policy: only STRING/CODE/FILE inputs may be encrypted, and a sensitive input of any other type fails the run |
| `internal/config` | Config loading: two-layer YAML deep-merge, `${VAR}` interpolation, env bindings, deprecated-alias helpers, the legacy-env fail-fast guard |
| `internal/report` | Shared status/log reporting: `Reporter`, `Progress`/`Observer`, `RunLog` |
| `internal/observability` | The unified `/healthz` + `/metrics` listener and the generic `runner_*` metrics (see §6) |
| `internal/dispatch` | Backend-agnostic claim/dispatch: `Loop`, the `Dispatcher`/`RunHandler` interfaces, the `InProcess` dispatcher, capability parsing, claim-and-fail-fast |
| `internal/k8sjob` | The Kubernetes-specific `Dispatcher`: Job/Secret/ServiceAccount manifest building, WIF/OIDC discovery, capacity guard, the type→image map |
| `internal/tf` | The Terraform domain: run engine, git/OpenTofu adapters, backend/workspace handling |
| `internal/manual`, `internal/gitlab`, `internal/azdevops`, `internal/github` | One package per runner type, each a `dispatch.RunHandler` |

The design is domain-driven: the domain packages (`tf`, `manual`, `gitlab`, `azdevops`, `github`) know how to execute
one run type and depend on the shared ports (`meshapi`, `report`, `config`, `dispatch`), while the adapters
(`k8sjob`, the external-forge clients) sit at the edges. Only `cmd/<type>` mains wire concrete dependencies together.

**Dependency direction** (domain must not import adapters) is enforced by `depguard` rules in
[`.golangci.yml`](../.golangci.yml), keyed on file globs per package rather than tree shape alone:

- Only `internal/observability`, `internal/dispatch`, `internal/k8sjob` and the `main` packages may import `prometheus/*`.
- Only `internal/meshapi`, `internal/crypto`, `internal/config`, `internal/report` and `internal/meshapitest` may
  import outside the module (plus `gopkg.in/yaml.v2`); every other package is stdlib-plus-siblings only.

The globs are anchored as `**/internal/X/*.go` (and `**/cmd/X/*.go`) so they match the absolute paths depguard sees;
because these packages are flat, no middle `/**/` segment is used (it would match zero files). A complementary AST
test in `internal/build` checks the logging stack.

**Coverage-excluded, real-I/O adapter files** (one file, one justification, in
[`tools/coverage/exclusions.txt`](../tools/coverage/exclusions.txt)): `internal/tf/git.go` (real git/SSH clone),
`internal/tf/tfbinaries.go` (real OpenTofu release downloads), `internal/k8sjob/cluster.go` (real
in-cluster/kubeconfig and OIDC discovery). Everything else in those packages is hermetically tested.

## 3. Execution model

A runner bootstrap (`cmd/<type>/main.go`, or the equivalent `cmd/bbrunner/<type>.go` wiring) builds:

1. A **handler** — a `dispatch.RunHandler` that knows how to execute one run type.
2. A **dispatcher** — either `dispatch.InProcess` (a goroutine per run, in-flight counter, drains on shutdown within
   `RUNNER_SHUTDOWN_GRACE` — a fit runner type defaults to 30s, the in-process superset to
   `dispatch.DefaultShutdownGrace` 120s; both honor the same env/`shutdownGraceSeconds` knob — after which an
   in-flight run is cancelled and reported terminal `ABORTED`; default concurrency `RUNNER_MAX_CONCURRENT_RUNS=3`,
   negative = unlimited with a 10-per-cycle backstop) or, for
   `run-controller` in-cluster, `k8sjob.KubernetesJobDispatcher` (dispatches a Kubernetes Job running the matching
   type's image in `EXECUTION_MODE=single-run`, default `maxConcurrentJobs=10`).
3. A **`dispatch.Loop`** — claims runs from meshStack on a fixed poll interval (default 10s) and hands each claimed
   run to the dispatcher.

**Type→image dispatch:** the Kubernetes dispatcher chooses the container image by the claimed run's type, from an
operator-supplied `implementations` map (`internal/k8sjob/config.go`), validated to the five concrete handler types.
There is no per-type branch in code — the controller dispatches a Job for **any** non-`ALL` type whose image is
configured.

**Claim-and-fail-fast:** a runner's registered *capability* (one concrete type, or `ALL`) only shapes what meshStack
lets it claim. A claimed run whose type has neither an in-process handler nor a Job template is immediately registered
and reported `FAILED` using the runner's own process credentials (never the claimed run's token — reaching for that
token before a handler owns the run would break the "runToken = executing handler only" invariant). The
Kubernetes-dispatcher and in-process-dispatcher messages differ deliberately: the Kubernetes message is a frozen,
customer-visible run-status string; the in-process one is actionable for a standalone operator. See
`internal/dispatch/unhandledtype.go`.

**Single-run mode:** every type supports `EXECUTION_MODE=single-run` (alias: `SPRING_PROFILES_ACTIVE` containing
`kubernetes`) — read one run from `RUN_JSON_FILE_PATH` (default `/var/run/secrets/meshstack/run.json`), execute it
with runToken-only auth, and exit. This is the shape a Kubernetes-dispatched Job runs. Every type — tf included —
executes the single run through the same `dispatch.RunHandler` its polling path uses, wrapped by the shared
`internal/runmode` single-run scaffold that pins the single-run wire and exit-code contract.

## 4. Frozen contracts register

These are the customer-facing, wire-visible contracts that must stay byte-for-byte stable. This section is the
authoritative pin list:

- **Wire shapes:** the claim/register/status-PATCH bodies, media types, the `X-Block-Runner-Node-Id`/runner headers,
  plan-artifact download (128 MiB cap).
- **The Kubernetes Job contract:** env vars (`EXECUTION_MODE=single-run`, `RUN_JSON_FILE_PATH`, `RUNNER_UUID`,
  `RUNNER_API_URL`), the run-JSON mount path, `BackoffLimit: 1`/`RestartPolicy: Never`, runToken-only auth for the
  dispatched Job.
- **Image names, tags and entrypoints** (`tf-block-runner`, `run-controller`, `manual-block-runner`,
  `gitlab-block-runner`, `azure-devops-block-runner`, `github-block-runner`) — customers reference these directly.
  The `tf-block-runner` image additionally ships a `/app/tfrunner` copy of its binary as a legacy `command:` override
  target. The four HTTP-only images share one `containers/http-runner/Dockerfile` and put their binary at the uniform
  in-image path `/app/runner` (not a documented `command:` target — the *image name* is what distinguishes them).
- **`/healthz`** body `OK`, and the management-port precedence `MANAGEMENT_PORT > PORT > per-type default`.
- **Every metric name and label** — Prometheus metric names are a de-facto public surface (operator dashboards scrape
  them). See §6.
- **All config keys and environment variables** — see §5 and [`DEPRECATIONS.md`](DEPRECATIONS.md).

When a run's details cannot be decrypted, the controller reports a terminal `FAILED` status with actionable
key-mismatch guidance (wording aligned with the tf runner's, `internal/tf/run.go`) and increments
`run_controller_decryption_errors_total` — a decrypt failure is never suppressed silently.

## 5. Configuration

Precedence: **compiled-in defaults < shared base YAML < per-type YAML < environment variables** (env always wins).
`${VAR}` interpolation inside either YAML layer is how legacy env-var spellings are honored declaratively, rather than
via a growing alias table in code. A config loader that sees a `BLOCKRUNNER_*`-prefixed (or otherwise known-legacy)
environment variable that no key or interpolation ever consumes fails startup with an actionable error — a stale
relaxed-binding override must never silently boot the runner on wrong defaults.

**Two-layer deep-merge.** `config.Loader.Load(basePath, perTypePath, into)` deep-merges an optional shared top-level
base (`containers/runner-config.yml`) *under* a per-type file (base < per-type, key-wise, nested maps merged). Today
only **gitlab** *wires* a non-empty base layer: its `LoadConfig` passes the base path (`RUNNER_BASE_CONFIG_FILE`,
default `containers/runner-config.yml`), so the base layer supplies the well-known local-dev private key that
gitlab's per-type file intentionally does not duplicate. The other three types load with an *empty* base
path — a single self-contained `containers/<type>/runner-config.yml`. (All four HTTP-only images physically bake the base
file, since they share one Dockerfile, but only gitlab reads it — it is inert for the other three.) The tf runner
and the controller decode their own single self-contained file via `os.ReadFile`+`yaml.Unmarshal`. The base layer is
the deliberate seam that keeps the one cross-type default (the dev key) DRY for the type that opted into it.

### 5.1 Alias inventory

The full, operator-facing alias inventory and its rationale live in [`DEPRECATIONS.md`](DEPRECATIONS.md). In short:
nothing is renamed without keeping the old spelling as a working, deprecation-logged alias, and no alias has been
removed. Config keys and environment variables are customer-facing API for a publicly released image set.

### 5.2 Concurrency

Standalone in-process runners default to `RUNNER_MAX_CONCURRENT_RUNS=3` (negative = unlimited, with a 10-per-cycle
backstop). `run-controller`'s Kubernetes dispatch defaults `maxConcurrentJobs` to 10 (file-only key).

## 6. Observability

Every long-running runner serves `GET /healthz` (`200 OK`, body `OK`) and `GET /metrics` on one listener whose
address `internal/observability` receives as a plain string. The env-var name `MANAGEMENT_PORT` is known only to
`internal/config` (which resolves `MANAGEMENT_PORT > PORT > per-type default` into a `config.Port` and hands
`observability` the resolved `:port` address) — `observability` itself has no knowledge of the env var. Default ports: `tf-block-runner`
8100, `azure-devops-block-runner` 8101, `github-block-runner` 8102, `gitlab-block-runner` 8103, `manual-block-runner`
8104, `run-controller` 2112.

Metric series:

- **`run_controller_*`** (run-controller only): fetch/registration/job-creation/decryption/capacity/loop counters and
  durations plus `run_controller_active_runners`, scraped from the controller's long-lived listener.
- **`runner_*`** (every other type, labeled by `runner_uuid`): `runner_runs_claimed_total`,
  `runner_runs_succeeded_total`, `runner_runs_failed_total`, `runner_run_duration_seconds`,
  `runner_poll_errors_total`.

Metric names are a de-facto public surface; any future rename needs the same alias/deprecation treatment as an
environment variable (§5).

### 6.1 Push-gateway metrics for Job-dispatched runs

Metrics use a **pull** model — Prometheus scrapes `GET /metrics`. This works for the long-running controller and for
standalone in-process runners. For runs the controller dispatches as Kubernetes Jobs, single-run mode serves no
listener and exits before Prometheus could scrape `GET /metrics`, so per-run execution metrics have no exposition path
via pull. The dispatching controller's own `run_controller_*` series (claim/dispatch/job-creation) are unaffected.

**Solution: Prometheus push gateway** (`internal/observability/pushgateway.go`). Each single-run process pushes its
collected metrics to a configured push gateway before exit, opt-in via `PUSH_GATEWAY_URL` environment variable (off by
default, no behavior change for operators who have not set it). The push is best-effort and bounded by a 5-second
timeout so a slow or unreachable gateway never blocks Job completion.

The grouping key carries only `run_id` per run (a per-run UUID). Every series a single run produces already carries its
own `runner_uuid` label (the frozen runner_* metric contract, §4), and the Pushgateway client rejects a push whose
grouping key repeats a label name a metric already carries — so `run_id` alone is already globally unique, matching
the §6.1 intent (every pushed group is fully attributable to one `runner_uuid` + run id) without violating the
Pushgateway constraint.

**Delete-on-success semantics:** once a successful run's series have been pushed, its group is deleted from the gateway
right away — Prometheus needs only scrape a successful run's series once, and a Job-dispatched single run never runs
again under the same run id, so nothing would ever come back to scrape a lingering group. A failed run's group is
deliberately left on the gateway for an operator to inspect. Both push and delete are bounded by the same 5-second
timeout and are logged on error, never fatal.

### 6.2 Naming note

The package is named `internal/observability` for its role as the unified observability listener: it serves both
the `/healthz` health endpoint and the `/metrics` Prometheus exposition. It takes a plain bind address (no env knowledge)
and stays focused on that single responsibility, leaving `MANAGEMENT_PORT` as a concern of `internal/config` alone.

## 7. Testing & gates

**Scenario tests over unit-test armadas.** The statement-coverage gate is reached with use-case/scenario tests that
drive the engine/handlers black-box against a real HTTP server (`internal/meshapitest`, an `httptest`-backed fake
meshStack shared by every runner's tests) rather than a hand-rolled `RoundTripper`. Unit tests are added only where a
unit has real decision surface of its own (parsers, crypto, type conversions) — coverage percentage is never, by
itself, a reason to add a test.

**Gate mechanics:** `task coverage` runs `go test -coverprofile=... ./...` then
[`tools/coverage/check.sh`](../tools/coverage/check.sh), which recomputes statement coverage per package-prefix
against [`tools/coverage/thresholds.txt`](../tools/coverage/thresholds.txt) (90% on every domain/shared package) after
dropping the lines in [`tools/coverage/exclusions.txt`](../tools/coverage/exclusions.txt) (§2). A package with zero
matching statements in the profile fails the gate rather than passing vacuously.

`task test` always runs with `-race` — the in-process dispatcher's concurrency hazards are exercised, not hoped away.
