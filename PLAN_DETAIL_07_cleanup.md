# Detail Plan 07 — Cleanup & Docs (Phase 7, final)

**Phase:** 7 (last) · **Branch:** `refactor/single-go-binary/phase-7-cleanup` (stacked on
`refactor/single-go-binary/phase-6d-github`) · **Delivery:** one single-commit PR
(§5 high-level plan) · **Binding:** §3 P1–P8, D7 (alias/deprecation policy), D12
(metric-name alias duty), D14 (**this phase owns the Go-only CI reshape incl. docker
builds**), D15/umbrella §10.12 (**this phase owns the slog migration of the
shared/tf/dispatch packages**), D9/D10 (frozen contracts) of `PLAN_HIGH_LEVEL.md`.

Phase character: **the sweeper.** Phase 7 (a) collects every item earlier plans
deferred here and disposes of each one explicitly (§4 ledger — do / drop with rationale
/ record as future work), (b) reshapes CI into the final Go-only form with the docker
image builds (§5), (c) rewrites the repo documentation to describe the end state and
records the final architecture (§6, §9), (d) audits and documents the config
deprecation surface (§7), and (e) finishes the slog migration (§8). Wire, image, port,
env-var and k8s contracts stay frozen — with exactly two flagged behavior deltas, both
inherited as named phase-7 candidates from plan 05 (§4 items L14/L15, §11).

All Kotlin/Gradle/JVM artifacts are assumed **gone** (06D's JVM endgame, umbrella §5.8 /
06D §12.2); this plan's CI design and doc rewrite build on that end state. Code
references are `main` @ `c3fce61` unless marked *post-N* (shape promised by plan N).

---

## 1. Assumptions from prior phases

Plans 00–06D are **not implemented yet**; everything below is a promise, not a fact.
Implementation **begins by running every verification step**. Any material failure is a
**STOP**: update this plan first, get the revision reviewed, then resume.

| # | Assumption | Promised by | Verification step |
|---|---|---|---|
| A1 | The phase-6d branch is green: `task test` (with `-race`), `task lint`, `task coverage` all pass; module `./runner`, no `go.work`, binary `bbrunner`, six personas registered (`tf-block-runner`, `run-controller`, `manual-block-runner`, `gitlab-block-runner`, `azure-devops-block-runner`, `github-block-runner`) + `tfrunner` alias. | Plans 00–06 | `git checkout refactor/single-go-binary/phase-6d-github && task test && task lint && task coverage`; `grep -n "Persona" runner/main.go` |
| A2 | **The JVM is gone**: no `*.gradle`, `gradlew*`, `gradle/`, `block-runner-core/`, module dirs, `containers/jvm.Dockerfile`, `entrypoint-jvm.sh`; `ci.yml` has no `jvm-runners-*` jobs; `flake.nix` has no `jdk21_headless`/`ktlint`; `.claude/settings.json` has no ktlint hook. | 06D §12.2 | `ls *.gradle gradlew 2>/dev/null` (empty); `grep -rn "gradle\|ktlint\|jvm" .github/workflows/ flake.nix .claude/settings.json` (no hits) |
| A3 | Packages: `runner/internal/{tf,gitsource,tofu,meshapi,crypto,config,report,mgmt,dispatch,k8sjob,manual,gitlab,azdevops,github,build}`; `internal/controller` is gone. | Plans 04 §11, 05 §5, 06A–D | `ls runner/internal` |
| A4 | Coverage gate: per-package thresholds at 90 for `tf,gitsource,tofu,meshapi,crypto,config,report,mgmt,dispatch,k8sjob,manual,gitlab,azdevops,github`; exclusions exactly `gitsource/git.go`, `tofu/tfbinaries.go`, `k8sjob/cluster.go`; CI (`go-runners-ci`) runs `tools/coverage/check.sh` gating. | Plans 04 §7.1, 05 §13, 06 §5.1 item 10 | `cat tools/coverage/thresholds.txt tools/coverage/exclusions.txt && task coverage` — record numbers |
| A5 | **Zero `FIXME(bug)` markers** (2b exit) and **zero temporary `.golangci.yml` exclusion blocks** (plan 00 §5.3 categories owned by phases 2/2b/3 all paid off). | Plans 02 §7 R13, 00 §12 | `grep -rn "FIXME(bug)" runner/` (empty); read `.golangci.yml` — only permanent config remains |
| A6 | Alias/deprecation warnings are already **implemented** where phases 03–06 introduced aliases: `RUNCONTROLLER_CONFIG_FILE`, `PORT`→`MANAGEMENT_PORT` (tf + 4 ported personas), `SPRING_PROFILES_ACTIVE` single-run trigger, `blockrunner:` yaml block, private-key key aliases, `logging./server./spring.` ignored-with-warning — via `config.EnvAlias`-style mechanics. Phase 7 audits wording + documents timeline; it does not invent the mechanism. | Plans 03 §5.3, 04 §4.3, 06 §5.4/§7.8 | `grep -rn "deprecat" runner/internal/config runner/persona_*.go`; run the alias tests |
| A7 | **No metric was renamed** in phases 3–6 (all additions: `runner_*` set + `runner_runs_unhandled_total` + `runner_at_capacity_skips_total`; `run_controller_*` byte-identical) ⇒ the D12 metric-alias inventory is empty. | Plans 04 §6.2, 05 §10.3 | `grep -rn "prometheus.NewCounter\|NewHistogram" runner/internal/{mgmt,dispatch}` — names match plans 04/05 |
| A8 | Logging is the umbrella-§10.12 interim state: phase-6 handler packages use `log/slog` (run id as attr); `config`, `report`, `mgmt`, `dispatch`, `k8sjob`, `tf`, `gitsource`, `tofu` and the persona bootstraps still use `*log.Logger` (persona prefixes `[TF RUNNER]`, `[RUN CONTROLLER]`; per-run `[RUN-<id>]` in the tf handler per plan 05 H3). | Umbrella §10.12, plans 03–05 signatures | `grep -rln "log/slog" runner/internal` vs `grep -rln "\*log.Logger" runner/internal` |
| A9 | Workflows post-06D: `ci.yml` = `go-runners-ci` (one `runner` leg, coverage steps) + `go-runners-image` (6 legs, `containers/runner.Dockerfile` + `target:`); `build-images.yml` = 6 legs, all `runner.Dockerfile` + `target:`, no `RUNNER_MODULE`; `release.yml`/`release-check.yml`/`pr-cleanup.yml` untouched since `main`. **No lint job exists** (plan 00: "No CI lint job until phase 7"). | Plans 04 §4.5, 06 §5.6/§5.8 | read the three workflow files; `grep -rn "golangci" .github/workflows/` (empty) |
| A10 | Single-run exit semantics are the 2b-R12 conditional; `BackoffLimit: 1` stands (plan 05 §16.3); the tf single-run path is still the *separate* `executeSingleRun`-shaped glue (plan 05 §2 deliberately did not unify it with the handler — deferred here, §16.7). | Plans 02 R12, 05 §2/§16.7 | read `runner/persona_tf.go` single-run tail |
| A11 | The controller decrypt-failure quirk is intact and **pinned**: decrypt error ⇒ log + `run_controller_decryption_errors_total` + `processFailed`, **no status report** (transcript-empty pin from plan 05 step 1). | Plan 05 §10.2/§11.1 step 1 | run the k8sjob decrypt-failure transcript test; confirm it asserts *no* register/PATCH |
| A12 | `run-controller`'s sample config (post-04 path `containers/run-controller/runner-config.yml`) still ships `SPRING_PROFILES_ACTIVE: kubernetes` in the four pipeline-runner job templates (flip deferred here, umbrella §9). | Plan 04 §4.4, umbrella §9 | `grep -n "SPRING_PROFILES_ACTIVE" containers/run-controller/runner-config.yml` |
| A13 | meshfed-release `local-dev-stack/SKILL.md` is the post-06A shape: tf via `go run . tf-block-runner`, manual via `go run . manual-block-runner`, readiness markers reference the current log lines (incl. the `[TF RUNNER]` prefix — plan 04 §9 verified it). | Plans 04 §9, 06A §15 | read the SKILL; note the exact readiness-marker strings (input to §8/§14) |
| A14 | The k8s example manifests (`run-controller/k8s/deployment.yaml`, `rbac.yaml` on `main`) still exist somewhere post-phase-4. **Plan 04 assigned them no destination** (its asset moves cover only `runner-config.yml`/`known_hosts` — flag §15.1); expected: left at their old path or moved ad hoc. | — (gap) | `find . -name deployment.yaml -o -name rbac.yaml`; record the location |

