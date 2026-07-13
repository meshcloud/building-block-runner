# Run-log addendum — phase-3/5 remediation

> This addendum lives in its own file because `PLAN_IMPL_RUN_LOG.md` is not tracked on the
> `refactor/single-go-binary/phase-7-cleanup` base this remediation branch is stacked on.
> It closes out **BLOCKER 2** (phase-3 shared-core) and **BLOCKER 3** (phase-5 dispatcher)
> from that run log.

## Follow-up remediation (phase-3/5) — 2026-07-10

Branch: `refactor/single-go-binary/phase-3-5-remediation` (stacked on `phase-7-cleanup`).
Every commit is green: `task test` (with `-race`), `task lint`, and all coverage gates.

### BLOCKER 2 — phase-3 shared-core

**Closed**

- **Injectable metrics seam (§5.6).** Added `dispatch.NewMetricsCollectorWithRegistry(reg
  prometheus.Registerer)`; the singleton `NewMetricsCollector()` is now a thin default-registry
  wrapper over it (customer-facing behavior + duplicate-registration protection preserved).
  Metric names/labels/help are byte-identical. The run-controller mgmt listener now constructs
  one collector against a dedicated `mgmt.NewRegistry` and serves it (dropping the reliance on
  `prometheus.DefaultRegisterer`/`DefaultGatherer`); the tf in-process path uses the same seam
  to land the `run_controller_*` loop series on the tf persona's own registry.
- **`DecryptRunDetails` + `Decryptor` into meshapi (step 8).** `meshapi.DecryptRunDetails(runJsonBase64,
  dec Decryptor)` now owns the run-JSON decryption (moved verbatim from `internal/k8sjob`,
  decoupled from the concrete crypto via the `Decryptor` seam + `NewCertDecryptorFromCrypto`);
  `k8sjob` calls it. The full decryption test suite moved to meshapi (coverage stays ≥90).
  `tf.Decryptor`/`tf.NoopDecryptor` are now aliases of the shared `meshapi` types (duplicate
  interface removed, zero assertion change). The crypto `forcetypeassert` paths were already
  fail-safe (checked assertions, phase 7); confirmed, no change needed.
- **`UseTestClient` de-globalized.** It was a dead switch (declared, read twice, written
  nowhere, bound to no env/config) — removed along with its always-false branches, zero
  behavior change. (Controller `AppConfig`/`DiscoveredOidcIssuer` globals the run log listed
  were already threaded as explicit config by phase 4/7 — verified: no such globals remain.)

**Deferred (with reasons — could not close green within this pass)**

- **Full `tf.AppConfig` de-globalization (~47 non-test + ~130 test read sites).** Unlike the
  controller globals (already threaded), `tf.AppConfig` is still a package-level global read
  throughout `internal/tf` and pinned by a large characterization suite that constructs/reads
  it directly (e.g. `config_test.go`, `runapi_*_test.go`). Threading it as explicit config is
  mechanical but touches the frozen wire pins (`runapi.go`, `dtos.go.toExternal` source id,
  node-id) and ~180 sites; doing it green-at-every-commit safely is a dedicated pass of its
  own. The new handler/dispatch code added here threads its config explicitly
  (`HandlerConfig`/`HandlerDeps`) rather than reading the global, so it does not deepen the
  debt. **Recommend a focused follow-up** that de-globals `tf.AppConfig` behind the existing
  characterization suite.
- **Wiring the shared `config`/`report` packages *into the tf runner*.** They are consumed by
  the controller (`internal/config`) and the four phase-6 personas (`internal/config` +
  `internal/report`), but `internal/tf` still uses its own `TfRunnerConfig`/`ReadConfig` and
  its own `RunStatus`/`ExecutionStatus`/`Progress`. Moving tf onto the shared `report` package
  is PLAN_DETAIL_03's own "riskiest step" (§6 step 9): it rewrites tf's status model and every
  PATCH-body characterization assertion. Not attempted here to avoid destabilizing the frozen
  tf wire pins; **recommend it accompany the `tf.AppConfig` de-global pass.**

### BLOCKER 3 — phase-5 dispatcher (tf in-process cutover)

**Closed**

- **`tf.NewHandler`** implements `dispatch.RunHandler`. `Execute` maps the claimed DTO with the
  cert `Decryptor` (polling semantics — pins intact), builds a per-run `RunApi` with the run's
  own runToken (H5: never shared across concurrent runs), and drives the exact
  `Worker.tfExecution` machinery so register/PATCH/artifact/metering wire behavior is
  byte-identical to the polling Worker. Scenario tests reuse the polling suite's hermetic
  fixtures (APPLY-succeeded, tf-failure-reported, mapping-failure-silent + metering).
