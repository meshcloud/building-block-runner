# Detail Plan 04 — Single Binary & argv[0] Personas (Phase 4)

**Phase:** 4 · **Branch:** `refactor/single-go-binary/phase-4-single-binary` (stacked on
`refactor/single-go-binary/phase-3-shared-core`) · **Delivery:** one single-commit PR
(§5 high-level plan) · **Binding:** §3 P1–P8, D1 (argv[0] persona + fallback), D2 (one
module), D7 (config aliases), D8 (one binary / thin images / published names stay),
D10 (rollout compat), D11 (module at `./runner`, flat `internal/` concept packages),
D12 (`MANAGEMENT_PORT` — plan 03 §5.6 explicitly deferred the listener unification,
per-persona defaults and standalone-runner metrics to this phase), D14 (CI reshaped only
where image builds strictly need it) of `PLAN_HIGH_LEVEL.md`.

Phase character: **behavior-preserving on every frozen contract** (wire shapes, headers,
k8s Job contract, image names, entrypoint paths, healthz port defaults, metric names),
with three **sanctioned additive observability changes** mandated by D12 (§5.4): the
controller gains `/healthz`, the tf runner gains `/metrics` + generic run metrics, and
`MANAGEMENT_PORT` is introduced (existing `PORT` keeps working as a tf-persona alias).
Kotlin runners are untouched — their personas arrive in phase 6.

---

## 1. Assumptions from prior phases

Plans 00–03 are **not implemented yet**; everything below is a promise, not a fact.
Implementation of phase 4 **begins by running every verification step**. Any material
failure is a **STOP**: update this plan (and cascading plans) first, get the revision
reviewed, then resume.