**STOP-A (before any coding):** any of A1–A13 materially false ⇒ update this plan first.
A14 failing (manifests deleted without replacement) is not a STOP — restoring them under
`containers/run-controller/k8s/` becomes a step-6 task either way.
**STOP-B (any time):** any characterization/transcript/golden **assertion** must change
beyond the declared slog retargets in §8.4 — the classic failure mode here is a step
`SystemMessage` byte change caused by reformatting a logger whose output feeds the
run-log segments (§8.3). Stop, record, review, resume.
**STOP-C (any time):** a gated package drops below 90 — add tests, never exclusions.
**STOP-D (step 4):** the controller decrypt-failure fix (§4 L14) — implementation does
not start before the PR-level review of the exact new wire behavior (message, metric,
report shape) signs off; if review rejects, the pin stays and L14 moves to future work.
**STOP-E (step 5):** the single-run unification (§4 L15) forces any pinned observable
(wire transcript, exit code, `RUN_JSON_FILE_PATH` handling) to change ⇒ abandon the
unification (keep the glue), record, continue — it is a should-have, not a must-have.
**STOP-F (step 12):** the meshfed-release local-dev-stack + acceptance flow fails —
the outer net; diagnose/replan before merging.

---

## 2. Scope

**In:**

- The **deferred-item ledger** (§4) and the disposition of every row marked "do here".
- **CI reshape (D14):** lint job, test/coverage job consolidation, image-build matrix
  tidy-up, hermetic-CI split of the network tests behind an `e2e` build tag + opt-in
  job (§5).
- **slog migration** of `config`, `report`, `mgmt`, `dispatch`, `k8sjob`, `tf`,
  `gitsource`, `tofu`, the persona bootstraps and `main` — ending the two-logging-stacks
  interim (§8; umbrella §10.12).
- **Docs:** root `README.md` rewrite, `runner/README.md` refresh, new
  `docs/ARCHITECTURE.md`, k8s example-manifest home, config-sample comment/flip,
  `.editorconfig` JVM-section removal, `.agents/skills/backend-go` rewrite (§6).
- **Deprecation audit + documented timeline** for every alias from phases 3–6 (§7);
  verification that the D12 metric-alias inventory is empty (A7).
- **Two flagged behavior fixes inherited by name:** the controller decrypt-failure
  silent hang (plan 05 §16.8 → L14, STOP-D) and the tf single-run/handler unification
  (plan 05 §16.7 → L15, STOP-E).
- **Verification sweeps:** zero `FIXME(bug)`, zero temporary lint exclusions, zero
  gradle/kotlin references outside `docs/plans/` + release notes, `.gitignore` residue.
- **Plan-file disposition:** `PLAN_*.md` → `docs/plans/` (§9.2).
- Cross-repo doc truth pass + the slog readiness-marker lock-step edit (§14).

**Out (with destination — this is the last phase, so "destination" means the
post-refactor follow-up list recorded in `docs/ARCHITECTURE.md`, §9.1):**

- Everything in §4 marked **future work**: `meshstack-go-sdk` extraction (high-level
  §8), the k8s-mode plaintext-impl-secret handover (06B flag 3), decryption-rule
  unification (06B flag 8), ADO/GitHub PATCH-cadence and dedup fixes (06C flag 2,
  06D flag 6), mapper else-holes (06C flag 5), GitHub run correlation (06D flag 7),
  abort support for ported runners (umbrella §7.5), mixed dispatch (plan 05 §16.1),
  per-type acceptance tests (umbrella §10.2), alias *removals* (§7.3).
- Any wire/API/image-name/port change beyond the two flagged items above.
- New features of any kind (high-level §8; feature freeze holds to the end).

---

## 3. Research evidence — current state

All references at `refactor/single-go-binary/plan` (= `main` @ `c3fce61`).

### 3.1 Documentation that phases 4–6 obsolete

- **`README.md`** — the phase-7 rewrite target, stale claims enumerated:
  runner table with per-module dirs + "Kotlin" language column (`README.md:14-20`);
  "two separate build systems in parallel" + go.work + Gradle (`:48-51`); shared-module
  bullets for `block-runner-core`/`go-meshapi-client` (`:53-57`); `make help` (`:59-63`
  — dead since phase 0); health-port table with `PORT` override examples incl.
  `./gradlew bootRun` (`:65-86` — superseded by `MANAGEMENT_PORT`, plan 04 §4.3, and the
  controller now *has* healthz); prerequisites "Go 1.22+, JDK 21+" (`:92-93` — Go is
  1.26, no JDK); `make test/fmt/vet/tidy/work-sync` (`:95-103`); "Build and test (JVM)"
  (`:105-110`); `make start-*` (`:112-117`); module-README links (`:119-122`). The
  Release section (`:124-145`) and Support/Contributing/Security sections stay true.
- **`tf-block-runner/README.md` / `run-controller/README.md`** — folded into
  `runner/README.md` by plan 04 step 7; phase 7 only *refreshes* that file (post-5/6
  content: dispatcher, `maxConcurrentRuns`, six personas).
- **`run-controller/k8s/{deployment.yaml,rbac.yaml}`** — example manifests
  (`deployment.yaml:21` pins `ghcr.io/meshcloud/run-controller:main`). **No prior plan
  moves them** (plan 04 §4.4 moves only `runner-config.yml`/`known_hosts`); A14/§6
  gives them a home.
- **`SECURITY.md`** — process-only, no build-system or path references (verified read):
  **no change needed**.
- **`cliff.toml`** — conventional-commit release notes, no JVM references (confirmed by
  06D §12.2): **keep as-is**.
- **`.editorconfig`** — `[*.{kt,kts}]` (lines 6-46, the whole ktlint rule block),
  `[*.gradle]` (lines 48-51) are dead after 06D; `root/insert_final_newline` and the
  `[*.sql]`/`[*.vm]` sections are unrelated to the JVM teardown (the `.vm` section is
  legacy but harmless — kept; flag §15.4). **06D's teardown list does not include
  `.editorconfig`** — swept here.
- **`.agents/skills/backend-go/SKILL.md` + `testing.md`** — reference `tf-block-runner/`
  as cwd (`SKILL.md:3,12`), `./tfrun/...` test paths and `goldie` regeneration
  (`testing.md:13,21-26`). Dead after phase 2 (`tfrun` → `internal/tf`) and phase 4
  (module gone). **No prior plan touches `.agents/`** — swept here (flag §15.2).
  `commit-messages/SKILL.md` is path-free (verified grep): no change.
- **`.gitignore`** — `.gradle`, `.kotlin/` and the `!tf-block-runner/build/` /
  `!run-controller/build/` negations; 06D §12.2 owns the gradle entries and asks this
  phase's sweep to confirm the negations died with plan 04's module collapse.

### 3.2 CI/workflows post-06D (the D14 baseline this phase reshapes)

- `ci.yml` today: `jvm-runners-ci`/`jvm-runners-image` (`:19-141` — gone after 06D),
  `go-runners-ci` (`:143-174`: matrix of two module dirs, plain `go test ./...` at
  `:174`; *post-0* + coverage steps, *post-4* single `runner` leg), `go-runners-image`
  (`:179-260`: two Dockerfile legs; *post-4/6*: six `runner.Dockerfile` `target:` legs).
  Tag scheme: `:main` + `:<sha>` on main, `pr-N` local-only on PRs (`:96-108`).
