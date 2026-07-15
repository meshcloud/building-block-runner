# PLAN — in-repo follow-up work

The forward plan (TODO only) for work that touches **this repository**. Companions:

- [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) — what the code *is* today.
- [`CROSS_REPO_TODO.md`](CROSS_REPO_TODO.md) — work that requires *changes* in another repo (meshfed-release
  local-dev-stack, customer release notes, the shared-SDK extraction).
- [`docs/DEPRECATIONS.md`](docs/DEPRECATIONS.md) — behavior/breaking changes made while burning this plan down.

## How these are executed (workflow + sentinel)

- Implemented by a Claude **workflow**: cheap, low-context models (sonnet/haiku) do the mechanical implementation;
  an **opus consolidator** reconciles their output, re-pins tests, and runs the gates
  (`task fmt && task lint && task test && task coverage`).
- A **sentinel** (opus/main loop) watches the running agents for stalls — a low-context model that either fails a
  step (reports incomplete) **or throws** (e.g. a StructuredOutput retry-cap on an over-long result). On detection:
  **resume/restart the stalled sub-agent on opus**, or **re-stage** the work into smaller fresh-context steps (proven
  fix: one sub-item per agent, fresh context each). The escalation MUST catch thrown agent errors, not only
  null/incomplete results — a throw that isn't caught crashes the whole run. Do not let a starved agent grind.
- **Live acceptance is driven by hand** — a single local meshStack (RAM-heavy, not fan-out-able), via the
  `local-acceptance` skill.
- **Run sequentially on the branch.** The Workflow `isolation:'worktree'` base is `git merge-base main HEAD`; **PR #65
  is not merging in the near future**, so that base stays stale (missing the branch commits) and isolated-worktree
  fan-out would get broken code. Disjoint packages still allow internal parallelism within one working tree.

---

## Branch & rebase strategy (two-commit PR)

This PR is kept as **exactly two commits, permanently** — never a growing stack:

1. **`refactor: …`** — the entire refactoring plus every *state-describing* doc (`docs/ARCHITECTURE.md`,
   `docs/DEPRECATIONS.md`, and the `AGENTS.md`/`CLAUDE.md` conventions). This commit touches essentially every path.
2. **`docs: …`** — the *forward-looking* docs only: this `PLAN.md` and `CROSS_REPO_TODO.md`.

**Future work is folded into those two commits, not stacked on top.** A change to code or state-docs is fixed up into
commit 1; a change to the plan/TODO docs into commit 2. The branch head must always show just the two commits
(`git log --oneline $(git merge-base main HEAD)..HEAD` → 2 lines):

```
# make the change, then fold it into the right commit and collapse back to two:
git commit --fixup <commit-1-or-2-sha>
git rebase -i --autosquash $(git merge-base main HEAD)
```

**Keeping current with `origin/main`.** `main` moves independently; reconcile its new commits by *intent*, not by
cherry-pick (the refactor moved every path, so a raw cherry-pick won't apply):

1. `git fetch origin && git log --oneline HEAD..origin/main` — read each new commit and understand its purpose.
2. Port that intent into the refactored layout as a **fix-up** into commit 1 (or commit 2 if it is plan/TODO). If
   porting a commit is more than a trivial transcription — a real feature or a cross-cutting change — **stop and plan
   it** as a ticket under "Bigger tickets" and run it through the normal scope → implement → gate flow, then fix it
   up. Do not blind-port larger work.
3. Only once `main`'s intent is already present in our two commits, rebase onto it and **let our version win every
   conflict** (conflicts are guaranteed — commit 1 rewrites nearly every file):

   ```
   git rebase -X theirs origin/main
   ```

   `-X theirs` is the "keep our version" option *during a rebase*: rebase inverts the sides (it checks out
   `origin/main` and replays our commits, so the commit being applied — our work — is `theirs`). Taking our side is
   safe precisely because step 2 already folded in `main`'s new changes; the conflict is just the refactor
   re-asserting its shape over the old layout. For any conflict `-X theirs` can't auto-resolve (rename/delete edge
   cases), resolve manually in favor of the branch (`git checkout --theirs -- <path>`, or re-apply our delete), then
   `git rebase --continue`.

After any fold or rebase, re-verify the two-commit invariant and the gates (`task fmt && task lint && task test &&
task coverage`) before force-pushing (`--force-with-lease`).

---

## Live validation

The happy-path live tf-acc suite (both dispatch paths, incl. sensitive decrypt, and the coordinator merging the
diffed status step-sets **by id**) passes live via the `local-acceptance` skill — proving that half of
`CROSS_REPO_TODO.md` X2. The shutdown paths are now proven too (2026-07-14):