| # | Assumption | Promised by | Verification step |
|---|---|---|---|
| A1 | Task targets exist and are green on the phase-3 branch: `task test`, `task lint`, `task coverage`, `task fmt`, `task tidy`, `task work-sync`, `task start:tf-block-runner`, `task start:run-controller`. | Plan 00 §12 | `git checkout refactor/single-go-binary/phase-3-shared-core && task test && task lint && task coverage` |
| A2 | tf code has the plan-02 shape: `tf-block-runner/internal/{tf,gitsource,tofu}`, one `Engine.Execute`, `Manager` polling loop, no `Worker`/`SingleRunWorker`; `tf-block-runner/main.go` is the polling/single-run persona bootstrap (mode switch `EXECUTION_MODE=single-run` + `RUN_JSON_FILE_PATH`, healthz on `PORT`/8100 in polling mode only — shape of today's `main.go:84-110`, engine wiring per plan 02 §5.3). | Plan 02 §5, §6 | `ls tf-block-runner/internal/{tf,gitsource,tofu}`; read `tf-block-runner/main.go`; `grep -rn "EXECUTION_MODE" tf-block-runner/main.go` |
| A3 | Shared packages live in `go-meshapi-client/{meshapi,crypto,config,report}` with names matching their D11 destinations; `meshapi.Identity` replaced `SetClientMetadata` (both mains construct clients with `Identity{Name, Version}`); `DecryptRunDetails` + `Decryptor` live in `meshapi`. | Plan 03 §5.1, §5.2.2, §5.2.5 | `ls go-meshapi-client/{meshapi,crypto,config,report}`; `grep -rn "Identity{" tf-block-runner/main.go run-controller/main.go` |
| A4 | Zero package-level mutable state in all three modules (`AppConfig`s, `crypto.Crypto`, `runnerName`/`runnerVersion`, `DiscoveredOidcIssuer`, `UseTestClient`, metrics singleton all gone); the controller's `MetricsCollector` is constructed with an injected `prometheus.Registerer` (`NewMetricsCollector(reg)`) — the seam plan 03 §5.6 built for this phase. | Plan 02 §6, Plan 03 §5.6/§6 step 7 | `grep -rn "^var [A-Z]" tf-block-runner run-controller go-meshapi-client --include='*.go' \| grep -v _test` (only consts/sentinels); read `controller/metrics.go` constructor |
| A5 | `tools/coverage/thresholds.txt` carries exactly five lines: `…/tf-block-runner/internal 90` and `…/go-meshapi-client/{meshapi,crypto,config,report} 90`; `exclusions.txt` names `internal/gitsource/git.go` and `internal/tofu/tfbinaries.go` (post-move paths); check.sh matches by import-path prefix. | Plan 02 step 11, Plan 03 §9 | `cat tools/coverage/thresholds.txt tools/coverage/exclusions.txt; task coverage` — record all numbers |
| A6 | `-race` is ON for the tf test leg (phase 2b R1); everything this phase moves must stay race-clean. | Plan 02 §7 R1 | `grep -rn '\-race' Taskfile.yml .github/workflows/` |
| A7 | Controller wire-characterization tests exist (fake-`RoundTripper` transcripts for claim/registration, k8s Job manifest goldens via `kubernetes/fake`) and are green — they are the proof that this phase's `git mv` of controller code changes nothing. | Plan 03 §6 step 1 | `grep -rn "func Test" run-controller/controller/*_test.go \| wc -l`; run `task test:run-controller` |
| A8 | Both legacy `build` packages still exist and are the ldflags targets (`tf-block-runner/build.Version`, `run-controller/build.Version`) — no prior plan touched them or the Dockerfiles/workflows. | Plans 00–03 scope | `git diff main..refactor/single-go-binary/phase-3-shared-core -- '*/Dockerfile' .github/workflows/ '*/build/'` — empty |
| A9 | Retry transport, `HttpError`, `RunClient`/`RunnerClient` split are in `meshapi` and the transcripts of every pinned scenario are unchanged (STOP-D of plan 03 held). | Plan 03 §5.2 | spot-run the tf scenario suite + controller transcript tests |
| A10 | Single-run exit semantics are the 2b-R12 conditional form (non-zero only when no terminal status was reported) — the persona move must not change whatever R12's reviewed condition is. | Plan 02 §7 R12 | read the post-2b `main.go` single-run tail; note the exact condition |
| A11 | The `.golangci.yml` depguard rules use the current module paths and the dependency-direction rules from plan 02 §5.7 / plan 03 §5.1; gci `localmodule` is configured. | Plans 00/02/03 | read `.golangci.yml` |
| A12 | meshfed-release `local-dev-stack` still starts the tf runner via `go run .` in `tf-block-runner/` (SKILL.md:78-82) and its acceptance flow was green at phase 3 exit. | Plan 03 §6 step 11 | read `meshfed-release/.agents/skills/local-dev-stack/SKILL.md:75-83`; check phase-3 PR evidence |

**STOP-A (before any coding):** any of A1–A12 materially false ⇒ update this plan first.
**STOP-B (step 1):** the merged `go.mod` surfaces a dependency version conflict with
behavior impact — see §5 step 1: MVS will lift run-controller's k8s-transitive
`golang.org/x/net v0.39.0` / `x/sys v0.33.0` / `x/text v0.25.0` to the tf side's
`v0.56.0` / `v0.46.0` / `v0.38.0` (`tf-block-runner/go.mod` vs `run-controller/go.mod`).
If `k8s.io/{api,apimachinery,client-go} v0.34.1` fails to compile or any controller
test/golden changes under the lifted versions, **do not bump k8s.io or pin x/\* ad hoc**
— stop, plan the dependency upgrade as a reviewed plan revision.
**STOP-C (step 1):** the thresholds split (§7.1: one aggregate prefix line becomes three
per-package lines for `tf`/`gitsource`/`tofu`) leaves any individual package below 90.
Do not touch `exclusions.txt`; either add the missing tests or adopt the reviewed
fallback mechanism (comma-joined prefix field in check.sh computing a combined total).
**STOP-D (step 5):** the per-persona image cannot be reproduced from the shared binary
(buildx `target` × platform quirk, symlinked-entrypoint failure, nix-install layer
breakage) in a way that would change a published image's name, entrypoint path, or
runtime behavior — that breaks D10; stop and replan the matrix.
**STOP-E (step 9):** the meshfed-release local-dev-stack + acceptance flow fails against
the new layout — D10's outer safety net; diagnose/replan before merging.

---

## 2. Scope

**In:**

- New Go module `github.com/meshcloud/building-block-runner/runner` at `./runner` (D11):
  root `main.go` + persona registry in **package main**, personas `tf-block-runner` and
  `run-controller` selected by `argv[0]` basename with `bbrunner <persona>` fallback (D1).
- Module consolidation: `git mv` of `tf-block-runner/internal/{tf,gitsource,tofu}`,
  `go-meshapi-client/{meshapi,crypto,config,report}`, `run-controller/controller` into
  `runner/internal/…`; the two `build` packages merge into `runner/internal/build`;
  the three legacy modules, both legacy mains, `go.work` + `go.work.sum` are deleted.
- D12 completion (the part plan 03 §5.6 deferred here): `runner/internal/mgmt` —
  one management listener per process serving `/healthz` **and** `/metrics` on
  `MANAGEMENT_PORT` with per-persona defaults (§4.3), controller finally gains healthz,
  tf gains metrics, plus the new generic standalone-runner metrics wired into the tf
  polling loop.
- Docker: one `containers/runner.Dockerfile` (shared builder + per-persona final
  stages), symlink entrypoints incl. legacy `/app/tfrunner` alias; the two legacy
  Dockerfiles die; persona runtime assets move to `containers/<persona>/`.
- Workflows: the *minimum* edits that keep tests/image builds working against the new
  layout (`ci.yml` go legs, `build-images.yml` matrix `file`/`target`) — D14 boundary
  argued in §4.5.
- Taskfile/thresholds/depguard/README path updates in lock-step with the moves.
- meshfed-release `local-dev-stack` SKILL update (lock-step cross-repo doc PR, D10).

**Out (deferred, with destination):**

- `Dispatcher`/`InProcessDispatcher`, dissolving `internal/controller` into
  `internal/dispatch` + `internal/k8sjob`, capability config → **phase 5**.
- Kotlin runner personas, their `PORT` 8101–8104 defaults, jvm.Dockerfile, Gradle CI legs
  → **phase 6/7**. Nothing Kotlin-side changes in this phase.
- Full CI reshape (lint job, Go-only CI, coverage job restructuring beyond path fixes)
  → **phase 7** (D14).
- Gating `internal/controller` coverage (plan 03 §9 decision stands) → **phase 5**.
- Config-surface growth (e.g. `RUNNER_UUID` env for the controller persona) — this phase
  adds exactly one new env var, `MANAGEMENT_PORT` (D12); everything else unchanged.
- README/docs beyond minimal truthful path+port updates → **phase 7**.

---

## 3. Research evidence — current state

References are `main` @ `c3fce61` (= the plan branch) unless marked *post-3* (shape
promised by prior plans, verified in step 0).

### 3.1 The two persona bootstraps to unify

- **tf-block-runner/main.go**: identity `SetClientMetadata("tf-block-runner", build.Version)`
  (`main.go:26`; *post-3*: `meshapi.Identity`); mode switch `EXECUTION_MODE == "single-run"`
  (`main.go:19-22,84-87`) — a **mode inside the persona, not a persona** (D1); crypto only
  in polling mode (`main.go:38-47`); healthz-only server on `PORT` default **8100**,
  fatal on bind failure, **polling mode only** (`main.go:64,89-110` — `startHealthServer`
  is not called on the single-run path `main.go:56-59`); single-run path reads
  `RUN_JSON_FILE_PATH`, uses runToken-only auth (`main.go:112-159`, frozen k8s contract).
- **run-controller/main.go**: identity `"run-controller"` (`main.go:21`); metrics-only
  listener hardcoded `":2112"`, `/metrics` via `promhttp.Handler()`, **non-fatal** on
  bind failure (`main.go:26-35` — `logger.Printf`, process continues), **no healthz at
  all**; OIDC discovery (`:38-44`), 10-min registration retry loop (`:48-64`),
  controller loop + signal handling (`:66-82`).
- Shared skeleton in both: logger construction, identity, config read, signal-driven
  `Stop()`, `wg.Wait()` — the persona functions keep their divergences; only the process
  frame (resolve persona → run it) and the management listener unify.

### 3.2 The argv[0] reference pattern

`meshfed-release/buildingblocks/multiplexing-block-runner/` is the referenced pattern
for **stdlib-only, env-first process bootstrap** (`main.go:22-60`, `configFromEnv` +
`envOr`/`envOrInt` `main.go:66-94`) — *note*: it does **not** itself dispatch on
`os.Args[0]` (grep over the package: no `os.Args` use; it is a single-purpose tool).
The high-level plan's §1 attribution ("the pattern of multiplexing-block-runner") holds
for its config/bootstrap style, not for a ready-made argv[0] registry — the registry is
designed fresh in §4.1 (flag §10.1).

### 3.3 Docker & entrypoint today

- `tf-block-runner/Dockerfile`: builder compiles `-o tfrunner` with ldflags
  `-X '…/tf-block-runner/build.Version=${VERSION}'` (`Dockerfile:24`); runtime = alpine
  3.22.4 + `bash git jq openssh curl ca-certificates xz coreutils python3 aws-cli`
  (`:28-30`), meshcloud uid 2000 + nix single-user install (`:33-59`); binary at
  **`/app/tfrunner`** (`:61`), `runner-config.yml` + `known_hosts` copied (`:62-63`),
  `ENV SSH_KNOWN_HOSTS=/app/known_hosts`, **`ENV PORT=8080`**, `EXPOSE 8080` (`:66-68`),
  `ENTRYPOINT ["/app/entrypoint.sh", "/app/tfrunner"]` (`:71`).
- `run-controller/Dockerfile`: builder `-o run-controller` (`:24`); runtime = alpine +
  `ca-certificates bash` only (`:30`); binary at **`/app/run-controller`** (`:38`),
  config copied (`:39`), no PORT/EXPOSE, `ENTRYPOINT ["/app/entrypoint.sh",
  "/app/run-controller"]` (`:44`).
- `containers/entrypoint-go.sh`: CA-cert import then **`exec "$@"`** (`:17`) — so the
  container's argv[0] is whatever path the ENTRYPOINT (or an operator's `command:`)
  names. **Operators can override command/args** via the job template
  (`controller/kubernetes.go:358-376` returns `jobSpec.Command`/`Args` verbatim), and the
  shipped controller config dispatches `ghcr.io/meshcloud/tf-block-runner:main` with
  `env: EXECUTION_MODE: single-run` (`run-controller/runner-config.yml`, TERRAFORM
  block) — therefore **`/app/tfrunner` and `/app/run-controller` are customer-facing
  paths** and must survive as symlink aliases (D10).