- `build-images.yml`: release matrix (two go legs + four JVM legs with `RUNNER_MODULE`,
  `:26-43`; *post-4/6*: six `target:` legs); `workflow_call` from `release.yml` +
  `v*` tag fallback.
- `release.yml` (tag + GitHub-Release + calls build-images), `release-check.yml`
  (cron nag for unreleased commits), `pr-cleanup.yml` (deletes `pr-N` tags for the six
  image names, `:20-27,55-62`) — all operate on image *names*, which never change: no
  phase-7 edits needed beyond verification.
- **No lint job anywhere** (`grep golangci .github/workflows/` — empty); plan 00 §5.5
  deliberately deferred it here. Provider precedent: SHA-pinned
  `golangci/golangci-lint-action` (provider `test.yml:55-58`).
- **Network in tests:** `tfrun/tfbinaries_test.go:16` really downloads
  terraform/opentofu (~50s, plan 00 §3); plan 01 kept it (gate-excluded file) and named
  the opt-in e2e split a "phase 0/7 CI concern" (plan 01 §2). Still unhermetic in CI.

### 3.3 Cross-repo state (exact refs)

- **meshfed-release `.agents/skills/local-dev-stack/SKILL.md`** (pre-04 line numbers;
  *post-06A* shape per A13): manual runner block `:67-71` (gradle bootRun → Go persona
  in 06A), tf block `:78-82` (`go run .` → `go run . tf-block-runner` in 04), pgrep hint
  `:90` (`'multiplexing-block-runner|tf-block-runner|BlockRunnerApplication'` — the
  `BlockRunnerApplication` alternative dies with 06A), readiness table `:103`
  (`Started BlockRunnerApplication` → Go marker in 06A; tf `[TF RUNNER]` marker —
  **input to the §8 slog lock-step**).
- **meshfed-release `docs/docs/guides/platform-ecosystem/how-to-run-building-block-runners.md`**:
  grep for `gradle|SPRING_PROFILES_ACTIVE|PORT|healthz` — **zero hits**; it references
  images by registry name and generic "Docker containers" prose (`:31,44`). Expected
  phase-7 outcome: verification only, no edit (§14).
- **terraform-provider-meshstack `.agents/skills/scratch-config-testing/SKILL.md`**:
  behavioral references only — mux `:8300`, `/tmp/tf-runner.log`, tofu download
  behavior (`:82-94`); no path/command dependency ⇒ no edit (re-verified; matches
  plan 04 §3.7).

### 3.4 The logging seams to migrate (§8 input)

Post-06 `*log.Logger` surface (per plans 03 §5.3/§5.4, 04 §4.3, 05 §4.1, umbrella
§10.12): `config.Path/Env/ManagementPort(log *log.Logger, …)`,
`mgmt.NewServer(log *log.Logger, …)`, `dispatch.NewLoop` deps + loop logging,
`k8sjob` logging, `report.NewRunLog(logger *log.Logger, path)`, the tf engine/manager
heirs (per-run `[RUN-<id>]` prefix, plan 05 H3/§16.9), `gitsource`/`tofu` adapters,
persona bootstraps (`[TF RUNNER]`, `[RUN CONTROLLER]` prefixes — plan 04 §4.1 keeps
them *for the readiness markers*), `main`. Phase-6 packages already use slog with
run-id attrs (umbrella §5.3). **Load-bearing subtlety:** the tf run-scoped logger
writes into the per-run log file whose byte segments become step `SystemMessage`
contents on the wire (`fileContentOrEmpty` mechanics, plan 02 §3.5) — pinned by the
characterization suite. §8.3 handles this; naive reformatting is STOP-B.

---

## 4. Deferred-item ledger (the sweep)

Every item any prior plan deferred "to phase 7", flagged without a destination, or left
as a follow-up candidate. Decisions: **DO** (this PR), **VERIFY** (this PR, expected
no-op), **DROP** (with rationale), **FUTURE** (recorded in `docs/ARCHITECTURE.md`'s
follow-up register, §9.1 — post-refactor work, out of this refactor per high-level §8).

