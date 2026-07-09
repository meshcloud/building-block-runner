# Detail Plan — Phase 0: Guardrails & baseline

**Parent:** `PLAN_HIGH_LEVEL.md` §5 "Phase 0 — Guardrails & baseline" · **PR branch:**
`refactor/single-go-binary/phase-0-guardrails` (based on `main`, one squash-merged commit) ·
**Plan branch:** `refactor/single-go-binary/plan`

Phase 0 has no prior phases; instead of "Assumptions from prior phases" this plan ends with
**"Promises to later phases"** — the exact artifacts plans 01+ may assume.

---

## 1. Objective & exit criteria (from the high-level plan)

- Coverage baseline measured per package and documented (§3 below — already measured for real).
- CI publishes coverage + threshold plumbing exists, **not yet gating** (gate flips ON for
  `tfrun` in phase 1, D6).
- golangci-lint v2 adopted; Makefile replaced by `Taskfile.yml`; separate `go vet` target
  dropped (D14).
- Inventory of untested behaviors against the D9 pin list (§8 below, seeded from real
  coverage data; completed during execution).
- meshfed-release local-dev-stack flow verified against this repo as the outer safety net.

## 2. Scope

**In:**
- Root `Taskfile.yml` replacing `Makefile` (all existing targets preserved except `vet`);
  `flake.nix` gains `pkgs.go-task`; `README.md` command sections updated.
- Root `.golangci.yml` (v2, provider-parity linter set + depguard skeleton) and the minimal
  lint cleanup needed for `task lint` to exit 0 — under a strict "inert changes only"
  decision rule (§5.3); everything behavior-touching is *pinned via config exclusion*, not fixed.
- Coverage tooling: `tools/coverage/check.sh` + `thresholds.txt` + `exclusions.txt`;
  `task coverage`.
- Additive coverage publishing in `.github/workflows/ci.yml` (reconciliation with the
  "CI functionally untouched" rule in §5.5).
- D9 untested-behavior inventory (research artifact, lands in this plan on the plan branch).
- local-dev-stack verification (evidence recorded in the PR description; no code).

**Out:**
- Any production-behavior change (bug fixes are phase 2b per D13 — including the confirmed
  swallowed workspace-select error, `tf-block-runner/tfrun/tfcmd.go:232-234`).
- Any coverage *gating* (phase 1), any new tests (phase 1), any package moves (phase 2+),
  CI reshaping / lint CI job / JVM-leg changes (phase 7 per D14).
- Cross-repo edits (meshfed-release, terraform-provider-meshstack are read-only references).

## 3. Coverage baseline — REAL measured numbers

Measured on this machine (go1.26.4, `main` @ c3fce61, 2026-07-08) via
`cd <module> && go test -coverprofile=/tmp/cover-<mod>.out ./... && go tool cover -func=…`.
All tests **pass**.

| Module | Package | Statement coverage |
|---|---|---|
| go-meshapi-client | `crypto` | 71.4% |
| go-meshapi-client | `meshapi` | 39.3% |
| **go-meshapi-client** | **module total** | **53.3%** |
| run-controller | `.` (main) | 0.0% |
| run-controller | `build` | no test files |
| run-controller | `controller` | 24.2% |
| **run-controller** | **module total** | **22.6%** |
| tf-block-runner | `.` (main) | 0.0% |
| tf-block-runner | `build` | no test files |
| tf-block-runner | `tfrun` | 59.4% |
| tf-block-runner | `util` | 0.0% |
| **tf-block-runner** | **module total** | **56.6%** |

