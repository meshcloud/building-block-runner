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
| [`tf-block-runner`](tf-block-runner/)                     | Go       | Executes OpenTofu plans and applies               |
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

- **Go modules** (`run-controller`, `tf-block-runner`, `go-meshapi-client`) are managed via a [Go workspace](go.work). Run `go work sync` from the root if module references change.
- **JVM modules** (`block-runner-core`, `github-block-runner`, `gitlab-block-runner`, `azure-devops-block-runner`, `manual-block-runner`) are managed via [Gradle](build.gradle).

Alongside the runners, the repository contains shared modules:

- [`run-controller`](run-controller/) — Kubernetes controller that executes Building Block Runs via Kubernetes Jobs, supporting all runner implementations with parallel execution
- [`block-runner-core`](block-runner-core/) — shared Kotlin library used by all JVM-based runners
- [`go-meshapi-client`](go-meshapi-client/) — shared Go client for the meshcloud API

Common tasks are available via `make`:

```
make help
```

## Health endpoint

Every runner exposes a `/healthz` endpoint that returns `200 OK` with body `OK`. This is intended for liveness probes in container orchestrators.

Each runner listens on a dedicated default port to avoid conflicts when running multiple runners locally alongside meshStack (which defaults to port 8080):

| Runner                  | Default port |
|-------------------------|--------------|
| `tf-block-runner`       | 8100         |
| `azure-devops-runner`   | 8101         |
| `github-block-runner`   | 8102         |
| `gitlab-block-runner`   | 8103         |
| `manual-block-runner`   | 8104         |

Override the port with the `PORT` environment variable:

```bash
PORT=9000 go run .           # tf-block-runner
PORT=9000 ./gradlew bootRun  # JVM runners
```

If you are running a runner through a Docker image, it will default to PORT=8080 as well. You can still decide to override the `PORT` environment variable in your environment.

## Development

### Prerequisites

- Go 1.22+
- JDK 21+

### Build and test (Go)

```bash
make test          # run all Go tests
make fmt           # format Go code
make vet           # run go vet
make tidy          # tidy go modules
make work-sync     # sync go.work entries
```

### Build and test (JVM)

```bash
./gradlew build
./gradlew test
```

### Run locally

```bash
make start-run-controller    # start run-controller
make start-tf-block-runner   # start tf-block-runner
```

See the individual module READMEs for module-specific instructions:

- [run-controller/README.md](run-controller/README.md)
- [tf-block-runner/README.md](tf-block-runner/README.md)

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