| # | Item | Source | Decision |
|---|---|---|---|
| L1 | CI lint job (golangci-lint) — deliberately absent until now | 00 §5.2/§5.5, D14 | **DO** — §5.2 |
| L2 | Go-only CI reshape + docker builds consolidation | D14, 04 §4.5 ("minimum edits only"), 06 §2 | **DO** — §5 |
| L3 | Opt-in real-tofu/real-git e2e split (network tests out of default CI) | 01 §2 ("phase 0/7 CI concern"), 00 §11.3 | **DO** — `e2e` build tag + `task test:e2e` + opt-in workflow, §5.4 |
| L4 | `go-meshapi-client` CI matrix leg (flagged reviewer decision) | 00 §11.1 | **VERIFY** — moot since phase 4 consolidated the module; confirm the single `runner` leg covers everything |
| L5 | `FIXME(bug)` leftovers — must be zero after 2b | 01 §6, 02 §7 R13 | **VERIFY** — `grep -rc "FIXME(bug)"` = 0 repo-wide (A5); a hit is a STOP (2b exit criterion violated) |
| L6 | Temporary `.golangci.yml` exclusion blocks (owned by 2/2b/3) | 00 §5.3/§12 | **VERIFY** — zero remain (A5) |
| L7 | Root README rewrite (structure/module/health tables all stale) | 04 §2 out, 06D §12.2 ("overhaul stays phase 7") | **DO** — §6.1 |
| L8 | Final architecture record ("memory of final architecture") | high-level §5 phase 7 | **DO** — `docs/ARCHITECTURE.md`, §9.1 |
| L9 | Deprecation-warning audit + documented timeline (D7 alias inventory of phases 3–6) | 03 §5.3, 04 §4.3/§6.3, 06 §5.4/§7.8 | **DO** — §7 |
| L10 | D12 metric-name aliases | D12, 03 §12.9 | **VERIFY** — inventory empty (A7): no metric was ever renamed; document the full metric set in ARCHITECTURE.md |
| L11 | slog migration of shared/tf/controller-successor packages | D15, umbrella §10.12/§10 flag 12 | **DO** — §8 |
| L12 | Flip `containers/run-controller/runner-config.yml` job-template envs `SPRING_PROFILES_ACTIVE: kubernetes` → `EXECUTION_MODE: single-run` (+ comment that the old form stays honored) | umbrella §9 ("deferred to phase 7 so rollback stays symmetric") | **DO** — §6.3; safe now: no JVM image generation remains that the sample must roll back to |
| L13 | Fail-fast message unification (two messages for `UnhandledTypeError`) | 05 §10.1/§16.4 | **DROP** — keep both: the controller string `no implementation handler configured for type '%s'` is frozen wire bytes (bit-identity mandate, 05 §12); collapsing to it would ship the vague text to standalone users, defeating D5. Documented in ARCHITECTURE.md |
| L14 | Controller decrypt-failure silent hang (no status report; run waits for coordinator timeout) | 05 §10.2/§16.8 ("latent-bug candidate for a post-refactor fix") | **DO, flagged (STOP-D)** — last in-refactor opportunity; P5 (never suppress silently). Fix: after decrypt failure, run the loop's `reportRunFailure` with an actionable key-mismatch message (wording aligned with the tf decrypt-failure guidance, D9); keep `run_controller_decryption_errors_total` firing. Flip the plan-05 transcript-empty pin. Reviewer may veto → FUTURE |
| L15 | tf single-run path reuse of the tf handler (delete the parallel glue) | 05 §2/§16.7 ("phase-7 cleanup candidate") | **DO, guarded (STOP-E)** — wire/exit observables must stay byte-identical under the existing CP3/single-run pins; abandon on any pin drift |
| L16 | `.editorconfig` Kotlin/gradle sections | not in 06D §12.2 inventory (gap) | **DO** — §6.5 |
| L17 | `.agents/skills/backend-go` stale paths (`tf-block-runner/`, `./tfrun/`) | no plan owns it (gap, §15.2) | **DO** — §6.6 |
| L18 | k8s example manifests (`run-controller/k8s/*.yaml`) — no destination assigned by plan 04 | gap (§15.1), A14 | **DO** — move to `containers/run-controller/k8s/`, referenced from README/ARCHITECTURE |
| L19 | `.gitignore` residue (gradle/kotlin entries, module `build/` negations) | 06D §12.2 (partial) | **VERIFY**, finish if residue found |
| L20 | Gradle/Kotlin reference grep gate repo-wide | 06D §12.2 grep gates | **VERIFY** — re-run; only `docs/plans/` + release notes may hit |
| L21 | meshfed-release docs full pass | umbrella §9, 06A–D §15 | **DO (verification-weighted)** — §14; grep shows `how-to-run-…` has no JVM-era refs, expected outcome "no edit" |
| L22 | Readiness markers in local-dev-stack SKILL after slog | 04 §9, 05 §15, A13 | **DO** — lock-step edit with §8 (the one mandatory cross-repo change) |
| L23 | Timeout-message quirk (`TfCommandTimeoutMins` printed while the engine timeout is a separate duration) | 02 §11.6 (preserved verbatim, no destination) | **FUTURE** — user-visible message change with no operational harm today; not cleanup |
| L24 | Vestigial claim node-id `<uuid>-worker-1` | 05 §16.5 | **FUTURE** — frozen observable header (D9); dropping it needs meshStack-side confirmation |
| L25 | `meshstack-go-sdk` extraction + recorded client deltas (AuthProvider signature, client timeout, media-type computation) | D3, 03 §7, high-level §8 | **FUTURE** — 03 §7's alignment table is copied into ARCHITECTURE.md as the merge input |
| L26 | k8s-mode plaintext `pipelineTriggerToken` inside `MESHSTACK_RUN` (controller decrypts impl secrets before handover) | 06B flag 3 | **FUTURE** — fixing means controller-side inputs-only decryption for pipeline types = k8s Secret contract change; needs its own compat plan |
| L27 | Two coexisting decryption rules (`DecryptRunDetails` any-sensitive vs `DecryptInputs` STRING/CODE/FILE-only) | 06B flag 8 | **FUTURE** — same contract blast radius as L26; both rules are pinned |
| L28 | ADO stage-PATCH every 10s regardless of change; COMPLETED stages re-sent | 06C flag 2 | **FUTURE** — coordinator-visible traffic shape, ported verbatim by design |
| L29 | ADO status-mapper else-holes (`completed+succeededWithIssues`→IN_PROGRESS etc.) | 06C flag 5 | **FUTURE** — UI-visible mapping change |
| L30 | GitHub completed-job re-reporting every poll batch | 06D flag 6 | **FUTURE** — same class as L28 |
| L31 | GitHub run-correlation heuristic can cross-track concurrent dispatches | 06D flag 7 | **FUTURE** — feature-level fix (dispatch correlation id); config docs already warn (06D) |
| L32 | Abort-flag support for ported runners | umbrella §7.5 | **FUTURE** — explicitly "a post-refactor feature, not part of a truthful port" |
| L33 | Mixed in-process/k8s dispatch in one process | 05 §16.1 | **FUTURE** — the `Dispatcher` seam makes it a small change when a persona needs it; none does |
| L34 | Per-type acceptance tests for gitlab/azdevops/github | umbrella §5.7/§10.2 | **FUTURE** — meshfed-release feature, out of this repo's scope |
| L35 | Kotlin INFO-logging of decrypted sensitive inputs (manual, k8s mode) | 06A flag 3 | **DROP** — moot: the Kotlin module is deleted; the Go port never logged values |
| L36 | `Test_UseCustomPredicate_*` misleading names | 01 F4 | **VERIFY** — renamed in phase 2 step 1; grep for `CustomPredicate` = 0 |
| L37 | Alias *removals* (`SPRING_PROFILES_ACTIVE`, `PORT`, `blockrunner:` block, `RUNCONTROLLER_CONFIG_FILE`) — umbrella §7.8 says "supported until phase 7 at the earliest" | umbrella §7.8, D7 | **DROP the removal** (keep every alias, warned): removal is a breaking config change gated on the §7.3 timeline (next major), never on a cleanup phase. Phase 7's job is the *documented schedule*, not the removal |
| L38 | Java-toString-compat rendering decisions (gitlab/ADO composite stringification) | 06B flag 4, 06C flag 6 | **VERIFY** consistency only — decided during 06B/06C review; phase 7 confirms both personas resolved it the same way and documents it |
| L39 | `BackoffLimit: 1` alignment question | 02 R12 → 05 §16.3 | **DROP** — resolved "keep 1" in plan 05; recorded in ARCHITECTURE.md as a settled decision |
| L40 | Errata in plan documents (umbrella test counts 06A §16.7/06D §16.1; umbrella §4 row 13 azdevops-sanitizer erratum 06C §16.7; umbrella §3.2 log-only-messages correction 06B §16.1; D9 same-origin staleness 01 F2) | 06A–D §16, 01 F2 | **DO (docs-only)** — a one-page `docs/plans/ERRATA.md` accompanies the archived plans so the historical record is honest (§9.2); no code impact |

Ledger totals: **40 items — 14 DO, 8 VERIFY, 4 DROP (each with rationale), 14 FUTURE**
(the FUTURE set becomes the ARCHITECTURE.md follow-up register verbatim, §9.1).

## 5. CI end-state design (D14)

Principles: image names, tag schemes (`:main`, `:<sha>`, `:pr-N`-local, release
`:<version>`/`:latest`) and trigger semantics are **frozen** (D8/D10); jobs are
restructured, not re-semanticized. CI installs plain `go` + the pinned lint action —
it does **not** depend on `task` or nix (phase-0 decision carried forward).

### 5.1 Final workflow set

| Workflow | End state |
|---|---|
| `ci.yml` | Three jobs: `lint` (§5.2) · `test` (§5.3) · `images` (§5.5). Triggers/concurrency unchanged (`push: main`, `pull_request`, cancel-in-progress). |
| `build-images.yml` | Already six `containers/runner.Dockerfile` + `target:` legs post-06D (A9). Phase-7 delta: none expected; verify no `RUNNER_MODULE` residue and that the ldflags `VERSION` build-arg path is `…/runner/internal/build.Version`. |
| `release.yml` / `release-check.yml` / `pr-cleanup.yml` | **Unchanged** — they operate on tags and image names only (§3.2). Verified, not edited. |
| `e2e.yml` (new) | Opt-in real-I/O tests (§5.4): `workflow_dispatch` + weekly `schedule`; never a PR gate. |

### 5.2 `lint` job (L1)

- SHA-pinned `golangci/golangci-lint-action` (repo pinning convention, e.g.
  `ci.yml:39`; provider precedent `test.yml:55-58`).
- `working-directory: runner`, `go-version-file: runner/go.mod`.
- **Pinned lint version matching the flake** (2.12.2 line from nixos-25.11,
  plan 00 §4) — *not* the provider's `version: latest` + `only-new-issues: true`:
  after phases 0–6 the tree is clean, so full-run/fail-on-any is achievable and
  `only-new-issues` would mask regressions. Local (`task lint`) and CI use the same
  major version; drift is a Renovate/flake-bump concern, documented in the workflow
  comment.
- Runs in parallel with `test` (no `needs:`); `images` needs both.

### 5.3 `test` job (evolution of `go-runners-ci`)

Post-04 the job is already a single `runner` leg with coverage steps (A9). Phase-7
deltas: rename job/display to `test` (cosmetic; flag §15.5 — branch-protection
required-check names change, coordinate at merge time), keep
`go test -race -coverprofile=coverage.out ./...` + `tools/coverage/check.sh` (gating,
all §A4 thresholds) + `coverage-runner` artifact + step-summary table — mechanics
unchanged since phase 0/4. Default run excludes the `e2e` build tag (§5.4), making
**default CI hermetic for the first time** (no terraform/tofu downloads, ~50s faster;
plan 00 §11.3 resolved).

