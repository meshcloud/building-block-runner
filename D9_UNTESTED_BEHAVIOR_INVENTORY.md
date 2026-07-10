# D9 Untested-Behavior Inventory (Phase 0, §8 finalization)

**Scope note:** this is a research artifact (no runtime code touched) seeded per
`PLAN_DETAIL_00_guardrails.md` §6 step 8 and feeds `PLAN_DETAIL_01_tf_characterization_tests.md`.
It finalizes the seed table already present in that plan's §8 by naming the concrete test
(or absence of one) backing each "covered"/"partial"/"untested" verdict, re-checked against
the measured baseline in `COVERAGE_BASELINE.md` (same commit, `main` @ `c3fce61`, 2026-07-08,
go1.26.4 — this branch is at the same commit, so the baseline numbers apply verbatim; no
re-measurement drift).

Convention: `path:line` is the production code; the "Existing test" column names the test
function that actually exercises the behavior (or "— none —" when the coverage-tool zero is
confirmed by grep, i.e., no test function references the code path at all).

| D9 pin | Production code | Existing test | Verdict |
|---|---|---|---|
| async `IN_PROGRESS` on successful handover | Kotlin `block-runner-core` only; no Go equivalent yet | n/a | **n/a until phase 6** — Go handlers don't exist yet; Kotlin behavior must be pinned by Kotlin tests per D6 before the phase-6 port. |
| abort flag via status PATCH cancels run context | `tfrun/worker.go` `observerRoutine` (96.4% stmt cov per baseline) | `worker_scenario_test.go:322` `Test_ApplyRunAborted` — asserts `apply` still runs with a cancelled context and that only ≤1 status update is sent in the 11s window (comment at `worker_scenario_test.go:350`) | **covered** |
| 10s status ticker | `tfrun/worker.go` `observerRoutine` | Same `Test_ApplyRunAborted` (`worker_scenario_test.go:322-358`) — the "11s duration / 10s update interval" comment (`:350`) is the explicit interval assertion | **covered** |
| run-token > base-auth precedence + `ClearRunToken` after execution | `tfrun/runapi.go` `SetRunToken`/`ClearRunToken` (100% per baseline) | `runapi_test.go:743` `Test_ClearRunToken_ResetsToBasicAuth`, `runapi_test.go:820` `Test_ClearRunToken_MultipleRunCycle` | **covered** |
| 409-on-register = success | `tfrun/runapi.go` `Register` (100% per baseline) | `runapi_status_test.go:83` `TestRegister_409Conflict_ReturnsNil` | **covered** |
| 404/409-on-claim = no run | `tfrun/worker.go:66` `handleFetchRunError` (0.0% per baseline) | — none — (grep of all `tfrun/*_test.go` finds no reference to `handleFetchRunError`) | **untested** (confirmed) |
| media types + `X-Block-Runner-Node-Id`/runner headers | `go-meshapi-client/meshapi/client.go` `setHeaders` (100%); `tfrun/runapi.go` header wiring | `runapi_test.go:265,343` assert the `X-Block-Runner-Node-Id` header is present; `runapi_test.go:564-698` assert V1 media type is sent (registration always V1, default V1, custom-predicate V1) | **covered** |
| plan-artifact download 128MiB cap | `go-meshapi-client/meshapi/client.go:20` `maxArtifactBytes = 128 << 20`, enforced at `client.go:153` via `io.LimitReader(resp.Body, maxArtifactBytes+1)` | `client_test.go:13` `TestDownloadArtifact_StreamsBodyIntoWriter` and `client_test.go:42` `TestDownloadArtifact_Non2xxReturnsErrorWithBody` exercise the happy/error paths but **neither feeds >128MiB** to hit the cap | **untested** (the cap itself; the streaming/error paths around it are covered) — NOTE per D9: the former same-origin check was deliberately reverted in `88d67d4`; do not reintroduce or pin it |
| meshStack HTTP backend fallback + `TF_HTTP_USERNAME/PASSWORD` ephemeral auth | `tfrun/tfcmd.go` `createMeshStackHttpBackendFile` (89.5%); `tfcmd.go:271` `detectBackend` (0.0% per baseline) | `tfcmd_test.go` covers `createMeshStackHttpBackendFile`; grep finds no test referencing `detectBackend` | **partial** — the backend-file-write path is covered, the backend-*detection* dispatch (`detectBackend`) is not |
| pre-run script contract (`$MESHSTACK_USER_MESSAGE`, run JSON on stdin) | `tfrun/scriptcmd.go` `runPreRunScript` (100%) | `tfcmd_prerunscript_test.go` (whole file) | **covered** |
| `aaaaaa_…auto.tfvars` + `meshStack_run_vars.tf` generation (run-scoped vars omitted on DETECT/saved-plan APPLY) | `tfrun/tfcmd.go` `vars`-related functions (82.0%); `encodeVarValueForEnv` (33.3% per baseline) | `tfcmd_vars_test.go:22` `TestParseVariableInputs`, `:34` `TestVarsFile_AddVariable` cover file generation at use-case level, but grep confirms **no test file references `encodeVarValueForEnv` by name** — its 33.3% coverage comes only from incidental invocation through those higher-level generation tests, so its untested branches (presumably non-string/bool value-type encodings) have no direct assertion | **partial** |
| FILE inputs as data-URLs | `tfrun/tfcmd.go` `extractContentFromDataUrl` (83.3%), `saveInputFiles` (70.0% per baseline) | `tfcmd_test.go:36/60/79/94` (`Test_saveInputFiles_savedUnencryptedTextFileViaDataUrl`, `_overwriteIfFileAlreadyExists`, `_IgnoresNonFileVariables`, `_ErrorsOnFilesAsEnvironments`) and `:399` `Test_extractContentFromDataUrl` — solid happy/error-path coverage at the named-function level; the remaining gap is coverage-tool-reported branches not enumerated by these table-driven cases (not independently re-verified line-by-line) | **partial** |
| env whitelist (`cleanSystemEnv`) | `tfrun/tfcmd.go` `cleanSystemEnv` (87.5% per baseline) | `tfcmd_test.go` | **covered** |
| decrypt-failure UX (key-mismatch guidance) | `go-meshapi-client/crypto/meshcertbasedcrypto.go:212-226` `validateKeyPair` (compares public/private key, returns `"public key and private key do not match: public exponent mismatch"` / `"...modulus mismatch"`); `controller/decryption.go` `decryptRunDetails` (29.7%) | `meshcertbasedcrypto_test.go:69` `Test_DecryptMeshCertBased_MissingPrivateKey` (missing-key path) and `:59` `Test_readRSAPrivateKey_PKCS8NonRSA` (non-RSA-key path) exist, but **no test calls `validateKeyPair` with a genuinely mismatched (but structurally valid) key pair** — the key-mismatch guidance message itself is unexercised; `run-controller/controller/decryption_test.go:58-146` covers unsupported-implementation-type and invalid-input paths, not decrypt-failure-from-mismatched-keys | **mostly untested** (confirmed: the specific mismatch-guidance branch has zero test coverage) |
| workspace select/create/delete naming logic | `tfrun/tfcmd.go` `selectWorkspace` (47.1%, contains the confirmed D13 bug: swallowed error, `tfcmd.go:232-234` `return "", nil`); `deleteWorkspaceIfNeeded` (76.9%) | No test function name contains "workspace" (grep across all `tfrun/*_test.go`) — exercised only incidentally through `worker_scenario_test.go`'s `Test_ApplySucceeded`/`Test_DestroySucceeded` end-to-end flows, consistent with D16 (scenario tests over unit armadas), but the swallowed-error branch specifically is not asserted by any test (nothing fails when the error is swallowed, so no test can distinguish "no error" from "error incorrectly suppressed") | **partial + known bug** (bug already flagged for D13 phase-2b fix; the FIXME(bug) pin doesn't exist yet — phase 1 must add it) |
| k8s single-run contract (`RUN_JSON_FILE_PATH`, `/var/run/secrets/meshstack/run.json`, `RUNNER_UUID`, `RUNNER_API_URL`, runToken-only auth) | `tfrun/singlerunworker.go` (all functions 0.0% per baseline: `NewSingleRunWorker`, `ExecuteRun`, `workRoutine`, `observerRoutine`, `sendInitFail`); `run-controller/controller/kubernetes.go` (all functions 0.0%) | — none — confirmed by `ls run-controller/controller/*_test.go`: **no `kubernetes_test.go` file exists at all**; and grep of `tfrun/*_test.go` finds no reference to any `singlerunworker.go` function | **untested on both sides — biggest gap** (matches §11 flag #4 in the plan; phase 1 must prioritize this beyond the "good facades already exist" framing in the high-level plan's §2, which is true only for the polling path) |
| `ABORTED` semantics (graceful shutdown, `IN_PROGRESS→ABORTED`, 409-abort-flag = no-op) | `ExecutionStatus` enum (`tfrun` / `controller` DTOs) — today only `PENDING/IN_PROGRESS/SUCCEEDED/FAILED`; `ABORTED` does not exist in the Go code yet | n/a — the enum value doesn't exist yet, so no test can reference it | **not yet implementable / n/a until the D9 `ABORTED` work lands** (tracked as its own D9 sub-bullet, not part of the pre-existing pin set that phase 1 characterizes; phase 2/2b or later introduces the enum value and its tests per the plan's graceful-shutdown design) |

