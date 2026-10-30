# Building Block Runner

This repository contains the open-source meshStack **Building Block Runners** — processes that pick up infrastructure
provisioning runs, invoke the underlying executor (Terraform, a CI/CD pipeline, or a manual process), and report the result
back. Runners are a core part of [meshStack](https://meshcloud.io), a sovereign internal developer platform where teams
publish self-service infrastructure units called **Building Blocks** that developers can order on demand.

meshStack ships with managed runners that work out of the box — no setup required. This repository lets you deploy and
operate runners yourself on your own infrastructure. We believe it is important that you can inspect exactly what runs
in your environment and deploy it on your own terms to achieve full sovereignty over the execution layer.

This repository builds one Go binary, [`cmd/runner`](cmd/runner/main.go) customized via build tags as follows.
The main images are `building-block-runner`, which runs every runner type, and `run-controller` for Kubernetes.

| Image                       | Build tag     | Description                                                                     |
|-----------------------------|---------------|---------------------------------------------------------------------------------|
| `building-block-runner`     | none          | Runs every runner type as a child process, usually inside a container           |
| `run-controller`            | `k8s`         | Starts each run as a Kubernetes Job, by default on `building-block-runner`      |
| `tf-block-runner`           | `tf`          | Executes OpenTofu plans and applies                                             |
| `github-block-runner`       | `github`      | Triggers GitHub Actions workflows                                               |
| `gitlab-block-runner`       | `gitlab`      | Triggers GitLab CI pipelines                                                    |
| `azure-devops-block-runner` | `azuredevops` | Triggers Azure DevOps pipelines                                                 |
| `manual-block-runner`       | `manual`      | Represents manual processes (transfers inputs to outputs)                       |

[`containers/Dockerfile`](containers/Dockerfile) builds every image. It builds the binary itself, from a clone with
its `.git`, so that the binary knows its version:

```bash
docker build -f containers/Dockerfile --target tf --build-arg TAGS=tf --build-arg FOR_TYPES=tf .
```

`task image:build` builds the binary on your machine, uncommitted changes included, and also works in a git worktree:

```bash
task image:build TARGET=tf TAGS=tf FOR_TYPES=tf IMAGE=tf-block-runner:dev
```

No image contains a `runner-config.yml`. Mount it at `/app/runner-config.yml`, or set `RUNNER_CONFIG_FILE` to its path.
[`internal/config/testdata/runner-config.yml`](internal/config/testdata/runner-config.yml) is an example with every key.

In a pod, the run-controller needs no `k8s` section: it starts the Jobs in its own namespace, with a container `runner`
on `ghcr.io/meshcloud/building-block-runner` at its own version, the release or, for a build of `main`, the git commit.
`k8s.jobSpecTemplates.ALL` merges onto that default, and the template of a runner type onto the result.

`building-block-runner` and `tf-block-runner` come with single-user Nix, and a run can install tools with
`nix profile add nixpkgs#<package>`. `aws` is installed this way on its first call. Tools that every run needs belong in
a derived image, which installs them from the nixpkgs the image pins:

```dockerfile
FROM ghcr.io/meshcloud/building-block-runner:<version>
RUN nix profile add nixpkgs#awscli2 nixpkgs#kubectl && rm -rf ~/.cache/nix
```

`NIX_CONFIG` reaches every run. Set `extra-substituters` and `extra-trusted-public-keys` in it to install from a nearby
binary cache.

## Running a runner

Each runner is distributed as a container image and configured via a YAML file. The general steps are:

1. Create a new Building Block Runner in meshStack and create an API key for the runner.
2. Write & configure a `runner-config.yml` with the meshStack API configuration.
3. Deploy the runner container on your infrastructure of choice (Kubernetes, a VM, or locally for testing).
4. Reference the new runner in the Building Block Definition of your choice.

The runner will then poll meshStack for pending runs and execute them.

On SIGTERM the runner stops claiming and waits for its runs: up to `dispatcher.timeout` plus `dispatcher.killGrace`
(62 minutes by default), the same limits a run-controller Job gets as `activeDeadlineSeconds` and
`terminationGracePeriodSeconds`. Give a single container at least this stop grace (`docker run --stop-timeout`, a pod's
`terminationGracePeriodSeconds`), or a stop kills its runs before they report a final status.

For full configuration reference and deployment guides, see the [meshStack documentation](https://docs.meshcloud.io/guides/platform-ecosystem/how-to-run-building-block-runners/).
We highly recommend creating a new runner via the meshStack UI as it will fully explain and generate the configuration for you.

## Support

Building Block Runners are part of the commercial meshStack SaaS platform. If you are a customer you can open a ticket
through [support@meshcloud.io](mailto:support@meshcloud.io). Building Block Runners fall under the SLA, and we will help
you out as quickly as we can.

If you have any community or open-source questions, you can open a GitHub issue right here.

For general information about the meshStack platform and Building Blocks, see the [meshStack documentation](https://docs.meshcloud.io).

## Repository structure

One [Go module](go.mod) at the repository root holds everything. Its one binary, [`cmd/runner`](cmd/runner/), runs in every image.
[`containers`](containers/) holds the Dockerfile of every image, and [`dev`](dev/) the runner configs of the local tasks.

Common tasks are available via [Task](https://taskfile.dev), which the [Nix](flake.nix) dev shell provides:

```
task --list
```

## Logs

The Go runner logs at the level in `RUNNER_LOG_LEVEL`: `debug`, `info` (default), `warn` or `error`, and in the
format in `RUNNER_LOG_FORMAT`: `text` (default) or `json`. A run process gets both, and `GOCOVERDIR` for a binary
built with `-cover`. Each line of a
step log, which holds the tofu output of the customer, is logged at `debug` only.

## Health endpoint

The runner serves `/healthz` for liveness probes and `/metrics` on the port in `RUNNER_METRICS_PORT`, or in `PORT` when
that is not set, and serves neither without them. Every image sets `RUNNER_METRICS_PORT=2112`.

## Development

### Prerequisites

`nix develop` provides everything below. Without Nix:

- Go 1.27+
- [Task](https://taskfile.dev)

### Build and test

```bash
task test          # run all Go tests
task lint          # run golangci-lint, including the format check; -- --fix formats and fixes
task tidy          # tidy go.mod and go.sum
```

CI runs the equivalent plain `go` commands rather than these tasks.

### Run locally

`task start:runner` runs the `ALL` runner from source with [`dev/local.yml`](dev/local.yml) against the local
meshStack at `http://localhost:8080`, or against the `MESHSTACK_ENDPOINT` you export:

```bash
task start:runner
```

To run one runner type, build with its tag and pass a config:

```bash
RUNNER_CONFIG_FILE=dev/local.yml MESHSTACK_ENDPOINT=http://localhost:8080 SSH_KNOWN_HOSTS=containers/known_hosts go run -tags tf ./cmd/runner
```

### Debug the Kubernetes dispatcher

Use minikube only to investigate Kubernetes-specific bugs. `task debug:minikube` runs the runner built with `-tags k8s`
on your machine with [`dev/minikube.yml`](dev/minikube.yml) and starts the runner Jobs in minikube, where a sidecar
forwards each Job's `localhost:8080` to meshfed-api on your machine. Build the `:dev` images into minikube first, as the
comment in `dev/minikube.yml` shows.

```bash
task debug:minikube
```

### Acceptance tests

`internal/testacc/` builds `cmd/runner` with coverage and starts it once per test against a live
meshStack, as a private runner that the test bootstraps with OpenTofu and the meshStack provider of
`../terraform-provider-meshstack`, or of the registry when that checkout is missing. The runner
claims with the hosted-runner API key, the harness uses the provider's acceptance key, and each test
asserts the runner's JSON log and what meshStack shows of the run. A whole meshStack only exists in the meshcloud-internal mono repo `meshfed-release`, so CI
here does not run the suite. `.github/workflows/test-acceptance.yml` asks that repository for the
run, and `meshstack-satellite.gradle` is everything the run reads from here. The workflow also
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

Release notes quality depends on [Conventional Commits](https://www.conventionalcommits.org/)
— keep using `feat:`, `fix:`, `chore:`, etc. The grouping is configured in
[cliff.toml](cliff.toml).

## Contributing

For **bugs**, please reach out via [support@meshcloud.io](mailto:support@meshcloud.io) — runner issues are covered under
the meshStack SLA and will be handled as quickly as possible.

For **feature requests**, open one at our [public feedback board](https://feedback.meshcloud.io)

Pull requests are welcome, but reaching out through the above channels first is preferred — it avoids duplicate effort
and ensures your contribution aligns with the roadmap. For larger changes, please get in touch before investing significant time.

## Security

If you discover a security vulnerability, please **do not open a public issue**. See [SECURITY.md](SECURITY.md) for responsible disclosure instructions.