### 5.4 e2e split (L3)

- `//go:build e2e` on `runner/internal/tofu/tfbinaries_test.go` (the real-download
  suite — the only network consumer left after plan 01 CP1 made everything else
  hermetic) and on any real-git test that survived in `gitsource`.
- `task test:e2e` = `go test -tags e2e ./internal/tofu/... ./internal/gitsource/...`;
  plain `task test` stays tag-free.
- `e2e.yml`: workflow_dispatch + `schedule: cron weekly`, runs `test:e2e` equivalent
  directly (`go test -tags e2e …`), non-blocking (no branch protection), failure
  notifies via the release-check pattern (`release-check.yml` slack/issue mechanism —
  reuse whatever it uses; verified at implementation).
- Coverage math: the tagged file is already on the exclusion list (A4) — the gate
  denominator is unchanged whether or not the tag is set. Verified by running
  `task coverage` before/after tagging (identical totals).

### 5.5 `images` job (consolidation of `go-runners-image` + the per-port legs)

One matrix, six legs — `app` = published image name = Dockerfile `target:`
(`tf-block-runner`, `run-controller`, `manual-block-runner`, `gitlab-block-runner`,
`azure-devops-block-runner`, `github-block-runner`); shared steps exactly today's
`go-runners-image` body (`ci.yml:196-260`: git-describe version, buildx,
GHCR+DockerHub logins gated on `meshcloud`+main, build-push with
`platforms: linux/amd64,linux/arm64`, PRs build-never-push). `needs: [lint, test]`.
This is a *merge* of matrix entries, not new behavior — every leg exists post-06D;
the job body was already shared.

### 5.6 What deliberately does not change

No pipeline caching additions, no new triggers, no matrix-of-Go-versions, no
release-process change (README `:124-145` release flow stays literally true), no
`task` in CI, no nix in CI. Rationale: D14's mandate is "Go-only CI including the
docker image builds", not a CI feature program; every addition beyond L1–L3 would be
scope creep in the final phase.

---

## 6. Docs rewrite inventory

### 6.1 Root `README.md` (L7) — full rewrite of the stale sections (§3.1 list)

| Section | End state |
|---|---|
| Runner table (`:14-20`) | One table of the six *personas* (same published image names, all Go), one sentence on the single-binary/argv[0] design, link to `docs/ARCHITECTURE.md` |
| "Running a runner" (`:22-34`) | Content stays (still true); add `EXECUTION_MODE`/`MANAGEMENT_PORT` pointers and the config-precedence one-liner (defaults < YAML < env, D7) |
| Repository structure (`:46-63`) | Rewritten: module `./runner` (packages under `internal/`), `containers/` (one Dockerfile, per-persona assets incl. `run-controller/k8s/` examples, L18), `tools/coverage/`, `docs/`. `task --list` replaces `make help` |
| Health endpoint (`:65-86`) | Becomes "Management endpoint": `/healthz` **and** `/metrics` on `MANAGEMENT_PORT`; per-persona default table gains `run-controller` = 2112 (now with healthz); `PORT` documented as deprecated alias with precedence `MANAGEMENT_PORT > PORT > default`; override examples become `MANAGEMENT_PORT=9000 go run . <persona>`; Docker default 8080 sentence stays true |
| Development (`:88-117`) | Prerequisites: Go 1.26 (or `nix develop`); `task test/lint/fmt/tidy/coverage/test:e2e/start:*`; JVM section deleted; "Run locally" = `task start:<persona>` + `go run . <persona>` |
| Release (`:124-145`) | Unchanged (verified true, §5.6) |
| New: Configuration & deprecations | The §7.2 alias table + timeline, rendered for operators |

Checkpoint rule (phase-0 precedent): **every command in the README executes.**

### 6.2 `docs/ARCHITECTURE.md` (L8) — see §9.1 for the outline.

### 6.3 Config samples (L12)

`containers/run-controller/runner-config.yml`: the four pipeline-runner job templates
flip `SPRING_PROFILES_ACTIVE: kubernetes` → `EXECUTION_MODE: single-run`, each with a
one-line comment that the old variable remains honored (deprecated). The TERRAFORM
template already uses `EXECUTION_MODE` (its config on `main`). Persona sample configs
(`containers/<persona>/runner-config.yml`) get a comment header pointing at the README
deprecation table; keys themselves unchanged.

### 6.4 k8s example manifests (L18/A14)

`git mv` `run-controller/k8s/{deployment.yaml,rbac.yaml}` (or wherever A14 finds them)
→ `containers/run-controller/k8s/`; refresh the deployment example: add the
liveness-probe stanza the controller can finally serve (`/healthz` on 2112, plan 04)
— **as commented-out example lines**, since adding a probe to a copied manifest changes
deployed behavior for users who paste it (flag §15.6); image reference
`ghcr.io/meshcloud/run-controller:main` unchanged.

### 6.5 `.editorconfig` (L16)

Delete `[*.{kt,kts}]` (incl. the whole ktlint rule block) and `[*.gradle]`; keep
`root = true`, `[*.sql]`, `[*.vm]` (flag §15.4). Add a `[*.go]` section only if it
states something (`indent_style = tab` is Go default via gofmt — **do not add**; the
formatter authority is `task fmt`, plan 00 §5.1).

### 6.6 `.agents/skills/backend-go` (L17)

Rewrite both files for the end state: cwd `runner/`, packages `internal/*`, commands
`task test` / `go test ./internal/tf/... -run …`, goldie note updated to the post-move
`testdata/` locations, a pointer to `docs/ARCHITECTURE.md` and the D11/D15 rules
(depguard-enforced layering, slog). Description line (`SKILL.md:3`) loses
"tf-block-runner service" — it is the repo-wide Go skill now.

### 6.7 `runner/README.md` refresh

Created by plan 04 step 7 (persona start commands, config paths); refresh for
phases 5/6 content: `maxConcurrentRuns`, `registration:` section, the six personas,
single-run activation (`EXECUTION_MODE` / deprecated `SPRING_PROFILES_ACTIVE`).

### 6.8 Explicit no-ops (verified, §3.1)

`SECURITY.md` (process-only), `cliff.toml` (no JVM refs; release flow unchanged),
`.claude/settings.json` (ktlint hook already removed by 06D — verify it is empty or
absent; if the file is left as `{"hooks":{}}` residue, delete it).

---

## 7. Deprecation-warning plan & timeline (D7/D12)

### 7.1 What phase 7 does (and does not)

The warning *mechanism* and each warning were implemented by the phase that introduced
the alias (A6). Phase 7: (a) **audits** that every alias in §7.2 actually logs, once,
at startup, through one shared helper with uniform wording
(`deprecated: <old> is deprecated, use <new> — see README#deprecations`); (b) adds any
missing warning found by the audit; (c) **documents the timeline** (§7.3) in README +
ARCHITECTURE.md; (d) removes **nothing** (L37).

### 7.2 The complete alias inventory (accumulated phases 3–6)

