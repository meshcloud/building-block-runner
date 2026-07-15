# Deprecations & behavior changes

This is the one operator-facing place documenting two kinds of thing:

1. **Compatibility surfaces** ([§ Compatibility surfaces](#compatibility-surfaces)) — every config surface (env var
   or YAML key) this repository keeps working for backward compatibility instead of renaming outright. Images, config
   keys and env vars are customer-facing API — nothing is renamed without keeping the old spelling as a working,
   deprecation-logged alias.
2. **Behavior changes** ([§ Behavior changes](#behavior-changes)) — deliberate changes to observable, customer-facing
   behavior (wire output, exit codes, status wording), including breaking ones. When a change is *worth noting* for an
   operator or downstream consumer, it is recorded there with an impact assessment, whether or not it is expected to
   break anything in practice.

**Policy in one line:** every compatibility alias is supported, warned once at startup, and kept for the current major
version; every noteworthy behavior change is called out here (and, if breaking, in release notes) rather than shipped
silently.

## Compatibility surfaces

Each section describes one surface: what it is, what to use instead, where it applies, and how it warns.

## 1. `RUNCONTROLLER_CONFIG_FILE` → `RUNNER_CONFIG_FILE`

Env var pointing at the controller's config file. Applies to `run-controller` (`cmd/bbrunner`). The canonical
`RUNNER_CONFIG_FILE` takes precedence; the old spelling warns once at startup. The controller's config-file
resolution goes through the shared `config.Loader.Path` (an `EnvAlias` with `Deprecated: true` for the old spelling)
like every other alias, and remains deprecation-logged with the canonical `RUNNER_CONFIG_FILE` — see
`internal/config/controller.go`.

## 2. `PORT` → `MANAGEMENT_PORT`

Env var selecting the management listener port. Applies to every type (fit binaries and `bbrunner <type>`).
`MANAGEMENT_PORT` takes precedence; `PORT` warns once. `run-controller`'s default/superset mode never read `PORT`, so
it has nothing to alias. Every published image but `run-controller` still bakes `PORT=8080` as a container default so
an operator's runtime `PORT` override keeps working unchanged.

## 3. `SPRING_PROFILES_ACTIVE` containing `kubernetes` → `EXECUTION_MODE=single-run`

Trigger for single-run mode (one run from a mounted JSON, then exit). Applies to `manual`, `gitlab`, `azdevops`,
`github`. When `SPRING_PROFILES_ACTIVE`'s comma-separated list contains `kubernetes`, single-run mode activates and
warns once. The tf runner's own single-run trigger was always `EXECUTION_MODE` and never spoke Spring profiles.

## 4. `blockrunner:` YAML block → flat, top-level keys

A compatibility YAML block carrying `uuid`, `version`, `api.url`, `auth.username`, `auth.password`,
`auth.api-key.client-id`, `auth.api-key.client-secret`. Each maps to the equivalent flat, Go-native key (`uuid`,
`api.url`, `api.username`, …). Applies to all four ported types via the shared `config.BlockRunnerCompat.ApplyShared`.
Warns uniformly, once per field applied (see § Uniform wording).

## 5. `blockrunner.privateKey` / `blockrunner.privateKeyFile` → flat `privateKey` / `privateKeyFile`

The RSA private key used for run decryption, or `RUNNER_PRIVATE_KEY_FILE`. Applies to `gitlab`, `azdevops`, `github`
(the three types that decrypt). `manual` never decrypts, so the key is inert there.

`config.BlockRunnerCompat` is one shared struct decoded by all four ported types, but this key only matters to the
decrypting types. To avoid silently dropping a key an operator set (leaving them wondering why a copy-pasted shared
config "didn't take"), every type logs a warning either way: **warn-and-use** for gitlab/azdevops/github, and
**warn-and-ignore** ("not applicable to this runner") for manual.

## 6. `blockrunner.debugMode` → flat `debugMode`

The manual runner's debug-mode toggle. Applies to `manual` only (the one type with a debug-mode concept). Mirror of
§5: **warn-and-use** for manual, **warn-and-ignore** for gitlab/azdevops/github. `config.BlockRunnerCompat`'s own doc
comment promises exactly this ("manual-only; other types warn-and-ignore").

## 7. `VERSION` env var → the compiled-in build version

A runtime override of the shipped build version, a holdover from the JVM-generation runners. Applies to `manual`,
`gitlab`, `azdevops`, `github`. The canonical source is the compiled-in `internal/build.Version` (baked via
`-ldflags` at image build time). tf and the controller never had this override. Warns once at startup.

## 8. `api.user` vs `api.username` — both accepted, no warning

Both spellings are shipped literally: tf historically used `user`, the controller `username`. This is **not** a
rename of one into the other, so there is **no warning** — the deprecation-warning duty applies to renames, not to two
coexisting original spellings. `username` wins when both are set. Applies to every type sharing `config.Api`.

## 9. `/app/tfrunner` image path — no log

The `tf-block-runner` image ships a `/app/tfrunner` copy of its binary as a `command:`/k8s-Job-template override
target, alongside the canonical `/app/tf-block-runner`. This is an operator override contract, not a config value the
process reads, so there is **no startup warning** — one would fire in every deployment using the old path for no
actionable reason. Documented here instead.

The four HTTP-only images (`manual`/`github`/`gitlab`/`azure-devops`) now share one
`containers/http-runner/Dockerfile` and place their binary at the **uniform** in-image path `/app/runner` (it was
previously `/app/<name>-block-runner`). Unlike tf's `/app/tfrunner`, that per-image path was never a documented
`command:` override target — customers reference these by *image name*, and the entrypoint is unchanged — so this is
a no-op for every normal deployment. Noted here for anyone who hard-coded the old in-image binary path in a custom
`command:`.

## 10. Prometheus metric names — no rename has happened

Metric names are a de-facto public surface (operator dashboards scrape them), so a future rename would need the same
alias treatment as an env var. None has happened: every `run_controller_*` and `runner_*` series ships under its
original name. This entry exists so the "have any metrics been renamed?" question has a documented answer (no), not
because there is a metric alias to honor.

## 11. `PUSH_GATEWAY_URL` — new config surface, opt-in, off by default

Not a rename or alias — a brand-new, **opt-in** env var (`internal/observability.EnvPushGatewayURL`), listed here
because every config surface this repository adds gets a DEPRECATIONS.md entry, not because anything is deprecated.
Applies to `manual`, `gitlab`, `azdevops`, `github` (their `RunSingleRun`) and `tf` (`cmd/tf`'s single-run path) —
the five single-run-mode entry points a Kubernetes-dispatched Job runs (docs/ARCHITECTURE.md §6.1). When unset (the
default), behavior is unchanged from before this feature existed: no push-gateway HTTP calls are made. When set to a
Pushgateway base URL, the single run's `runner_*` metrics (success/failure, duration) are pushed to that gateway
under a `run_id` grouping key before the process exits (each series still carries its own `runner_uuid` label, so
the pushed group stays fully attributable to one runner + run without violating the Pushgateway client's rule
against a grouping label repeating a label a pushed metric already has), bounded by a 5s client timeout so a slow
or unreachable gateway never blocks Job completion (`BackoffLimit:1`/`RestartPolicy:Never`). A successful run's
pushed group is deleted from the gateway right after the push (Prometheus need only scrape it once); a failed run's
group is left for an operator to inspect. See `internal/observability/pushgateway.go`.

## Uniform wording

Every warning in §§1–7 is emitted through one shared helper
(`internal/config.WarnDeprecated(log, old, replacement)`, used directly and via the `Deprecated`/`Canonical` fields on
`config.EnvAlias`/`config.EnvBinding`) so the phrasing never drifts between call sites:

```
deprecated: <old> is deprecated, use <replacement> instead -- see docs/DEPRECATIONS.md
```

Each warning fires **once per process start** (during startup config resolution, not per request/run), so a
long-running polling runner logs it once, not once per poll cycle.

## Legacy Spring/JVM YAML blocks (`logging.*` / `server.*` / `spring.*`): warn-and-ignore

A customer-mounted, JVM-era `runner-config.yml` can still carry top-level `logging:`, `server:` or `spring:` blocks —
Spring Boot's own logging/embedded-server settings and the `spring.*` property tree. The Go runners consume none of
them. When any of these top-level blocks appears in a config file loaded through the shared `internal/config.Loader`
(the four ported types), the loader logs one warn-and-ignore line per block:

```
ignoring unsupported legacy config block '<block>:'; it configured only the Spring/JVM runner
generation and has no effect on this Go runner -- see docs/DEPRECATIONS.md
```

Implementation: `config.Loader.Load` records which of `{logging, server, spring}` appear as top-level keys in the
merged document (`recordIgnoredLegacyBlocks`), and each type calls `config.Loader.WarnIgnoredLegacyYAMLBlocks(log)`
once at startup, right after `Load`.

This is a **warning, not a failure** — the mirror image of the `FailOnUnconsumedLegacyEnv("BLOCKRUNNER_")` guard: a
stray legacy *env var* must halt startup (it could silently boot the runner on wrong defaults), whereas a leftover
Spring *YAML block* is inert and only warned.

**Scope.** The check targets exactly these three well-known block names, not "any unrecognized key". `yaml.Unmarshal`
still silently drops other stray top-level keys, and the tf runner and the controller (which decode with their own
`os.ReadFile`+`yaml.Unmarshal`, not the shared `config.Loader`) are out of scope — their config is Go-native and never
spoke Spring. A general strict/known-fields decode across every type struct would be a larger future change; this
targeted check closes the concrete promise without that structural churn.

## Behavior changes

Deliberate changes to observable, customer-facing behavior made in this repository, newest first. Each entry states
the change, its blast radius, and whether it is expected to break anything in practice. A ⚠️ marks a change that
*could* be breaking for some consumer; unmarked entries are corrections with no expected real-world impact.

### ⚠️ tf pre-run script receives the meshfed run object verbatim, not a re-serialized DTO subset

The building-block run JSON handed to a tf pre-run script — on stdin, and in the
`meshstack_building_block_run_b64` variable — is now the decrypted meshfed run object **verbatim** (`cr.RawJson`),
instead of a re-serialization of the runner's typed `RunDetailsDTO`. Previously the runner re-marshaled its own DTO,
which silently dropped every backend field the DTO did not model (for example `spec.trigger`, the run's trigger
reason and author). The script now receives the object as the backend sent it — a superset of the old payload —
matching the documented contract that the script gets the `meshBuildingBlockRun` object. **Blast radius:** a
pre-run script now sees *more* fields (and the exact backend shape/casing) than before; a script that only reads
known keys is unaffected, but one that assumed the old DTO-filtered field set should be reviewed. Decrypted input
values were already present in the old payload, so this is not a new secret-exposure surface. See
`internal/tf/handler.go` (`run.RunJsonBase64 = cr.RawJson`) and `internal/tf/dtos.go`.

### run-controller config now loads through the shared `config.Loader`

The `run-controller`'s config file now loads through the same shared `internal/config.Loader` as the tf runner,
rather than a bespoke read-and-unmarshal path. This is additive, not a rename: it gains two features the loader
already gave the other types — `${VAR}` interpolation inside the YAML, and the Kotlin-compat `blockrunner:` block
(`config.BlockRunnerCompat.ApplyShared` normalizing `uuid`/`api.url`/`auth.*`). tf and the controller now share the
single `config.Loader` load path.

**Impact.** Non-breaking. A controller config that never used `${VAR}` interpolation or a `blockrunner:` block is
unaffected; one that does now has those surfaces work where they previously did not.

### manual/gitlab/azdevops/github now honor the backend's `runAborted` flag, matching tf

`report.Reporter.Report(RunStatus)` has always returned `(abort bool, err error)` — the backend
can flag a run aborted (`runAborted: true`) on the response to any status PATCH — but only the tf
runner ever acted on it (`internal/tf/worker.go`/`singlerunworker.go`: cancel the run's context,
report terminal `ABORTED`). The four ported types discarded the bool outright, so a backend-side
abort request left them running to their own conclusion and reporting `SUCCEEDED`/handed-over
`IN_PROGRESS` regardless.

All four types now act on it: on `abort == true` from a Report response, each stops its own work
and reports terminal `report.ExecutionStatus.ABORTED` instead of whatever it was about to report
— never a stale `IN_PROGRESS`, never `SUCCEEDED`/`FAILED`. Per type:

- **manual** has no poll loop; its one abort window is the echo-run's own status Report, which it
  now overrides with `ABORTED` instead of the otherwise-due terminal update.
- **gitlab** is async-only (handover); its one abort window is the handover Report itself,
  overridden with `ABORTED` instead of leaving the run handed over `IN_PROGRESS`.
- **azdevops** and **github** each have a poll loop, but only in *synchronous* mode
  (`impl.Async == false`) — in async mode they have the same no-op window as gitlab, on their
  trigger-success Report. In sync mode, a `runAborted` on any in-loop status PATCH now breaks the
  poll loop promptly and reports terminal `ABORTED`; this first cut only **stops local polling** —
  it does not yet cancel the remote Azure DevOps pipeline run or GitHub Actions workflow run, a
  provider-specific follow-up left for later.

This is **distinct** from the shutdown-signal `ABORTED` (the runner process itself terminating,
entry below) — a different trigger, reaching the same terminal state.

**Impact.** No new wire field (reuses the existing `runAborted` on `RunUpdateResponseDTO`); happy
path is unchanged (a backend that never sets `runAborted` sees identical behavior). An operator or
backend that previously relied on these four types ignoring `runAborted` (there was no such
consumer — the field existed but was write-only from every type but tf) would now see the run
transition to `ABORTED` instead of running to completion.

### `internal/meshapi` HTTP retry/timeout policy: one process-wide client and retry policy, not two split ones

`internal/meshapi` now routes every stdlib HTTP round-trip through a single generic facade (`DoRequest`/
`DoAuthorizedRequest`, `internal/meshapi/dorequest.go`), backed by one process-wide singleton `*http.Client` wrapped
in `internal/httpclient.RetryTransport` and one global `RetryOptions` (`globalRetryOptions`, `internal/meshapi/retry.go`).
This replaces the former per-client `&http.Client{}` construction and the two split, independently-tuned retry
policies (`RunClient`/`RunnerClient` and `ApiKeyAuth` each had their own, both `MaxRetries: 4`, 1–8s exponential
backoff). Concretely:

- `MaxRetries` 4 → 12, and the exponential backoff cap 8s → 30s (worst case, all retries exhausted, roughly 4.5
  minutes instead of well under a minute).
- The `ApiKeyAuth` login client's request timeout 30s → 5 minutes (`sharedHTTPClient`'s `Timeout`), matching every
  other meshapi call now that they all share one client.

The whitelist of retryable POST paths is the union of the two former policies: `/status/source` (register-source;
a replay lands a 409 that is already treated as success) and `/api/login` (token mint; side-effect-free to repeat).
The claim POST (`.../create`) and the run status PATCH deliberately **stay off** the whitelist and remain
non-retryable — an ambiguous failure there must never risk double-claiming a run or delaying abort detection, and
this change does not alter that.

**Impact.** A transient 429/5xx/transport blip while minting or refreshing a token, or while registering a source,
now rides out via retry instead of failing that poll cycle outright — the same outcome polling already tolerated on
its next cycle, now recovered a cycle sooner and with less log noise. Talking to a hung backend now fails at the
5-minute request bound instead of the old 30s login-specific bound, so a genuinely wedged backend is detected more
slowly on the login path specifically (every other meshapi call was already effectively unbounded by a per-call
timeout and relied on retry/backoff, not a short client timeout, to fail fast). No wire format changes, and no change
to default-level logging: the existing Debug-level request/response body logging at the `DoRequest` facade is
unchanged and still masks the `Authorization` header. Scope: this entry originally covered `internal/meshapi` only;
the CI clients now share the same facade and singleton (below).

**CI clients (`internal/gitlab`, `internal/github`, `internal/azdevops`) join the singleton.** Every request these
three now make goes through the same `DoRequest`/`DoAuthorizedRequest` facade and the one process-wide singleton
`*http.Client`, replacing each client's former bespoke `http.NewRequest`+`Do`+`io.ReadAll`+status-check plumbing and
its own no-follow-redirect client. Concretely:

- **Bound + ctx-cancellable + retried.** Every CI call is now timeout-bounded at 5 minutes and cancelled with its
  run's context, and retries the same transient set as every other meshapi call (429/502/503/504/transport,
  `Retry-After` honored) on idempotent GETs. Of the CI POSTs, only github's installation-token mint
  (`.../access_tokens`) is whitelisted for retry — it idempotently re-mints a token with no other side effect. The
  three trigger POSTs (gitlab `/trigger/pipeline`, github `.../dispatches`, azdevops `.../runs`) deliberately **stay
  off** the whitelist and are never retried: an ambiguous failure there must fail hard rather than risk
  double-triggering a build.
- **Redirects now follow by default.** The three clients previously ran a no-follow client for every call. The
  shared singleton follows redirects by default instead (safe: TLS end to end, and Go's stdlib already strips the
  `Authorization` header on a cross-host redirect), and disables following per-request via `WithNoRedirect` only on
  the three secret-carrying trigger POSTs — so no body-borne trigger secret can follow a redirect to another host.
  Every other CI call (status polls, artifact/log downloads, token mints) now follows redirects it previously would
  have rejected as a non-2xx error.
- **Debug body logging.** The CI clients gain the same request/response Debug-level body logging meshapi's own call
  sites already emit (the `Authorization` header masked; other headers and bodies — including a trigger POST's
  body-borne secret — logged verbatim at Debug). This is an accepted opt-in-operator trade-off, unchanged at every
  other log level and matching the position already taken for meshapi's own Debug body logging above.
- **Behavior preserved.** Domain error classification is unchanged: github's 422 "unsupported inputs", gitlab's
  error-row parsing, and azdevops's 203/HTML sign-in-page quirk (an expired/invalid PAT answering with
  `203 Non-Authoritative` plus an HTML page instead of a clean 401) are ported as-is onto the facade via
  `WithStrictJSONSuccess`, not silently dropped or turned into a JSON parse error.

**Residual gap.** `cmd/github`'s and `cmd/azdevops`'s POLLING wiring (`cmd/github/polling.go`,
`cmd/bbrunner/github.go`, `cmd/azdevops/polling.go`, `cmd/bbrunner/azdevops.go`) still inject their own
non-singleton `*http.Client` (github: a bare no-follow client; azdevops: `azdevops.NewHTTPClient`, a standalone
10s-timeout no-follow client) rather than `meshapi.SharedHTTPClient()` — out of this change's file scope
(`internal/gitlab`/`internal/github`/`internal/azdevops` plus the shared `internal/meshapi`/`internal/httpclient`
support). Single-run wiring and gitlab's polling wiring already default to the singleton. Folding the two remaining
`cmd/` call sites onto the singleton is deferred to a follow-up.

### ⚠️ tf single-run mode: a failed `apply`/`destroy` is now metered and pushed as failed, not succeeded

`cmd/tf`'s single-run (k8s Job) path used `SingleRunWorker.ExecuteRun`'s returned `error` as the
`runner_runs_succeeded_total`/`runner_runs_failed_total` metric signal and as the push-gateway delete-on-success
decision (`observability.PushRunMetrics`, see entry 11 below). `ExecuteRun`'s error is, by design, scoped to
*pre-flight* failures only (workdir setup, run-JSON parse, registration — see its doc comment); once tofu
`init`/`apply`/`destroy` has actually started, `ExecuteRun` always returns `nil`, even when the tofu command itself
fails. A run whose `apply`/`destroy` failed was therefore always recorded as "succeeded": counted on
`runner_runs_succeeded_total`, and (if `PUSH_GATEWAY_URL` is set) its push-gateway group pushed then immediately
deleted, as if nothing needed inspecting.

`SingleRunWorker` now also exposes `TerminalStatus()`, the run's real terminal `report.ExecutionStatus` (read from
the same `report.Progress` snapshot `report.Observer` PATCHes from). `cmd/tf/main.go`'s single-run path uses
`TerminalStatus() == report.SUCCEEDED` — not `ExecuteRun`'s error — as the metric/delete-on-success signal, via the
new `observability.InstrumentSingleRunResult` (an `InstrumentSingleRun` variant that takes an explicit success flag
alongside the error, for exactly this case). The single-run **exit code** contract is unchanged: it still follows
`ExecuteRun`'s pre-flight-only error, so the k8s Job's `BackoffLimit:1`/`RestartPolicy:Never` still never
auto-retries a stateful terraform run that has actually begun (docs/ARCHITECTURE.md's rationale, unchanged).

**Impact.** A failed `apply`/`destroy` in single-run mode now increments `runner_runs_failed_total` instead of
`runner_runs_succeeded_total`, and (only if `PUSH_GATEWAY_URL` is set — off by default, entry 11 below) leaves its
push-gateway group in place for an operator to inspect instead of deleting it right away. No change to the process
exit code, to polling-mode metrics (`internal/tf/worker.go` already read the real terminal status), or to any
consumer that does not scrape `runner_runs_*_total`/the push gateway.

### tf: a run still in flight at shutdown is reported ABORTED after a configurable grace, instead of finishing on its own timeout

Previously, SIGINT/SIGTERM against the tf runner's polling process just `wg.Wait()`ed: an in-flight run kept
executing tofu until it hit its own `TfCommandTimeoutMins` (up to 60 minutes by default) or completed normally, and
only then did the process exit. The tf runner now reuses `dispatch.InProcess.Wait`'s existing drain mechanism: an
in-flight run gets `RUNNER_SHUTDOWN_GRACE` seconds (default 30, matching a typical k8s
`terminationGracePeriodSeconds`) to finish on its own; if it is still running when the grace expires, its tofu
command is cancelled and the run is reported terminal `ABORTED` (`report.ExecutionStatus`) rather than left to
finish on its own timeout or reported `FAILED`.

**Impact.** Expected none for a normal graceful rollout, where runs typically finish well inside the grace window.
For an operator who previously relied on a long-running run surviving a restart because nothing ever cancelled it,
a run in flight at shutdown now becomes terminal (`ABORTED`) after `RUNNER_SHUTDOWN_GRACE`, rather than continuing
to completion across the restart. The `IN_PROGRESS`→`ABORTED` wire transition is the same one `report.Observer`
already supports for the four non-tf runner types; validated by the hand-driven live tf-acc pass (X2), same as the
diffed-step change below.

### tf status PATCH now carries only the changed steps per tick, not the full step set

The tf runner adopted the shared `internal/report` reporting facility (`report.Observer` + `report.Progress`),
retiring its bespoke `RunStatus`/`ExecutionStatus`/`Progress`/`observerRoutine`/`toExternal` status model. The
observable consequence is on the periodic status PATCH the runner sends every 10s while a run executes: it now
transmits only the steps that **changed** since the previous send (the running step whose log grew, plus any step
whose status flipped), rather than re-sending the **full** step snapshot on every tick as the former `observerRoutine`
did. The first tick after registration still carries every step (there is nothing to diff against yet), and the
terminal/final update carries whatever changed since the last successful send.

The on-wire body shape is **unchanged** — it is still the full `RunStatusUpdateDTO` (`blockRunId`/`source`/`type`/
`status`/`summary`/`steps`/`artifact`), produced by `report.ToStatusUpdate`; only the *number of entries in `steps`*
per PATCH shrinks to the delta. tf deliberately keeps this DTO (full step outputs) rather than switching to the lean
`SourceUpdateDTO` the four non-tf runners use.

**Rationale.** The meshStack coordinator's runner-facing status endpoint upserts steps by id, so a partial `steps`
array is merged into the persisted run — sending the full set every tick was redundant bandwidth. Each transmitted
step still carries its FULL current message text (the backend overwrites `userMessage`/`systemMessage` by assignment,
never by appending), so a diffed step is never a partial-message delta.

**Impact.** Expected none for the meshStack coordinator (it already merges step updates by id). Any *third-party*
consumer that scraped the runner status PATCH and assumed every PATCH re-states the complete step list would need to
accumulate step updates across PATCHes instead. This is the same open question tracked for the decrypt-failure
`FAILED` transition in `CROSS_REPO_TODO.md`; validated by the hand-driven live tf-acc pass (X2).

A minor sub-change rides along: an unmapped `ExecutionStatus` value now stringifies to `"UNKNOWN"` instead of
panicking (`report.ExecutionStatus` crosses package boundaries, so a process-crashing stringer is the wrong failure
mode). No production path ever produces an unmapped value.

### ⚠️ Sensitive inputs of a non-STRING/CODE/FILE type now fail the run

A building-block input marked sensitive (`isSensitive: true`) may only carry an encrypted value if its declared
type is `STRING`, `CODE` or `FILE` — the only three input types meshStack supports encrypting. Previously such a
misconfigured input was handled three-way inconsistently across the runner types: the ported runner types
(gitlab/azdevops/github) **warned and left the ciphertext as-is**, the run-controller's k8s Secret handover blindly
**decrypted it if it happened to be a string, or passed it through otherwise**, and the tf runner's own polling
decrypt path (its former behavior) **decrypted every sensitive value regardless of its declared type**. Now
every runner type shares one policy
(`internal/secret`): any sensitive input whose declared type is not `STRING`, `CODE` or `FILE` causes the run to be
reported **FAILED** with an actionable error (`sensitive input "<key>" has type "<TYPE>"; only STRING, CODE and FILE
inputs may be encrypted`).

**Rationale.** Only `STRING`/`CODE`/`FILE` support encrypted values in meshStack, so a sensitive `BOOLEAN`/`INTEGER`/
`SINGLE_SELECT`/`MULTI_SELECT`/`LIST` input signals a misconfigured building block. Silently forwarding its ciphertext
downstream — into generated tfvars, GitLab pipeline variables, Azure DevOps template parameters or GitHub
`workflow_dispatch` inputs — is a data-integrity and secret-leak hazard (a downstream consumer would receive an opaque
ciphertext string where it expects a boolean/number/selection).

**Impact (⚠️ potentially breaking).** Any building block that marked a `BOOLEAN`/`INTEGER`/`SINGLE_SELECT`/
`MULTI_SELECT`/`LIST` input as sensitive will now fail rather than run. The fix is to unmark that input as sensitive
(these types cannot legitimately be encrypted). No correctly-configured building block is affected.

**Timing shift (superset/polling).** `internal/rundecrypt` now decrypts once at the claim boundary, before the run
is dispatched to its type handler -- so in the superset and per-type polling binaries this misconfiguration now
fails the run at claim/decrypt time rather than mid-run once the handler reaches the offending input. The
single-run and k8s Job dispatch paths are unchanged: they already decrypted before handing the run to its handler,
so their failure point is the same as before.

### Empty-ciphertext handling is now uniformly `""` across all runner types

The decrypt seam now lives in one shared package (`internal/secret`), so the Kotlin `decrypt("") == ""` guard
(an empty ciphertext decrypts to an empty string rather than surfacing the underlying "encrypted value empty or too
short" crypto error) applies uniformly. The github and tf runners previously lacked this guard on their own
package-local decryptors; they now inherit it. **Impact: none expected** — in practice these decryptors are only ever
asked to decrypt present values (an appPem, an SSH key, a sensitive input); the empty case was latent.

### github: string-map inputs render as inline JSON, not Go formatting

When github coerces a run-input value into the `workflow_dispatch` string-input map (the Mode-B system tokens
`MESHSTACK_API_TOKEN`/`MESHSTACK_RUN_TOKEN`/`MESHSTACK_ENDPOINT`, and the value handed to the sensitive-input
decryptor), it now uses the shared `internal/valuestring.Render`: a JSON `null` renders as the literal `"null"` and a
composite (array/object) renders as compact JSON, instead of the previous empty string for null and Go's `fmt.Sprint`
format (`[a b]` / `map[k:v]`) for composites. A third-party backend that only accepts string inputs must never receive
language-specific formatting or a silent-empty-for-null; inline JSON is the only portable form (and matches what
azdevops and gitlab already emit).

**Impact: none expected.** These specific inputs are always strings in practice (tokens, an endpoint URL, a ciphertext
string), and strings render verbatim — byte-identical to before. The change only affects the previously-latent
null/composite edge, which does not occur for these keys. Mode-A dispatch (the base64-JSON run object) already
serialized via `json.Marshal` and is unaffected. Recorded here because it is a deliberate departure from the old
byte-for-byte Kotlin twin, not because a live deployment is expected to observe a difference.

### gitlab: `MESHSTACK_RUN` no longer carries the implementation object

The `MESHSTACK_RUN` pipeline variable gitlab's trigger payload forwards is now produced by
`meshapi.SanitizeRunObjectForHandover`, which reduces `spec.buildingBlockDefinition.spec.implementation` to just its
`type` field (every other field of the run document is untouched). This applies in every mode, including the k8s
single-run path — previously `MESHSTACK_RUN` carried the full, encrypted `implementation` object (on both the main/
polling path and single-run), and under the new claim-boundary decrypt (see above) it would otherwise have carried
that same object **decrypted, in plaintext** (e.g. a plaintext `pipelineTriggerToken`) had it not been stripped.
github's and azdevops's payloads are byte-unchanged by this refactor: github already stripped the implementation
object before this change, and azdevops never forwards a run object at all.

**Impact (⚠️ potentially breaking for custom pipelines).** A customer building-block that parses
`implementation`/`pipelineTriggerToken` (or any other implementation field) out of `MESHSTACK_RUN` in its own gitlab
pipeline will stop finding it there. The reference integration (`gitlab.com/meshcloud/meshstack-integration`) is
unaffected — it only checks for `MESHSTACK_RUN`'s presence, never its contents. See `CROSS_REPO_TODO.md` for the
customer-facing follow-up.

### gitlab/github/azdevops: forwarded sensitive-input keys now logged as a WARN

Each of these three runner types now emits one WARN (via `meshapi.SensitiveInputKeys`) listing the sensitive input
keys about to leave the process in a run's outbound payload, before dispatch. This is observability only — it does
not change what is forwarded, only that a sensitive key's presence is now visible in the runner's own log stream.
