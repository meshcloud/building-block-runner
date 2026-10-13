---
name: backend-go
description: Go development conventions for the Go runner — load when working with any Go code in this repository
---
## Layout

One Go module at the repository root builds one binary, `cmd/runner`.

- `internal/config` loads `runner-config.yml` (the new layout and the legacy ones) and validates it per mode.
- `internal/crypto` holds the private keys and decrypts the sensitive inputs of a run.
- `internal/http` is the HTTP client; `internal/http/meshapi` holds the meshStack runner DTOs and client.
- `internal/dispatcher` claims runs, or runs the one in `RUNNER_CLAIM_RUN_YML`, and hands them to the
  dispatcher the build tags select: `inprocess` (default) or `k8s` (`-tags k8s`).
- `internal/build` holds the version, set with `-ldflags -X`.
- `tf-block-runner/tfrun` and `manual-block-runner/manual` are the executors of the in-process dispatcher.

The run-controller, tf and manual images all build `./cmd/runner`; only run-controller uses `-tags k8s`.

## Companion Files

- `testing.md` — Write or adapt tests when adding or changing any Go code. It covers test structure, libraries in use, and how to run tests.

## Mandatory Steps After Every Change

```bash
task fmt
task lint
task test
```

`task lint` and `task test` run with and without `-tags k8s`. Never skip formatting or tests. Fix all failures before finishing.
