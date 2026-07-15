# Go Testing for building-block-runner

## Mandatory

- **Always write or extend tests** for any behaviour you add or modify — not as a follow-up, but alongside the implementation.
- **Scenario/integration tests over unit armadas (D16).** Prefer one hermetic scenario test driving real behaviour end to end over many near-duplicate unit tests; group related cases as `t.Run` subtests. Coverage is never a reason to add a unit test (P7) — the ≥90% gate is met by scenario coverage of packages under `internal/*`.
- Test naming: `Test_<Condition>_<ExpectedOutcome>` (e.g. `Test_PlanSucceeded_ArtifactInStatusUpdate`).
- **Test observable outcomes, not implementation details.** Assert on externally visible results (e.g. HTTP request bodies sent, status update payloads, files written) rather than on internal function call sequences or private state. Avoid mocking just to verify that a method was called; verify the effect instead.

## Libraries & fixtures in use

- Assertions: `github.com/stretchr/testify/assert` + `require`
- Suite pattern: `github.com/stretchr/testify/suite` — use for tests that need shared setup (follow the existing `WorkerTestSuite` pattern)
- Snapshot tests: `github.com/sebdah/goldie/v2` — for complex expected outputs; golden files live in each package's `testdata/`, regenerate with `go test ./internal/tf/... -update`
- meshfed-API mock: the shared `internal/meshapitest` httptest server (D6) — a real HTTP server the client dials; prefer it over hand-rolled `http.RoundTripper` fakes
- Git: clone from a local bare repo built in `testdata` (see `internal/tf/fixtures_test.go`) rather than the network, so cloning is exercised end to end
- Tool mocks: hand-rolled fakes in `internal/tf/mockedtffacade.go` / `mockedgitfacade.go` remain only where driving the real tool is impractical — do NOT introduce new mock libraries
- Real tofu/terraform download tests are gated behind the `e2e` build tag (`task test:e2e`); they are excluded from the default suite and the coverage gate (see `docs/ARCHITECTURE.md`)

## Running Tests

```bash
# Whole suite at the repo root, with the race detector (== task test)
task test
# or directly:
go test -race ./...

# Specific package with verbose output
go test ./internal/tf/... -v

# Single test by name
go test ./internal/tf/... -run Test_PlanSucceeded -v

# Opt-in e2e (real tofu/terraform download, network)
task test:e2e

# Coverage gate (per-package thresholds in tools/coverage/thresholds.txt)
task coverage
```
