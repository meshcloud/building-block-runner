# Architecture

This is the maintained record of the Building Block Runner repository's architecture. It targets maintainers and
contributors (operators should start with the root [`README.md`](../README.md)). Keep it current as the codebase
evolves — describe what the code *is* and why it is shaped that way, not how it got here.

## 1. One binary, build-tag-selected types, auto-detected run mode

There is a single Go module and a single `main` package, `cmd/bbrunner`. Every published image is the same binary
built with a different combination of build tags, which gate the *imports* (a tagged-out type links none of its
handler/client dependencies) — not a runtime flag:

| Build tags      | Linked types           | Published image(s)                                                                  |
|-----------------|------------------------|---------------------------------------------------------------------------------------|
| *(none)*        | all five (`ALL`)       | local-dev superset — every handler in-process, no Kubernetes dispatcher              |
| `type_tf`       | Terraform only         | `tf-block-runner` — go-git, OpenTofu exec, no Kubernetes                            |
| `type_manual`   | Manual only            | `manual-block-runner` — meshapi/report/config only                                  |
| `type_gitlab`   | GitLab only            | `gitlab-block-runner` — meshapi/report/config only                                  |
| `type_azdevops` | Azure DevOps only      | `azure-devops-block-runner` — meshapi/report/config only                            |
| `type_github`   | GitHub only            | `github-block-runner` — meshapi/report/config only                                  |
| `k8s`           | none (handler-free)    | `run-controller` — links no runner-type handler, only the Kubernetes-Job dispatcher  |

Each linked type self-registers into `typeRegistry` (`cmd/bbrunner/registry.go`) from an `init()` in its own
tag-gated file (`tf.go`, `manual.go`, `gitlab.go`, `azdevops.go`, `github.go`); `registry.go` itself stays free of
every type package so it compiles identically no matter which tags are passed.

There is **no subcommand**: invocation is always plain `bbrunner`, and both what it serves and how it runs are
auto-detected at startup (`cmd/bbrunner/main.go`):

- **Linked type set** is fixed at build time by the tags above. Exactly one type linked (a `type_X` build) routes
  straight to that type's own bootstrap. Zero types linked (`-tags k8s`) or all five (no tags, the superset) both
  route to the **controller**, which auto-detects its dispatcher (`cmd/bbrunner/dispatcher.go`, overridable with
  `RUNNER_DISPATCHER`): `KUBERNETES_SERVICE_HOST` present → `k8sjob.KubernetesJobDispatcher`, dispatching each
  claimed run as a Kubernetes Job running the image configured for that run's type; otherwise → `dispatch.InProcess`,
  running every linked handler in one process under the controller's single `ALL` identity.
- **Run mode** is auto-detected solely from `RUN_JSON_FILE_PATH` (`internal/runmode.DetectSingleRun`): set and
  resolving to a non-empty run file → single-run (execute that one run and exit); unset → poll (the default). Set
  but the file missing or empty is a fail-fast startup error, never a silent fall-back to polling. Single-run only
  makes sense on a single-linked-type build — a mounted run file on a zero- or five-type build could never be
  served unambiguously, so requesting it there is also a fail-fast error rather than a silent poll.

Each type's bootstrap stamps its own identity on the meshStack runner headers and keeps its own log-line identity,
whether it runs as the one linked type or in-process as part of the superset/controller.

The `ALL` type is single-valued in the backend enum — a runner registers exactly one concrete type or `ALL`; subsets
are not representable. Only the controller (zero- or five-type build) registers `ALL`; it claims every run type and
fans them out (to per-type Jobs in-cluster, or to per-type in-process handlers out of cluster).

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
| `internal/catrust` | Trust-store seeding: `RootCAs()` builds the process-wide `x509.CertPool` (system pool + `CUSTOM_CA_CERTS_PATH` PEMs) consumed by every runner type's HTTP transport; `SyncSystemStore` updates the on-disk store for tf's subprocess consumers |

The design is domain-driven: the domain packages (`tf`, `manual`, `gitlab`, `azdevops`, `github`) know how to execute
one run type and depend on the shared ports (`meshapi`, `report`, `config`, `dispatch`), while the adapters
(`k8sjob`, the external-forge clients) sit at the edges. Only `cmd/bbrunner`'s per-type files wire concrete
dependencies together.

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

A runner bootstrap (each linked type's `cmd/bbrunner/<type>.go` wiring, or the controller's `runController`) builds:

1. A **handler** — a `dispatch.RunHandler` that knows how to execute one run type.
2. A **dispatcher** — either `dispatch.InProcess` (a goroutine per run, in-flight counter, drains on shutdown within
   `RUNNER_SHUTDOWN_GRACE` — a fit runner type defaults to 30s, the in-process superset to
   `dispatch.DefaultShutdownGrace` 120s; both honor the same env/`shutdownGraceSeconds` knob — after which an
   in-flight run is cancelled and reported terminal `ABORTED`; default concurrency `RUNNER_MAX_CONCURRENT_RUNS=3`,
   negative = unlimited with a 10-per-cycle backstop) or, for
   `run-controller` in-cluster, `k8sjob.KubernetesJobDispatcher` (dispatches a Kubernetes Job running the matching
   type's image with `RUN_JSON_FILE_PATH` mounted, triggering that image's own single-run auto-detect; default
   `maxConcurrentJobs=10`).
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

**Single-run mode:** auto-detected solely from `RUN_JSON_FILE_PATH` (§1) — read one run from that path (default
`/var/run/secrets/meshstack/run.json`), execute it with runToken-only auth, and exit. This is the shape a
Kubernetes-dispatched Job runs. Every type — tf included — executes the single run through the same
`dispatch.RunHandler` its polling path uses, wrapped by the shared `internal/runmode` single-run scaffold that pins
the single-run wire and exit-code contract.

