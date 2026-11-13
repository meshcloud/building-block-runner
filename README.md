# Building Block Runner

This repository contains the meshStack **Building Block Runners** — processes that pick up infrastructure
provisioning runs, invoke the underlying executor (Terraform, a CI/CD pipeline, or a manual process), and report the result
back. Those runners ship ready-to-use as a core part of [meshStack](https://meshcloud.io), a sovereign internal developer platform where teams
publish self-service infrastructure units called **Building Blocks**.
This public repository lets you deploy and operate runners yourself on your own infrastructure against your meshStack instance.

## Building a runner

This repository builds one Go binary in [`cmd/runner`](cmd/runner/main.go) and containerizes it using Go build tags.
The main [containers](containers/Dockerfile) are `building-block-runner`, which runs every runner type,
and `run-controller` for dispatching runs as Kubernetes jobs.

| Container                   | Go Build tag  | Description                                                                     |
|-----------------------------|---------------|---------------------------------------------------------------------------------|
| `building-block-runner`     | none          | Runs every runner type as a child process, usually inside a container           |
| `run-controller`            | `k8s`         | Starts each run as a Kubernetes Job, by default on `building-block-runner`      |
| `tf-block-runner`           | `tf`          | Executes OpenTofu plans and applies                                             |
| `github-block-runner`       | `github`      | Triggers GitHub Actions workflows                                               |
| `gitlab-block-runner`       | `gitlab`      | Triggers GitLab CI pipelines                                                    |
| `azure-devops-block-runner` | `azuredevops` | Triggers Azure DevOps pipelines                                                 |
| `manual-block-runner`       | `manual`      | Represents manual processes (transfers inputs to outputs)                       |

`task container:build` builds a container from source:

```bash
task container:build TARGET=tf TAGS=tf FOR_TYPES=tf IMAGE=tf-block-runner:dev
```

## Running a runner

Each runner is distributed as a container image and configured via a YAML file. The general steps are:

1. Create a new Building Block Runner in meshStack and create an API key for the runner.
2. Write a `runner-config.yml` as described in [Configuration](#configuration).
3. Deploy the runner container on your infrastructure of choice (Kubernetes, a VM, or locally for testing).
4. Reference the new runner in the Building Block Definition of your choice.

The runner will then poll meshStack for pending runs and execute them.

On SIGTERM the runner stops claiming and waits for its runs: up to `dispatcher.timeout` plus `dispatcher.killGrace`
(62 minutes by default), the same limits a run-controller Job gets as `activeDeadlineSeconds` and
`terminationGracePeriodSeconds`. Give a single container at least this stop grace (`docker run --stop-timeout`, a pod's
`terminationGracePeriodSeconds`), or a stop kills its runs before they report a final status.

For deployment guides, see the [meshStack documentation](https://docs.meshcloud.io/guides/platform-ecosystem/how-to-run-building-block-runners/).
We highly recommend creating a new runner via the meshStack UI as it will fully explain and generate the configuration for you.

## Configuration

The runner reads `runner-config.yml` in its working directory, which is `/app` in every image, or the file that
`RUNNER_CONFIG_FILE` names. No image contains one. An environment variable overrides its key of the file.
[`internal/config/testdata/runner-config.yml`](internal/config/testdata/runner-config.yml) is an example with every key.

| Key | Environment variable | Default | Meaning |
|---|---|---|---|
| `uuid` | `RUNNER_UUID` |  | The uuid of the runner in meshStack |
| `meshstack.endpoint` | `MESHSTACK_ENDPOINT` |  | The URL of the meshStack API |
| `meshstack.apiKey.clientId`, `meshstack.apiKey.clientSecret` | `MESHSTACK_API_KEY`, `MESHSTACK_API_SECRET` |  | The API key of the runner |
| `meshstack.profile` | `MESHSTACK_PROFILE` |  | A stored profile of the meshStack CLI, for a runner on a developer machine |
| `meshstack.workspace` | `MESHSTACK_WORKSPACE` |  | The workspace that owns the runner, needed by `dispatcher.register` |
| `crypto.privateKeys` | `RUNNER_CRYPTO_PRIVATE_KEYS` |  | Private keys as PEM. Inline keys and key files together take exactly one key |
| `crypto.privateKeyFiles` | `RUNNER_CRYPTO_PRIVATE_KEY_FILES` | `runner-private.pem` | Files with a private key each |
| `dispatcher.forTypes` | `RUNNER_DISPATCHER_FOR_TYPES` | set by each image | One of `ALL`, `tf`, `manual`, `github`, `gitlab`, `azureDevOps` |
| `dispatcher.register.displayName` | `RUNNER_DISPATCHER_REGISTER_DISPLAY_NAME` |  | Updates the runner in meshStack with this name, the public key and the type on start |
| `dispatcher.pollingInterval` | `RUNNER_DISPATCHER_POLLING_INTERVAL` | `10s` | Time between two claims |
| `dispatcher.maxConcurrentJobs` | `RUNNER_DISPATCHER_MAX_CONCURRENT_JOBS` | `20` | Runs at the same time, a negative value means unlimited |
| `dispatcher.timeout` | `RUNNER_DISPATCHER_TIMEOUT` | `60m` | Deadline of a run |
| `dispatcher.killGrace` | `RUNNER_DISPATCHER_KILL_GRACE` | `120s` | Time a stopped run gets before it is killed, at least `tf.abortGrace` + 30s |
| `dispatcher.keycloakWif.{url,realm,clientId,clientSecret,tokenLifetime}` | `RUNNER_DISPATCHER_KEYCLOAK_WIF_{URL,REALM,CLIENT_ID,CLIENT_SECRET,TOKEN_LIFETIME}` | lifetime `90m` | Issues a Keycloak workload identity token for each tf run |
| `metrics.port` | `RUNNER_METRICS_PORT` | `2112` in every image | Port of `/healthz` and `/metrics` |
| `tf.cacheDir` | `RUNNER_TF_CACHE_DIR` | below the temp dir | Holds the installed tofu and terraform versions |
| `tf.initTimeout` | `RUNNER_TF_INIT_TIMEOUT` | `5m` | One deadline for the clone, the install, `tofu init` and the workspace selection |
| `tf.initRetries` | `RUNNER_TF_INIT_RETRIES` | `3` | Retries of a clone after a network failure and of each registry request and provider download |
| `tf.abortGrace` | `RUNNER_TF_ABORT_GRACE` | `60s` | Time tofu gets after an abort to save its state |
| `tf.insecureHostKeys` | `RUNNER_TF_INSECURE_HOST_KEYS` | `false` | Skips the SSH host key check of a git clone |
| `k8s.namespace` | `RUNNER_K8S_NAMESPACE` | the namespace of the pod | Namespace of the Jobs of the run-controller |
| `k8s.jobTtl` | `RUNNER_K8S_JOB_TTL` | `60m` | Time a finished Job stays |
| `k8s.jobSpecTemplates.<type>` |  |  | A Job template per runner type. `ALL` merges onto the built-in template and each type onto `ALL` |

`RUNNER_LOG_LEVEL` (`debug`, `info`, `warn`, `error`) and `RUNNER_LOG_FORMAT` (`text`, `json`) have no key in the file.

To clone from an SSH host that [`containers/known_hosts`](containers/known_hosts) does not list, mount your own file
over `/app/known_hosts` of the tf image or point `SSH_KNOWN_HOSTS` to it. `tf.insecureHostKeys` is the last resort.

The config layouts of the runners before version 2 still work, together with `RUNNER_API_URL`, `RUNNER_API_CLIENT_ID`
and `RUNNER_API_CLIENT_SECRET`, and the runner logs a warning for them on start.

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