### 3.4 CI / release wiring touched by the layout change

- `ci.yml` `go-runners-ci` (`:143-174`): matrix `run-controller`/`tf-block-runner` with
  `working-directory: <module>` and `go-version-file: <module>/go.mod` — both die with
  the module dirs. (*post-0*: plus a `go-meshapi-client` leg and coverage steps,
  plan 00 §5.5.)
- `ci.yml` `go-runners-image` (`:179-260`): matrix `file: <module>/Dockerfile`, pushes
  `:main`/`:<sha>` tags per app — **image names must stay** (D8).
- `build-images.yml` (`:26-43,75-90`): release matrix, go legs
  `dockerfile: <module>/Dockerfile`, `VERSION` build-arg, no `target:` today; JVM legs
  share `containers/jvm.Dockerfile` + `RUNNER_MODULE` arg (the precedent for a
  shared-Dockerfile matrix).
- `release.yml` builds via `build-images.yml` (`release.yml:130-138`) — untouched.
  `pr-cleanup.yml` operates on image *names* (`:26-27,60-61`) — names stay, untouched.
  `release-check.yml` — no app refs, untouched.
- Makefile start targets run `go run ./<module>` from the repo root with per-module
  config env (`Makefile:28-32`) — only possible because of `go.work`; *post-0* these are
  `task start:*` with the same mechanics (plan 00 §5.1).

### 3.5 Ports & metrics inventory (D12 inputs)

| Process | Listener today | Evidence |
|---|---|---|
| tf-block-runner (polling) | `/healthz` only, `PORT` default 8100, fatal bind | `main.go:89-110` |
| tf-block-runner (single-run) | **none** | `main.go:56-59` skips `startHealthServer` |
| tf-block-runner (Docker) | `ENV PORT=8080` ⇒ healthz on 8080 | `Dockerfile:67` |
| run-controller | `/metrics` only, hardcoded `:2112`, non-fatal bind, **no healthz** | `main.go:26-35` |
| Kotlin runners | Spring on `PORT` defaults 8101–8104 | `*/src/main/resources/application.yml:8` |

Controller metric names `run_controller_*` with `controller_uuid`/`error_type` labels
(`controller/metrics.go:70-155`) are scrape-visible and frozen (plan 03 §12.9). The tf
runner has **zero metrics** today. `MANAGEMENT_PORT` appears nowhere in the repo yet
(grep). `README.md:66-86` documents the healthz table + `PORT` override + the
"Docker defaults to PORT=8080" sentence — needs updating.

### 3.6 Dependency-merge facts (STOP-B basis)

Direct requires to merge: tf (`go-git v5.19.1`, `hc-install v0.9.5`, `hcl/v2 v2.24.0`,
`terraform-exec v0.25.2`, `tofudl v0.0.1`, `goldie v2.8.0`, `jsonpath v0.1.1`,
`go-version v1.9.0`, `go-cty v1.18.1`, `x/crypto v0.53.0`, `yaml.v2 v2.4.0`, testify) ∪
controller (`prometheus/client_golang v1.20.5`, `yaml.v2 v2.4.0`,
`k8s.io/{api,apimachinery,client-go} v0.34.1`) ∪ client (testify only). No two direct
requires conflict. Indirect deltas MVS will lift: `x/net 0.39.0→0.56.0`,
`x/sys 0.33.0→0.46.0`, `x/text 0.25.0→0.38.0` (k8s stack runs on newer x/\* than it was
tidied with). `replace` directive and the `go-meshapi-client v0.0.0` require disappear.
Accepted D2 cost: k8s client-go linked into every persona (binary size only).

### 3.7 Cross-repo dependents of the layout

- `meshfed-release/.agents/skills/local-dev-stack/SKILL.md`: line 78
  (`cd ../building-block-runner/tf-block-runner && : > /tmp/tf-runner.log`), lines 79-82
  (env vars + `nohup go run . > /tmp/tf-runner.log 2>&1 &`), lines 88-91 (pgrep hint
  `'multiplexing-block-runner|tf-block-runner|BlockRunnerApplication'`), lines 92-93
  ("`tf-block-runner` ships the matching private key"), readiness table line 104.
  The manual-runner block (lines 64-71, gradle) is untouched until phase 6.
- `terraform-provider-meshstack/.agents/skills/scratch-config-testing/SKILL.md:82-95`:
  references the tf runner behaviorally (mux `:8300`, `/tmp/tf-runner.log`) — **no
  path/command dependency**, no edit needed (verified by reading the section).
- `meshfed-release/docs/docs/guides/platform-ecosystem/how-to-run-building-block-runners.md`:
  references the Docker images by registry name only (line 44); no ports/entrypoints —
  no edit needed. Acceptance tests reach runners via the mux (wire frozen) — no edit.

---

## 4. Target design

