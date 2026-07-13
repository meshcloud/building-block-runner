# Deprecations: accumulated alias inventory & timeline

This is the one operator-facing place documenting every config surface (env var or yaml
key) that the single-Go-binary refactor kept working for backward compatibility instead of
renaming outright, plus the removal schedule that applies to all of them (D7/D8/D12 of
`docs/plans/PLAN_HIGH_LEVEL.md`: images, config keys and env vars are customer-facing API —
nothing is renamed without an alias + deprecation period). Every deprecation warning emitted
by this codebase links back here.

No alias in this document has been removed, and none will be before the schedule in
[§2](#2-removal-timeline) allows it — this refactor's cleanup phase only **audits and
documents**, it never breaks a working deployment (see the ledger decision "keep every
alias, warn" in `docs/plans/PLAN_DETAIL_07_cleanup.md` §4 row L37).

## 1. Alias inventory

| # | Deprecated / compat surface | Canonical replacement | Applies to | Warned at startup? |
|---|---|---|---|---|
| 1 | env `RUNCONTROLLER_CONFIG_FILE` | `RUNNER_CONFIG_FILE` | `run-controller` (`cmd/bbrunner`) | yes |
| 2 | env `PORT` | `MANAGEMENT_PORT` | every persona (fit binaries + `bbrunner <persona>`); `run-controller`'s default/superset mode never read `PORT`, so it has nothing to alias | yes |
| 3 | env `SPRING_PROFILES_ACTIVE` containing `kubernetes` in its comma-separated list | `EXECUTION_MODE=single-run` | `manual`, `gitlab`, `azdevops`, `github` (the four ported personas; tf's own single-run trigger was always `EXECUTION_MODE` and never spoke Spring profiles) | yes |
| 4 | yaml `blockrunner:` compat block: `uuid`, `version`, `api.url`, `auth.username`, `auth.password`, `auth.api-key.client-id`, `auth.api-key.client-secret` | the equivalent flat Go-native key (e.g. `uuid`, `api.url`, `api.username`, ...) | all four ported personas (shared `config.BlockRunnerCompat.ApplyShared`) | yes, uniformly (see [§3](#3-uniform-wording)) |
| 5 | yaml `blockrunner.privateKey` / `blockrunner.privateKeyFile` | flat `privateKey` / `privateKeyFile`, or `RUNNER_PRIVATE_KEY_FILE` | `gitlab`, `azdevops`, `github` (these three decrypt); `manual` never decrypts, so the key is inert there | yes — warn-and-use for gitlab/azdevops/github; warn-and-**ignore** for manual (see note below) |
| 6 | yaml `blockrunner.debugMode` | flat `debugMode` | `manual` only (the one persona with a debug-mode concept) | yes — warn-and-use for manual; warn-and-**ignore** (not applicable) for gitlab/azdevops/github |
| 7 | env `VERSION` (runtime override of the shipped build version) | the compiled-in build version (`internal/build.Version`, baked via `-ldflags` at image build time) | `manual`, `gitlab`, `azdevops`, `github` (a Kotlin-runner holdover — tf and the controller never had this override) | yes |
| 8 | yaml `api.user` | yaml `api.username` | every persona sharing `config.Api` | **no warning — deliberate.** Neither spelling is a rename of the other; both were shipped literally (tf historically used `user`, the controller `username`). D7's warning duty applies to renames, not to two coexisting original spellings. `Username` wins when both are set. |
| 9 | image path `/app/tfrunner` | `/app/tf-block-runner` | `tf-block-runner` image only | **no log — deliberate.** This is an operator `command:`/k8s-Job-template override contract, not a config value read by the running process; a startup warning would fire in every deployment using the old path for no actionable reason. Documented here instead. |
| 10 | Prometheus metric names | — | `run_controller_*` (13 series: `runs_fetch_errors_total`, `runs_fetch_duration_seconds`, `jobs_created_total`, `job_creation_errors_total`, `job_creation_duration_seconds`, `jobs_at_capacity_skips_total`, `service_accounts_created_total`, `service_account_creation_errors_total`, `decryption_errors_total`, `runner_registration_success_total`, `runner_registration_errors_total`, `loop_iterations_total`, `active_runners`) | **inventory empty — verified, not a gap.** No existing metric name was ever renamed during this refactor; every metric above already existed under this exact name. Metric names are a de-facto public surface (operator dashboards scrape them, D12) — a future rename would need the same alias treatment as env vars, but none has happened. |

Every warning in rows 1–7 fires **once per process start** (loaded during startup config
resolution, not per request/run), so a long-running polling persona logs it once, not once
per poll cycle.

### Note on rows 5/6 (warn-and-ignore vs. warn-and-use)

`config.BlockRunnerCompat` is one shared yaml struct decoded by all four ported personas,
but `privateKey`/`privateKeyFile` only matter to the personas that decrypt (gitlab, azdevops,
github), and `debugMode` only matters to `manual`. Rather than silently dropping a key a
given persona doesn't act on (which would leave an operator who copy-pasted a shared
`runner-config.yml` across personas wondering why a value "didn't take"), every persona logs
a warning either way: "use the value" (with the deprecation wording, §3) when it applies to
that persona, or "ignoring ... ; not applicable to this runner" when it doesn't. This is a
correctness fix made during the phase-7 alias audit, not a design carried over unchanged from
earlier phases — `internal/github`'s port had originally applied `privateKey`/`privateKeyFile`
without logging anything, and `internal/gitlab`/`internal/azdevops` had not implemented the
warn-and-ignore for `debugMode` that `config.BlockRunnerCompat`'s own doc comment already
promised ("manual-only; other personas warn-and-ignore"). Both gaps are closed as part of this
audit; see the "Uncertainties / found-by-audit fixes" section in the accompanying PR
description for the exact diffs.

## 2. Removal timeline

- **Now (this refactor's GA):** every alias in §1 works and logs its warning once at
  startup (rows 1–7); rows 8–10 are deliberately silent/empty for the reasons stated.
- **Guarantee window:** every alias is kept for **all releases of the current major
  version, and at minimum 12 months from this refactor's GA — whichever is longer.**
  Config keys and env vars are customer-facing API for a publicly released set of Docker
  images; the natural removal boundary is the next major release, not an arbitrary date.
- **Removal:** earliest at the next major release, and only after at least one full minor
  release cycle in which the warning already named the concrete target removal version.
  Each removal is called out in that release's notes.
- **`SPRING_PROFILES_ACTIVE` (row 3) special case:** removal is additionally gated on "no
  supported rollback path to the JVM-generation images remains" — i.e. not before the last
  pre-Go-port release has left its own support window. Tying the removal to a rollback
  guarantee, not just a calendar date, keeps a rollback from a Go image back to a JVM image
  symmetric for as long as anyone might reasonably need it.
- **No alias in this document is removed by this cleanup phase.** This document's job is
  the audit + the schedule, not the removal (see `docs/plans/PLAN_DETAIL_07_cleanup.md` §4
  row L37 and its rationale).

## 3. Uniform wording

Every warning in rows 1–7 is emitted through one shared helper
(`internal/config.WarnDeprecated(log, old, replacement)`, used both directly and via the
`Deprecated`/`Canonical` fields on `config.EnvAlias`/`config.EnvBinding`) so the phrasing
never drifts between call sites:

```
deprecated: <old> is deprecated, use <replacement> instead -- see docs/DEPRECATIONS.md
```

Before this audit, four call sites (`config.Path`, `config.ManagementPort`,
`config.BlockRunnerCompat.ApplyShared`, `config.SingleRunMode`) each logged their own
independently worded message. `cmd/bbrunner`'s controller-config-file resolution
(row 1) predates the shared `config.Loader` mechanism entirely and still uses the stdlib
`*log.Logger` the rest of that file uses; it reproduces the same wording by hand rather than
being rewired onto `config.Loader.Path` (out of scope for this audit — see
`cmd/bbrunner/controller_config.go`'s `resolveControllerConfigFile` doc comment).

## 4. Legacy Spring/JVM yaml blocks (`logging.*` / `server.*` / `spring.*`): warn-and-ignore

A customer-mounted, Kotlin-era `runner-config.yml` can still carry top-level `logging:`,
`server:` or `spring:` blocks — Spring Boot's own logging/embedded-server settings and the
`spring.*` property tree. The Go runners consume none of them. When any of these top-level
blocks appears in a config file loaded through the shared `internal/config.Loader` (the four
ported personas — `manual`, `gitlab`, `azdevops`, `github`), the loader now logs one
**warn-and-ignore** line per block, pointing back here:

```
ignoring unsupported legacy config block '<block>:'; it configured only the Spring/JVM runner
generation and has no effect on this Go runner -- see docs/DEPRECATIONS.md
```

Implementation: `config.Loader.Load` records which of `{logging, server, spring}` appear as
top-level keys in the merged config document (`recordIgnoredLegacyBlocks`), and each persona
calls `config.Loader.WarnIgnoredLegacyYAMLBlocks(log)` once at startup, right after `Load`.

This is deliberately a **warning, not a failure** — it is the mirror image of the
`FailOnUnconsumedLegacyEnv("BLOCKRUNNER_")` guard: a stray legacy *env var* must halt startup
(it could silently boot the runner on wrong defaults, P5), whereas a leftover Spring *yaml
block* is inert and only warned. The warning fires once per process start, like every other
warning in this document.

**Scope / remaining honest gap.** The check targets exactly these three well-known Spring
block names, not "any unrecognized key". `yaml.Unmarshal` still silently drops other stray
top-level keys, and the tf runner and the run-controller (which decode with their own
`os.ReadFile`+`yaml.Unmarshal` loaders, not the shared `config.Loader`) are out of scope —
their config is Go-native and never spoke Spring. A general strict/known-fields decode across
every persona struct remains a larger future change (it would have to reconcile with how
`FailOnUnconsumedLegacyEnv` already defines "recognized" for env vars); this targeted check
closes the concrete plan promise (`docs/plans/PLAN_DETAIL_07_cleanup.md` §7.2 row 6) without
that structural churn.
