# Go Testing

## Mandatory

- **Always write or extend tests** for any behaviour you add or modify — not as a follow-up, but alongside the implementation.
- Test naming: `Test_<Condition>_<ExpectedOutcome>` (e.g. `Test_PlanSucceeded_ArtifactInStatusUpdate`).
- **Test observable outcomes, not implementation details.** Assert on externally visible results (e.g. HTTP request bodies sent, status update payloads, files written) rather than on internal function call sequences or private state. Avoid mocking just to verify that a method was called; verify the effect instead.

## Libraries in Use

- Assertions: `github.com/stretchr/testify/assert` + `require`
- Suite pattern: `github.com/stretchr/testify/suite` — use for tests that need shared setup (follow the existing `WorkerTestSuite` pattern)
- Snapshot tests: `github.com/sebdah/goldie/v2` — for complex expected outputs; golden files live in `testdata/`, regenerate with `go test ./<package>/... -update`
- Mocks: hand-rolled fakes, such as `tfrun/mockedtffacade.go` / `tfrun/mockedgitfacade.go` — do NOT introduce new mock libraries

## Running Tests

```bash
# All tests, with and without -tags k8s
task test

# Specific package with verbose output
go test ./tf-block-runner/tfrun/... -v

# Single test by name
go test ./tf-block-runner/tfrun/... -run Test_PlanSucceeded -v
```
