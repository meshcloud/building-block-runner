# Cross-repo hand-off notes

Notes accumulated during the single-Go-binary refactor that require action or awareness
in another repo. Not actioned here — tracked for a human to follow up.

> **STATUS UPDATE (2026-07-13, verified against code).** The *in-repo* "deferred to the removal PR" items in the
> module-consolidation and Kotlin→Go port sections below (delete the Kotlin `*-block-runner/` modules, drop
> `settings.gradle`, flip the CI legs to the Go `containers/*/Dockerfile`) are **already DONE** — the JVM/Gradle tree
> was removed in the "JVM endgame" commit and CI is Go-only. What remains here is genuinely **cross-repo** (meshfed-release
> `local-dev-stack` edits, the mux retirement, per-type acceptance tests) plus the customer release-note call-outs.
> The in-repo forward plan lives in the repo-root [`PLAN.md`](PLAN.md); this file holds the cross-repo work.
> (The former `FOLLOW_UP.md` was split into those two documents and removed.)

## Forward cross-repo work (canonical list)

The items below are **not achievable in this repo alone** — each needs another repo, a live meshStack/cluster, or a
customer-facing release. The in-repo counterparts are tracked in [`PLAN.md`](PLAN.md); land each pair in lock-step
(the rollback story treats a repo pair as one revertible unit). Detail for the older per-slice notes follows further
down.

- **X1 — Live local-dev-stack acceptance (required before merge/GA).** _Largely satisfied 2026-07-14 (local, via
  the `local-acceptance` skill):_ against a real local meshStack, the `TestAccBuildingBlockV2` suite passes for
  both the **in-process superset** (`RUNNER_DISPATCHER=inprocess` ALL runner replacing the mux) **and** the
  **minikube `run-controller`** dispatching real single-run Kubernetes Jobs — MANUAL (`01`/`02`) and TERRAFORM
  (`03`/`04`, incl. real STRING/CODE sensitive-input decryption) to terminal `SUCCEEDED`. **Still open:** gitlab and
  azdevops have no live/smoke coverage (X7); a CI-hosted (not just local-dev) pass. This de-risks the corresponding
  in-repo work in PLAN.md.
- **X2 — Confirm the decrypt-failure `FAILED` wire change is coordinator-safe.** The controller now actively PATCHes
  a terminal `FAILED` (with key-mismatch guidance) on a decrypt failure instead of waiting out the coordinator
  timeout (`internal/k8sjob/kubernetes.go` → `internal/dispatch/loop.go`; happy paths byte-identical,
  `run_controller_decryption_errors_total` preserved). Confirm meshStack's coordinator accepts an *active* `FAILED`
  for a decrypt failure; if not, revert this one isolated, cleanly-revertible change. **Same open question** applies
  to PLAN.md's diffed-step status PATCH work — verify both in the same live pass.
