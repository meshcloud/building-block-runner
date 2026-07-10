# Building Block Runner

This repository contains the open-source meshStack **Building Block Runners** — processes that pick up infrastructure
provisioning runs, invoke the underlying tool (Terraform, a CI/CD pipeline, or a manual process), and report the result
back. Runners are a core part of [meshStack](https://meshcloud.io), a sovereign internal developer platform where teams
publish self-service infrastructure units called **Building Blocks** that developers can order on demand.

meshStack ships with managed runners that work out of the box — no setup required. This repository lets you deploy and
operate runners yourself on your own infrastructure. We believe it is important that you can inspect exactly what runs
in your environment and deploy it on your own terms — full sovereignty over the execution layer, with no black boxes.

This repository contains multiple runners, one for each supported tool:

| Runner                                                    | Language | Description                                       |
|-----------------------------------------------------------|----------|---------------------------------------------------|
| [`tf-block-runner`](cmd/tf/)                              | Go       | Executes OpenTofu plans and applies               |
| [`github-block-runner`](github-block-runner/)             | Kotlin   | Triggers GitHub Actions workflows                 |
| [`gitlab-block-runner`](gitlab-block-runner/)             | Kotlin   | Triggers GitLab CI pipelines                      |
| [`azure-devops-block-runner`](azure-devops-block-runner/) | Kotlin   | Triggers Azure DevOps pipelines                   |
| [`manual-block-runner`](manual-block-runner/)             | Kotlin   | No-op runner for manually managed building blocks |

## Running a runner

Each runner is distributed as a container image and configured via a YAML file. The general steps are:

1. Create a new Building Block Runner in meshStack and create an API key for the runner.
2. Write & configure a `runner-config.yml` with the meshStack API configuration.
3. Deploy the runner container on your infrastructure of choice (Kubernetes, a VM, or locally for testing).
4. Reference the new runner in the Building Block Definition of your choice.

The runner will then poll meshStack for pending runs and execute them.

For full configuration reference and deployment guides, see the [meshStack documentation](https://docs.meshcloud.io/guides/platform-ecosystem/how-to-run-building-block-runners/).
We highly recommend creating a new runner via the meshStack UI as it will fully explain and generate the configuration for you.

## Support

Building Block Runners are part of the commercial meshStack SaaS platform. If you are a customer you can open a ticket
through [support@meshcloud.io](mailto:support@meshcloud.io). Building Block Runners fall under the SLA, and we will help
you out as quickly as we can.

If you have any community or open-source questions, you can open a GitHub issue right here.

For general information about the meshStack platform and Building Blocks, see the [meshStack documentation](https://docs.meshcloud.io).

## Repository structure

This repository uses two separate build systems in parallel:

- **Go** — a single Go module rooted at the repository root (`go.mod`). Persona entrypoints live under [`cmd/`](cmd/) (`cmd/tf`, `cmd/bbrunner`) and shared code under [`internal/`](internal/) (`internal/{tf,meshapi,crypto,config,report,mgmt,controller,build}`). There is no Go workspace.
- **JVM modules** (`block-runner-core`, `github-block-runner`, `gitlab-block-runner`, `azure-devops-block-runner`, `manual-block-runner`) are managed via [Gradle](build.gradle).

Notable Go components:

- [`cmd/bbrunner`](cmd/bbrunner/) — the `run-controller` image: the Kubernetes controller (executes Building Block Runs via Kubernetes Jobs, supporting all runner implementations with parallel execution) and superset entrypoint; `bbrunner <persona>` forces one persona in-process for local development
- [`cmd/tf`](cmd/tf/) — the lean `tf-block-runner` persona binary
- [`internal/meshapi`](internal/meshapi/) — shared Go client for the meshcloud API
- [`block-runner-core`](block-runner-core/) — shared Kotlin library used by all JVM-based runners

Common tasks are available via [`task`](https://taskfile.dev):

```
task --list
```

## Management endpoint (health & metrics)

Each runner process exposes a single management listener serving `GET /healthz`
(`200 OK`, body `OK` — intended for liveness probes) and `GET /metrics` (Prometheus
exposition). Each runner listens on a dedicated default port to avoid conflicts when
running multiple runners locally alongside meshStack (which defaults to port 8080):

| Runner                  | Default port | Serves                                  |
|-------------------------|--------------|-----------------------------------------|
| `tf-block-runner`       | 8100         | `/healthz` + `/metrics` (`runner_*`)    |
| `run-controller`        | 2112         | `/healthz` + `/metrics` (`run_controller_*`) |
| `azure-devops-runner`   | 8101         | `/healthz`                              |
| `github-block-runner`   | 8102         | `/healthz`                              |
| `gitlab-block-runner`   | 8103         | `/healthz`                              |
| `manual-block-runner`   | 8104         | `/healthz`                              |

Override the port with the `MANAGEMENT_PORT` environment variable:

```bash
MANAGEMENT_PORT=9000 go run ./cmd/tf   # tf-block-runner
MANAGEMENT_PORT=9000 ./gradlew bootRun # JVM runners (once ported)
```

On the `tf-block-runner` persona the legacy `PORT` variable is still honored as a
deprecation-logged alias (`MANAGEMENT_PORT` takes precedence). The `tf-block-runner`
Docker image keeps `PORT=8080` as its baked default, so a runtime `PORT` override
continues to work; the `run-controller` persona never read `PORT`. The single-run
mode of `tf-block-runner` serves no listener (short-lived Job pods, no probe).

## Development

### Prerequisites

- Go 1.22+
- JDK 21+

### Build and test (Go)

```bash
task test          # run all Go tests
task lint          # lint (golangci-lint; runs go vet internally, so there is no separate vet task)
task fmt           # format Go code
task tidy          # tidy the go module
task build         # build every persona binary (cmd/tf, cmd/bbrunner)
task coverage      # measure and report test coverage
```

### Build and test (JVM)

```bash
./gradlew build
./gradlew test
```

### Run locally

```bash
task start:run-controller    # go run ./cmd/bbrunner (controller/superset, auto/default mode)
task start:tf-block-runner   # go run ./cmd/tf
```

Equivalently, run a persona directly: `go run ./cmd/tf` (the standalone tf runner) or
`go run ./cmd/bbrunner tf` (the superset forcing the tf persona in-process). The
`run-controller` persona expects an in-cluster Kubernetes API in its default mode; for
local development against minikube, start minikube and point its `runner-config.yml`
`api.url` at your meshfed instance (e.g. `http://host.minikube.internal:8080`).

## Release

Releases are cut from GitHub — no local checkout or manual tagging needed:

1. Go to **Actions → Release → Run workflow** (on the `main` branch).
2. Pick the **bump**:
   - `auto` (default) — derives the next version from the conventional commits
     since the last release (`feat:` → minor, `fix:`/`chore:`/… → patch,
     `feat!:`/`BREAKING CHANGE` → major).
   - `patch` / `minor` / `major` — force a specific bump.
   - Or set **version** to an explicit value (e.g. `v1.2.3`) to override the bump.
3. Run it. The workflow computes the next version, creates the git tag, publishes
   a **GitHub Release** with notes generated from the conventional commits, and
   then builds and pushes the Docker images.

Release notes quality depends on [Conventional Commits](https://www.conventionalcommits.org/)
— keep using `feat:`, `fix:`, `chore:`, etc. The grouping is configured in
[cliff.toml](cliff.toml).

> Pushing a `v*` tag manually still works as a fallback (it triggers
> [build-images.yml](.github/workflows/build-images.yml) directly), but it will
> **not** create a GitHub Release with notes — prefer the workflow above.

## Contributing

For **bugs**, please reach out via [support@meshcloud.io](mailto:support@meshcloud.io) — runner issues are covered under
the meshStack SLA and will be handled as quickly as possible.

For **feature requests**, open one at our [public feedback board](https://feedback.meshcloud.io)

Pull requests are welcome, but reaching out through the above channels first is preferred — it avoids duplicate effort
and ensures your contribution aligns with the roadmap. For larger changes, please get in touch before investing significant time.

## Security

If you discover a security vulnerability, please **do not open a public issue**. See [SECURITY.md](SECURITY.md) for responsible disclosure instructions.

