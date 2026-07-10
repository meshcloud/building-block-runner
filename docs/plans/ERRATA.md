# Errata — Known Factual Corrections

This file documents known factual corrections discovered during the implementation of the phase-7 refactor.
These corrections do not invalidate the plans' designs; rather, they clarify the historical record so the archived text remains an honest reference.

## Test coverage counts

**Plan:** PLAN_DETAIL_06A_manual.md §16.7, PLAN_DETAIL_06D_github.md §16.1

**Correction:** The test counts in the manual and github runner sub-plans' acceptance criteria or coverage assertions may differ from the final implementation due to (a) merging of related scenario tests into shared subtests, or (b) discovery of untested code paths during implementation.

**Impact:** The specific numbers are illustrative; the coverage gate and gated-package list (ARCHITECTURE.md) are the source of truth.

---

## Azure DevOps sanitizer erratum

**Plan:** PLAN_DETAIL_06_kotlin_ports_umbrella.md §10.12 & PLAN_DETAIL_06C_azdevops.md §16.7

**Correction:** The Azure DevOps runner's stage-name sanitization rule (what characters are replaced) or the rendering of certain edge cases may have been documented with a simplification. The Kotlin-source behavior, pinned by tests in the phase-6C port, is the ground truth.

**Impact:** Behavior is faithfully ported; the docstrings in the Go code are correct.

---

## "Log-only messages" definition

**Plan:** PLAN_DETAIL_06_kotlin_ports_umbrella.md §3.2 & PLAN_DETAIL_06B_gitlab.md §16.1

**Correction:** The categorization of which messages are "log-only" (vs. step-visible messages on the wire) in the context of external pipeline runners may have been stated imprecisely. The Kotlin-source precedent and phase-6B's transcript tests are the definitive reference.

**Impact:** No code change; clarification for future readers.

---

## Same-origin check deletion

**Plan:** PLAN_HIGH_LEVEL.md §3 risk #2, D9 (artifact cap) & PLAN_DETAIL_01_tf_characterization_tests.md F2

**Correction:** The plan cautions "the former same-origin check was deliberately reverted in `88d67d4`." This revision was made before the refactor began, and the safeguard has not been reintroduced in phases 0–7. The artifact-download codebase does not enforce a same-origin restriction.

**Impact:** The current behavior (any URL, subject only to the 128MiB size cap) is correct and intentional, not an overlooked regression.

---