- **`maxConcurrentRuns`** config + `RUNNER_MAX_CONCURRENT_RUNS` env (default 3, documented in
  `containers/tf-block-runner/runner-config.yml`; `=1` reproduces the serial cadence). Additive.
- **tf `registration:` opt-in + startup PUT.** `tf.Register` PUTs a WIF-less
  `MeshBuildingBlockRunnerDTO` for the configured capability; absent section => never
  self-registers (as today); 404 => the frozen "create it via the meshStack UI" message.
- **Two additive `runner_*` metrics** (`runner_runs_unhandled_total{runner_uuid,type}`,
  `runner_at_capacity_skips_total{runner_uuid}`) on `mgmt.RunMetrics`, driven via an optional
  `dispatch.Loop` `StandaloneMetrics` hook (nil for the controller => byte-identical). Confirmed
  the exact names against PLAN_DETAIL_05 §16.
- **`cmd/bbrunner` dispatcher auto-detect** (`detectDispatcherKind`: in-cluster via
  `KUBERNETES_SERVICE_HOST` => k8sjob; else => inprocess; `RUNNER_DISPATCHER` overrides),
  unit-tested.
- **tf in-process dispatch path wired** (`tf.NewDispatchRunner` builds the loop + InProcess +
  claim client with the frozen `<uuid>-worker-1` node-id + the tf claim classifier reproducing
  `handleFetchRunError`), selectable via `RUNNER_DISPATCHER=inprocess` in both `cmd/tf` and
  `bbrunner tf`.

**Deferred (with reasons — kept safe, not deleted on faith)**

- **Deleting `tf.NewManager` + the `SetRunToken`/`ClearRunToken` token protocol.** Per the
  task's explicit instruction, these are **kept** and the Manager remains the DEFAULT tf polling
  path; the new dispatch path is opt-in behind `RUNNER_DISPATCHER=inprocess`. Reason: proving
  full equivalence requires driving the *entire* phase-1 characterization suite through the loop
  (step 6 exit criterion) — the handler reuses `Worker.tfExecution` unchanged (so execution IS
  identical) and new handler scenario tests pass, but the loop/claim *cadence* equivalence
  (10s idle / 60s fetch-error / immediate-after-done vs. the Manager token protocol) is
  characterized only at the component level, not by re-running the whole suite through the loop.
  Deleting the Manager on that basis would be "on faith." Once a follow-up drives the
  characterization suite through the loop and adds the N-concurrent acceptance smoke, the
  Manager + token protocol can be deleted and the new path made the default.
- **`RUNNER_DISPATCHER=inprocess` superset in `cmd/bbrunner` (controller).** The auto-detect
  MECHANISM is implemented and tested, and the in-cluster/out-of-cluster-via-kubeconfig k8sjob
  paths are byte-identical to before. But the *InProcess superset* (running all five persona
  handlers in-process to retire meshfed-release's multiplexing-block-runner) needs each
  persona's own config loaded into the controller bootstrap, which is a larger wiring task; an
  explicit `RUNNER_DISPATCHER=inprocess` on the controller therefore fails fast with an
  actionable message rather than silently running k8sjob. Standalone personas already run
  in-process today via `bbrunner <persona>` / `cmd/<persona>`.
- **ABORTED-on-shutdown for in-flight tf runs.** The plan wants a grace-period-then-cancel that
  reports terminal `ABORTED`. The handler intentionally does NOT propagate the InProcess
  shutdown context into the run, matching today's Manager behavior (an in-flight run finishes on
  its own `TfCommandTimeoutMins`), to keep the handler path equivalent to the Manager path it
  stands in for. Adopting ABORTED-on-shutdown is a behavior change that belongs with the
  Manager deletion + `report.ExecutionStatus` ABORTED work.

### Live-acceptance evidence still owed (unchanged from the original run log)

The new tf dispatch path and the tf registration PUT are proven by hermetic tests (fake
transport / httptest / MockedTfFacade), not a live meshStack. Before flipping the default tf
path to in-process, run the local-dev-stack acceptance (≥1 TERRAFORM + ≥1 MANUAL) and an
N-concurrent smoke (`RUNNER_DISPATCHER=inprocess`, `maxConcurrentRuns=2`, two overlapping
runs), and drive the phase-1 characterization suite through the loop.