**RULED (grill r2 — slog-native):** the single-binary and persona-wiring packages
(`main`, `mgmt`, `config`) are slog-native (`log/slog`) **from the start** — no
`*log.Logger` seam and no `slog.NewLogLogger` bridge (consistent with plan 03's
shared-core ruling). Every logger parameter below is a `*slog.Logger`.

### 4.1 Naming decision & persona registry (D1, D2, D11)

**Module path:** `github.com/meshcloud/building-block-runner/runner` at `./runner`.
**Binary name:** `bbrunner` (D1's fallback spelling; the neutral name the persona
symlinks point at). **Persona names = published image names** (D8): `tf-block-runner`,
`run-controller`; legacy alias `tfrunner` (the historical in-image binary name, §3.3).

**Where the registry lives — reconciling D2's "`cmd/` persona registry" with D11:**
D11 mandates packages *exactly one level below the module root under `internal/`* and
that **only `main.go` wires** (its depguard rule). A `cmd/` tree would either violate
the depth rule or force exported wiring packages in a public repo. Persona bootstraps
*are* wiring — they import adapters, config, mgmt and hand them to the engine/loop — so
they belong in **package main**: `runner/main.go` (registry + dispatch) plus one small
file per persona (`persona_tf.go`, `persona_controller.go`) containing the verbatim
post-phase-3 main bodies. This is a deliberate, flagged refinement of D2's literal
`cmd/` (§10.2); D2's substance (root `main.go`, one binary, registry) is kept.

Illustrative shape (no more code than signatures, P8-typed):

```go
type Persona string // canonical names; also the meshapi.Identity.Name values (frozen headers)

const (
    PersonaTf         Persona = "tf-block-runner"
    PersonaController Persona = "run-controller"
)

// personas maps every accepted invocation name to its bootstrap. Aliases map to the
// same bootstrap as their canonical persona; Identity always carries the canonical name.
var personas = map[string]Persona{
    "tf-block-runner": PersonaTf,
    "tfrunner":        PersonaTf, // legacy in-image binary name (Dockerfile:61) — operators' custom `command:` uses it
    "run-controller":  PersonaController,
}

// resolvePersona: p := basename(argv[0]);
//   known(p)                    -> (persona, remaining args)
//   else if len(args) > 1 && known(args[1]) -> (persona, args[2:])   // `bbrunner <persona>`, `go run . <persona>`
//   else -> usage error listing personas (fail fast, P5)
func resolvePersona(args []string) (Persona, []string, error)
```

- `go run .` ergonomics: `go run`'s temp binary basename (`runner`) is unrecognized, so
  the arg fallback carries it: `go run . tf-block-runner`. No flags exist today; the
  remaining args are required to be empty (fail fast on garbage rather than ignore).
- Unknown argv[0] **with** a valid persona arg is the normal fallback path (that is also
  what a renamed/copied binary does); unknown argv[0] **without** one exits non-zero
  with a one-line usage listing the canonical personas — never a silent default (P5).
- Each persona bootstrap keeps its own logger prefix (`[TF RUNNER]`, `[RUN CONTROLLER]`
  — log-format continuity for operators and the local-dev-stack readiness markers).

### 4.2 Identity & version (single `build` package)

`runner/internal/build` with the single `Version = "dev"` var replaces the two
identical legacy packages (`tf-block-runner/build`, `run-controller/build`); the ldflags
path in every image build becomes
`-X 'github.com/meshcloud/building-block-runner/runner/internal/build.Version=${VERSION}'`.
`main.go` constructs `meshapi.Identity{Name: string(persona), Version: build.Version}`
once and passes it to the persona bootstrap — persona-specific identity with **byte-
identical headers** (`User-Agent`, `X-Meshcloud-Runner-Name/-Version`) because the
canonical persona strings equal today's `SetClientMetadata` literals (`main.go:26`,
`run-controller/main.go:21`), including when invoked via the `tfrunner` alias.

### 4.3 `MANAGEMENT_PORT` & `runner/internal/mgmt` (D12)

**Package decision (P3 justification):** a new concept package `runner/internal/mgmt`
(management listener + process-level run metrics). Not in `report` (that is the run-
status backchannel to the meshStack API — different concept, plan 03 §5.4) and not in
`config` (mgmt *consumes* config). Consumers: both personas now, four more in phase 6 —
a real ≥2-consumer package, not speculative. Contents:

```go
// Server serves GET /healthz (200, body "OK" — byte-identical to main.go:96-99) and
// GET /metrics (promhttp for an injected prometheus.Gatherer) on one listener.
// Nothing is served twice (D12); bind failure is fatal (P5 — see sanctioned change §6).
func NewServer(log *slog.Logger, addr string, g prometheus.Gatherer) Server
func (s Server) Start() error // binds, then serves in a goroutine (today's pattern, main.go:100-109)

// RunMetrics is the D12 generic standalone-runner instrumentation (new, additive names):
//   runner_runs_claimed_total, runner_runs_succeeded_total, runner_runs_failed_total,
//   runner_run_duration_seconds, runner_poll_errors_total — all labeled runner_uuid.
func NewRunMetrics(reg prometheus.Registerer, runnerUuid string) *RunMetrics
```

Port resolution lives in `runner/internal/config` (it is D7 alias mechanics):

```go
// ManagementPort resolves MANAGEMENT_PORT, then the persona's legacy aliases
// (deprecation-logged, D7), then the persona default.
func ManagementPort(log *slog.Logger, def Port, aliases ...EnvAlias) Port
type Port uint16 // typed; a set-but-unparseable value is fatal (mux precedent, envOrInt)
```

**Per-persona defaults table (the D12 contract of this phase):**

| Persona | Mode | Today | Phase 4: listener on | Default | Legacy alias | Gains |
|---|---|---|---|---|---|---|
| `tf-block-runner` | polling | `/healthz` on `PORT` (8100; image `PORT=8080`) | `MANAGEMENT_PORT` | **8100** | `PORT` (tf persona only, deprecation-logged once) | `/metrics` + `runner_*` run metrics |
| `tf-block-runner` | single-run | no listener | **no listener** (unchanged — see flag §10.4) | — | — | — |
| `run-controller` | — | `/metrics` on hardcoded `:2112` | `MANAGEMENT_PORT` | **2112** | **none** — `PORT` was never read by the controller; honoring it now would change deployed behavior | `/healthz` |
| Kotlin personas | — | Spring `PORT` 8101–8104 | untouched until phase 6 | — | — | — |