## 4. Frozen contracts register

These are the customer-facing, wire-visible contracts that must stay byte-for-byte stable. This section is the
authoritative pin list:

- **Wire shapes:** the claim/register/status-PATCH bodies, media types, the `X-Block-Runner-Node-Id`/runner headers,
  plan-artifact download (128 MiB cap).
- **The Kubernetes Job contract:** env vars (`RUN_JSON_FILE_PATH`, `RUNNER_UUID`, `RUNNER_API_URL`), the run-JSON
  mount path, `BackoffLimit: 1`/`RestartPolicy: Never`, runToken-only auth for the dispatched Job.
- **Image names, tags and entrypoints** (`tf-block-runner`, `run-controller`, `manual-block-runner`,
  `gitlab-block-runner`, `azure-devops-block-runner`, `github-block-runner`) — customers reference these directly.
  The `tf-block-runner` image additionally ships a `/app/tfrunner` copy of its binary as a legacy `command:` override
  target. The four HTTP-only images (and run-controller) put their binary at the uniform in-image path `/app/runner`
  (not a documented `command:` target — the *image name* is what distinguishes them); tf's binary is `/app/tf-block-runner`.
- **`/healthz`** body `OK`, and the management-port precedence `MANAGEMENT_PORT > PORT > per-type default`.
- **Every metric name and label** — Prometheus metric names are a de-facto public surface (operator dashboards scrape
  them). See §6.
- **All config keys and environment variables** — see §5 and [`DEPRECATIONS.md`](DEPRECATIONS.md).

When a run's details cannot be decrypted, the controller reports a terminal `FAILED` status with actionable
key-mismatch guidance (wording aligned with the tf runner's, `internal/tf/run.go`) and increments
`run_controller_decryption_errors_total` — a decrypt failure is never suppressed silently.

### 4.1 Container build

All six shipped images build from ONE multi-target root `Dockerfile` driven by `docker-bake.hcl` (invoked via
`task images` → the standalone `docker-buildx bake` binary — the single source of truth CI and local dev share).
A shared builder stage compiles `./cmd/bbrunner` once per target, parameterized by a `CMD_TAGS` build-arg:
empty = the in-process superset (dev-local, not shipped), `type_<x>` = a lean single-type fit image, `k8s` = the
run-controller (Kubernetes-Job dispatcher, no in-process handlers). It shares the `go mod download` layer and a
`--mount=type=cache` Go build cache across targets.

Two runtime stages: a **scratch** runtime shared by the four HTTP fit images *and* the run-controller (they differ
only by `CMD_TAGS` + the baked config), and an **alpine** runtime for tf, which cannot be scratch because it shells
out to `git`/`jq`/`openssh`/`curl`/`xz`/`coreutils`/`python3`/`aws-cli` plus a single-user `nix` install. There is
no shell entrypoint wrapper: CA setup is in-binary — `catrust.RootCAs` seeds every runner type's outbound-HTTPS
trust pool from the frozen `ca-certificates.crt` bundle, and `catrust.SyncSystemStore` (tf-only) updates the
on-disk store for tf's subprocess consumers.

Config lives under `cmd/`: the single shared fit `cmd/runner-config.yml` is baked at `/app/runner-config.yml` in
all five fit images; the run-controller's own `cmd/bbrunner/runner-config.yml` is baked separately; tf's
`cmd/tf/known_hosts` is baked at `/app/known_hosts`. The shared fit file carries only `uuid` + the common `api`
credentials; tf's timeout/dir values are now compiled-in defaults (`tf.Default*` in `internal/tf/config.go`), so
the shared file need not carry them.

## 5. Configuration

Precedence: **compiled-in defaults < per-type YAML < environment variables** (env always wins). `${VAR}`
interpolation inside the YAML is how legacy env-var spellings are honored declaratively, rather than via a growing
alias table in code. A config loader that sees a `BLOCKRUNNER_*`-prefixed (or otherwise known-legacy) environment
variable that no key or interpolation ever consumes fails startup with an actionable error — a stale relaxed-binding
override must never silently boot the runner on wrong defaults.

**Single-layer load.** `config.Loader.Load(path, into)` decodes one self-contained `runner-config.yml` per type —
every fit runner (tf, manual, github, gitlab, azdevops) calls it identically, resolving `runner-config.yml`
CWD-relative with a `RUNNER_CONFIG_FILE` override, with no base/override merge across files. All five fit images
share ONE physical file, `cmd/runner-config.yml`, baked at `/app/runner-config.yml`; it carries only the
`uuid: 98520496-627d-43e6-82da-ce499179ff3f` local-dev runner identity plus the common `api` credentials (username/
password + blank clientId/secret). Per-type `api.url` and tf's timeout/dir values are compiled-in defaults, not
baked keys. Fit images no longer bake a private key: `crypto.privateKey` is operator-supplied
(`RUNNER_PRIVATE_KEY_FILE` / `privateKey`) for standalone polling. The controller keeps its own separate
`cmd/bbrunner/runner-config.yml`, which *does* bake `crypto.publicKey`/`crypto.privateKey` — the well-known
local-dev key pair used by the superset/controller's claim-boundary decryptor. That's a legitimate per-type field
(the controller decrypts on behalf of runs; the fit runners don't), not a mechanism divergence — both load through
the same single-path `Load`. The tf runner's own YAML key was historically `runnerUuid:`; it now accepts `uuid:`
like the others, with `runnerUuid:` kept as a deprecation-logged alias (see [`DEPRECATIONS.md`](DEPRECATIONS.md)).

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
