# Cross-repo hand-off notes

Notes accumulated during the single-Go-binary refactor that require action or awareness
in another repo. Not actioned here — tracked for a human to follow up.

## Phase 2b (tf bug-fix pass, PLAN_DETAIL_02 §7)

- **B2 fix — workspace-delete naming (meshfed-release, `local-dev-stack`):**
  `tfrun`'s `selectWorkspace`/`deleteWorkspaceIfNeeded` previously deleted a workspace name that
  never matched an actual tofu workspace on disk (a bug), so DESTROY runs silently left the real
  workspace behind. As of this fix, DESTROY now deletes the real, matched workspace name. This is
  a behavior change for any long-lived local-dev-stack state in meshfed-release: previously
  "destroyed" building blocks may have left orphaned tofu workspaces around, and those will now
  actually be removed on the next DESTROY. Flag this to meshfed-release maintainers; no code
  changes needed there, just an awareness note (and possibly a one-time cleanup of already-orphaned
  workspaces in shared local-dev environments).

- **B5 fix — sensitive non-string-like inputs are now decrypted (customer-visible):**
  `Variable.decryptIfSensitive` previously decrypted only CODE/STRING/FILE sensitive inputs; any
  other sensitive type (BOOLEAN, INTEGER, SINGLE_SELECT, MULTI_SELECT, LIST) silently passed its
  ciphertext through unchanged into the generated tfvars/env. It now decrypts every sensitive
  input regardless of type. Any building block that was (perhaps unknowingly) relying on the old
  passthrough-ciphertext behavior for a sensitive non-string-like input will see a different,
  correct value after this fix. Worth a release-notes call-out for customers.

- **B11 fix — single-run exit code (run-controller / k8s Job semantics):**
  The tf-block-runner's `EXECUTION_MODE=single-run` binary now exits non-zero for failures
  *before* the run's first potentially state-mutating step (workdir setup, run-JSON parse,
  registration) — previously it always exited 0. This is deliberately scoped narrower than "any
  failure exits non-zero" because the controller's k8s Job template uses
  `BackoffLimit: 1` + `RestartPolicy: Never` (`run-controller/controller/kubernetes.go`): a blanket
  non-zero exit on any failure (including a failed tofu apply) would make k8s automatically
  re-run a failed terraform run once — an unwanted second, automatic APPLY/DESTROY. No action
  needed in run-controller today (the Job template's `BackoffLimit`/`RestartPolicy` already match
  the assumption this fix relies on), but flag this coupling if that Job template ever changes.

## Phase 4 (single-binary, module consolidation, `PLAN_DETAIL_04_single_binary.md` §9)

**Status: hand-off note only — nothing actioned in meshfed-release from this repo's
workflow (repo-boundary rule); this section is the exact edit list for that repo's owner.
Land this meshfed-release doc PR in lock-step with (merged together with, not before) this
repo's phase-4 PR — plan-04 step 8 / §8 rollback story treats the two as one revertible
unit.** Line numbers below are as researched by plan 04 §3.7/§9 against
`meshfed-release` at the time that plan was authored; re-verify against the
`meshfed-release` HEAD you're actually editing before applying (they may have drifted).

Why this changes: phase 4 moves the tf runner from its own module/dir
(`tf-block-runner/`, run via `go run .`) into one root Go module with a fit entrypoint
`cmd/tf` (plus an optional `cmd/bbrunner tf` subcommand that forces the same persona
in-process from the controller/superset binary) — see `PLAN_DETAIL_04_single_binary.md`
§4.1/§9. Neither `bbrunner tf` nor `./cmd/tf` contains the substring `tf-block-runner`,
so anything that pattern-matches the process command line needs updating too.