Critical alias detail: the tf image must **keep `ENV PORT=8080` and must not set
`MANAGEMENT_PORT`**. If the image baked `MANAGEMENT_PORT=8080`, a customer's runtime
`PORT=9000` override (today's documented mechanism, `README.md:79-86`) would silently
lose to the image env — a D10 healthz-port regression. With alias precedence
`MANAGEMENT_PORT > PORT > default`, every existing deployment resolves to today's port.

Metrics wiring per persona (all against an injected `prometheus.NewRegistry()` per
process — no default-registry globals, consistent with plan 03 §5.6):

- **run-controller:** existing `NewMetricsCollector(reg)` (A4) + `mgmt.NewServer` on
  2112. Metric names/labels byte-identical (§3.5). It does **not** additionally get the
  new `runner_*` series — its `run_controller_*` set already covers claim/dispatch;
  duplicate series would be dashboard noise (interpretation flag §10.5).
- **tf-block-runner (polling):** `mgmt.NewRunMetrics(reg, uuid)` implements a small
  consumer-side interface declared in `internal/tf` (P3: defined where consumed, with a
  fake for tests): `RunClaimed()` on successful fetch, `RunSucceeded/RunFailed(d)` after
  `Engine.Execute`, `PollError()` on non-norun fetch errors — hooked into the polling
  loop, not the engine (single-run stays metrics-free).

### 4.4 Dockerfile matrix (D8)

One new `containers/runner.Dockerfile` (sibling of `jvm.Dockerfile`, which already
proves the shared-file + matrix-arg pattern, §3.4); the two legacy Dockerfiles are
deleted. Stages:

| Stage | Base | Content |
|---|---|---|
| `builder` | `golang:1.26-alpine` (`--platform=$BUILDPLATFORM`) | `COPY runner/go.mod runner/go.sum` → `go mod download` → `COPY runner/` → `CGO_ENABLED=0 go build -trimpath -buildvcs=false -ldflags "-s -w -X '…/runner/internal/build.Version=${VERSION}'" -o bbrunner .` — no go.work needed (§4.6) |
| `run-controller` (final) | `alpine:3.22.4` (digest-pinned as today) | apk/user layers verbatim from `run-controller/Dockerfile:28-36`; binary + symlink; config = base `containers/runner-config.yml` + per-persona `containers/run-controller/runner-config.yml` (deep-merged, §4.4 RULED); `ENTRYPOINT ["/app/entrypoint.sh", "/app/run-controller"]` |
| `tf-block-runner` (final) | `alpine:3.22.4` (same pin) | apk/user/nix layers verbatim from `tf-block-runner/Dockerfile:28-59`; binary + symlinks; config = base `containers/runner-config.yml` + per-persona `containers/tf-block-runner/runner-config.yml` (deep-merged, §4.4 RULED) + `known_hosts` from `containers/tf-block-runner/`; `ENV SSH_KNOWN_HOSTS=/app/known_hosts`, `ENV PORT=8080` (kept, §4.3), `EXPOSE 8080`; `ENTRYPOINT ["/app/entrypoint.sh", "/app/tf-block-runner"]` |

**Entrypoint / symlink table (per image):**

| Image (published name unchanged) | Real binary | Symlinks (`ln -s bbrunner …`) | ENTRYPOINT argv | Legacy paths that keep working |
|---|---|---|---|---|
| `tf-block-runner` | `/app/bbrunner` | `/app/tf-block-runner`, `/app/tfrunner` *(legacy alias)* | `/app/entrypoint.sh /app/tf-block-runner` | operator `command: ["/app/entrypoint.sh","/app/tfrunner"]` or `["/app/tfrunner"]` (argv[0]=`tfrunner` → alias row in the registry) |
| `run-controller` | `/app/bbrunner` | `/app/run-controller` | `/app/entrypoint.sh /app/run-controller` | `command: ["/app/run-controller"]` (name unchanged) |

`entrypoint-go.sh` is unchanged — its `exec "$@"` (`:17`) is exactly what makes the
symlink path become argv[0]. **RULED (grill r2 — config file tree):** the `containers/*`
layout ships a shared top-level **base** `containers/runner-config.yml` (common keys)
plus **per-persona** override files `containers/tf-block-runner/runner-config.yml` and
`containers/run-controller/runner-config.yml`, which the loader **deep-merges** (base
then per-impl then env — plan 03 §5.3). Runtime assets move from the deleted module dirs
to those paths plus `containers/tf-block-runner/known_hosts` (Dockerfile-adjacent, like
the entrypoint scripts); in-image both layers are copied (base `/app/runner-config.yml`
+ per-persona `/app/<persona>/runner-config.yml`, `/app/known_hosts`), each layer
byte-identical to today's per-persona file. D10 check: old-controller→new-tf-image (env contract + default
entrypoint: works), new-controller→old-tf-image (controller code untouched
behaviorally: works), custom `command:` referencing legacy paths: works via symlinks.

### 4.5 Release/CI workflow delta (D14 boundary)

D14 says CI is reshaped in phase 7; the high-level phase-4 line nevertheless says
"CI/release matrix builds one binary, N images". Reconciliation: **change only what the
new layout makes false** — job structure, triggers, tag schemes, JVM legs all stay.

| File | Delta | Why strictly needed |
|---|---|---|
| `build-images.yml:26-31` | go matrix legs become `dockerfile: containers/runner.Dockerfile` + new `target: run-controller` / `target: tf-block-runner`; build step gains `target: ${{ matrix.target }}` (empty for JVM legs ⇒ default stage of `jvm.Dockerfile` — behavior unchanged) | old Dockerfile paths cease to exist; image names/tags unchanged |
| `ci.yml:150-155` (`go-runners-ci`) | matrix collapses to one leg `app: runner, go-dir: runner` (the *post-0* `go-meshapi-client` leg dies with the module); `go-version-file: runner/go.mod` | `working-directory: tf-block-runner` etc. cease to exist. Coverage artifact becomes `coverage-runner` (flag §10.6 — artifact *names* change) |
| `ci.yml:189-193` (`go-runners-image`) | `file: containers/runner.Dockerfile` + `target:` per app; matrix keys and image names unchanged | old Dockerfile paths |
| `release.yml`, `pr-cleanup.yml`, `release-check.yml`, JVM jobs | **untouched** | operate on image names / Gradle, both unchanged |

### 4.6 go.work endgame & tooling

- **`go.work` and `go.work.sum` are deleted** in the final consolidation step. D2's
  "workspace stays during migration" ends here: one module needs no workspace, a
  one-entry workspace is pure ceremony (P2), and the Docker builder gets simpler
  (§4.4 copies only `runner/`). Consequence: repo-root `go run ./runner` no longer
  resolves — all Go commands run in `runner/` (Taskfile handles cwd via `dir:`).
