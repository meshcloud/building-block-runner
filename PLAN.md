# PLAN — in-repo follow-up work

The forward plan for work that touches **this repository**. Everything here is **OPEN / TODO** by definition —
an item is deleted from this file once it lands (its realized state lives in the commits + `docs/ARCHITECTURE.md`
+ `docs/DEPRECATIONS.md`), so this file only ever describes work not yet done. Companions:

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
- **Keep per-agent overhead low: fold the compile-check INTO the implementer.** Have each implementation agent run
  its `go build` (in every required tag mode) before returning, rather than spawning a separate build-check agent
  per step — a standalone checker re-pays fixed per-agent overhead (system prompt + tool schemas + file re-reads,
  only partly recovered by prompt caching) for a trivial task, and re-reads context the implementer already holds.
  Reserve separate agents for genuine escalation, and the opus consolidator for the final gate. (The first
  convergence workflow used per-step haiku checkers — cheap thanks to caching, but fold-into-impl is the house
  style going forward.)
- **Never touch the git index while a workflow is running.** Its impl agents stage deletions (`git rm`) and edit
  tracked files, so any `git add`/`commit`/rebuild would race the agents and fold half-done state into the wrong
  commit. Fold changes into the two commits only *after* the run lands and its gate is reviewed.
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
   it** as a new item in this file and run it through the normal scope → implement → gate flow, then fix it up. Do
   not blind-port larger work.
3. Only once `main`'s intent is already present in our two commits, rebase onto it and **let our version win every
   conflict** (conflicts are guaranteed — commit 1 rewrites nearly every file):

   ```
   git rebase -X theirs origin/main
   ```

   `-X theirs` is the "keep our version" option *during a rebase*: rebase inverts the sides (it checks out
   `origin/main` and replays our commits, so the commit being applied — our work — is `theirs`). Taking our side is
   safe precisely because step 2 already folded in `main`'s new changes; the conflict is just the refactor
   re-asserting its shape over the old layout. For any conflict `-X theirs` can't auto-resolve, resolve manually in
   favor of the branch (`git checkout --theirs -- <path>`, or re-apply our delete), then `git rebase --continue`.

   **Known edge case — rename detection.** When `main` edited a file the refactor *moved*, git can follow the rename
   and merge `main`'s edit into the moved file *on top of* an intent-port already there (step 2), yielding duplicates
   (e.g. a doubled type declaration → compile error). This is why step 2 folds intent in first and why the gates
   below are mandatory after every rebase: if a rebased file won't build, restore the branch's version of that one
   path (`git checkout <branch> -- <path>`) and re-fold.

   **Known edge case — tracked-but-`.gitignore`d files vanish on a soft/mixed-reset rebuild.** `.gitignore` only
   affects *untracked* files, so a file that is tracked despite matching an ignore rule stays fine — until a
   history rewrite does `git reset --mixed <base>` (which untracks everything) followed by `git add -A` (which
   *skips* now-ignored paths). Those files then silently drop out of the rebuilt commit. This bit the golden
   fixtures `internal/tf/testdata/*.golden.tfvars` (matched by `*.tfvars`, 2026-07-16); fixed by a narrower
   `.gitignore` negation (`!**/testdata/*.golden.tfvars`). Guard for any *other* such file: before committing a
   rebuild, diff the new tree against the pre-rewrite head (`git diff --name-status <backup> HEAD`) and confirm the
   only differences are the changes you intended — an unexpected `D` is almost always an ignored-but-tracked file
   that needs `git add -f`.

After any fold or rebase, re-verify the two-commit invariant and the gates (`task fmt && task lint && task test &&
task coverage`) before force-pushing (`--force-with-lease`).

---

## Restore `internal/tf` coverage to ≥90%

The `BaseConfig` refactor (2026-07-16) converged tf's config loader onto `config.ResolveBase` and
dropped the `user:` / `runnerUuid:` aliases; deleting the alias + legacy tests dipped `internal/tf`
coverage to **89.7%** — 0.3% under the gate, and the only package below threshold (every other gated
package is ≥90%). Backfill focused tests for the tf paths the rewrite left uncovered (the
`ResolveBase`-based `ReadConfig` error branches, `RUNNER_SHUTDOWN_GRACE` parse/warn, and
`applyPrivateKeyFile`) to restore ≥90%. Do NOT lower the threshold in
[`tools/coverage/thresholds.txt`](tools/coverage/thresholds.txt).

## Forward goal (tracked, not yet scheduled): Helm chart(s) under `deploy/`

A chart to deploy the published images (controller + per-type runners), parameterizing image tags,
the type→image map, resource/RBAC, the API credentials/secret, and the management port
(`MANAGEMENT_PORT` — the images no longer bake a port; each uses its compiled default: controller
2112, fit types 810x). The raw `deploy/run-controller/` manifests are the interim example.
