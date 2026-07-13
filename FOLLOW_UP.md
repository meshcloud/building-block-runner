# Follow-up — single-Go-binary refactor

The single-Go-binary refactor (phases 0→7 + the phase-3/5 remediation pass) is **implemented and
green**: `task lint` reports 0 issues, `task test` passes with `-race`, and all 11 gated packages are
≥90 % coverage (`tf` 90.3, `config` 97.3, `meshapi` 91.5, `report` 98.8, `mgmt` 96.7, `dispatch` 95.7,
`k8sjob` 98.4, `manual` 97.7, `gitlab` 92.2, `azdevops` 93.2, `github` 91.2). The JVM/Gradle tree is gone
and CI is Go-only.

This file is the **single, code-verified** list of what remains. It supersedes the scattered "deferred"
notes in [`docs/plans/PLAN_IMPL_RUN_LOG.md`](docs/plans/PLAN_IMPL_RUN_LOG.md),
[`docs/plans/PLAN_IMPL_RUN_LOG_ADDENDUM.md`](docs/plans/PLAN_IMPL_RUN_LOG_ADDENDUM.md),
[`CROSS_REPO_TODO.md`](CROSS_REPO_TODO.md) and [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) §8 — each
item below was re-checked against the current tree, not copied from the plan prose.

