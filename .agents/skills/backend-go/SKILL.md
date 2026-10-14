---
name: backend-go
description: Go development conventions for the Go runner — load when working with any Go code in this repository
---
## Layout

One Go module at the repository root builds one binary, `cmd/runner`.

- `internal/config` loads `runner-config.yml` (the new layout and the legacy ones) and validates it per mode.
- `internal/crypto` holds the private keys and decrypts the sensitive inputs of a run.
- `internal/dispatcher` claims runs, or runs the one in `RUNNER_CLAIM_RUN_YML`, and hands them to the
  dispatcher the build tags select: `inprocess` (default) or `k8s` (`-tags k8s`). It builds the meshStack client
  with meshstack-cli's `auth.ResolveClient` and `config.Api` as the setting source.
- `internal/testmeshstack` points that client at a test server.
- `internal/build` holds the version, set with `-ldflags -X`.
- `tf-block-runner/tfrun` and `manual-block-runner/manual` are the executors of the in-process dispatcher. Each
  registers itself in `internal/dispatcher/inprocess/executor_<type>.go` behind its build tag; a build with neither
  `tf` nor `manual` links both.

All images build `./cmd/runner`: run-controller with `-tags k8s` (no executor), tf with `-tags tf`, manual with
`-tags manual`. A binary that links one executor defaults `dispatcher.forTypes` to its runner type.
`cmd/runner/deps_test.go` pins what each build links.

## Companion Files

- `testing.md` — Write or adapt tests when adding or changing any Go code. It covers test structure, libraries in use, and how to run tests.

## Mandatory Steps After Every Change

```bash
task fmt
task lint
task test
```

`task lint` and `task test` run once per build tag an image uses: none, `k8s`, `tf` and `manual`. Never skip formatting or tests. Fix all failures before finishing.