## Additional zero-coverage areas outside the pin list (input to phase 1's exclusion-list decision)

Re-confirmed against the current tree (unchanged from `COVERAGE_BASELINE.md`):
- `tfrun/manager.go` — all functions 0.0% (no `manager_test.go` exists)
- `tfrun/git.go` — azure/auth clone paths 0% (`gitsource_test.go`'s `Test_GitSourceCloneSimpleAzure`/`Test_GitSourceCloneWithRefAzure` exercise `GitSource`'s dispatch logic against a **mocked** `GitFacade` (`gitsource_test.go:58` `gitFacade: mock`) — they assert which facade method *should* be called, but never invoke the real `git.go:163` `azureClone`/auth implementations, so those remain genuinely untested)
- `controller/registration.go`, `controller/runapi.go` — 0% (no corresponding `_test.go` files)
- both `main.go` files — 0% (entry points, expected)

## Method

For each pin: (1) pulled the baseline statement-coverage number from `COVERAGE_BASELINE.md`
(measured at the same commit this branch is based on, so no re-measurement was needed); (2)
grepped the relevant `_test.go` files for the named production function to confirm whether a
test actually calls it, rather than trusting the aggregate package percentage (a package can
show partial coverage while a *specific* named function inside it is 0%); (3) where the
plan's seed table said "verify" (register 409, abort-cancellation, ticker interval, media
types), named the exact test function; (4) where the seed table already said "untested",
confirmed by grep that literally no test references the function, rather than re-asserting
the same claim unverified.

## Feeds

`PLAN_DETAIL_01_tf_characterization_tests.md` — this table is the direct input for mapping
every D9 pin + `tfrun` use case to new/existing scenario tests and for shaping the D6
coverage-exclusion list (real-I/O adapters vs. genuinely-missing-test gaps).
