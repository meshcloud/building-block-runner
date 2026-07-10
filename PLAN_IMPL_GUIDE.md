# Implementation Guide — parallelism, model tiering & consolidation

Companion to `PLAN_HIGH_LEVEL.md` + `PLAN_DETAIL_*.md`. This file is **orchestration
docs only**. It does **not** change any planned gate, STOP marker or exit criterion —
those live in the detail plans and are obeyed verbatim by the workflow below.

## The one concrete artifact

Everything here is executed by a single Claude Code workflow script:

> **`.claude/workflows/implement-refactor.js`**

Run the whole refactor autonomously:

```
Workflow({ name: 'implement-refactor' })      # phases 0→7 in order
```

Or drive a single phase (targeted rerun):

```
Workflow({ name: 'implement-refactor', args: { phase: '3' } })
# phases: 0, 1, 2, 2b, 3, 4, 5, 6, 7
```

Watch it live with `/workflows`. It writes a human-facing **`PLAN_IMPL_RUN_LOG.md`**
recording every gate outcome and surprise.

## The shape each phase runs (encoded in the script)

```
Verify STOP gates  →  Fan-out implement (parallel)  →  Consolidate (opus)
   sonnet               [prereq?] + haiku/sonnet          + writes run-log
```

1. **Verify STOP gates** (sonnet) — runs the plan's *"Assumptions from prior phases"*
   verification steps. In this full-autonomous run a failed assumption is **resolved in
   place** and, only if the agent is genuinely unsure, recorded as an *uncertainty* — the
   run does not halt.
2. **Fan-out implement** — parallel sub-agents, each owning a **disjoint file set** so
   they don't collide. A phase with a shared prerequisite (e.g. phase 1 fixtures, phase 4
   module move, phase 6 template) lands that **first**, then fans out.
3. **Consolidate** (opus, `effort: high`) — the deliberate trade for aggressive
   parallelism: one agent merges the slices, de-dupes helpers, fixes interface drift, runs
   `task test`/`task lint`/coverage, checks **cross-plan consistency**, confirms exit
   criteria, and flags sanctioned deltas + quirks. Only then is the phase a green,
   single-commit, reviewable PR.

## Model tiering (why each tier)

| Tier | Used for | Examples |
|---|---|---|
| **haiku** | mechanical / measurement, no design judgment | coverage baseline, build/identity package, moving plan files |
| **sonnet** | spec-driven bulk implementation from a precise checkpoint | characterization CPs, config loader, report facility, Dockerfiles, CI, docs, gitlab/azdevops ports |
| **opus** | hard reasoning + **every consolidation agent** | DDD migration (ph2), client consolidation (ph3), dispatcher/concurrency (ph5), 06A template + github App-JWT (ph6), slog/SystemMessage (ph7) |

## Parallelism map (per phase)

| Phase | Prereq (serial) | Parallel fan-out | Notes |
|---|---|---|---|
| 0 guardrails | — | coverage-baseline · golangci-v2 · taskfile · ci-coverage · d9-inventory | fully parallel, low complexity |
| 1 characterization | cp1-fixtures (shared) | cp2 · cp3 · cp4 · use-cases · bug-inventory | disjoint `_test.go` per slice |
| 2 DDD refactor | — | ddd-migration (single **opus**) | **low parallelism by design** — ≤15-step always-compiling sequence is serial |
| 2b bug-fix | — | bugfix-pass (sonnet) | sequential per inventory bug |
| 3 shared core | — | meshapi(**opus**) · config · report · meshapitest · registration | independent packages |
| 4 per-persona binaries | module-move (**opus**, fragile git mv) | cmd-tf+bbrunner(**opus**) · mgmt · dockerfiles · ci-release · build-identity · meshfed-handoff | prereq gates the rest; cross-repo edits → `CROSS_REPO_TODO.md` |
| 5 dispatcher | — | dispatcher+inproc(**opus**) · concurrency-tests · capability-config · controller-dissolve | |
| 6 kotlin ports | 6a-manual template (**opus**) | 6b-gitlab · 6c-azdevops · **6d-github(opus)** — **worktree-isolated** | consolidator **stacks B→C→D in order** + runs Template fit-checks |
| 7 cleanup | — | docs · ci-reshape · slog(**opus**) · deprecation-ledger · plan-disposition | |

