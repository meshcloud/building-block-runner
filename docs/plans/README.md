# Design Plans — Single-Go-Binary Refactor (2026)

This directory contains the historical design records of the 2026 single-binary refactor.
They document the decision rationale, phase sequence, and key constraints that shaped the architecture.

**These plans are superseded by [`../ARCHITECTURE.md`](../ARCHITECTURE.md)** — the maintained record of the final state.

## Files

- **`PLAN_HIGH_LEVEL.md`** — the refactor's goal, phases, prime directives (P1–P8), and design decisions (D1–D16)
- **`PLAN_IMPL_GUIDE.md`** — brief navigation for reviewers of ported-runner PRs (phase 6)
- **`PLAN_DETAIL_*.md`** — detailed phase specifications (phases 0–7), each with assumptions, scope, step-by-step migration, and test plans
- **`COVERAGE_BASELINE.md`**, **`D9_UNTESTED_BEHAVIOR_INVENTORY.md`**, **`PHASE0_LOCAL_DEV_STACK_VERIFICATION.md`** — supporting refactor records (coverage baseline, the D9 untested-behavior inventory, and the phase-0 local-dev-stack verification), moved here from the repo root in phase 7

## Reference & history

These plans are not maintained as the codebase evolves. Use them for:
- Understanding *why* a quirk or constraint exists (e.g., why two different fail-fast messages, why `BackoffLimit: 1`)
- Tracking deferred items and accepted trade-offs (see `ARCHITECTURE.md` follow-up register)
- Cross-referencing a PR description that quotes a plan section

See also `ERRATA.md` for known factual corrections discovered by later plans.
