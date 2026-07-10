# Coverage Baseline — Phase 0

**Measured:** 2026-07-10 on `refactor/single-go-binary/phase-0-guardrails` @ c3fce61 (main)  
**Go version:** go1.26.4  
**All tests passing:** ✓

## Per-module statement coverage

| Module | Total | Status |
|---|---|---|
| `go-meshapi-client` | 53.3% | baseline |
| `run-controller` | 22.6% | baseline |
| `tf-block-runner` | 56.6% | baseline |

## Per-package coverage

### go-meshapi-client

| Package | Coverage | Note |
|---|---|---|
| `crypto` | 71.4% | MeshCertBasedCrypto + MeshSymmetricCrypto |
| `meshapi` | 39.3% | Client + Auth + DTOs; many 0.0% functions (uncovered in polling path) |
| **Total** | **53.3%** | |

### run-controller

| Package | Coverage | Note |
|---|---|---|
| `.` (main) | 0.0% | Entry point, untested |
| `build` | — | No test files |
| `controller` | 24.2% | Kubernetes + RunAPI + Registration + Decryption (partial) |
| **Total** | **22.6%** | |

### tf-block-runner

| Package | Coverage | Note |
|---|---|---|
| `.` (main) | 0.0% | Entry point, untested |
| `build` | — | No test files |
| `tfrun` | 59.4% | Worker + SingleRunWorker (partial; single-run path 0%) + TfCmd + Scenarios |
| `util` | 0.0% | Untested utilities |
| **Total** | **56.6%** | |

## Coverage gaps (input to Phase 1 — D9 inventory)

### Critical zero-coverage areas

- `tf-block-runner/tfrun/singlerunworker.go` — all functions 0.0% (`NewSingleRunWorker`, `ExecuteRun`, `workRoutine`, `observerRoutine`, `sendInitFail`)
- `tf-block-runner/tfrun/manager.go` — all functions 0.0%
- `run-controller/controller/kubernetes.go` — all functions 0.0%
- `run-controller/controller/registration.go` — all functions 0.0%
- `run-controller/controller/runapi.go` — all functions 0.0%
- Both `main.go` files — 0.0% (expected; entry point)

### Partial coverage areas with untested paths

- `tf-block-runner/tfrun/worker.go` — `handleFetchRunError` 0.0%
- `tf-block-runner/tfrun/tfcmd.go` — `detectBackend` 0.0%
- `run-controller/controller/decryption.go` — `decryptRunDetails` 29.7%
- `tf-block-runner/tfrun/tfcmd.go` — `encodeVarValueForEnv` 33.3%

## Test runtime characteristics

- `go-meshapi-client`: ~1.8s wall time
- `run-controller`: ~12s wall time (mostly k8s client-go compilation)
- `tf-block-runner`: ~53s wall time (49.6s for `tfrun` alone)
  - **Note:** `tfrun` tests download real terraform binaries (1.3.7, 1.3.8, 1.5.5) + opentofu (1.11.0) and require network access
  - Already present in CI; no new flakiness introduced by coverage measurement

## Promises to Phase 1

This baseline is the input to `PLAN_DETAIL_01_tf_characterization_tests.md`:
1. Per-package statement coverage totals (above)
2. Zero-coverage function inventory for exclusion-list decisions (D6)
3. Critical paths requiring new scenario tests before DDD refactor
4. Real-world test timing for CI gate design