Phase 6's ports are built in parallel git **worktrees** off the template branch (they'd
otherwise conflict), then rebased into the frozen stacked order **autonomously** by the
opus consolidator — it resolves the shared-wiring rebase conflicts (registration, config,
`cmd/*`) itself, propagates any forced shared-interface change to the 06A/plan-05 artifacts
+ all dependents to keep the tree green, and records that change as an uncertainty rather
than stopping.

## Anticipated agent roster & token budget

A full run (`phase 0→7`) spawns **~66 sub-agents**. Each phase = `verify` (sonnet) +
`git-setup` (haiku) + optional prereq + parallel tasks + `consolidate` (opus); plus one
final `run-log` (opus). Opus is spent only where judgment or reconciliation demands it —
**73% of agents run on haiku/sonnet.**

| Phase | haiku | sonnet | opus | opus is spent on |
|---|--:|--:|--:|---|
| 0 guardrails | 2 | 5 | 1 | consolidate |
| 1 characterization | 1 | 7 | 1 | consolidate |
| 2 DDD refactor | 1 | 1 | 2 | ddd-migration + consolidate |
| 2b bug-fix | 1 | 2 | 1 | consolidate |
| 3 shared core | 1 | 5 | 2 | meshapi-consolidate + consolidate |
| 4 per-persona binaries | 2 | 5 | 3 | module-move + cmd-tf/bbrunner + consolidate |
| 5 dispatcher | 1 | 4 | 2 | dispatcher/in-proc + consolidate |
| 6 kotlin ports | 1 | 3 | 3 | 6a-template + 6d-github + consolidate |
| 7 cleanup | 2 | 4 | 2 | slog/SystemMessage + consolidate |
| run-log | — | — | 1 | run-log |
| **total** | **12** | **36** | **18** | 9 consolidators + run-log + 8 hard implementers |

Where the budget goes efficiently:
- **haiku (12)** — every `git-setup`, plus pure measurement/mechanical slices
  (coverage-baseline, build-identity, plan-disposition).
- **sonnet (36)** — every `verify` gate and all spec-driven implementation (the phase-1
  characterization CPs, phase-3 config/report/meshapitest, Dockerfiles, CI, docs, the
  gitlab/azdevops ports). This is the bulk of the work and none of it needs opus.
- **opus (18)** — the 9 per-phase consolidators + run-log, and only the 8 implementer
  slots with real design risk (DDD migration, client consolidation, the fragile module
  move + superset wiring, dispatcher/concurrency, the 06A template, github App-JWT, slog).

## Autonomy & the gating ledger

The run is **autonomous end-to-end and never halts**. The plan's §5 STOP ("halt for human
replanning") is replaced by *resolve-and-record*:

- Every phase resolves its own assumption failures, conflicts and rebases autonomously.
- It records **only the calls it is genuinely unsure about** as *uncertainties*
  (`blocker` = touched a frozen/customer-facing contract; `judgment-call` otherwise) in
  **`PLAN_IMPL_RUN_LOG.md`**. That file leads with a "Review this first" list so you see
  exactly where to look closer — routine resolutions are a one-line count, not the list.
- **Repo boundary:** agents never edit outside this repo. Required cross-repo edits (e.g.
  meshfed-release local-dev-stack docs) are captured in `CROSS_REPO_TODO.md` for hand-off.

### Git output per phase

Each phase creates its stacked branch off the prior phase's branch, and the **opus
consolidator** commits the green result and opens a **draft PR** (`gh pr create --draft`,
base = prior phase). Phase 6 opens four stacked draft PRs (manual→gitlab→azdevops→github).
Nothing is merged — reviewers review and squash-merge each PR. Commit discipline:
parallel same-tree siblings never commit (they'd race the branch); the consolidator owns
the commit. Only worktree-isolated tasks (phase 6 ports) commit inside their own worktree.
