# Building Block Runner

This repository contains the meshStack **Building Block Runners**: processes that pick up infrastructure
provisioning runs, invoke the underlying executor (Terraform, a CI/CD pipeline, or a manual process), and report the result
back. Those runners ship ready-to-use as a core part of [meshStack](https://meshcloud.io), a sovereign internal developer platform where teams
publish self-service infrastructure units called **Building Blocks**.
This public repository lets you deploy and operate runners yourself on your own infrastructure against your meshStack instance.

## Support and contributing

Building Block Runners are part of the commercial meshStack SaaS platform and fall under its SLA.

- **Bugs:** if you are a customer, open a ticket through [support@meshcloud.io](mailto:support@meshcloud.io), and we
  will help you out as quickly as we can.
- **Feature requests:** open one at our [public feedback board](https://feedback.meshcloud.io).
- **Community and open-source questions:** open a [GitHub issue](https://github.com/meshcloud/building-block-runner/issues)
  right here.
- **Pull requests** are welcome. Please reach out through one of the channels above first, especially for a larger
  change, so that your contribution fits the roadmap and nobody does the same work twice.
  [DEVELOPMENT.md](DEVELOPMENT.md) explains how to build, test and release the runner.
- **Security vulnerabilities:** please **do not open a public issue**. See [SECURITY.md](SECURITY.md) for responsible
  disclosure instructions.

For general information about the meshStack platform and Building Blocks, see the [meshStack documentation](https://docs.meshcloud.io).

## Building a runner

This repository builds one Go binary in [`cmd/runner`](cmd/runner/main.go) and containerizes it using Go build tags.
The main [containers](containers/Dockerfile) are `building-block-runner`, which runs every runner type,
and `run-controller` for dispatching runs as Kubernetes jobs.

| Container                   | Go Build tag  | Description                                                                          |
|-----------------------------|---------------|--------------------------------------------------------------------------------------|
| `building-block-runner`     | none          | Runs every runner type as a child process, usually inside a container                |
| `run-controller`            | `k8s`         | Starts each run as a Kubernetes Job using `building-block-runner` as container image |
| `tf-block-runner`           | `tf`          | Executes OpenTofu plans and applies                                                  |
| `github-block-runner`       | `github`      | Triggers GitHub Actions workflows                                                    |
| `gitlab-block-runner`       | `gitlab`      | Triggers GitLab CI pipelines                                                         |
| `azure-devops-block-runner` | `azuredevops` | Triggers Azure DevOps pipelines                                                      |
| `manual-block-runner`       | `manual`      | Represents manual processes (transfers inputs to outputs)                            |

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

For deployment guides, see the [meshStack documentation](https://docs.meshcloud.io/guides/platform-ecosystem/how-to-run-building-block-runners/).
We highly recommend creating a new runner via the meshStack UI as it will fully explain and generate the configuration for you.

## Configuration

The runner reads `runner-config.yml` in its working directory, which is `/app` in every image, or the file that
`RUNNER_CONFIG_FILE` names. No image contains one. An environment variable overrides its key of the file.
[`internal/config/testdata/runner-config.yml`](internal/config/testdata/runner-config.yml) is an example with every key.

| Key | Environment variable | Default | Meaning |
|---|---|---|---|
| `uuid` | `RUNNER_UUID` |  | The uuid of the runner in meshStack |
| `meshstack.endpoint` | `MESHSTACK_ENDPOINT` |  | The endpoint URL of the meshStack API |
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
| `dispatcher.keycloakWif.{url,realm,clientId,clientSecret,tokenLifetime}` | `RUNNER_DISPATCHER_KEYCLOAK_WIF_`<br>`{URL,REALM,CLIENT_ID,CLIENT_SECRET,TOKEN_LIFETIME}` | lifetime `90m` | Issues a Keycloak workload identity token for each tf run |
| `metrics.port` | `RUNNER_METRICS_PORT` | `2112` in every image | Port of `/healthz` and `/metrics` |

The config layouts of the runners before version 2 still work, together with `RUNNER_API_URL`, `RUNNER_API_CLIENT_ID`
and `RUNNER_API_CLIENT_SECRET`, and the runner logs a warning for them on start.

### OpenTofu/Terraform

These keys apply to a runner that executes OpenTofu or Terraform runs: `building-block-runner` and `tf-block-runner`.

| Key | Environment variable | Default | Meaning |
|---|---|---|---|
| `tf.cacheDir` | `RUNNER_TF_CACHE_DIR` | below the temp dir | Holds the installed tofu and terraform versions |
| `tf.initTimeout` | `RUNNER_TF_INIT_TIMEOUT` | `5m` | One deadline for the clone, the install, `tofu init` and the workspace selection |
| `tf.initRetries` | `RUNNER_TF_INIT_RETRIES` | `3` | Retries of a clone after a network failure and of each registry request and provider download |
| `tf.abortGrace` | `RUNNER_TF_ABORT_GRACE` | `60s` | Time tofu gets after an abort to save its state |
| `tf.insecureHostKeys` | `RUNNER_TF_INSECURE_HOST_KEYS` | `false` | Skips the SSH host key check of a git clone |

To clone from an SSH host that [`containers/known_hosts`](containers/known_hosts) does not list, mount your own file
over `/app/known_hosts` of the tf image or point `SSH_KNOWN_HOSTS` to it. `tf.insecureHostKeys` is the last resort.

### Kubernetes Controller

These keys apply to the `run-controller` only.

| Key | Environment variable | Default | Meaning |
|---|---|---|---|
| `k8s.namespace` | `RUNNER_K8S_NAMESPACE` | the namespace of the pod | Namespace of the Jobs of the run-controller |
| `k8s.jobTtl` | `RUNNER_K8S_JOB_TTL` | `60m` | Time a finished Job stays |
| `k8s.jobSpecTemplates.<type>` |  |  | A Job template per runner type. `ALL` merges onto the built-in template and each type onto `ALL` |

### Logs and Health

The runner logs at the level in `RUNNER_LOG_LEVEL`: `debug`, `info` (default), `warn` or `error`, and in the
format in `RUNNER_LOG_FORMAT`: `text` (default) or `json`. Both are environment variables without a key in the file.
A run process gets both, and `GOCOVERDIR` for a binary built with `-cover`. Each line of a step log, which holds the
tofu output of the customer, is logged at `debug` only.

The runner serves `/healthz` for liveness probes and `/metrics` on the port in `metrics.port` or `RUNNER_METRICS_PORT`,
or in `PORT` when neither is set, and serves neither endpoint without a port. Every image sets `RUNNER_METRICS_PORT=2112`.

## Development

[DEVELOPMENT.md](DEVELOPMENT.md) describes the repository structure, how to build, test and run the runner locally, and
how to release it.