**What was verified as already CLOSED** (so you don't chase it): the run log's BLOCKER 2 (phase-3
shared-core) and BLOCKER 3 (phase-5 dispatcher) are done — `dispatch.NewMetricsCollectorWithRegistry`,
`meshapi.DecryptRunDetails`/`Decryptor`, removal of `controller.AppConfig`/`DiscoveredOidcIssuer`/
`UseTestClient`, `tf.NewHandler`, `RUNNER_MAX_CONCURRENT_RUNS`, `registration:`+startup PUT, the two
`runner_*` metrics, and `cmd/bbrunner`'s `RUNNER_DISPATCHER` auto-detect all exist and are wired/tested.
The `slog` migration is complete. The Kotlin modules + CI legs are deleted.

## Priority legend

| | Meaning |
|---|---|
| **P0** | Do before merging to `main` / before GA — correctness or customer-facing risk. |
| **P1** | High — a real defect or an enforcement gap other work leans on; cheap to fix. |
| **P2** | Medium — the headline features exist but are dormant/partial; unblock the downstream goal. |
| **P3** | Low — tidiness, dead code, consistency, narrow-environment gaps. |

---

## P0 — before merge / GA

### P0.1 Live acceptance runs (nothing merged was exercised end-to-end)
Every phase was proven by hermetic tests, mocks, `kubernetes/fake` goldens and code-reading — **no live
run** was executed. Before merge, run against the meshfed-release `local-dev-stack`:
- ≥1 **TERRAFORM** and ≥1 **MANUAL** building-block run to a terminal status (HTTP-triggered, real meshStack).
- A **kind/minikube** `run-controller` smoke that dispatches a real single-run Job.
- The four ported personas (manual/gitlab/azdevops/github) at least once each against their real backend, or
  the sibling `meshstack-smoke-tests` suite where it exists (github/tf/manual have it; gitlab/azdevops do not
  — see P3.5).

**Why P0:** correctness of the whole refactor currently rests on "the tests are faithful," never observed live.
**Effort:** moderate (stack bring-up). **Risk if skipped:** a wire/behavior drift merges undetected.

### P0.2 Confirm the L14 decrypt-failure `FAILED` wire change is coordinator-safe
Phase 7 changed one **customer-facing error path**: on a decrypt failure the controller now actively PATCHes a
terminal `FAILED` (with key-mismatch guidance) instead of letting the run sit until the coordinator's timeout
(`internal/k8sjob/kubernetes.go:83-102` → `internal/dispatch/loop.go:224-258`). Happy paths are byte-identical
and `run_controller_decryption_errors_total` is preserved.

**Action:** confirm meshStack's coordinator behaves correctly when it receives an *active* `FAILED` for a
decrypt failure. If it does **not**, revert this single isolated change (it is a plan-sanctioned FUTURE, cleanly
revertible). **Why P0:** unverified customer-visible error-path behavior change. **Effort:** small (one
coordinator-side check).

---

## P1 — high

### P1.1 Re-activate the `depguard` layering rules (D11 is currently unenforced)
The per-package file-globs in `.golangci.yml` are written `internal/X/**/*.go` **without the leading `**/`**
depguard needs to match the absolute paths it sees, so every group matches **zero files**
(`.golangci.yml:64-335`; confirmed against the sibling `terraform-provider-meshstack/.golangci.yml`, which uses
the `**/` form). Dependency direction (D11) is therefore enforced only by review and by the
`internal/build/loggingstack_test.go` AST test — **not by the linter**, despite `docs/ARCHITECTURE.md` §2
having claimed otherwise (now annotated).

**Action:** change each pattern to `**/internal/X/**/*.go`, run `task lint`, and fix any previously-masked
violations that surface. **Why P1:** several phases' layering assumptions rest on this being enforced; the fix
is mechanical. **Risk:** may reveal real violations that were silently passing — that is the point.

---

## P2 — medium (dormant / partial features; unblock the downstream goal)

### P2.1 Wire the controller in-process **superset** (retires meshfed-release's mux)
`cmd/bbrunner` auto-detect exists, but `RUNNER_DISPATCHER=inprocess` on the *controller* (no subcommand)
**fails fast** — it does not run all five persona handlers in one process (`cmd/bbrunner/controller.go:46-61`).
This needs each persona's config loaded into the controller bootstrap. Until it lands, the `run-controller`
image cannot replace meshfed-release's `multiplexing-block-runner` — **the refactor's stated downstream goal**.
**Effort:** medium (per-persona config wiring). **Blocks:** cross-repo mux retirement (P-X.3).

### P2.2 Make the tf in-process dispatch path the default; delete `tf.NewManager` + the token protocol
The new tf `dispatch` path (`tf.NewHandler`/`tf.NewDispatchRunner`) is wired and unit-tested but **opt-in only**
via `RUNNER_DISPATCHER=inprocess`; the legacy `tf.NewManager` polling loop + `SetRunToken`/`ClearRunToken`
protocol remain the **default** (`internal/tf/manager.go:47`, `internal/tf/runapi.go:84-92`, gated in
`cmd/tf/main.go:29-33` and `cmd/bbrunner/tf.go:70-99`). Deletion was deliberately **not** done on faith.
**Prerequisite (do first):** drive the full phase-1 characterization suite *through the loop* and run an
N-concurrent acceptance smoke (`RUNNER_DISPATCHER=inprocess`, `maxConcurrentRuns=2`, two overlapping runs) to
prove cadence equivalence. Then flip the default and delete the Manager + token protocol.
**Effort:** medium; **Blocked by:** P0.1 (live acceptance). **Payoff:** the phase-5 headline (concurrent tf
runs) becomes live instead of dormant.

### P2.3 `tf.AppConfig` de-globalization
`internal/tf` still reads a mutable package-level global `var AppConfig TfRunnerConfig`
(`internal/tf/config.go:16`; ~60 non-test + ~130 test references, ~220 tokens repo-wide). New handler/dispatch
code already threads config explicitly (`HandlerConfig`/`HandlerDeps`) so the debt is **not deepening**, but
the global remains. Threading it out is mechanical yet touches the frozen wire pins (`runapi.go`, `dtos.go`
source-id, node-id) and the large characterization suite — a dedicated green-at-every-commit pass.
**Effort:** medium-large. **Risk:** touches frozen pins — keep the characterization suite as the guard.

### P2.4 Move the tf runner onto the shared `report` package
`internal/report` is consumed by the four ported personas but **not** by `internal/tf`, which still uses its own
`RunStatus`/`ExecutionStatus`/`Progress` (confirmed: no `internal/report` import under `internal/tf`). This is
PLAN_DETAIL_03 §6 step 9 — its own "riskiest step": it rewrites tf's status model and every PATCH-body
assertion. The intended cross-runner de-duplication is thus only partly realized. **Do this together with
P2.3** (same frozen-pin surface). **Effort:** large. **Risk:** high (frozen tf wire).

---

## P3 — low (tidiness, dead code, consistency, narrow gaps)

### P3.1 `config.Loader.Load` deep-merge is dead code
The two-layer base+override YAML deep-merge (`internal/config/config.go:85`, PLAN_DETAIL §4.4, described in
`ARCHITECTURE.md` §5) has **zero non-test callers** anywhere — every persona still loads a single self-contained
`runner-config.yml`. Decide: wire the base+override layering in (so `containers/runner-config.yml` actually
overlays per-persona files), **or** delete the unused machinery + the doc claim. Leaving it is a silent
"documented-but-inert" feature.

### P3.2 Unify the metrics seam across the four ported personas
Only the controller mgmt listener and the tf in-process path use `dispatch.NewMetricsCollectorWithRegistry`;
azdevops/gitlab/manual/github (polling + `bbrunner` subcommands) still call the singleton `NewMetricsCollector()`
against `prometheus.DefaultRegisterer`/`DefaultGatherer` (e.g. `cmd/gitlab/polling.go:37-40`). Works fine;
purely a consistency/testability cleanup.

### P3.3 `ABORTED`-on-shutdown for in-flight tf runs
The plan wants a grace-period-then-cancel reporting terminal `ABORTED`; the handler intentionally matches
today's Manager behavior (an in-flight run finishes on its own `TfCommandTimeoutMins`). Belongs with P2.2
(Manager deletion + a `report.ExecutionStatus` `ABORTED`). **Blocked by:** P2.2.

### P3.4 `logging.*` / `server.*` / `spring.*` warn-on-ignore
`ARCHITECTURE.md` §5.1 / `DEPRECATIONS.md` §4 intend these legacy YAML blocks to be *ignored-with-warning*, but
`yaml.Unmarshal` silently drops unknown keys — no warning fires. Add known-fields/strict decoding to warn.
(Docs now flag this as unimplemented.)

### P3.5 Minor code TODOs / narrow test gaps (from the repo sweep)
- `internal/tf/authSsh.go:121` — TODO: auto-discover `known_hosts` (feature idea).
- `internal/tf/worker.go:299` — TODO: inefficient append-mode read (`io.Seeker` limitation); functional.
- `internal/tf/tfbinaries_test.go` (`//go:build e2e`) — real tofu/terraform-binary path is opt-in
  (`task test:e2e` / `.github/workflows/e2e.yml`), excluded from default CI. Intentional, but that real-I/O path
  (`git.go`/`tfbinaries.go`) isn't in the default gate.
- `internal/tf/tfcmd_prerunscript_test.go:258` — `t.Skip("bash not available")` on bash-less hosts.

---

## Cross-repo (meshfed-release) — not actionable in this repo

Full detail in [`CROSS_REPO_TODO.md`](CROSS_REPO_TODO.md). Land the meshfed-release edits **in lock-step** with
the matching change here (the phase-4 §8 rollback story treats the two as one revertible unit).

- **P-X.1 (lock-step with phase-4 merge):** `meshfed-release .agents/skills/local-dev-stack/SKILL.md` — the tf
  start command moves to `go run ./cmd/tf` (or `./cmd/bbrunner tf`) from repo root, add
  `RUNNER_CONFIG_FILE=containers/tf-block-runner/runner-config.yml`, update the pgrep hint and the readiness
  marker to key on the `slog` `persona=tf-block-runner` attribute (the old `[TF RUNNER]` prefix is gone).
- **P-X.2 (per persona, when each JVM image is retired):** the manual/gitlab/azdevops/github start snippets +
  readiness markers, likewise.
- **P-X.3:** retire the `multiplexing-block-runner` once **P2.1** lands.
- **P-X.4 — release notes** for the three customer-visible phase-2b behavior changes: DESTROY now deletes the
  real matched tofu workspace (B2); every sensitive input type is now decrypted, not just STRING/CODE/FILE (B5);
  single-run exits non-zero for pre-apply failures (B11).

---

## Human review — judgment calls (no code change required, but worth a look)

From the run log's "Review this first"; these are decisions the autonomous run made that a human should sanity-check:
- **Phase-1 CP7–CP13 were authored by the consolidator**, not the fan-out — review those test files for altitude
  and that they pin *current* behavior, not idealized behavior.
- **Thin coverage margins**: several packages cleared 90 % by ~0.2 pp, and a few pins were added specifically to
  step off the exact-90.0 knife-edge. A small change could trip a gate.
- **Shared decryptor reconciliation**: `meshapi.DecryptInputs` (byte-based, gitlab) vs `DecryptInputSpecs`
  (typed, azdevops); azure-devops silently inherited the stricter `decrypt("") == ""` empty-ciphertext behavior;
  github keeps a package-local decryptor without that guard (inert today). Confirm the API shape is intended.
- **Characterization tests are more white-box than PLAN_DETAIL assumed** (A5) — this is the root reason the
  phase-2 DDD work kept slipping; feed it into any future phase-2-completion replanning.

---

## Suggested sequencing

1. **P1.1** (depguard) — cheap, unblocks honest layering; do immediately.
2. **P0.1 / P0.2** (live acceptance + L14 coordinator check) — gate the merge.
3. **P2.2** (flip tf to in-process, delete Manager) — needs P0.1's N-concurrent smoke.
4. **P2.1** (controller superset) then **P-X.3** (retire the mux) — the downstream goal.
5. **P2.3 + P2.4** as one dedicated pass (tf de-global + shared `report`).
6. **P3.\*** as opportunistic cleanup.