- **Taskfile:** module loops collapse (`MODULES` → `runner`); `task work-sync` is
  deleted; per-module test subtargets collapse into `task test` (kept:
  `task test`, `lint`, `fmt`, `tidy`, `coverage`); start targets become
  `task start:tf-block-runner` = `RUNNER_CONFIG_FILE=../containers/tf-block-runner/runner-config.yml go run . tf-block-runner`
  (dir `runner/`) and the run-controller twin with its config path — same env semantics
  as `Makefile:28-32`/plan 00 §5.1. **New promise set for plans 05+ recorded in §11.**
- **Lint:** `.golangci.yml` depguard module-prefix rewrite (`…/runner/internal/…`);
  direction rules preserved (adapters ↛ consumers; only `main` wires — now genuinely
  enforceable since personas are package main); new rules: only `mgmt` + `controller`
  (+ main) may import `prometheus/*`; `tf` may not import `controller`/`mgmt`
  (it sees its own metrics interface). gci: former cross-module imports become
  `localmodule` — a mechanical import-block reshuffle across every moved file, part of
  the move step's diff (anticipated by plan 00 §5.2).
- Test fixtures that today resolve module-relative (e.g. `../resources/test.pem`
  referenced from tf test helpers, plan 01 CP1) move to `runner/resources/` with the
  same relative-depth adjustment in the move step — hermetic suites stay hermetic.

---

## 5. Migration sequence — always-green checkpoints

**Shims vs atomic cutover — decided: atomic, forced by Go.** A "legacy mains become
thin shims first" sequence is impossible: once `internal/tf` etc. move under
`runner/internal/`, the legacy `tf-block-runner` module *cannot* import them (Go's
`internal/` visibility is module-scoped — the same wall as plan 03 flag §12.1), and a
shim cannot exec its way out. So the instruction's "legacy modules keep building until
their final removal step" is satisfied degenerately: steps before step 1 don't touch
them; step 1 moves code and deletes the legacy mains **in the same tree state**. This is
D10-safe because rollout compatibility rides on *published images and wire contracts*,
not on repo-internal source layout: every released image is built from a green commit,
the phase merges as one squash commit, and the k8s Job env/entrypoint contract is frozen
(§6). Within the working branch, each step below is independently green.

Rules: after every step `task test` + `task lint` green, `task coverage` ≥ gate; record
numbers per working commit (squashed on merge).