| # | Deprecated / compat surface | Canonical | Introduced | Warned? |
|---|---|---|---|---|
| 1 | env `RUNCONTROLLER_CONFIG_FILE` | `RUNNER_CONFIG_FILE` | 03 §5.3 | yes (audit) |
| 2 | env `PORT` (tf + all four ported personas; images bake `PORT=8080`) | `MANAGEMENT_PORT` | 04 §4.3, 06 §5.4 | yes — **fires in every default container** (plan 04 §6.3 accepted one startup line); wording must not spam per request |
| 3 | env `SPRING_PROFILES_ACTIVE` ∋ `kubernetes` (single-run trigger) | `EXECUTION_MODE=single-run` | 06 §7.8 | yes |
| 4 | yaml `blockrunner:` compat block (uuid, api, auth incl. kebab-case `api-key.client-id`, debugMode, version) | flat persona keys | 06 §5.4/06A §6.4 | yes |
| 5 | yaml `blockrunner.privateKey`/`privateKeyFile` + default path `/app/runner-private.pem` | `RUNNER_PRIVATE_KEY_FILE` / persona key | 06A §6.5 | where divergent (audit) |
| 6 | yaml `logging.*` / `server.*` / `spring.*` blocks | — (ignored) | 06 §5.4 | ignored-with-warning |
| 7 | yaml `api.user` (tf) vs `api.username` (controller) | both accepted | 03 §5.3 | **no warning — deliberate**: neither spelling was ever renamed; D7's warning duty applies to renames only. Documented as dual-spelling, not deprecation |
| 8 | env `VERSION` runtime override of the baked build version | ldflags `build.Version` | 06A flag 6 | **decide here**: keep honoring, add a deprecation log (it exists only to keep a shipped Kotlin name literal); removal on the §7.3 schedule |
| 9 | binary path `/app/tfrunner` + registry alias `tfrunner` | `/app/tf-block-runner` | 04 §4.1/§4.4 | **no log** — it is an operator `command:` contract, not a config knob; documented, never warned (a warning would spam every custom-command deployment) |
| 10 | metric names | — | — | **empty** (A7/L10): `run_controller_*` unchanged, all `runner_*` series are new. No alias duty. Verified by diffing the exposition against plans 04 §4.3/05 §10.3 |

`EXECUTION_MODE=single-run` itself and the k8s env contract are **not** deprecations —
frozen contract (D9). Spring relaxed-binding variants beyond the literal shipped
spellings stay unsupported (umbrella §10.4 boundary, restated in the README table).

### 7.3 Documented timeline (the operator-facing promise)

