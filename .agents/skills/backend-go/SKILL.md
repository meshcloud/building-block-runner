---
name: backend-go
description: Go development conventions for the Go runner — load when working with any Go code in this repository
---
## Layout

One Go module at the repository root builds one binary, `cmd/runner`.

- `internal/config` loads `runner-config.yml` (the new layout and the legacy ones) and validates it per mode.
- `internal/crypto` holds the private keys and decrypts the sensitive inputs of a run.
- `internal/poller` claims and decrypts runs and hands them to the dispatcher the build tags select. It builds the
  meshStack client with meshstack-cli's `auth.ResolveClient` and `config.Api` as the setting source, and reports
  FAILED for a run whose exit code says it did not deliver its status.
- `internal/dispatcher/process` (default) starts the binary again for each run, with the run yml in `RUNNER_RUN_YML`, an
  allowlisted env and stdin as lifeline. On a timeout it writes the reason to stdin and closes it.
- `internal/dispatcher/k8s` (`-tags k8s`) starts a Job per run on the runner image of its type, with the run mounted
  from a Secret. It reads the exit code from the Job conditions.
- `internal/executor` runs the run in `RUNNER_RUN_YML` in this process and reports through the `Step`s it declares on a
  `Reporter`; `Step.Run` sets a step's status, and its log goes to meshStack and to slog at debug. Its exit code is 0 when it delivered a final or handed-over status, 3 when it
  delivered FAILED, and anything else otherwise. The run logs through `slog.Default()` with the run's attributes; the
  poller adds the run to its context with `dispatcher.WithRun`.
- `internal/testmeshstack` isolates that client in tests and fakes the status endpoints of a run.
- `internal/build` holds the version, set with `-ldflags -X`.
- `internal/executor/tf` and `internal/executor/manual` are the executors. Each registers itself with
  `executor.Register`, linked by `cmd/runner/executor_<type>.go` behind its build tag; a build with neither `tf` nor
  `manual` links both.

All images build `./cmd/runner`: run-controller with `-tags k8s` (no executor), tf with `-tags tf`, manual with
`-tags manual`. `cmd/runner/deps_test.go` pins what each build links.

## Companion Files

- `testing.md` — Write or adapt tests when adding or changing any Go code. It covers test structure, libraries in use, and how to run tests.

## Mandatory Steps After Every Change

```bash
task lint -- --fix
task test
```

`task lint` and `task test` run once per build tag an image uses: none, `k8s`, `tf` and `manual`. Never skip formatting or tests. Fix all failures before finishing.
