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

The [Release](.github/workflows/release.yml) workflow publishes a **GitHub Release** with notes generated from the
conventional commits since the previous tag, and then builds and pushes the Docker images. A tag that does not match
`vX.Y.Z` starts no release.

The quality of the release notes depends on [Conventional Commits](https://www.conventionalcommits.org/), so keep using
`feat:`, `fix:`, `chore:`, etc. The grouping is configured in [cliff.toml](cliff.toml).
