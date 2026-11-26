# Development

This page is for contributors to the Building Block Runner. To deploy and configure a runner, see the [README](README.md).

## Repository structure

One [Go module](go.mod) at the repository root holds everything. Its one binary, [`cmd/runner`](cmd/runner/), runs in every image.
[`containers`](containers/) holds the Dockerfile of every image, and [`dev`](dev/) the runner configs of the local tasks.

Common tasks are available via [Task](https://taskfile.dev), which the [Nix](flake.nix) dev shell provides:

```
task --list
```

## Prerequisites

`nix develop` provides everything below. Without Nix:

- Go 1.27+
- [Task](https://taskfile.dev)

## Build and test

```bash
task test          # run all Go tests
task lint          # run golangci-lint, including the format check; -- --fix formats and fixes
task tidy          # tidy go.mod and go.sum
```

CI runs the equivalent plain `go` commands.

## Run locally

`task start:runner` runs the `ALL` runner from source against the local meshStack at `http://localhost:8080`, or
against the `MESHSTACK_ENDPOINT` you export. It reads [`runner-config.yml`](runner-config.yml), a link to
[`dev/local.yml`](dev/local.yml), and authenticates with the profile that `meshstack login` stored for that endpoint,
or with `MESHSTACK_API_KEY` and `MESHSTACK_API_SECRET`:

```bash
task start:runner
```

To run one runner type, build with its tag:

```bash
SSH_KNOWN_HOSTS=containers/known_hosts go run -tags tf ./cmd/runner
```

## Debug the Kubernetes dispatcher with minikube

Use minikube only to investigate Kubernetes-specific bugs. `task debug:minikube` runs the runner built with `-tags k8s`
on your machine with [`dev/minikube.yml`](dev/minikube.yml) and starts the runner Jobs in minikube, where a sidecar
forwards each Job's `localhost:8080` to meshfed-api on your machine. Build the `:dev` images into minikube first, as the
comment in `dev/minikube.yml` shows.

```bash
task debug:minikube
```

## Acceptance tests

`internal/testacc/` builds `cmd/runner` with coverage and starts it once per test against a live
meshStack, as a private runner that the test bootstraps with OpenTofu and the meshStack provider of
`../terraform-provider-meshstack`, or of the registry when that checkout is missing. The harness uses
the provider's acceptance key and creates an API key for each runner, and each test
asserts the runner's JSON log and what meshStack shows of the run. A whole meshStack only exists in the meshcloud-internal mono repo `meshfed-release`, so CI
here does not run the suite. `.github/workflows/test-acceptance.yml` asks that repository for the
run, and `.github/meshstack-satellite.gradle` is everything the run reads from here. The workflow also
requests the terraform provider's suite against the same branch, which drives real terraform and
manual runs through the runners.

To run the suite yourself, bring up the local stack of `../meshfed-release` (its `local-dev-stack`
skill); `./gradlew :building-block-runner:satelliteEnv` there writes
`../.env-testacc-building-block-runner`. Then run the suite from here with plain `go test`:

```bash
set -a; . ../.env-testacc-building-block-runner; set +a
go test -count=1 -timeout 60m ./internal/testacc/ -run TestAcc
```

Without `BUILDING_BLOCK_RUNNER_TEST_ACC=1`, which that file sets, the suite skips. With
`RUNNER_TESTACC_KEEP=1` it keeps the objects in meshStack and the dirs of each test.
`task test:acc:coverage` runs the unit tests and the suite and reports their merged coverage.

## Release

Push a semver tag on `main`, for example:

```shell
git push origin main:refs/tags/v1.2.3
```

The [Release](.github/workflows/release.yml) workflow publishes a **GitHub Release** and then builds and pushes the
Docker images. A tag that does not match `vX.Y.Z` starts no release. The repository has no changelog file:
[`tools/releaser`](tools/releaser/) builds the release notes from the commits on `main` since the previous tag. Every
`feat` commit, every `fix` commit and every commit with `!` gets an entry, and the text of the entry is a leading
blockquote in the pull request description. Preview the notes and the next version with

```bash
GOWORK=off go run ./tools/releaser "$(git describe --tags --abbrev=0 HEAD^)..HEAD"
```

The notes go to stdout, the warnings and the next version go to stderr. The releaser needs a token in `GITHUB_TOKEN`
or `GH_TOKEN`, for example from `export GH_TOKEN=$(gh auth token)`. The `Release Notes Preview` workflow shows the same
on every push to `main`. The [`release-notes`](.agents/skills/release-notes/SKILL.md) skill describes how to write an
entry, how to link an issue that the change closes and how to pick the version.

Commit titles follow [Conventional Commits](https://www.conventionalcommits.org/), as the
[`commit-messages`](.agents/skills/commit-messages/SKILL.md) skill describes. Pull requests are rebase-merged, so every
commit title lands on `main`. A ruleset on `main` accepts only commit titles that match

```text
^(feat|fix|docs|test|ci|chore|refactor|build|perf|revert)(\([^)]+\))?!?: .+
```