- **File:** `.agents/skills/local-dev-stack/SKILL.md`
  - **Line 78** — before:
    ```
    cd ../building-block-runner/tf-block-runner && : > /tmp/tf-runner.log
    ```
    after:
    ```
    cd ../building-block-runner && : > /tmp/tf-runner.log
    ```
    Why: the tf persona is no longer a standalone module dir; all `go run` invocations now
    happen from the repo root.
  - **Lines 79–82** (the `nohup go run .` block) — before: sets env vars then
    `nohup go run . > /tmp/tf-runner.log 2>&1 &` (implicitly module-rooted at
    `tf-block-runner/`). After: add `RUNNER_CONFIG_FILE=containers/tf-block-runner/runner-config.yml`
    to the env list (the per-persona config file now lives under `containers/<app>/`, not
    the module dir — plan 04 §4.4) and change the command to either
    `nohup go run ./cmd/bbrunner tf > /tmp/tf-runner.log 2>&1 &` (forces the tf persona
    in-process via the superset binary) or equivalently the standalone
    `nohup go run ./cmd/tf > /tmp/tf-runner.log 2>&1 &` (the fit binary directly — prefer
    this form, it's the leaner/more direct equivalent of today's command and needs no
    subcommand token).
  - **Lines 88–91** (pgrep readiness hint) — before:
    ```
    pgrep -f 'multiplexing-block-runner|tf-block-runner|BlockRunnerApplication'
    ```
    after: add `cmd/tf|bbrunner` to the alternation, e.g.
    ```
    pgrep -f 'multiplexing-block-runner|tf-block-runner|cmd/tf|bbrunner|BlockRunnerApplication'
    ```
    Why: the process command line is now `go run ./cmd/tf ...` or
    `go run ./cmd/bbrunner tf ...` (or the compiled binary path `/app/tf-block-runner`),
    none of which contain the literal `tf-block-runner` module-dir name the current
    pattern matches against (`tf-block-runner` is kept in the alternation only for the
    compiled-binary/image case, e.g. `/app/tf-block-runner`).
  - **Lines 92–93** (private-key file-location sentence) — reword to point at
    `containers/tf-block-runner/runner-config.yml` (the per-persona config overlay
    shipped alongside the Dockerfile, deep-merged with the shared base
    `containers/runner-config.yml` per plan 03 §5.3/plan 04 §4.4) instead of the old
    `tf-block-runner/runner-config.yml` module-relative path.
  - **Line 104** (readiness table) — no command/path change; just re-verify after editing
    that the `[TF RUNNER]` log-line prefix still appears (it does — the persona keeps its
    logger prefix regardless of how it's launched, plan 04 §4.1).
  - **Manual-runner block, lines 64–71 (gradle):** leave untouched — that persona doesn't
    move until phase 6.
- **No edit needed** (verified, listed for completeness): `meshfed-release` acceptance
  tests, the `multiplexing-block-runner` mux, and
  `docs/docs/guides/platform-ecosystem/how-to-run-building-block-runners.md` (line 44,
  image name reference only) — wire contract and published image names are frozen by
  this refactor (D8/D10), so none of these need changes for phase 4.
- **terraform-provider-meshstack** — `.agents/skills/scratch-config-testing/SKILL.md:82-95`
  was checked too (behavioral references only: mux `:8300`, `/tmp/tf-runner.log`, no
  path/command dependency) — **no edit needed** there either; not this repo's concern to
  action, noted here only so the meshfed-release owner doesn't have to re-derive it.

## Phase 6a (manual Kotlin→Go port, PLAN_DETAIL_06A §9 step 10 / §12 / §15)

This slice landed the **Go manual persona and every phase-6 template artifact additively**
(new `internal/manual`, `cmd/manual`, the `report.NewReporter` event seam, the `config`
compat helpers, `dispatch.StandaloneClaimClassifier`, the per-persona Dockerfile). It did
**not** delete the Kotlin `manual-block-runner` module, flip its CI legs, or edit
meshfed-release, because those steps hinge on the §11 acceptance gate (local-dev-stack run +
k8s single-run smoke against a live meshStack) that cannot be executed in this environment.
The following remain for the human/PR that flips manual over to the Go image:

- **meshfed-release `.agents/skills/local-dev-stack/SKILL.md` (lock-step, §15):** replace the
  manual-runner block (`./gradlew :manual-block-runner:bootRun`, lines ~64-71) with the Go
  start `nohup go run . manual manual-block-runner`… actually `go run ./cmd/manual` (or
  `go run ./cmd/bbrunner manual`) from the repo root, env
  `RUNNER_API_URL=http://localhost:8301` + `RUNNER_CONFIG_FILE=containers/manual-block-runner/runner-config.yml`;
  update the readiness marker (`Started BlockRunnerApplication` → the Go slog "starting
  manual-block-runner" line) and the pgrep hint (`BlockRunnerApplication` → `manual`). Only
  when the JVM manual image is actually retired.
- **This repo, deferred to the removal PR:** `git rm -r manual-block-runner/`;
  `settings.gradle` drop `include 'manual-block-runner'`; `.github/workflows/ci.yml` +
  `build-images.yml` flip the manual JVM leg to
  `dockerfile: containers/manual-block-runner/Dockerfile` and add a `./cmd/manual` build leg.
  Kept intact here so CI stays green and the sibling ports (6b–6d) can stack on the template.
- **Kotlin pin tests (§3):** not added — gradle is not runnable in this environment and the
  module is not being modified/removed in this commit. The Go scenario suite
  (`internal/manual/*_test.go`) preserves the same observable behavior (M-P1–M-P8 twins) and
  is the surviving pin; the block-runner-core wire pins (C-P1–C-P7) likewise live as Go
  transcript twins in `internal/report`/`internal/meshapi` tests.

## Phase 6b (gitlab Kotlin→Go port, PLAN_DETAIL_06B §9 step 10 / §12 / §15)

This slice landed the **Go gitlab persona additively** (new `internal/gitlab`, `cmd/gitlab`,
`meshapi.Decryptor`/`DecryptInputs`/`gitlab.ExternalCallError` — the umbrella-assigned
shared artifacts this port ships first — the per-persona Dockerfile, and the shared
top-level base `containers/runner-config.yml` this port introduces to carry the well-known
dev private key, umbrella §10.5). It did **not** delete the Kotlin `gitlab-block-runner`
module, flip its CI legs, or touch meshfed-release, for the same reason as 06A: those steps
hinge on the §11 acceptance gate (side-by-side transcript equivalence + a manual smoke
against a real GitLab) that cannot be executed in this environment. Per plan 06B §15,
**gitlab has no mandatory cross-repo edits at all** (unlike 06A) — verified:
`local-dev-stack/SKILL.md` has no gitlab-runner entry, the mux/acceptance suite is
read-only, and `run-controller/runner-config.yml`'s sample stays valid unchanged.

- **This repo, deferred to the removal PR:** `git rm -r gitlab-block-runner/`;
  `settings.gradle` drop `include 'gitlab-block-runner'`; `.github/workflows/ci.yml` +
  `build-images.yml` flip the gitlab JVM leg to
  `dockerfile: containers/gitlab-block-runner/Dockerfile` and add a `./cmd/gitlab` build leg.
  Kept intact here so CI stays green and the sibling ports (6c–6d) can stack on top.
- **Kotlin pin tests (§3, G-P1–G-P13):** not added — gradle is not runnable in this
  environment and the module is not being modified/removed in this commit. The Go scenario
  suite (`internal/gitlab/*_test.go`) preserves the same observable behavior (the trigger
  payload field set, the four error-classification rows, the always-async handover, the
  secret-hygiene asymmetry/leak test, the k8s single-run wire) and is the surviving pin.
  **Flagged for human follow-up:** unlike 06A, this gap was not even partially offset by an
  attempt to run `./gradlew`; a reviewer who can run Gradle should add the G-pins to
  `gitlab-block-runner`'s Kotlin test sources before this port's Kotlin module is deleted, so
  the pinning-then-porting sequence (D6) is actually satisfied rather than skipped outright.
- **Shared base private-key formatting (quirk, not a behavior change):** the Kotlin
  classpath yaml bakes the dev private key as ONE unbroken base64 line (no PEM newlines);
  Go's stdlib `encoding/pem` (unlike Kotlin's hand-rolled loader) refuses to parse that —
  `containers/runner-config.yml` re-wraps the identical DER bytes into standard multi-line
  PEM (verified byte-for-byte in `internal/config/basekey_test.go`). Azdevops/github (06C/06D)
  reusing this same shared base file inherit the fix for free; noted here in case a reviewer
  wonders why the checked-in key's textual form differs from the Kotlin source.