Runtime and caveats (relevant for CI wiring):
- `tf-block-runner` takes **~53s wall** (`tfrun` alone 49.6s) and **requires network**:
  `tfrun/tfbinaries_test.go:16` ("These tests verify the behavior of the *real* tfBinaries
  download") downloads terraform 1.3.7/1.3.8/1.5.5 and opentofu 1.11.0. CI already absorbs
  this today (same `go test ./...`), so coverage adds no new fragility — but plan 01 should
  consider whether these belong on the D6 adapter exclusion list.
- `run-controller` ~12s wall (mostly compiling k8s client-go; test time itself 0.008s).
- `go-meshapi-client` ~1.8s.

Notable per-function zeros that matter later (evidence for §8):
`tfrun/singlerunworker.go` — **every function 0.0%** (`NewSingleRunWorker`, `ExecuteRun`,
`workRoutine`, `observerRoutine`, `sendInitFail`); `tfrun/manager.go` all 0.0%;
`tfrun/worker.go:66 handleFetchRunError` 0.0%; `tfrun/tfcmd.go:271 detectBackend` 0.0%;
`run-controller/controller/kubernetes.go` **all functions 0.0%**; `controller/runapi.go` and
`controller/registration.go` all 0.0%; `controller/decryption.go decryptRunDetails` 29.7%.

## 4. Research evidence — current state

- **Makefile** (`Makefile:6-53`): targets `help`, `start-run-controller`,
  `start-tf-block-runner`, `test`, `test-run-controller`, `test-tf-block-runner`, `fmt`,
  `vet`, `tidy`, `work-sync`; a `run-in-modules` loop over
  `MODULES := go-meshapi-client run-controller tf-block-runner` (`Makefile:2`).
- **CI** (`.github/workflows/ci.yml`): Go leg `go-runners-ci` runs plain `go test ./...`
  (line 174) with a matrix of **only** `run-controller` and `tf-block-runner`
  (lines 150-155) — **`go-meshapi-client` is never tested in CI** (flagged in §11).
  No vet, no lint, no coverage anywhere (`grep` over all five workflows: only ci.yml:174
  runs `go test`). Other workflows (`build-images.yml`, `pr-cleanup.yml`,
  `release-check.yml`, `release.yml`) contain no Go test/lint steps and stay untouched.
- **README** documents `make` targets at `README.md:62` and `README.md:98-116`
  (incl. `make vet` at line 100) — must be rewritten for `task`.
- **flake.nix** already ships `pkgs.golangci-lint` (`flake.nix:24`) but **not** `go-task`;
  meshfed-release's flake shows the addition to mirror (`meshfed-release/flake.nix:82:
  pkgs.go-task  # go-task runner used by terraform-provider Taskfile.yml`). Local toolchain
  verified: golangci-lint **2.12.2** (v2 config schema), go-task **3.50.0**, go 1.26.4.
- **Reference lint configs:**
  - `terraform-provider-meshstack/.golangci.yml`: `version: "2"`, formatters `gci`
    (sections standard/default/localmodule) + `gofmt`; linters `default: none` with an
    explicit enable list incl. `govet` (lines 18-39); extensive strict-mode `depguard`
    rules (lines 41-137).
  - `meshfed-release/buildingblocks/multiplexing-block-runner/.golangci.yml`: same schema,
    trimmed enable list; header comment (lines 1-4) is the precedent for dropping `go vet`:
    "`govet` runs here, so do not run `go vet` separately". Its `Taskfile.yml:17-19` wires
    `golangci-lint run {{.CLI_ARGS}} ./...` with a `-- --fix` passthrough.
  - **Version pinning approach:** neither repo hard-pins golangci-lint for local use — both
    get it from nixpkgs via their flake (provider `flake.nix:31-32`). Provider CI uses the
    SHA-pinned `golangci/golangci-lint-action@…# v9.3.0` with `version: latest` and
    `only-new-issues: true` (`test.yml:55-58`) — irrelevant here until phase 7 since phase 0
    adds no lint CI job. This repo pins implicitly via `flake.lock` (nixos-25.11 →
    golangci-lint 2.12.2). Good enough; no extra pinning mechanism invented.
  - `terraform-provider-meshstack/Taskfile.yml`: task naming style (`test`, `lint`,
    `build`, `clean`; `{{.CLI_ARGS}}` passthrough).
- **Lint baseline (actually run, provider linter set minus depguard, golangci-lint 2.12.2):**
  **231 findings** total — go-meshapi-client 26 (errcheck 9, testifylint 11, makezero 2,
  staticcheck 2, forcetypeassert 1, unconvert 1); run-controller 39 (godot 32, staticcheck 3,
  errcheck 2, gci 1, unparam 1); tf-block-runner 166 (testifylint 50, errcheck 49, godot 30,
  forcetypeassert 10, thelper 8, gci 7, misspell 4, unparam 3, staticcheck 2, usetesting 2,
  ineffassign 1). `govet` reported **zero** findings, so dropping the `vet` target loses
  nothing. Distribution facts that drive §5.3: of tf-block-runner's 49 errcheck findings,
  ~33 are in `_test.go` files; production hits are `tfcmd.go`×5, `gitsource.go`×3,
  `authSsh.go`×3, `logwrapper.go`×2, and 1 each in `worker.go`, `tfapply.go`,
  `singlerunworker.go`, `manager.go`, `main.go`; go-meshapi-client production hits are
  `meshapi/client.go`×4, `meshapi/auth.go`×1. All 4 misspell findings are in **comments**
  (verified: `behavior.go:13`, `gitsource.go:87`, `manager.go:102`, `tfcmd.go:251`) — inert.
- **Safety net:** `meshfed-release/.agents/skills/local-dev-stack/SKILL.md` starts the tf
  runner from this repo via `go run .` in `tf-block-runner/` (SKILL.md:78-82) and the manual
  runner via `./gradlew :manual-block-runner:bootRun` (SKILL.md:67-71). It references **no
  Makefile target** (grep over `.agents/skills/` — zero make/task hits), so the
  Makefile→Taskfile swap cannot break it. D10 intact by construction.

## 5. Design

### 5.1 Taskfile (D14)

Root `/Taskfile.yml`, schema `version: '3'`, replacing every Makefile target 1:1 except
`vet` (dropped; govet runs inside golangci-lint — mux precedent §4). Module iteration via a
`MODULES` var + Task `for` loops (equivalent of `run-in-modules`, `Makefile:8-14`).

Targets (exact names — these are promises, §12):

| Task | Behavior |
|---|---|
| `test` | `go test ./...` in all three modules |
| `test:go-meshapi-client` / `test:run-controller` / `test:tf-block-runner` | per-module tests |
| `lint` | `golangci-lint run {{.CLI_ARGS}} ./...` per module (so `task lint -- --fix` works) |
| `fmt` | `golangci-lint fmt` per module (gofmt+gci — supersedes `go fmt`; one formatter authority) |
| `tidy` | `go mod tidy` per module |
| `work-sync` | `go work sync` |
| `start:run-controller` / `start:tf-block-runner` | same env + `go run` as `Makefile:28-32` |
| `coverage` | per-module `go test -coverprofile` + `tools/coverage/check.sh` (report + gate) |

Naming: colon-namespaced subtasks (Task idiom) instead of the Makefile's dash names —
acceptable rename because nothing outside README references the old names (§4, cross-repo
grep). `desc:` on every task so `task --list` replaces `make help`.

### 5.2 golangci-lint v2 config

One root `/.golangci.yml` (v2 config discovery walks up from each module dir, so one file
serves all three modules; `task lint` still runs *per module* because `./...` cannot cross
module boundaries). Content mirrors `terraform-provider-meshstack/.golangci.yml`:

- `version: "2"`, `issues.max-same-issues: 0`.
- Formatters: `gci` (standard/default/localmodule) + `gofmt`. Note: sibling-module imports
  (e.g. run-controller importing `…/go-meshapi-client`) classify as *default*, not
  *localmodule*, until module consolidation in phase 4 — accepted, deterministic.
- Linters: `default: none`; enable the full provider set (`.golangci.yml:20-39`):
  depguard, durationcheck, errcheck, copyloopvar, forcetypeassert, godot, ineffassign,
  makezero, misspell, nilerr, predeclared, staticcheck, usetesting, unconvert, unparam,
  unused, **govet**, testifylint, thelper. Full parity now means later phases never
  re-litigate the linter set — they only delete exclusions.
- **depguard skeleton (D11/D14):** one strict rule per module allowing `$gostd`,
  `github.com/meshcloud/building-block-runner/…`, and exactly the module's current direct
  dependencies (enumerated from each `go.mod` at implementation time), plus a test rule
  adding testify. Effect: any *new* dependency needs a visible config edit (mechanizes P2's
  dependency-justification). The domain-vs-adapter direction rules grow in phase 2+ as
  `internal/` packages appear.
- **Temporary legacy exclusions** (see §5.3) live in `linters.exclusions.rules` in this same
  file — one visible place, each block commented with justification + the phase that deletes
  it (mirrors D6's exclusion-list philosophy).

Pinning: golangci-lint stays a flake-provided tool (2.12.2 from nixos-25.11, `flake.nix:24`);
`.golangci.yml` `version: "2"` guards the config schema. No CI lint job until phase 7 (D14).

### 5.3 Lint-adoption decision rule (keeps phase 0 behavior-neutral)

`task lint` must exit 0 at PR time without risking behavior before the phase-1 test net
exists. Rule, applied finding-by-finding:

1. **Fix** if provably inert: formatter output (gofmt/gci), comment-only (godot, all 4
   misspell hits), `_test.go`-only findings (testifylint×61, thelper×8, usetesting,
   errcheck-in-tests via `require.NoError`), and semantics-preserving mechanical rewrites
   (unconvert; staticcheck QF-series like `tfcmd.go:384` QF1002 tagged-switch and
   `authSsh.go:123` QF1008 embedded-selector).
2. **Pin, don't fix** if the fix would alter any executed production path or signature:
   errcheck/forcetypeassert/nilerr/makezero/ineffassign/unparam findings in non-test files
   (~25 sites, listed in §4). Mechanism: path+linter-scoped `exclusions.rules` blocks in
   `.golangci.yml`, one per legacy module, comment naming the removal owner — `tfrun` block
   dies in phase 2/2b, `controller` + `meshapi` blocks die in phase 3. Rationale: adding
   error handling to code that silently ignored errors *is* a behavior change — exactly what
   D13 defers to phase 2b.
3. **Record** any staticcheck SA-series finding that reveals a real bug in the D13 bug
   inventory seed (§8) instead of fixing.

The PR self-review (P8 gate) includes a diff audit proving no production logic changed
outside category 1.

### 5.4 Coverage threshold plumbing (D6 mechanics)

No new Go dependency (P2): a POSIX-sh + `go tool cover`/awk script.

- `tools/coverage/check.sh <module-dir> <profile>`:
  1. strips lines matching `tools/coverage/exclusions.txt` from the profile,
  2. prints the filtered `go tool cover -func` per-package/total report (and appends it to
     `$GITHUB_STEP_SUMMARY` when set),
  3. for each `<import-path-prefix> <min-percent>` line in `tools/coverage/thresholds.txt`
     that falls inside this module, recomputes the total over matching profile lines and
     **exits non-zero** below the minimum.
- `tools/coverage/thresholds.txt`: ships **with zero active lines** (header comment explains
  format + that phase 1 adds `github.com/meshcloud/building-block-runner/tf-block-runner/tfrun 90`).
  Empty file ⇒ gate is vacuous ⇒ "plumbing, not yet gating" exactly as the phase demands.
- `tools/coverage/exclusions.txt`: format `<import-path>/<file.go>  # justification`; ships
  empty — the D6 adapter exclusion list is phase-1 content, but the *mechanism* and its
  one-visible-place location are fixed here.

### 5.5 CI wiring — reconciling "publishes coverage" with "functionally untouched"

The two constraints (D14: "GitHub Actions CI is left functionally as-is until phase 7" vs
phase-0 exit "CI publishes coverage") reconcile as follows: **"functionally untouched" means
no existing trigger, job, or pass/fail outcome changes for a codebase that passed before;
the phase description explicitly grants the additive "CI coverage report + threshold
plumbing (not yet gating)"**. Concretely, only `ci.yml`'s `go-runners-ci` job changes:

- Test step (ci.yml:173-174) becomes `go test -coverprofile=coverage.out ./...` — same
  tests, same failure semantics, plus a profile.
- New steps after it: run `../tools/coverage/check.sh` (report to `$GITHUB_STEP_SUMMARY`;
  gate vacuous per §5.4) and upload `coverage.out` as artifact `coverage-<app>` via a
  SHA-pinned `actions/upload-artifact` (matching the repo's pinning convention, e.g.
  ci.yml:39).
- No lint job (D14), no changes to `jvm-runners-ci`, image jobs, or the other four
  workflows. CI does not need `task` or golangci-lint installed.
- When phase 1 adds the first thresholds line, the already-wired check.sh starts gating —
  no further CI edit needed; that flip is phase 1's documented change.

**`go-meshapi-client` CI matrix leg (§11):** add the `go-meshapi-client` leg to the
`go-runners-ci` matrix in this PR — a sanctioned additive exception to D14 ("CI
functionally untouched until phase 7"), called out in the PR description. Phase 1 places
the D9 128MiB plan-artifact-cap pin test in `go-meshapi-client/meshapi/client_test.go`;
without this leg that frozen-contract pin would not be CI-enforced until phase 3.

## 6. Implementation order (always-green checkpoints, one squash commit)

Each step ends with the stated checkpoint green before the next begins; the branch is
squash-merged, so "always-green" applies to the local sequence and the final PR state.

1. **Preflight.** On `main`-based branch `refactor/single-go-binary/phase-0-guardrails`:
   re-run the three coverage commands; confirm numbers match §3 (tooling drift check) and
   `main` CI is green. *Proves:* baseline reproducible; a mismatch ⇒ update §3, not the code.
2. **Add `Taskfile.yml`** (Makefile still present). *Checkpoint:* `task test`, `task fmt`,
   `task tidy`, `task work-sync` succeed and are behavior-identical to their make
   counterparts; `task --list` shows descriptions. *Test:* run both old and new side by side
   once; `git status` clean after `fmt`/`tidy` (proves no pending drift smuggled in).
3. **`flake.nix`: add `pkgs.go-task`** next to `pkgs.golangci-lint` (`flake.nix:24`),
   mirroring `meshfed-release/flake.nix:82`. *Checkpoint:* `nix develop` shell provides
   `task` and `golangci-lint` (nix users); non-nix path unaffected.
4. **Add `.golangci.yml` + lint cleanup** per §5.3 (run `task lint -- --fix`, hand-fix the
   remaining category-1 findings, add exclusion blocks for category 2, seed §8 with any
   category-3 finds). *Checkpoint:* `task lint` exits 0 in all three modules **and**
   `task test` still green **and** diff audit: non-test production files contain only
   comment/import/formatting/mechanical-rewrite changes. *Test:* full `task test` rerun;
   reviewer-facing audit note in PR description.
5. **Coverage tooling** (`tools/coverage/*` + `task coverage`). *Checkpoint:*
   `task coverage` green and its report reproduces §3's totals; gate mechanism proven by
   temporarily adding a thresholds line above baseline → check.sh fails → remove line
   (mechanism test, not left in). *Test:* that induced-failure exercise, documented in the
   PR description.
6. **README update + delete Makefile** (`README.md:62`, `README.md:98-116`: make→task,
   `vet` row removed with a one-line "govet runs inside `task lint`" note; add
   `task coverage`). *Checkpoint:* every command in README executes; repo-wide grep finds no
   remaining Makefile/`make <target>` references (incl. `.claude/` and module READMEs).
7. **CI wiring** in `ci.yml` per §5.5 (+ the flagged `go-meshapi-client` matrix leg).
   *Checkpoint:* push branch, open draft PR — all jobs green; `go-runners-ci` step summary
   shows the coverage tables; artifacts present; every other job's log functionally
   identical to a recent `main` run.
8. **D9 untested-behavior inventory** — complete §8: for every pin verify the "covered"
   claims by naming the existing test (e.g. in `worker_scenario_test.go`,
   `tfplan_scenario_test.go`, `runapi_status_test.go`) and re-checking function coverage.
   Output feeds `PLAN_DETAIL_01`. Lands on the plan branch (the code PR stays code-only).
   *Checkpoint:* every D9 bullet has a row with evidence.
9. **local-dev-stack verification** per `meshfed-release/.agents/skills/local-dev-stack/SKILL.md`:
   compose up (mariadb/ravendb/keycloak/mux), three meshfed services, manual runner
   (gradle) + tf runner (`go run .` from this branch, SKILL.md:78-82). *Checkpoint:*
   readiness markers observed (`[mux] claiming upstream`, `Started BlockRunnerApplication`,
   tf runner polling `:8300`) and at least one MANUAL and one TERRAFORM run executed via the
   acceptance-testing skill; evidence (log excerpts) recorded in the PR description.
   A failure here is a **STOP**: it invalidates the outer safety net all later phases lean
   on — diagnose/replan before merging.
10. **Self-review gate (P1-P8) + PR.** Squash-merge as one commit; commit message lists the
    change categories (tooling, mechanical lint fixes, pins, CI-additive) so `git blame`
    stays navigable.

## 7. Frozen contracts touched (D9/D10)

**None modified.** No runtime code path changes (guaranteed by §5.3's decision rule); no
image names, ports, env vars, config keys, k8s Job contract, or claim/mux contract touched.
Step 9 *exercises* D10 (mux claim contract, `go run .` layout, decrypt pairing) read-only as
verification. The deleted `Makefile` is not part of any frozen contract (§4: no cross-repo
references).

## 8. D9 untested-behavior inventory (seeded; completed in step 8)

Evidence = statement coverage from §3's profiles at `main` @ c3fce61.

| D9 pin | Evidence today | Verdict (seed) |
|---|---|---|
| async IN_PROGRESS on handover | Kotlin-only (`block-runner-core`); no Go code | n/a until phase 6 (Kotlin pinning per D6) |
| abort flag via PATCH cancels run ctx | `worker.go observerRoutine` 96.4%; `runapi_status_test.go` exists | likely covered — verify test asserts cancellation |
| 10s status ticker | `observerRoutine` 96.4% | likely covered — verify interval asserted |
| run-token precedence + `ClearRunToken` | `tfrun/runapi.go` `SetRunToken`/`ClearRunToken` 100% | covered |
| 409-on-register = success | `tfrun/runapi.go Register` 100% | verify explicit 409 case |
| 404/409-on-claim = no run | `worker.go:66 handleFetchRunError` **0.0%** | **untested** |
| media types + runner headers | `runapi_test.go`; `meshapi/client.go setHeaders` 100% | partially — verify assertions |
| plan-artifact download (same-origin, 128MiB cap) | `meshapi DownloadArtifact` 76.2%; `tfrun DownloadPredecessorArtifact` 100% | mostly covered |
| meshStack HTTP backend fallback + `TF_HTTP_*` ephemeral auth | `createMeshStackHttpBackendFile` 89.5%; `tfcmd.go:271 detectBackend` **0.0%** | **partial** |
| pre-run script contract | `runPreRunScript` 100%; `scriptcmd*` 66-100%; `tfcmd_prerunscript_test.go` | covered |
| tfvars/`meshStack_run_vars.tf` generation rules | `vars` 82.0%; `encodeVarValueForEnv` **33.3%** | partial |
| FILE inputs as data-URLs | `extractContentFromDataUrl` 83.3%; `saveInputFiles` 70.0% | partial |
| env whitelist (`cleanSystemEnv`) | 87.5% | covered |
| decrypt-failure UX | `crypto NewCertBasedDecryptor*` **0.0%**; `controller decryptRunDetails` 29.7% | **mostly untested** |
| workspace select/create/delete naming | `selectWorkspace` **47.1%** (contains the D13 bug: swallowed error, `tfcmd.go:232-234` confirmed `return "", nil`); `deleteWorkspaceIfNeeded` 76.9% | partial + known bug |
| k8s single-run contract (`RUN_JSON_FILE_PATH`, runToken-only auth, …) | `tfrun/singlerunworker.go` **all 0.0%**; `controller/kubernetes.go` **all 0.0%** | **untested on both sides — biggest gap** |

Additional zero-coverage areas outside the pin list (input to plan 01's exclusion-list
decision): `tfrun/manager.go` (all 0%), `tfrun/git.go` azure/auth clone paths (0%),
`controller/registration.go` + `controller/runapi.go` (0%), both `main.go`s (0%).

## 9. Rollback story

Single squash commit ⇒ `git revert` restores the Makefile, removes Taskfile/lint/coverage
tooling, and returns `ci.yml` to plain `go test ./...`. No migrations, no image/config/API
surface changes, no cross-repo edits — rollback is purely local and instant. The plan-branch
inventory (§8) survives a revert harmlessly (documentation only).

## 10. Cross-repo touch points

- **meshfed-release:** read-only. local-dev-stack SKILL.md references `go run .` and gradle,
  never make/task (§4) — no doc update needed. Step 9 uses it as verification.
- **terraform-provider-meshstack:** pattern source only (`.golangci.yml`, `Taskfile.yml`).
- **multiplexing-block-runner:** pattern source only (trimmed lint config, vet-in-lint note).

## 11. Flags — findings the high-level plan did not anticipate

1. **`go-meshapi-client` is not tested in CI at all** (`ci.yml:150-155` matrix lacks it;
   only `make test` covers it locally). The matrix leg is added in this PR as an additive
   guardrail (see §5.5); deferring to phase 7 would leave a shared module unguarded through
   the entire refactor.
2. **231 lint findings** at provider parity — the high-level plan's "adopt golangci-lint v2"
   silently implied a nontrivial cleanup; §5.3's inert-vs-pin rule is new policy this plan
   adds so phase 0 stays behavior-neutral. ~25 production-code findings get *pinned*, not
   fixed, and become phase 2/2b/3 obligations.
3. **`tfrun` tests need internet** (real terraform/opentofu downloads, ~50s) — affects how
   plan 01 shapes the D6 exclusion list and keeps CI honest about flakiness sources.
4. The **k8s single-run contract has zero Go test coverage on both ends**
   (`singlerunworker.go`, `kubernetes.go`) despite being a frozen D9/D10 contract —
   raises the stakes for phase 1's scope beyond what §2 of the high-level plan suggests
   ("good facades already exist" is true only for the polling path).

## 12. Promises to later phases

Later plans may assume, verbatim:

- **Task targets:** `task test`, `task test:go-meshapi-client`, `task test:run-controller`,
  `task test:tf-block-runner`, `task lint` (supports `task lint -- --fix`), `task fmt`,
  `task tidy`, `task work-sync`, `task coverage`, `task start:run-controller`,
  `task start:tf-block-runner`. No Makefile exists; there is no vet target (govet is inside
  lint).
- **Coverage gate mechanics:** enabling a gate = adding one line
  `<import-path-prefix> <min-percent>` to `tools/coverage/thresholds.txt` (phase 1 adds
  `github.com/meshcloud/building-block-runner/tf-block-runner/tfrun 90`); CI
  (`go-runners-ci`) already runs `tools/coverage/check.sh` on every module's profile and
  will fail the build once a matching line exists. Coverage profiles are published as CI
  artifacts `coverage-<module>` plus a step-summary table.
- **Exclusion-list mechanism:** `tools/coverage/exclusions.txt`, one
  `<import-path>/<file.go>  # justification` per line, filtered out of profiles before the
  threshold math; ships empty — phase 1 populates it for real-I/O adapter files.
- **Lint config:** root `/.golangci.yml`, golangci-lint v2 (2.12.2 via flake), provider-
  parity linter set incl. govet, gci localmodule ordering, per-module strict depguard rules
  (new dependency ⇒ config edit), and *temporary* `exclusions.rules` blocks whose removal is
  owned by: `tfrun` block → phase 2/2b, `controller`/`meshapi` blocks → phase 3. Phase 2+
  extends depguard with `internal/` dependency-direction rules (D11).
- **Baseline numbers** (§3) and the **D9 inventory** (§8, finalized on the plan branch) as
  the measured starting point and as direct input to `PLAN_DETAIL_01`.
- **Toolchain:** `nix develop` provides go, golangci-lint, go-task.

## 13. Open questions

None.