- **Shutdown-abort — DONE (both runtimes).** Superset: SIGTERM mid tf-run → grace expiry → the run reports
  `IN_PROGRESS`→`ABORTED` as a **single** terminal PATCH accepted by the coordinator (200) — the fix that suppresses
  the observer's premature terminal FAILED on shutdown (which live deleted the ephemeral key and 401'd the ABORTED
  override) (`0079820`). run-controller/minikube: killing a runner Job pod reports terminal `ABORTED` and does **not**
  rerun (`BackoffLimit:0`), and killing the controller leaves in-flight Jobs running (`cf10ca3`). Closes the X2
  shutdown gap.
- **F3 — DONE.** Real `docker build` of the single lean `containers/run-controller/Dockerfile` (now `-tags k8s`:
  Job-dispatching controller, no in-process handlers) succeeds and serves `/healthz` → `200 OK` (was: only `go
  build`/`go list -deps` verified). The two `Dockerfile.lean-*` experiments were folded into this one image + the
  simplified single-`k8s`-tag model.

---

## Bigger tickets

Deliberate behavior-change / feature / test-debt items.

> **Done (landed on the branch — see `docs/DEPRECATIONS.md` + the commits):**
>
> - **T2a** (`42d68bc`) grew `internal/meshapi` into the generic `DoRequest[R]`/`DoAuthorizedRequest[R]` facade +
>   process-wide singleton client (one `http.Client.Do` site binary-wide) with the hardcoded tf-provider retry
>   policy; **T1** (`e50032e`) made manual/gitlab/azdevops/github honor the backend `runAborted` flag; **T2b**
>   (`4df8728`) deleted the three hand-rolled CI clients and routed gitlab/github/azdevops through the facade.
>   T2a + the T2b meshapi-singleton change are live-validated via `local-acceptance`. The X6 convergence
>   (`CROSS_REPO_TODO.md`) is now an in-repo fact — X6 is just the extract-to-module step.
> - **T6** (`b3831c5`) lifted decryption to the claim boundary (`internal/rundecrypt` decorator running
>   `meshapi.DecryptRunDetails` once) so the typed handlers are key-oblivious (`Decryptor` removed from
>   tf/gitlab/github/azdevops; the key lives only in the run-controller + the cmd bootstrap); a shared
>   `meshapi.SanitizeRunObjectForHandover` strips the `implementation` (secrets) from gitlab's `MESHSTACK_RUN` and
>   github's `buildingBlockRun` (azdevops forwards no run object — unchanged); `DecryptInputs`/`DecryptInputSpecs`
>   retired. **T4** (`f645b6c`) deleted tf's forked `SingleRunWorker`, routing single-run through the shared
>   `Handler.Execute` (+ `cmd/tf/singlerunmeter.go` for the single-run pushgateway metrics). **T5** (`78ea8d5`)
>   extracted `internal/runmode` so all five `cmd/*/main.go` share one signal-derived shutdown ctx + mode branch +
>   drain.
> - **Config consolidation + T3** (`cff4e00`) made `internal/config` the single owner of YAML loading and all config
>   data types: the k8s-job config types moved in (`config.K8sJobConfig`; `k8sjob` now depends on `config`, which
>   stays client-go-free — no import cycle), the run-controller config became `config.ControllerConfig` /
>   `LoadController`, and the controller's ~257 lines of hand-rolled api/yaml/env duplication collapsed to a 21-line
>   `os.Exit` wrapper (reusing `config.Api` + `config.Loader` + `EnvBinding`). tf was converged onto `config.Loader`
>   too, so `gopkg.in/yaml` is now imported by exactly one file (`internal/config/merge.go`). This **closes T3**: the
>   testable config logic is now gated in `internal/config` (97.2%), and `cmd/bbrunner` is left as package-main
>   wiring (`main` / `run*Polling` / `runController*` / superset serve) — intentionally ungated like every other
>   `cmd/*`, documented in `tools/coverage/thresholds.txt`. The controller now shares the loader's `${VAR}`
>   interpolation + `blockrunner:` compat (`docs/DEPRECATIONS.md`).
> - **Dockerfile consolidation** — the four HTTP-only runner images (`manual`/`github`/`gitlab`/`azure-devops`),
>   whose Dockerfiles differed only in the compiled package + baked per-impl config, collapsed into one
>   parameterized `containers/http-runner/Dockerfile` driven by `CMD_PKG` + `CONFIG_DIR` build-args (CI matrices
>   in both `.github/workflows/{ci,build-images}.yml` pass them per leg). The in-image binary path is now the
>   uniform `/app/runner` (only tf documents an `/app/<name>` legacy `command:` override; the customer-referenced
>   *image names* are unchanged); the shared base `containers/runner-config.yml` is copied into all four but read
>   only by gitlab (the other three load an empty base path — inert). tf + run-controller keep their own
>   Dockerfiles (genuinely different runtime surface: nix/git/tofu, resp. `-tags k8s`). Verified with a real
>   `docker build` + boot of both a base-consuming (gitlab: resolves the inline key from the base layer) and a
>   base-ignoring (manual) runner type — `/healthz` → `OK`, behavior identical.
> - All gate-green in both build modes (coverage ≥90% per gated package — `internal/config` 97.2%, `internal/runmode`
>   100%, `secret` 100%, `internal/k8sjob` 98.3%, `internal/tf` 90.2%); security-reviewed and pushed. The now-realized design docs
>   (`PLAN_DETAIL_HTTP_RETRY`, `PLAN_DETAIL_ABORT_FLAG`, `PLAN_DETAIL_DECRYPT_BOUNDARY`,
>   `PLAN_DETAIL_TF_SINGLE_RUN_UNIFICATION`) were pruned — the realized behavior lives in the commits +
>   `docs/DEPRECATIONS.md`.

