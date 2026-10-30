# Go Testing

## Mandatory

- **Always write or extend tests** for any behaviour you add or modify — not as a follow-up, but alongside the implementation.
- Test naming: `Test_<Condition>_<ExpectedOutcome>` (e.g. `Test_PlanSucceeded_ArtifactInStatusUpdate`).
- **Test observable outcomes, not implementation details.** Assert on externally visible results (e.g. HTTP request bodies sent, status update payloads, files written) rather than on internal function call sequences or private state. Avoid mocking just to verify that a method was called; verify the effect instead.
- **Acceptance tests first.** `internal/testacc` and the terraform provider's suite run real runs. A unit test pins only what
  they cannot reach reliably, such as type mapping, config precedence and exit codes.
- A test that starts a run process starts the test binary. Its `TestMain` acts as the run with a fake executor when
  `RUNNER_RUN_YML` is set, as in `internal/dispatcher/process`.

## Libraries in Use

- Assertions: `github.com/stretchr/testify/assert` + `require`
- Snapshot tests: `github.com/sebdah/goldie/v2` — for complex expected outputs; golden files live in `testdata/`, regenerate with `go test ./<package>/... -update`
- Fakes: hand-rolled, such as the `client.MeshRunnerRunClient` fakes in `internal/executor` — do NOT introduce mock libraries

## Running Tests

```bash
# All tests, once per build tag an image uses
task test

# Specific package with verbose output
go test ./internal/executor/tf/... -v

# Single test by name
go test ./internal/executor/tf/... -run Test_PlanSucceeded -v
```

## Acceptance tests (`internal/testacc`)

- One `TestAcc<Case>` per folder `testdata/<case>`; its variants are `t.Run` subtests. `caseFiles(t)` derives the
  folder from the test name, `TestAccStalePlan` reads `testdata/staleplan`. A test without testdata needs nothing.
- A case sees only its own folder, as an `fs.FS` from the embedded `testdata`. A case with a module only borrows the
  bootstrap of `tfblock` through `tfblockBootstrap(t)` at the call site.
- The harness lives in subpackages: `harness` (suite, deploy, bootstrap), `runnerproc` (the runner process and its
  JSON log), `keycloak`, `mockpipeline`, `mockgithub`, `mockgitlab`, `mockazuredevops`, `sshgit` and `toxiproxy`.
- Every exported function of a subpackage takes `t *testing.T` first and no `context.Context`: it uses
  `t.Context()`, and `context.WithoutCancel(t.Context())` in a `t.Cleanup`, where `t.Context()` is already canceled.
  The exceptions are what a mock's HTTP handler calls, as a handler has no `t`, and methods of plain data.
  A mock server or a Keycloak client belongs to one test and stops in its `t.Cleanup`.
- `depguard` keeps `internal/testacc` out of non-test code.
