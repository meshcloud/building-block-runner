---
name: development
description: How the runner's Go code is laid out and written - packages, values and pointers, modern Go idioms and the checks after every change. Use when writing, changing or reviewing Go code in this repository.
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
- `internal/dispatcher/k8s` (`-tags k8s`) starts a Job per run on the building-block-runner image, or the image of
  its type's Job template, with the run mounted from a Secret. It reads the exit code from the Job conditions.
- `internal/executor` runs the run in `RUNNER_RUN_YML` in this process and reports through the `Step`s it declares on a
  `Reporter`, also after it registered; `Step.Run` sets a step's status and fails the run on an error, `Step.SetStatus` sets it
  alone, and a step's log goes to meshStack and to slog at debug. Its exit code is 0 when it delivered a final or handed-over status, 3 when it
  delivered FAILED, and anything else otherwise. The run logs through `slog.Default()` with the run's attributes; the
  poller adds the run to its context with `dispatcher.WithRun`.
- `internal/testmeshstack` isolates that client in tests and fakes the status endpoints of a run.
- `internal/build` reads the module version and the git revision from the Go build info, which needs a build in a git
  checkout with its tags; the module path is `github.com/meshcloud/building-block-runner/v2`, so Go takes `v2.x.y` tags.
  `ImageTag` is the tag CI pushes the images of the build with.
- `internal/executor/tf`, `internal/executor/manual`, `internal/executor/gitlab`, `internal/executor/azuredevops` and
  `internal/executor/github` are the executors. Each registers itself with `executor.Register`, linked by
  `cmd/runner/executor_<type>.go` behind its build tag; a build without an executor tag links all of them.
  `executor.NewPipelineClient` builds the HTTP client of the pipeline executors on meshstack-cli's `client/http`.

`containers/Dockerfile` builds every image from `./cmd/runner`: run-controller with `-tags k8s` (no executor),
building-block-runner without a tag (every executor), every other image with the tag of its executor.
`task image:build` builds the binary on the host and passes `BINARY=prebuilt`, so the image takes `bin/runner-linux-<arch>`.
`cmd/runner/deps_test.go` pins what each build links.

## Modern Go

Follow the `use-modern-go` skill of the `modern-go-guidelines` plugin, which `.claude/settings.json` enables. `go.mod`
pins the Go version it applies to. On top of it:

- A generic method needs a receiver with real state, such as `Deployment.Output[T]`. Otherwise write a generic
  function.
- `new(expression)` makes an inline pointer: `new("run-1")`, `new(int64(1))`. Do not add a `ptr`-style helper or a
  variable that only exists to take its address.
- Write the struct literal at the call site. A constructor that only fills defaults hides which fields a caller sets.

## Values, pointers and receivers

**A method takes a value receiver by default.** A value cannot be nil, so the method needs no nil check, and a caller
can pass a zero value. Take a pointer receiver only when the method changes its receiver, when the type must not be
copied (it holds a mutex or a lock file, say), or when a nil receiver means something. `recvcheck` keeps all methods
of a type on one kind of receiver, so one such method makes them all pointers.

**A constructor returns a value** unless one of these reasons applies. Values that share mutable state hold one
pointer to that state and stay values themselves.

**A field is a plain value** unless a caller must tell null from the zero value: `K8s.JobTtl` is nil until the
defaults apply, because 0 deletes a finished Job at once.

## Companion Files

- `testing.md` — Write or adapt tests when adding or changing any Go code. It covers test structure, libraries in use, and how to run tests.

## Mandatory Steps After Every Change

```bash
task lint -- --fix
task test
```

`task lint` and `task test` run once per build tag an image uses: none, `k8s`, `tf`, `manual`, `gitlab`, `azuredevops` and
`github`. Never skip formatting or tests. Fix all failures before finishing.