### T7 — tf runner cleanups — DONE (2026-07-15)

Small, self-contained polish in `internal/tf`; landed via the `t7-tf-cleanups` workflow (sonnet impl +
haiku build-check + opus gate), gate-green (both build modes; `internal/tf` 90.2% ≥ 90%).

- **`internal/tf/worker.go`** — `fileContentOrEmpty` rewritten to open its own read handle and `ReadAt` the
  requested `[startIdx,endIdx)` range instead of reading the whole file and slicing it (which the old
  `io.Seeker`/append-mode note flagged, and which panicked on an out-of-range end index). Out-of-range
  indices now clamp to the bytes actually read; a table-driven test (`worker_filecontent_test.go`) covers
  normal range, past-EOF clamp, equal/inverted indices, and a missing file.
- **`internal/tf/tfcmd_prerunscript_test.go`** — added a shared `requireBash(t)` helper and applied it to the
  16 tests that actually exec `bash` (via `scriptcmd.go`); the two nil/empty-script tests stay unguarded
  (they short-circuit before bash runs). The suite is now green on a bash-less host (skips rather than fails).
- **tf real-binary e2e** — *already done, no work needed*: `.github/workflows/e2e.yml` runs `go test -tags
  e2e ./...` (workflow_dispatch + weekly cron + Slack-on-failure), mirrored by `task test:e2e`; the network
  test (`tfbinaries_test.go`) is `//go:build e2e`. Kept out of the PR gate by design.
- **`internal/tf/authSsh.go` "auto-discover known_hosts"** — *dropped as a deliberate non-goal* (2026-07-15).
  Auto-discovering/scanning a server's host key is trust-on-first-use and would defeat host-key verification;
  resolution stays explicit (configured entry → `SSH_KNOWN_HOSTS` → `~/.ssh/known_hosts`). The musing `TODO`
  in `authSsh.go` was replaced with a comment recording this decision.

### Speculative (no consumer today; revisit when one appears)

_None. Mixed dispatch was dropped (2026-07-15): the `Dispatcher` seam already makes it cheap to add if a real
consumer appears, and building it now would need new per-type config, a third fat image linking both client-go
and every handler toolchain, and a combined capacity model — pure YAGNI infrastructure. The tf cleanups were
promoted to T7 above._

---

## Review before merge

No code change required, but worth a reviewer's eyes:

- **Workflow-authored characterization tests** — parts of the tf/gitlab/github/azdevops/meshapi/runmode suites were
  written by workflow implementation + reconciliation steps (T2/T4/T5/T6); confirm those files pin *current*
  behavior, not idealized behavior.
- **Thin coverage margins** — several packages (incl. `internal/tf` at ~90.2%) clear the 90% gate by a hair; a small
  change can trip it.
- **T6 handover wire change** — gitlab's `MESHSTACK_RUN` no longer carries the `implementation` object (see
  `docs/DEPRECATIONS.md` + `CROSS_REPO_TODO.md` X8); the reference `meshstack-integration` pipeline is unaffected
  (presence-gate only), but custom customer pipelines that parsed it must adapt.