- **X3 — local-dev-stack start commands (meshfed-release).** Point `local-dev-stack` at the Go entrypoints
  (`go run ./cmd/tf`, and per type as each JVM image is retired), add `RUNNER_CONFIG_FILE`, and update the pgrep hint
  and readiness markers to key on the `slog` attribute (e.g. `runnerType=tf-block-runner` / the stable "Running in
  polling mode" message) instead of the old bracketed `[TF RUNNER]` prefixes. Exact edit list in the
  module-consolidation section below.
- **X4 — Retire the `multiplexing-block-runner` (meshfed-release).** Unblocked in-repo: the out-of-cluster all-types
  superset lets the `run-controller` image replace the mux (`RUNNER_DISPATCHER=inprocess`). Swap the mux compose
  service for it, in lock-step with X1. Caveats to verify live: the superset reports every run under the controller's
  single ALL uuid (+ the run's runToken), so meshStack must accept status from the ALL runner for all types; tf runs
  use the shipped tf defaults until the tf-config threading follow-up lands. Detail in the remediation section below.
- **X5 — Customer release notes** for the sanctioned behavior changes (all tracked in
  [`docs/DEPRECATIONS.md`](docs/DEPRECATIONS.md) § Behavior changes): DESTROY now deletes the real matched tofu
  workspace; single-run exits non-zero for pre-apply failures; **a sensitive input of a non-STRING/CODE/FILE type now
  fails the run** (previously warn-and-passthrough); github renders string-map inputs as inline JSON. Note the
  earlier "every sensitive type is now decrypted" phrasing is **superseded** — the policy is STRING/CODE/FILE only,
  with a hard guard.
- **X6 — `meshstack-go-sdk` extraction.** `internal/meshapi` and `terraform-provider-meshstack/client` are separate
  packages in separate repos; extracting one shared module is inherently multi-repo (not repo-alone). `internal/meshapi`
  already adopted the provider client's retry/backoff design so an eventual merge stays cheap. **T2a (PLAN.md) does this
  convergence in-repo** — growing meshapi into the same generic `DoRequest[R]`/`Authorization`/`RequestOption` shape as
  `terraform-provider-meshstack/client/internal` (matching its `client.New` retry values) — so X6 collapses to a
  mechanical extract-the-now-identical-facade-into-a-shared-module step.
- **X7 — Per-type acceptance tests for gitlab and azure-devops** in `meshstack-smoke-tests` — these two ports have no
  smoke coverage there (github/tf/manual do); commissioning them is out of this repo's scope.
- **X8 — gitlab `MESHSTACK_RUN` wire change: customer release-note call-out.** `MESHSTACK_RUN` no longer carries the
  `implementation` object (stripped to just `{type}` by `meshapi.SanitizeRunObjectForHandover`, in every mode
  including k8s single-run; see `docs/DEPRECATIONS.md`). Any custom customer gitlab pipeline that parses
  `implementation`/`pipelineTriggerToken` out of `MESHSTACK_RUN` must adapt to read it elsewhere (or stop relying on
  it). The reference integration `gitlab.com/meshcloud/meshstack-integration` is unaffected — it only uses
  `MESHSTACK_RUN` as a presence gate, never parsing its contents. Needs a customer-facing release note alongside X5.

## tf bug-fix pass

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

## Single-binary, module consolidation

**Status: hand-off note only — nothing actioned in meshfed-release from this repo's
workflow (repo-boundary rule); this section is the exact edit list for that repo's owner.
Land this meshfed-release doc PR in lock-step with (merged together with, not before) this
repo's module-consolidation PR — the rollback story treats the two as one revertible
unit.** Line numbers below were researched against
`meshfed-release` at the time; re-verify against the
`meshfed-release` HEAD you're actually editing before applying (they may have drifted).

Why this changes: the module consolidation moves the tf runner from its own module/dir
(`tf-block-runner/`, run via `go run .`) into one root Go module with a fit entrypoint
`cmd/tf` (plus an optional `cmd/bbrunner tf` subcommand that forces the same runner type
in-process from the controller/superset binary). Neither `bbrunner tf` nor `./cmd/tf`
contains the substring `tf-block-runner`, so anything that pattern-matches the process
command line needs updating too.

- **File:** `.agents/skills/local-dev-stack/SKILL.md`
  - **Line 78** — before:
    ```
    cd ../building-block-runner/tf-block-runner && : > /tmp/tf-runner.log
    ```
    after:
    ```
    cd ../building-block-runner && : > /tmp/tf-runner.log
    ```
    Why: the tf runner type is no longer a standalone module dir; all `go run` invocations now
    happen from the repo root.
  - **Lines 79–82** (the `nohup go run .` block) — before: sets env vars then
    `nohup go run . > /tmp/tf-runner.log 2>&1 &` (implicitly module-rooted at
    `tf-block-runner/`). After: add `RUNNER_CONFIG_FILE=containers/tf-block-runner/runner-config.yml`
    to the env list (the per-runner-type config file now lives under `containers/<app>/`, not
    the module dir) and change the command to either
    `nohup go run ./cmd/bbrunner tf > /tmp/tf-runner.log 2>&1 &` (forces the tf runner type
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
    `containers/tf-block-runner/runner-config.yml` (the per-runner-type config overlay
    shipped alongside the Dockerfile, deep-merged with the shared base
    `containers/runner-config.yml`) instead of the old
    `tf-block-runner/runner-config.yml` module-relative path.
  - **Line 104** (readiness table) — **CHANGED by the slog migration.**
    The tf runner type no longer prints the `[TF RUNNER]` `log.Logger`
    prefix; it now emits `log/slog` text-handler lines on stdout carrying a
    `runnerType=tf-block-runner` attribute (e.g.
    `time=... level=INFO msg="Running in polling mode" runnerType=tf-block-runner`). The
    readiness marker must key on `runnerType=tf-block-runner` (or the stable message
    `msg="Running in polling mode"`) instead of the old `[TF RUNNER]` prefix. Log format
    was never a wire contract, but the local-dev-stack readiness detection
    is, hence this mandatory lock-step edit. Same applies to any per-run/worker line: the
    former `[WORKER-nnn]`/`[behavior] [runId]` prefixes are now `worker=`, `behavior=`,
    `run=` attributes.
  - **Manual-runner block, lines 64–71 (gradle):** leave untouched — that runner type doesn't
    move until the manual port.
- **No edit needed** (verified, listed for completeness): `meshfed-release` acceptance
  tests, the `multiplexing-block-runner` mux, and
  `docs/docs/guides/platform-ecosystem/how-to-run-building-block-runners.md` (line 44,
  image name reference only) — wire contract and published image names are frozen by
  this refactor, so none of these need changes for the module consolidation.
- **terraform-provider-meshstack** — `.agents/skills/scratch-config-testing/SKILL.md:82-95`
  was checked too (behavioral references only: mux `:8300`, `/tmp/tf-runner.log`, no
  path/command dependency) — **no edit needed** there either; not this repo's concern to
  action, noted here only so the meshfed-release owner doesn't have to re-derive it.

## Manual Kotlin→Go port

This slice landed the **Go manual runner type and every shared port-template artifact additively**
(new `internal/manual`, `cmd/manual`, the `report.NewReporter` event seam, the `config`
compat helpers, `dispatch.StandaloneClaimClassifier`, the per-runner-type Dockerfile). It did
**not** delete the Kotlin `manual-block-runner` module, flip its CI legs, or edit
meshfed-release, because those steps hinge on the acceptance gate (local-dev-stack run +
k8s single-run smoke against a live meshStack) that cannot be executed in this environment.
The following remain for the human/PR that flips manual over to the Go image:

- **meshfed-release `.agents/skills/local-dev-stack/SKILL.md` (lock-step):** replace the
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
  Kept intact here so CI stays green and the sibling ports (gitlab/azure-devops/github) can stack on the template.
- **Kotlin pin tests:** not added — gradle is not runnable in this environment and the
  module is not being modified/removed in this commit. The Go scenario suite
  (`internal/manual/*_test.go`) preserves the same observable behavior (M-P1–M-P8 twins) and
  is the surviving pin; the block-runner-core wire pins (C-P1–C-P7) likewise live as Go
  transcript twins in `internal/report`/`internal/meshapi` tests.

## GitLab Kotlin→Go port

This slice landed the **Go gitlab runner type additively** (new `internal/gitlab`, `cmd/gitlab`,
`meshapi.Decryptor`/`DecryptInputs`/`gitlab.ExternalCallError` — the
shared artifacts this port ships first — the per-runner-type Dockerfile, and the shared
top-level base `containers/runner-config.yml` this port introduces to carry the well-known
dev private key). It did **not** delete the Kotlin `gitlab-block-runner`
module, flip its CI legs, or touch meshfed-release, for the same reason as the manual port: those steps
hinge on the acceptance gate (side-by-side transcript equivalence + a manual smoke
against a real GitLab) that cannot be executed in this environment.
**gitlab has no mandatory cross-repo edits at all** (unlike the manual port) — verified:
`local-dev-stack/SKILL.md` has no gitlab-runner entry, the mux/acceptance suite is
read-only, and `run-controller/runner-config.yml`'s sample stays valid unchanged.

- **This repo, deferred to the removal PR:** `git rm -r gitlab-block-runner/`;
  `settings.gradle` drop `include 'gitlab-block-runner'`; `.github/workflows/ci.yml` +
  `build-images.yml` flip the gitlab JVM leg to
  `dockerfile: containers/gitlab-block-runner/Dockerfile` and add a `./cmd/gitlab` build leg.
  Kept intact here so CI stays green and the sibling ports (azure-devops/github) can stack on top.
- **Kotlin pin tests (G-P1–G-P13):** not added — gradle is not runnable in this
  environment and the module is not being modified/removed in this commit. The Go scenario
  suite (`internal/gitlab/*_test.go`) preserves the same observable behavior (the trigger
  payload field set, the four error-classification rows, the always-async handover, the
  secret-hygiene asymmetry/leak test, the k8s single-run wire) and is the surviving pin.
  **Flagged for human follow-up:** unlike the manual port, this gap was not even partially offset by an
  attempt to run `./gradlew`; a reviewer who can run Gradle should add the G-pins to
  `gitlab-block-runner`'s Kotlin test sources before this port's Kotlin module is deleted, so
  the pinning-then-porting sequence is actually satisfied rather than skipped outright.
- **Shared base private-key formatting (quirk, not a behavior change):** the Kotlin
  classpath yaml bakes the dev private key as ONE unbroken base64 line (no PEM newlines);
  Go's stdlib `encoding/pem` (unlike Kotlin's hand-rolled loader) refuses to parse that —
  `containers/runner-config.yml` re-wraps the identical DER bytes into standard multi-line
  PEM (verified byte-for-byte in `internal/config/basekey_test.go`). Azdevops/github
  reusing this same shared base file inherit the fix for free; noted here in case a reviewer
  wonders why the checked-in key's textual form differs from the Kotlin source.

## Azure DevOps Kotlin→Go port

This slice landed the **Go azure-devops-block-runner runner type additively** (new
`internal/azdevops`, `cmd/azdevops`, `cmd/bbrunner/azdevops.go` registration, the per-runner-type
Dockerfile/runner-config.yml, `internal/azdevops 90` coverage gate, the `azdevops` depguard
group). It did **not** delete the Kotlin `azure-devops-block-runner` module, flip its CI
legs, or edit meshfed-release — same rationale as the manual port: those steps hinge on the
acceptance gate (side-by-side Kotlin/Go transcript equivalence + a manual smoke against a
real Azure DevOps org) that cannot be executed in this environment. The following remain for
the human/PR that flips azure-devops over to the Go image:

- **meshfed-release `.agents/skills/local-dev-stack/SKILL.md`:** verified —
  **no azure-devops runner entry exists today**, so no edit is owed
  here; an optional start snippet (`go run ./cmd/azdevops` or `go run ./cmd/bbrunner
  azdevops`, env `RUNNER_API_URL=http://localhost:8304` +
  `RUNNER_CONFIG_FILE=containers/azure-devops-block-runner/runner-config.yml`) is a
  maintainer choice, not gate-relevant.
- **This repo, deferred to the removal PR:** `git rm -r azure-devops-block-runner/`;
  `settings.gradle` drop `include 'azure-devops-block-runner'`; `.github/workflows/ci.yml` +
  `build-images.yml` flip the azure-devops JVM leg to
  `dockerfile: containers/azure-devops-block-runner/Dockerfile` and add a `./cmd/azdevops`
  build leg. Kept intact here so CI stays green and the sibling ports (gitlab/github) can stack on
  the template.
- **Kotlin pin tests:** not added — gradle is not runnable in this environment and the
  module is not being modified/removed in this commit. The Go scenario suite
  (`internal/azdevops/*_test.go`) reproduces all 28 planned pins (S-P1–6 mapper tables,
  U-P1–8 update shapes, P-P1–5 poll-loop semantics, A-P1–6 trigger/failure-ladder, K-P1–2
  single-run wire+exit, F-P1 decrypt asymmetry) as the surviving pin, driven against a fake
  Azure DevOps `httptest` transport + the shared `meshapitest` mock + an injected `Clock`
  (sleep-free).
- **Shared-package additions this slice made (flagged, STOP-C4):** `meshapi.Decryptor` +
  `meshapi.DecryptInputs` were specified in the manual port as the gitlab port's responsibility, first
  consumer wins. At the time this slice was authored, `phase-6b-gitlab` had not landed them
  (`git diff phase-6a-manual..phase-6b-gitlab` was empty), so azdevops shipped both to the
  manual-port-specified contract instead of forking a local copy (STOP-C4). **If the gitlab port lands its own
  version in parallel, the two need reconciling** — same
  shape expected (mirrors `internal/tf.Decryptor`'s existing interface), but this is flagged
  for the consolidation pass to verify, not assumed safe.
- **`internal/crypto` fix this slice made (flagged):** `readRSAPrivateKey` gained a
  `normalizePEM` fallback for single-line PEM blobs (no newline after `-----BEGIN...-----`) —
  the exact shape every Kotlin runner's classpath `runner-config.yml` ships its baked dev
  `privateKey` in (`PrivateKeyLoader.kt`'s hand-rolled parser tolerates it; Go's stdlib
  `encoding/pem.Decode` does not). Discovered because azure-devops is the **first real
  consumer** of `config.ResolvePrivateKey` + private-key decryption among the Go ports (manual
  never decrypts). Without this fix, a customer's real, currently-deployed Kotlin
  `runner-config.yml` (or this port's own baked dev key) would fail to decrypt on the new Go
  image — a silent config-compat break — so it was fixed here rather than deferred.
  The fix is purely additive tolerance (falls back only when a bare `pem.Decode` fails) and
  is exercised by `internal/crypto/pemnormalize_test.go` plus a container smoke (`docker run`
  the built image, confirm it logs a successful private-key resolution and serves
  `/healthz`). **The gitlab and github ports should re-verify their own baked keys parse now that this fix
  exists** rather than rediscovering the same gap independently.

### Consolidation reconciliation

The STOP-C4 duplication above was reconciled when the azure-devops port was stacked onto the gitlab port:
`meshapi.Decryptor`/`meshapi.NoopDecryptor`/`meshapi.NewCertDecryptor` and the byte-based
`meshapi.DecryptInputs` are kept from the gitlab port (its `CertDecryptor` carries the Kotlin
`decrypt("") == ""` empty-string guard, which is the correct parity for every runner type). The azure-devops port's
typed-DTO input decryptor was kept as well but renamed to `meshapi.DecryptInputSpecs`
(`internal/meshapi/decryptinputspecs.go`) — the two consumers need different shapes: gitlab
forwards the whole raw run document (byte-preserving) while azure-devops already holds
the parsed `[]BuildingBlockInputSpecDTO` and decrypts it directly. Both apply the identical
STRING/CODE/FILE branch rule. The azure-devops port's `internal/crypto` `normalizePEM` single-line-PEM tolerance
and the gitlab port's multi-line re-wrap of the shared dev key coexist (the normalize pass is a no-op on
already-valid multi-line PEM); both survive.

## GitHub Kotlin→Go port

This slice landed the **Go github runner type additively** (new `internal/github`, `cmd/github`,
`cmd/bbrunner/github.go` registration, the per-runner-type Dockerfile/runner-config.yml,
`internal/github 90` coverage gate, the `github` depguard group). It did **not** delete the
Kotlin `github-block-runner`/`block-runner-core` modules, flip its CI legs, or edit
meshfed-release — same rationale as the other ports: those steps hinge on the acceptance
gate (side-by-side transcript equivalence + a real-GitHub smoke) not runnable here.

- **This repo, deferred to the removal PR:** `git rm -r github-block-runner/`;
  `settings.gradle` drop `include 'github-block-runner'`; `.github/workflows/ci.yml` +
  `build-images.yml` flip the github JVM leg to `containers/github-block-runner/Dockerfile`
  and add a `./cmd/github` build leg. Kept intact so CI stays green while the stack lands.
- **Kotlin pin tests:** not added (gradle not runnable here); the Go scenario suite
  (`internal/github/*_test.go`) reproduces the planned pins (G-P1/G-P2 JWT+PEM, the 422
  heuristic + permission-gate tables, Mode A/B input parity incl. the G-P10 secret-leak pin,
  the async/sync/find-timeout/poll-timeout/ctx-cancel handler+poller ladder, G-P11 single-run
  exit) as the surviving pin.
- **Fit-check reconciliation (consolidation, flagged):** the original github-port plan assumed github
  would consume a shared `meshapi.DecryptInputs` and a shared `ExternalCallError` from the gitlab port. In
  the landed stack neither is consumed by github: (a) the gitlab/azure-devops/github ports each keep `ExternalCallError`
  **package-local** (gitlab put it in `internal/gitlab`, not `internal/meshapi`) because each
  runner type classifies its own external-call failures — so there is no shared type to consume,
  and github's `externalCallError` is consistent with that; (b) github's Mode A/B input
  handling decodes into its own `[]runInput` via `decodeAndDecryptInputs`, which neither the
  byte-based `meshapi.DecryptInputs` (gitlab) nor the typed `meshapi.DecryptInputSpecs`
  (azdevops) can express, so it keeps a package-local decode+decrypt path. github likewise
  keeps a package-local `Decryptor`/`NoOpDecryptor`/`NewCertDecryptor` twin — the same choice
  `internal/tf` already makes (documented in `internal/meshapi/decryptor.go`), so this is
  consistent with the established precedent rather than a new divergence. Recorded for a
  reviewer who wants to later promote a single shared decryptor trio once all ports are the
  only consumers. One behavioral nuance: github's package-local `certDecryptor` does NOT carry
  the Kotlin `decrypt("") == ""` empty-string guard that `meshapi.CertDecryptor` has; github
  never decrypts a legitimately-empty ciphertext (appPem/token are always present), so this is
  inert today, but a future convergence onto `meshapi.CertDecryptor` would be strictly safer.

## In-process tf-dispatch remediation (2026-07-10)

- **New optional tf-block-runner env/config (meshfed-release local-dev-stack, awareness):**
  The tf runner type now supports an in-process dispatch mode alongside the legacy Manager loop.
  All additive; the legacy Manager loop remains the DEFAULT, so no meshfed-release change is
  required unless you want to try the new path. New knobs:
  - `RUNNER_DISPATCHER=inprocess` — opt into the dispatch.Loop + InProcess + tf-handler path.
  - `RUNNER_MAX_CONCURRENT_RUNS` (or `maxConcurrentRuns:` in runner-config.yml, default 3) —
    concurrent in-process runs; set to 1 for the historic serial cadence.
  - an optional `registration:` section (displayName / ownedByWorkspace / publicKey /
    capability) enabling a WIF-less startup self-registration PUT. Absent => never
    self-registers, exactly as today.
  No meshfed-release edits are required today. When the in-process tf path is later promoted to
  the default (after live acceptance), revisit the
  local-dev-stack tf-block-runner deployment to pick a `maxConcurrentRuns`.

- **Retire the meshfed-release `multiplexing-block-runner` — now UNBLOCKED in-repo.** The out-of-cluster
  InProcess *superset* is wired in `cmd/bbrunner/superset.go`: `bbrunner` with no subcommand, run outside a
  cluster or with `RUNNER_DISPATCHER=inprocess`, now registers all five runner type handlers (tf + manual + gitlab +
  azdevops + github) into one `dispatch.InProcess` + `dispatch.Loop` serving one `/healthz` + `/metrics` listener,
  claiming under the controller's single ALL identity — the exact fan-out the mux performed (claim upstream as one
  runner, route by type). The in-cluster `run-controller` default is unchanged (still k8s-Job dispatch).
  **Cross-repo action (meshfed-release, when ready):** replace the `multiplexing-block-runner` docker-compose service
  in `local-dev-stack` with the `run-controller` image run out-of-cluster (`RUNNER_DISPATCHER=inprocess`), pointed at
  the coordinator and configured with one runner uuid registered as capability `ALL`; drop the per-type mux port
  fan-out (`:8300`–`:8304`). Do this in lock-step with the live acceptance pass (X1 above) — the superset's
  claim/report wiring is proven by hermetic tests, not yet observed against a live meshStack. Caveats to verify in
  that pass: (a) the superset reports each run under the controller's single uuid + the run's own runToken (not five
  per-type uuids), so meshStack must accept status from the ALL runner for every type; (b) tf runs use the shipped
  tf defaults (`/tmp/runner/{tfbin,wd}`, 60-min timeout) until full per-type tf config is threaded through (tracked in
  [`PLAN.md`](PLAN.md)).
