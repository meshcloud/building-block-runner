# Building Block Runner

This repository contains the open-source meshStack **Building Block Runners** — processes that pick up infrastructure
provisioning runs, invoke the underlying tool (Terraform, a CI/CD pipeline, or a manual process), and report the result
back. Runners are a core part of [meshStack](https://meshcloud.io), a sovereign internal developer platform where teams
publish self-service infrastructure units called **Building Blocks** that developers can order on demand.

meshStack ships with managed runners that work out of the box — no setup required. This repository lets you deploy and
operate runners yourself on your own infrastructure. We believe it is important that you can inspect exactly what runs
in your environment and deploy it on your own terms — full sovereignty over the execution layer, with no black boxes.

This repository is **one Go module** that builds one fit-for-purpose binary per runner **persona**, plus a superset
binary (`bbrunner`) that ships as the `run-controller` image. See [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) for
the full design record.

| Runner (published image)                    | Persona entrypoint | Description                                                              |
|-----------------------------------------------|---------------------|----------------------------------------------------------------------------|
| [`tf-block-runner`](cmd/tf/)                  | `cmd/tf`            | Executes OpenTofu plans and applies                                        |
| [`run-controller`](cmd/bbrunner/)              | `cmd/bbrunner`      | Kubernetes controller that dispatches runs of any type as Jobs; the same binary can also force a single persona to run standalone (`bbrunner <persona>`) |
| [`manual-block-runner`](cmd/manual/)          | `cmd/manual`        | No-op runner for manually managed building blocks                          |
| [`gitlab-block-runner`](cmd/gitlab/)           | `cmd/gitlab`         | Triggers GitLab CI pipelines                                               |
| [`azure-devops-block-runner`](cmd/azdevops/)  | `cmd/azdevops`       | Triggers Azure DevOps pipelines                                             |
| [`github-block-runner`](cmd/github/)          | `cmd/github`         | Triggers GitHub Actions workflows                                          |

All six images are built from the same Go module — one direct binary per image, no shared/multiplexed entrypoint (see
[Repository structure](#repository-structure)).

## Running a runner

Each runner is distributed as a container image and configured via a YAML file. The general steps are:

1. Create a new Building Block Runner in meshStack and create an API key for the runner.
2. Write & configure a `runner-config.yml` with the meshStack API configuration.
3. Deploy the runner container on your infrastructure of choice (Kubernetes, a VM, or locally for testing).
4. Reference the new runner in the Building Block Definition of your choice.

The runner will then poll meshStack for pending runs and execute them.

Configuration is resolved with precedence **compiled-in defaults < shared base YAML < per-runner YAML < environment
variables** (env always wins); the tf runner additionally supports a single-run mode
(`EXECUTION_MODE=single-run`, or the legacy `SPRING_PROFILES_ACTIVE=kubernetes` alias) that executes exactly one run
from a mounted run JSON and exits, used by `run-controller`'s Kubernetes-Job dispatch. See
[Configuration & deprecations](#configuration--deprecations) below and `docs/ARCHITECTURE.md` §5 for the full
reference.

For full configuration reference and deployment guides, see the [meshStack documentation](https://docs.meshcloud.io/guides/platform-ecosystem/how-to-run-building-block-runners/).
We highly recommend creating a new runner via the meshStack UI as it will fully explain and generate the configuration for you.

## Support

Building Block Runners are part of the commercial meshStack SaaS platform. If you are a customer you can open a ticket
through [support@meshcloud.io](mailto:support@meshcloud.io). Building Block Runners fall under the SLA, and we will help
you out as quickly as we can.

If you have any community or open-source questions, you can open a GitHub issue right here.

For general information about the meshStack platform and Building Blocks, see the [meshStack documentation](https://docs.meshcloud.io).

## Repository structure

- **One Go module, rooted at the repository root** (`go.mod`) — no Go workspace, no per-runner submodules.
- [`cmd/`](cmd/) — one `main` package per persona (`cmd/tf`, `cmd/manual`, `cmd/gitlab`, `cmd/azdevops`, `cmd/github`)
  plus the `cmd/bbrunner` superset that *is* the `run-controller` image. Each persona binary links only the
  dependencies it needs (e.g. `cmd/tf` links go-git/OpenTofu but not Kubernetes client-go; the four ported runners
  link neither). `cmd/*` is wiring only — no domain logic lives there.
- [`internal/`](internal/) — shared and domain code, one concept per package (no `util`/`common`/`core` catch-alls):
  `meshapi` (meshStack API client), `crypto`, `config`, `report` (status/log reporting), `mgmt` (health/metrics),
  `dispatch` (claim/dispatch loop + in-process dispatcher), `k8sjob` (Kubernetes Job dispatcher), and one package per
  runner persona (`tf`, `manual`, `gitlab`, `azdevops`, `github`). See `docs/ARCHITECTURE.md` §2 for the full package
  map and its dependency-direction rules.
- [`containers/`](containers/) — one `Dockerfile` + `runner-config.yml` per published image, plus the shared entrypoint
  script and the cross-persona base `runner-config.yml` (deep-merged under each per-image file). The controller's
  example Kubernetes manifests live under [`containers/run-controller/k8s/`](containers/run-controller/k8s/).
- [`tools/coverage/`](tools/coverage/) — the per-package statement-coverage gate (`thresholds.txt` +
  `exclusions.txt`) enforced by `task coverage`.
- [`docs/`](docs/) — [`ARCHITECTURE.md`](docs/ARCHITECTURE.md) (the maintained architecture record) and
  [`docs/plans/`](docs/plans/) (the historical, no-longer-maintained design plans for this refactor).

Common tasks are available via [`task`](https://taskfile.dev):

```
task --list
```

## Management endpoint (health & metrics)

Every runner process exposes a single management listener serving `GET /healthz`
(`200 OK`, body `OK` — intended for liveness probes) and `GET /metrics` (Prometheus
exposition, `runner_*` series — or `run_controller_*` for the controller). Each persona
listens on a dedicated default port so several can run side by side locally:

| Persona                     | Default `MANAGEMENT_PORT` | Metrics series               |
|------------------------------|----------------------------|-------------------------------|
| `tf-block-runner`            | 8100                       | `runner_*`                    |
| `azure-devops-block-runner`  | 8101                       | `runner_*`                    |
| `github-block-runner`        | 8102                       | `runner_*`                    |
| `gitlab-block-runner`        | 8103                       | `runner_*`                    |
| `manual-block-runner`        | 8104                       | `runner_*`                    |
| `run-controller`             | 2112                       | `run_controller_*`            |

Override the port with the `MANAGEMENT_PORT` environment variable:

```bash
MANAGEMENT_PORT=9000 go run ./cmd/tf
```

The legacy `PORT` variable is still honored as a deprecation-logged alias on every persona but `run-controller`
(`MANAGEMENT_PORT` takes precedence); every published image but `run-controller` still bakes `PORT=8080` as its
container default, so an operator's runtime `PORT` override keeps working unchanged. The tf runner's single-run mode
(one Kubernetes Job, one run, then exit) serves no listener — a Job has no liveness/scrape lifecycle.

## Development

### Prerequisites

- Go 1.26 (or `nix develop`, see [`flake.nix`](flake.nix))

### Build and test

```bash
task test          # run all tests (-race)
task lint          # lint with golangci-lint (runs go vet internally; no separate vet task)
task fmt           # format Go code (golangci-lint fmt is the one formatter authority)
task tidy          # tidy the go module
task build         # build every persona binary (go build ./cmd/...)
task coverage      # measure coverage and enforce the per-package gate (tools/coverage/)
```

### Run locally

```bash
task start:run-controller    # go run ./cmd/bbrunner   (controller, dispatches Jobs to a Kubernetes cluster)
task start:tf-block-runner   # go run ./cmd/tf
```

Any other persona can be run directly, either as its own fit binary or forced in-process through the superset:

```bash
go run ./cmd/manual                # standalone manual-block-runner
go run ./cmd/bbrunner manual       # the bbrunner superset, forced to the manual persona
```

The `run-controller` persona (`cmd/bbrunner` with no subcommand) expects a Kubernetes API to dispatch Jobs to; for
local development against minikube, start minikube and point its `runner-config.yml`'s `api.url` at your meshfed
instance (e.g. `http://host.minikube.internal:8080`).

## Configuration & deprecations

Config keys and environment variables are a customer-facing surface: nothing is renamed without keeping the old
spelling as a working, deprecation-logged alias. The current alias set (all still fully supported):

| Deprecated                                                          | Prefer instead                     |
|----------------------------------------------------------------------|-------------------------------------|
| `PORT`                                                                | `MANAGEMENT_PORT`                   |
| `RUNCONTROLLER_CONFIG_FILE`                                          | `RUNNER_CONFIG_FILE`                |
| `SPRING_PROFILES_ACTIVE` containing `kubernetes`                     | `EXECUTION_MODE=single-run`         |
| the `blockrunner:` YAML block (`uuid`, `api`, `auth`, `debugMode`, `privateKey`/`privateKeyFile`, incl. kebab-case `api-key.client-id`) | flat, persona-level YAML keys |
| `logging.*` / `server.*` / `spring.*` YAML blocks                    | ignored (with a startup warning)    |

Every alias is kept for at least the current major version's lifetime (and at minimum 12 months from general
availability of this refactor), whichever is longer — see `docs/ARCHITECTURE.md` §5 for the full timeline and the
rationale for each entry. Removals, if any, are always announced in release notes ahead of time.

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