- **Now (refactor GA):** every §7.2 alias works and logs once at startup.
- **Guarantee window:** aliases are kept for **all releases of the current major
  version, and at minimum 12 months from refactor GA** — whichever is longer.
  Rationale: config keys and env vars are customer-facing API (high-level risk #7);
  the repo releases from `main` with semver tags (README `:124-145`), so the natural
  removal boundary is the next major.
- **Removal:** earliest at the next major release, each removal listed in release
  notes (cliff.toml `feat!:`/`BREAKING CHANGE` conventions produce the right notes),
  and only after one full minor-release cycle in which the warning pointed at the
  concrete removal version.
- **`SPRING_PROFILES_ACTIVE` special case:** additionally gated on "no supported
  rollback path to JVM-generation images remains" — satisfied once the last pre-port
  release leaves the supported window; tracked in the ARCHITECTURE.md follow-up
  register (L37).

The timeline table lands in README §Configuration & deprecations (§6.1) and
ARCHITECTURE.md; the warning text links there.

---

## 8. slog migration (umbrella §10.12 / D15)

Goal: one logging stack. Phase-6 packages already use slog natively; this phase
migrates everything else (§3.4 inventory) and deletes the `slog.NewLogLogger` bridges.

### 8.1 Target shape

- One `slog.Logger` constructed in `main`: `slog.New(slog.NewTextHandler(os.Stderr,
  nil))` — the D15 default, no handler ceremony, no level machinery beyond default
  (nothing in the codebase logs Debug today).
- Persona identity as an attribute: `logger.With("persona", string(persona))` replaces
  the `[TF RUNNER]` / `[RUN CONTROLLER]` prefixes (plan 04 §4.1). **Operator-visible
  log-format change** — sanctioned by the umbrella §8 precedent (phase-6 personas
  already emit slog format; "log format was never a wire contract"), but the
  local-dev-stack readiness markers key on the old lines ⇒ mandatory lock-step SKILL
  edit (L22, §14).
- Per-run identification: `logger.With("run", runId)` replaces the tf handler's
  `[RUN-<id>]` prefix (plan 05 H3/§16.9) — H3's observable ("log lines attributable to
  their run") retargets onto the attribute, exactly as the umbrella already ruled for
  phase-6 handlers (§10 flag 12).
- Signatures: every `*log.Logger` parameter in `config`, `report`, `mgmt`, `dispatch`,
  `k8sjob`, `tf`, `gitsource`, `tofu` becomes `*slog.Logger`. Constructor-injected as
  before (P3); no package-level default loggers.

### 8.2 What is *not* migrated

- The **run-log file sink**: `report.RunLog` is a byte sink for process output
  (tofu/git/step output) whose segments become wire-visible step `SystemMessage`
  contents (§3.4). It is not application logging — it keeps its current writer
  mechanics and byte format untouched.
- Third-party output captured into the run log (tofu CLI output) — obviously.

### 8.3 The SystemMessage hazard (the one real risk)

Engine/step code that today writes *into the per-run log file* via a `*log.Logger`
(the `logwrap`/`RunLog` heirs — e.g. step banners, "Using existing backend." lines,
`HINT_INIT_FAILED`) produces bytes that the phase-1 suite pins inside PATCH bodies.
Rule: **any logger whose output lands in the RunLog keeps producing byte-identical
lines.** Mechanically: the run-scoped file logger stays a `*log.Logger` (or an
equivalently-formatted writer) owned by `report.RunLog`; only *process* logging (poll
loop, config, mgmt, dispatch decisions, errors to stderr) migrates to slog. The
migration step therefore splits each call site by destination before touching it.
Violations surface as characterization failures ⇒ STOP-B.

### 8.4 Declared test retargets (assertions unchanged; beyond this list = STOP-B)

1. Harness logger construction: `log.New(...)` → `slog.New(slog.NewTextHandler(...))`
   / `slogt`-style test loggers — construction only.
2. Tests asserting *presence* of a log line by substring (e.g. loop cadence tests,
   deprecation-warning tests) retarget to the slog rendering of the same message;
   the asserted *message content* (not format) is unchanged.
3. Tests capturing the `[RUN-<id>]` prefix (plan 05 H3 twin) retarget to the
   `run=<id>` attribute.
4. Everything asserting wire bytes (PATCH bodies, SystemMessage segments, transcripts,
   goldens): **zero changes** — guaranteed by §8.3.

### 8.5 Depguard follow-through

Add a depguard deny for `"log"` (the stdlib package) in every `internal/` package
except the `report` run-log file (§8.2 exception, one allow) — the same mechanism that
enforces D11, now enforcing the single logging stack so it cannot regress.

---

## 9. Final architecture record & plan-file disposition

### 9.1 `docs/ARCHITECTURE.md` (L8) — outline

A `docs/` file, not a README section: it is a maintainer/contributor record (~4-6
pages), while the README serves operators. Sections:

1. **One binary, six personas** — argv[0] resolution, `bbrunner <persona>` fallback,
   alias `tfrunner`, persona ↔ image ↔ Identity-name table.
2. **Package map** (D11) — every `runner/internal/*` package with its concept, the
   depguard direction rules (adapters ↛ consumers, only main wires, prometheus
   allowance, the §8.5 `log` deny), and the gate-excluded real-I/O files.
3. **Execution model** — `dispatch.Loop` (capacity, claim, fail-fast taxonomy incl.
   the two `UnhandledTypeError` messages and why they differ, L13),
   `InProcess` vs `k8sjob` dispatchers, `RunHandler` contract, the tf `Engine`,
   single-run mode.
4. **Frozen contracts register** — the living successor of D9/D10: wire shapes (three
   PATCH body dialects and who speaks which), media types, headers/node-ids, k8s Job
   env contract (`EXECUTION_MODE` is deployment config; `SPRING_PROFILES_ACTIVE`
   accepted), image entrypoint paths/symlinks, mux contract, artifact cap. Corrections
   baked in (same-origin check does not exist, 01 F2).
5. **Configuration** — precedence (defaults < YAML < env), per-persona config
   structs, the §7.2 alias table + §7.3 timeline.
6. **Observability** — `MANAGEMENT_PORT` defaults table, the complete metric
   inventory (`run_controller_*` + `runner_*` + the plan-05 counters) with the
   "names are public surface" rule (D12).
7. **Testing & gates** — scenario-first policy (D16), coverage gate mechanics +
   thresholds/exclusions, `-race`, e2e tag, the "no unit tests to move the number"
   rule.
8. **Follow-up register** — the §4 FUTURE rows (L23–L34, L37 removal schedule),
   each with source-plan reference and blast-radius note. This is the "memory" the
   high-level plan asks for: the next engineer finds every consciously-not-done item
   in one place.

### 9.2 Plan-file disposition — **move to `docs/plans/`, do not delete** (recommendation)

- **What:** `git mv PLAN_HIGH_LEVEL.md PLAN_DETAIL_*.md docs/plans/` + a short
  `docs/plans/README.md` ("historical design records of the 2026 single-binary
  refactor; superseded by ../ARCHITECTURE.md; not maintained") + the L40
  `ERRATA.md` (known factual corrections discovered by later plans, so nobody
  re-litigates them from the archived text).
- **Why move, not delete:** (a) the plans are the *decision record* — D13 bug
  inventory, retry-policy rationale, every sanctioned delta — referenced from eight
  merged PR descriptions; deletion makes those links point at nothing but a git hash.
  (b) The repo is public and the plans document *why* customer-visible quirks exist
  (e.g. the two fail-fast messages) — discoverability beats `git log` archaeology.
  (c) Deletion saves nothing: the bytes stay in history anyway.
- **Why not keep at the root:** fourteen plan files at the top level of a public repo
  drown the actual entry points (README, SECURITY) and read as *current* guidance —
  the one thing executed plans must not do (§5 high-level: plans are historical once
  executed).
- **Grep-gate interplay:** all repo-wide sweep gates (L20 and 06D's) already allow
  hits under plan docs; the path moves to `docs/plans/` in those gate definitions.
- **Branch hygiene:** the `refactor/single-go-binary/plan` branch is tagged
  (`plan/single-go-binary-final`) and deleted after this phase merges — the plans'
  canonical home is now `docs/plans/` on `main`.

---

## 10. Migration sequence — always-green steps

Rules as always: after every step `task test` + `task lint` green, `task coverage` ≥
gate; numbers recorded per working commit (squashed on merge). Code first, CI gate
additions after the code they gate is clean, docs last (so they describe reality).

| # | Step | What changes | What proves it |
|---|---|---|---|
| 0 | **Preflight.** Run all §1 verifications on the phase-6d branch; branch `phase-7-cleanup`. Record: coverage numbers, A13 readiness markers, A14 manifest location. | nothing | A1–A14 verified (STOP-A) |
| 1 | **Verification sweep (L4–L6, L19, L20, L36, L38).** Run every VERIFY row; each failure is triaged: trivial residue (gitignore lines, leftover grep hits) fixed in place, anything structural is a STOP. | at most residue deletions | grep gates green; sweep results table in the PR description |
| 2 | **slog: shared packages.** `config`, `report` (process logging only, §8.2/§8.3 split first), `mgmt`, `gitsource`, `tofu` → `*slog.Logger`; §8.4 retargets 1–2. | signatures + call sites | full suite green; wire/golden tests untouched (STOP-B) |
| 3 | **slog: dispatch/k8sjob/tf + personas + main.** Loop/dispatcher/engine process logging; persona attr replaces prefixes; per-run attr replaces `[RUN-<id>]` (§8.4.3); delete the `slog.NewLogLogger` bridges; §8.5 depguard deny. | rest of the tree | suite green incl. every characterization pin (SystemMessage bytes unchanged — §8.3); `grep -rn '"log"$' runner/internal` hits only the §8.2 allow |
| 4 | **Controller decrypt-failure fix (L14) — after STOP-D review.** `k8sjob.Dispatch` decrypt error path gains `reportRunFailure` with the actionable key-mismatch message; metric unchanged; flip the transcript-empty pin to assert register+FAILED-PATCH. | `internal/k8sjob` + its pin | new transcript pin green; all other controller goldens byte-identical |
| 5 | **Single-run unification (L15) — STOP-E guarded.** `persona_tf.go` single-run tail becomes: read file → `ClaimedRun` → tf handler with NoOp decryptor + provided runToken; the parallel DTO→engine glue is deleted. Exit semantics (R12 condition) preserved at the persona level. | `persona_tf.go`, `internal/tf` | single-run scenario suite green with zero assertion changes; exit-code tests green; if not ⇒ STOP-E (revert the step, record) |
| 6 | **e2e split (L3).** Build tag on the real-download/real-git tests; `task test:e2e`; verify `task coverage` totals unchanged. | test files, Taskfile | default `task test` runs offline (spot-check); tagged run passes locally |
| 7 | **CI reshape (L1, L2, §5).** `lint` job; `test` job rename + tag-exclusion note; `images` consolidation; `e2e.yml`; verify `build-images.yml`/`pr-cleanup.yml`/`release*.yml` need nothing. | `.github/workflows/` | draft-PR run: lint+test+6 image legs green; e2e workflow dispatchable; job-name change coordinated with branch protection (flag §15.5) |
| 8 | **File hygiene (L16–L18).** `.editorconfig` JVM sections out; `.agents/skills/backend-go` rewritten; k8s manifests moved to `containers/run-controller/k8s/` (+ commented probe example). | listed files | `task lint`/editors unaffected; skill commands execute; manifests `kubectl apply --dry-run=client` clean |
| 9 | **Deprecation audit (L9, §7).** Wording unification through the shared helper; add missing warnings found; alias tests extended to assert the uniform message + link. | `internal/config`, persona files | alias test matrix green; one-startup-line rule verified in container smoke |
| 10 | **Docs (L7, L8, L12, §6).** README rewrite; `docs/ARCHITECTURE.md`; `runner/README.md` refresh; config-sample flip + comments; §6.8 no-op verifications. | docs + samples | every README command executes; sample config boots the controller persona (config parse test); ARCHITECTURE follow-up register = §4 FUTURE rows exactly |
| 11 | **Plan-file disposition (L40, §9.2).** `git mv` plans → `docs/plans/` + README + ERRATA.md; grep-gate path updates; tag the plan branch. | docs/plans/ | repo root contains no `PLAN_*.md`; gates green |
| 12 | **Cross-repo lock-step + acceptance (L21, L22).** meshfed-release PR: readiness markers/pgrep hints for the slog format (§14); doc-truth verification of `how-to-run…` (expected no edit). Then the full local-dev-stack flow + ≥1 MANUAL and ≥1 TERRAFORM acceptance run + container smoke of all six images (healthz+metrics on default ports; legacy `PORT` override; `tfrunner` alias boot). | meshfed-release docs | evidence in PR description (STOP-F) |
| 13 | **Self-review gate + PR.** P1–P8 walk; ledger cross-check: every §4 DO row maps to a diff hunk, every VERIFY row to a recorded result, every DROP/FUTURE row to an ARCHITECTURE.md line. PR description lists the two behavior deltas (L14, L15) and the log-format delta. | — | reviewer checklist; the ledger *is* the review script |

13 steps + preflight. Riskiest: 3 (§8.3 hazard) and 4/5 (the only behavior-touching
steps — both individually revertible working commits before the squash).

---

## 11. Frozen contracts touched (D9/D10)

**Preserved byte-identically:** every wire shape (claim, register, all three PATCH
dialects, artifact download + 128MiB cap), media types, headers/node-ids, the entire
k8s Job contract (env, mounts, manifests, `BackoffLimit: 1`), `EXECUTION_MODE` and the
`SPRING_PROFILES_ACTIVE` alias, image names/tags/entrypoint paths/symlinks, healthz
body `OK` + all resolved ports (`MANAGEMENT_PORT > PORT > default`), every metric name
and label, all config keys/env vars (nothing added, nothing removed — only warnings
audited), the mux contract, single-run exit semantics (R12 condition — preserved
through step 5), release tag scheme and triggers. Step SystemMessage/UserMessage bytes
(§8.3). The step-5 unification and step-7 CI reshape are proven contract-neutral by
the untouched pin suites and image smoke tests.

**Changed with sanction (each individually flagged in the PR):**
1. **L14 (STOP-D):** the controller persona now *reports* decrypt failures
   (register + FAILED PATCH with actionable message) instead of silently letting the
   run hit the coordinator timeout. New wire behavior on an error path; the frozen
   happy paths are untouched. The coordinator already accepts this shape (it is the
   existing `reportRunFailure` pattern).
2. **Log format** (slog text handler + attrs replacing `[PREFIX]` styles) — operator-
   visible, never a wire contract; readiness markers updated lock-step (§14).
3. **CI job names/structure** (D14 mandate) — pass/fail semantics preserved; the lint
   job is a new required check (flag §15.5).

---

## 12. Test plan

- **The inherited suites are the primary net:** the full characterization +
  transcript + golden corpus (phases 1, 3, 5, 6) runs green after every step with the
  §8.4 construction-only retargets — that is the proof that a "cleanup" phase cleaned
  and did not change.
- **L14:** the flipped decrypt-failure transcript pin (register + FAILED PATCH,
  message asserted byte-exactly); all other k8sjob goldens unchanged.
- **L15:** the existing single-run scenario suite + exit-code tests, zero assertion
  edits (STOP-E otherwise).
- **slog:** §8.4 retargets; the depguard `log` deny proves no regression path; the
  scenario suites prove SystemMessage stability (§8.3).
- **Deprecation audit:** alias test matrix (one case per §7.2 row) asserting behavior
  + uniform warning text; container smoke asserting exactly one `PORT` deprecation
  line in a default `tf-block-runner` container.
- **CI:** draft-PR run of the reshaped workflows (lint/test/6 images green); induced-
  failure exercise for the lint job (introduce a violation on the working branch →
  job fails → revert), mirroring the phase-0 gate-proof pattern; `e2e.yml` dispatched
  once with evidence.
- **Docs:** every README/skill command executed verbatim; sample configs parsed by the
  personas' config tests; k8s manifests dry-run applied.
- **Sweeps as tests:** the L5/L6/L20 greps are executable checks recorded in the PR;
  coverage totals identical before/after the e2e tagging (§5.4).
- **End-to-end (step 12):** local-dev-stack + acceptance (MANUAL + TERRAFORM) + the
  six-image container smoke matrix (healthz, metrics, alias boot paths).

## 13. Rollback story

One squash commit on a stacked branch: `git revert` restores the pre-cleanup CI
(no lint job, network tests inline), the two logging stacks, the old README/plan-file
locations, the `SPRING_PROFILES_ACTIVE` sample, and the two behavior deltas (L14
decrypt-failure silence returns; L15 glue returns). No image name, wire shape, port,
env var, or config key changes in either direction, so published images and deployed
configs are indifferent to the revert; `:main` floats back on the next CI run. The
meshfed-release readiness-marker edit (step 12) must revert in the same motion —
linked PRs, same as plan 04 §8. Release tags are immutable. The plan-branch tag
(§9.2) is unaffected by a revert (tags are not branch state).

Partial-risk note: because steps 4 and 5 are the only behavior carriers, review can
also demand splitting them out — the sequence is ordered so that dropping steps 4/5
(revert of two working commits before squash) leaves a purely mechanical cleanup PR.

## 14. Cross-repo touch points

- **meshfed-release `.agents/skills/local-dev-stack/SKILL.md` — must change (lock-step
  PR, step 12):** the readiness-marker table and pgrep hints for the slog format.
  Post-06A the markers are the Go manual-runner line and the tf `[TF RUNNER]`-prefixed
  polling line (A13 records the exact post-06A strings at step 0); after §8 they
  become the slog text-handler renderings with `persona=…` attrs. Also drop the
  now-dead `BlockRunnerApplication` alternative from the pgrep hint (`SKILL.md:90` on
  `main`) if 06A left it. Start commands (`go run . <persona>`) are unchanged.
- **meshfed-release `docs/docs/guides/platform-ecosystem/how-to-run-building-block-runners.md`
  — verify, expected no edit:** grep on `main` finds no gradle/`SPRING_PROFILES_ACTIVE`/
  `PORT`/healthz references (§3.3); the page describes runners at the image/registry
  level (`:31,44`). Step 12 re-verifies against the page's then-current state; any
  drift found is a minimal truth edit on the same lock-step PR.
- **meshfed-release acceptance tests / mux:** read-only outer net; wire frozen.
- **terraform-provider-meshstack `.agents/skills/scratch-config-testing/SKILL.md` —
  no edit:** behavioral references only (mux `:8300`, `/tmp/tf-runner.log`,
  `SKILL.md:82-94`); the log *path* is a redirect in the sibling skill, not a runner
  artifact, and survives the slog migration untouched. Re-verified at step 12.
- **meshStack/meshfed API:** untouched. L14's new FAILED report uses the existing,
  already-accepted `reportRunFailure` wire shape.

## 15. Flags — findings prior plans did not anticipate

1. **Plan 04 orphaned the k8s example manifests.** Its asset-move table covers
   `runner-config.yml`/`known_hosts` only; `run-controller/k8s/{deployment,rbac}.yaml`
   (deployment pins `ghcr.io/meshcloud/run-controller:main`, `deployment.yaml:21`)
   have no assigned destination in any plan. A14 locates them; L18 homes them.
2. **`.agents/skills/backend-go` is stale since phase 2 and no plan owns it.** It
   hardwires `tf-block-runner/` and `./tfrun/...` (`SKILL.md:3,12`,
   `testing.md:13-26`) — an in-repo agent skill giving wrong instructions for five
   phases. Swept here (L17); worth noting for future refactors: `.agents/` belongs on
   the phase-0 inventory checklist.
3. **`.editorconfig` escaped the 06D JVM-endgame inventory** — its Kotlin/ktlint
   block (lines 6-46) and `[*.gradle]` section outlive the language they configure.
   L16. (06D caught `.claude/settings.json` and `flake.nix` but not this file.)
4. **The `[*.vm]` Apache Velocity section** in `.editorconfig` matches nothing in the
   repo (grep: no `.vm` files) — kept anyway: deleting unrelated legacy config is not
   this refactor's job; noted so its survival is a decision, not an oversight.
5. **CI job renames touch branch protection.** Required-check names
   (`<app> - test`/`- image`) change with the §5 reshape; the merge needs a
   coordinated branch-protection update — an ops step no plan mentions, called out in
   the PR description and step 7.
6. **The controller healthz probe cannot be added to the shipped example manifest
   unconditionally** — users who copied `deployment.yaml` earlier would see changed
   behavior on re-apply; hence the commented-out probe example (§6.4).
7. **The deprecation *mechanism* needed no phase-7 work, only the audit + timeline** —
   the high-level phase-7 line "config deprecation warnings" reads as if the warnings
   land here; phases 3–6 already shipped them alias-by-alias (A6). What was genuinely
   missing until now is the operator-facing removal schedule (§7.3).
8. **The D12 metric-alias inventory is empty by construction** (A7) — every
   observability change in phases 4–6 was additive. Phase 7's D12 duty collapses to
   verification + documentation (L10).
9. **`VERSION` as a runtime override (06A flag 6) is the one alias with no removal
   pressure and no canonical env replacement** — §7.2 row 8 resolves it (keep, warn,
   schedule) rather than leaving it undecided.

## 16. Open questions (self-grilled)

All decision branches were walked and resolved from the codebase and the predecessor
plans; the judgment calls a reviewer may veto are encoded as flags/STOPs, not
questions: the L14 decrypt-failure fix (STOP-D — veto ⇒ FUTURE), the L15 single-run
unification (STOP-E — abandonable mid-step), the L13 keep-two-messages decision, the
L37 keep-all-aliases decision with the §7.3 timeline, the pinned-lint-version choice
(§5.2, vs the provider's `latest`), the plan-file move-not-delete recommendation
(§9.2), and the commented-out probe example (§6.4). *(empty otherwise)*