| # | Step | What changes | What proves it |
|---|---|---|---|
| 0 | **Preflight.** Run all §1 verifications on the phase-3 branch; branch `phase-4-single-binary`. Record: coverage numbers per package (A5), the R12 exit condition (A10), the post-3 main shapes (A2/A3). | nothing | A1–A12 verified (STOP-A) |
| 1 | **Atomic module consolidation (mechanical; the big diff).** (a) create `runner/go.mod` (union of the three requires, §3.6) + `go mod tidy`; (b) `git mv tf-block-runner/internal/{tf,gitsource,tofu} runner/internal/`; `git mv go-meshapi-client/{meshapi,crypto,config,report} runner/internal/`; `git mv run-controller/controller runner/internal/controller` (transitional name — phase 5 dissolves it into `dispatch`/`k8sjob`, flag §10.3); merge the two `build` packages into `runner/internal/build`; (c) write `runner/main.go` + `persona_tf.go`/`persona_controller.go`: registry per §4.1, persona bodies = **verbatim** post-phase-3 main bodies (incl. the old ad-hoc listeners — D12 lands in step 3, not here); delete the legacy mains + module files; (d) rewrite import paths (mechanical sed) + gci run; (e) `go.work` shrinks to `use ./runner` (deleted fully in step 7 — kept one step so tooling transitions are reviewable separately); (f) lock-step tooling paths: `thresholds.txt` per §7.1, `exclusions.txt` paths, depguard/module prefixes, Taskfile module list + start targets, test-fixture paths (§4.6). | everything moves; **zero semantic edits** — the diff is `git mv` + import paths + the new main frame | `task test`/`task lint`/`task coverage` green (STOP-B, STOP-C live here); `go run . tf-block-runner` & `go run . run-controller` boot to their config-read stage; `git diff --find-renames` shows moves, not rewrites; controller transcripts + k8s goldens (A7) and tf characterization suite green with **zero assertion changes** |
| 2 | **Persona resolution hardening + tests.** `resolvePersona` table tests: canonical names, `tfrunner` alias → tf persona with Identity name `tf-block-runner`, `bbrunner tf-block-runner`, `go run .`-style unknown argv[0]+arg, unknown-without-arg → usage error, trailing-garbage rejection. | `runner/main.go` + `main_test.go` | new tests green (main pkg — outside the gate denominator, like today's mains) |
| 3 | **D12 listener unification.** Add `internal/mgmt` (Server + RunMetrics, §4.3) and `config.ManagementPort`; persona_controller drops the ad-hoc `:2112` block (`main.go:26-35` shape) for `mgmt.NewServer` (default 2112, now with healthz, fatal bind — sanctioned §6); persona_tf drops `startHealthServer` for `mgmt.NewServer` (default 8100, `PORT` alias, now with metrics; still polling-mode-only). | `internal/mgmt`, `internal/config`, both persona files | httptest-level tests: `/healthz` body byte-identical `OK`; `/metrics` exposition contains `run_controller_*` (controller) resp. the go-collector baseline (tf); alias/default table test incl. `MANAGEMENT_PORT>PORT>8100` precedence and unparseable-value fatal; gate: `mgmt` ≥90 (§7.1) |
| 4 | **Generic run metrics (D12).** `mgmt.NewRunMetrics`; consumer-side meter interface in `internal/tf`; polling-loop hooks (claim/succeed/fail/duration/poll-error); wire in persona_tf. | `internal/mgmt`, `internal/tf` (loop only), `persona_tf.go` | fake-meter loop tests (claim→success increments; fetch-error→poll_errors); scenario suite untouched; `-race` clean (A6) |
| 5 | **Docker matrix.** Add `containers/runner.Dockerfile` (§4.4); `git mv` runtime assets to `containers/<persona>/`; delete the two legacy Dockerfiles. | containers/, asset moves | `docker build --target tf-block-runner` + `--target run-controller` succeed locally (amd64 at minimum); smoke: default entrypoint boots each persona; `docker run --entrypoint /app/tfrunner <tf-img>` boots the tf persona (legacy alias); `wget -qO- localhost:8080/healthz` inside the tf container → `OK`; controller container serves `/healthz`+`/metrics` on 2112 (STOP-D) |
| 6 | **Workflows.** Apply the §4.5 table to `ci.yml` + `build-images.yml`; nothing else. | 2 workflow files | draft-PR run: single `runner - test` leg green with coverage summary; both image jobs build (PRs build without push, `ci.yml:137,257`); JVM jobs byte-identical logs |
| 7 | **Tooling endgame.** Delete `go.work` + `go.work.sum`; drop `task work-sync`; README truth pass: root `README.md:66-86` health section → `MANAGEMENT_PORT` + controller row + Kotlin note, component links `tf-block-runner/`→`runner/`; fold the two module READMEs' still-true content into `runner/README.md` (`go run . <persona>`, config paths). | go.work, Taskfile, READMEs | every README command executes; repo grep: no reference to the dead module paths outside CHANGELOG/plan docs |
| 8 | **Cross-repo lock-step docs.** meshfed-release PR editing `local-dev-stack/SKILL.md` per §9 (exact lines); merged together with (not before) this phase's PR. | meshfed-release only | skill's commands executed verbatim against this branch |
| 9 | **Acceptance + self-review gate.** Full local-dev-stack flow with the tf runner started the *new* way; ≥1 MANUAL + ≥1 TERRAFORM acceptance run; controller persona smoke vs a kind/minikube namespace **or** (fallback) the A7 goldens + container smoke as the documented k8s evidence; P1–P8 walk; PR description lists sanctioned changes (§6), flags (§10), and the new promise set (§11). | — | evidence in PR description (STOP-E) |

9 steps + preflight. Riskiest: step 1 (size — mitigated by its zero-semantic-edit rule
and rename-detection review) and step 5 (STOP-D).

---

## 6. Frozen contracts touched (D9/D10)

**Preserved byte-identically (and proven by moved-not-changed tests):** all wire shapes,
media types, `User-Agent`/`X-Meshcloud-Runner-*`/node-id headers (Identity names equal
today's literals, §4.2); the entire k8s single-run contract — `RUN_JSON_FILE_PATH`,
`/var/run/secrets/meshstack/run.json`, `RUNNER_UUID`, `RUNNER_API_URL`, runToken-only
auth, `EXECUTION_MODE` as deployment config (persona ≠ mode, D1), R12 exit semantics
(A10); the k8s Job manifest (controller code moved verbatim; A7 goldens); **published
image names + tag scheme** (D8); in-image paths `/app/tfrunner`, `/app/run-controller`,
`/app/runner-config.yml`, `/app/known_hosts`, `/app/entrypoint.sh` (symlinks/copies);
tf healthz reachable on the same resolved port in every existing deployment (`PORT`
alias precedence, §4.3); healthz body `OK`; controller metrics names/labels + port 2112;
`run_controller_*` series; mux claim contract; config file default name + all keys/env
vars (only *additive* `MANAGEMENT_PORT`).

**Changed with D12 sanction (additive/flagged):**
1. controller serves `/healthz` (new) and its listener bind failure becomes **fatal**
   (was silent-continue, `main.go:32-34`) — a liveness-probed listener that dies
   silently defeats D12; P5. Called out for review.
2. tf polling serves `/metrics` (new) + new `runner_*` metric series (new names — no
   rename, so no D12 alias duty).
3. `PORT` on the tf persona logs a one-line deprecation notice (D7) — fires in default
   containers (image sets `PORT=8080`); accepted as one startup log line.

---

## 7. Test plan & coverage-gate survival

### 7.1 Gate continuity across the `git mv` (D6)

`thresholds.txt` after step 1 (paths per plan-00 mechanics — a threshold edit is one
line each; check.sh matches by prefix, A5):

```
github.com/meshcloud/building-block-runner/runner/internal/tf         90
github.com/meshcloud/building-block-runner/runner/internal/gitsource  90
github.com/meshcloud/building-block-runner/runner/internal/tofu       90
github.com/meshcloud/building-block-runner/runner/internal/meshapi    90
github.com/meshcloud/building-block-runner/runner/internal/crypto     90
github.com/meshcloud/building-block-runner/runner/internal/config     90
github.com/meshcloud/building-block-runner/runner/internal/report     90
github.com/meshcloud/building-block-runner/runner/internal/mgmt       90   (step 3)
```

Why the split: the phase-2 aggregate line (`…/tf-block-runner/internal 90`) cannot be
transplanted as `…/runner/internal 90` — that prefix would newly gate
`internal/controller` (deliberately ungated until phase 5, plan 03 §9/§12.4). The
`tf`/`gitsource`/`tofu` aggregate therefore becomes three per-package lines. Risk: the
aggregate hid per-package variance; if any package lands <90 individually → **STOP-C**
(preferred fix: the small test top-up; reviewed fallback: comma-joined prefix support in
check.sh so one line can express the old aggregate). `exclusions.txt` paths become
`runner/internal/gitsource/git.go`, `runner/internal/tofu/tfbinaries.go` (same files,
same justifications). Step-1 checkpoint includes the plan-00 induced-failure exercise
(temporarily set one line to 99 → check.sh fails → revert) proving the rewritten paths
actually match.

### 7.2 What proves each piece

- **Move fidelity:** the entire phase-1 characterization suite + controller transcript/
  golden suite (A7) green with zero assertion edits; `git diff --find-renames` review
  rule: step 1 contains no hunk inside a moved function body.
- **Persona registry:** step-2 table tests (aliases, fallback, usage error).
- **mgmt:** step-3 httptest suite incl. byte-identical healthz body, exposition-format
  checks, port-resolution table (incl. image scenario `PORT=8080` + unset
  `MANAGEMENT_PORT` → 8080), fatal-on-bind test; joins the gate at 90 (hermetic — no
  exclusion entry; `httptest`/ephemeral ports, no real network).
- **Run metrics:** step-4 fake-meter tests; prometheus `testutil`-style counter asserts
  on `RunMetrics`; `-race` (A6).
- **Images:** step-5 smoke matrix (default entrypoint, legacy `command:` paths, healthz
  from inside the container, `--target` builds for both personas).
- **End-to-end:** step 9 — local-dev-stack acceptance (TERRAFORM + MANUAL) with the tf
  persona from the new module; controller evidence via goldens + container smoke (the
  controller still has no in-repo e2e — inherited gap, plan 03 §12.3, unchanged here).

---

## 8. Rollback story

One squash commit on a stacked branch: `git revert` restores the three modules, both
mains, both Dockerfiles, `go.work`, workflows, thresholds paths — all in-repo. No wire
shape, image name, tag scheme, port default, env var, config key, or k8s contract
changed (§6), so **already-published images remain correct under rollback**: `:main`
floats back to legacy-built images on the next CI run; release tags are immutable and
were built from whichever tree state was tagged. The only additive surfaces
(`MANAGEMENT_PORT`, controller healthz, tf metrics) disappear with the revert —
operators who adopted them within the window lose them (documented in the PR as the
rollback cost). The meshfed-release SKILL edit (step 8) must be reverted in the same
motion — it is the one cross-repo dependency of the new layout (its PR is linked from
this phase's PR precisely so the pair reverts together).

---

## 9. Cross-repo touch points (exact files/lines)

- **meshfed-release — `.agents/skills/local-dev-stack/SKILL.md` (must change, lock-step
  PR, step 8):**
  - line 78: `cd ../building-block-runner/tf-block-runner && : > /tmp/tf-runner.log`
    → `cd ../building-block-runner/runner && : > /tmp/tf-runner.log`
  - lines 79-82 (the `nohup go run .` block): add
    `RUNNER_CONFIG_FILE=../containers/tf-block-runner/runner-config.yml` to the env list
    and change the command to `nohup go run . tf-block-runner > /tmp/tf-runner.log 2>&1 &`
  - lines 88-91: pgrep hint still matches (`tf-block-runner` appears as the persona
    arg on the command line) — verify, no edit expected
  - lines 92-93: private-key sentence — reword the file location
    (`containers/tf-block-runner/runner-config.yml` ships the matching key)
  - line 104 (readiness table): marker/log unchanged; verify the `[TF RUNNER]` prefix
    still appears (it does — persona keeps its logger prefix, §4.1)
  - manual-runner block lines 64-71: **untouched** (gradle; phase 6)
- **meshfed-release — acceptance tests / mux / `how-to-run-building-block-runners.md`
  (line 44 image reference):** no edits — wire and image names frozen (§3.7).
- **terraform-provider-meshstack —
  `.agents/skills/scratch-config-testing/SKILL.md:82-95`:** behavioral references only
  (mux `:8300`, `/tmp/tf-runner.log`); no path/command dependency ⇒ **no edit**;
  verified in step 8 by re-reading after the SKILL.md change lands.
- **terraform-provider-meshstack** otherwise: pattern source only (D3) — no edit.

---

## 10. Flags — findings the high-level/prior plans did not anticipate

1. **The mux is not actually an argv[0] example.** `multiplexing-block-runner` never
   reads `os.Args[0]` (§3.2) — the high-level §1 attribution holds only for its
   stdlib/env-first bootstrap style. The persona registry here is designed fresh; the
   busybox-symlink idea carries over, the code does not.
2. **D2's literal `cmd/` collides with D11.** Resolved (§4.1): persona bootstraps are
   wiring and live in package main; no `cmd/` tree. Flagged, not silent.
3. **D11's package list has no home for the controller.** `dispatch`/`k8sjob` are
   phase-5 shapes; this phase needs a transitional `runner/internal/controller`
   (moved verbatim). Named here so phase 5's plan starts from it.
4. **D12's "every persona serves healthz+metrics" meets the single-run mode.** Today
   single-run serves nothing (`main.go:56-59`); a listener inside short-lived Job pods
   is new behavior with no probe consuming it. Interpretation: D12 speaks per persona
   process *serving* mode; single-run stays listener-free. Reviewer may override.
5. **Generic `runner_*` metrics are not added to the controller** — its
   `run_controller_*` series already covers the same events; duplication would be
   scrape noise. D12's "all personas" is read as "all personas that lack metrics".
   Reviewer may override.
6. **Legacy `/app/tfrunner` is load-bearing**: operators can override the Job
   `command:` (`kubernetes.go:358-376`), so the old binary path is a de-facto contract
   the high-level plan never mentions — hence the alias symlink *and* the `tfrunner`
   registry alias (§4.1/§4.4).
7. **The tf image must NOT set `MANAGEMENT_PORT`** or it would break today's documented
   runtime `PORT` override (§4.3) — the naive "set the new var in the image" move is a
   D10 regression; caught at design time.
8. **CI must change more than D14's letter suggests**: the per-module test matrix and
   `go-version-file` paths die with the modules — reshaping the go test job is
   *layout-forced*, not the phase-7 CI redesign (§4.5). Coverage artifact names change
   (`coverage-<module>` → `coverage-runner`).
9. **Controller bind failure flips from silent to fatal** with the healthz addition
   (§6.1) — a behavior change D12 implies but never states.
10. **The thresholds aggregate cannot survive the move as one line** (§7.1) — the gate
    mechanics of plans 00–02 meet the D11 tree for the first time here; per-package
    splitting (with STOP-C) is new policy this plan adds.

---

## 11. Promise set for later phases (05+)

- Module: `github.com/meshcloud/building-block-runner/runner` at `./runner`; **no
  go.work**; binary `bbrunner`; personas registered in `runner/main.go`
  (`Persona` + `personas` map + `resolvePersona` fallback); adding a persona = one
  `persona_<name>.go` + one registry entry + one Dockerfile final stage + one
  build-matrix leg (the phase-6 template hook).
- Packages: `runner/internal/{tf,gitsource,tofu,meshapi,crypto,config,report,mgmt,
  controller,build}` — `controller` is transitional for phase 5.
- Task targets: `task test`, `task lint`, `task fmt`, `task tidy`, `task coverage`,
  `task start:tf-block-runner`, `task start:run-controller` (no `work-sync`, no
  per-module test subtargets).
- Observability: `mgmt.NewServer` (healthz+metrics, one listener),
  `config.ManagementPort` (alias-aware), `mgmt.RunMetrics` (`runner_*` series) — phase-6
  personas reuse all three; per-persona defaults per §4.3 (phase 6 assigns 8101–8104 to
  the ported personas).
- Coverage: thresholds are per-package lines (§7.1); `mgmt` gated; `controller` not.
- Images: `containers/runner.Dockerfile` multi-target; persona assets under
  `containers/<persona>/`; ldflags path `…/runner/internal/build.Version`.

## 12. Open questions (self-grilled)

All decision branches were walked and resolved from the codebase; the judgment calls a
reviewer may veto are encoded as flags/STOPs, not questions: the single-run
no-listener interpretation (§10.4), controller-without-`runner_*`-metrics (§10.5),
controller fatal-bind (§10.9), the per-package gate split fallback (STOP-C), and the
step-9 controller-evidence fallback (goldens + container smoke vs a live cluster).
*(empty otherwise)*
