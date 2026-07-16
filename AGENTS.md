# AGENTS.md — meshStack Building Block Runner

Conventions for working in this repo. This is the single source of truth for both AI agents and
humans. Deeper, on-demand procedures live in skills under `.agents/skills/` (symlinked from
`.claude/skills/`) and are referenced from the relevant sections below:
- **`backend-go`** — Go development conventions for this repo; load for any `cmd/*` / `internal/*` work.
- **`commit-messages`** — commit/PR-title conventions; they drive the generated release notes.
- **`local-acceptance`** — live end-to-end validation against a real local meshStack: the in-process `ALL` runner, and the Kubernetes Job dispatcher in minikube.

One Go module that builds one fit-for-purpose binary per runner type (`cmd/tf`, `cmd/manual`,
`cmd/gitlab`, `cmd/azdevops`, `cmd/github`) plus **`cmd/bbrunner`** — the fat binary shipped as the
`run-controller` image, serving the `ALL` type and auto-detecting its dispatcher (in-process superset,
or the in-cluster `internal/k8sjob` Job dispatcher). Design record: [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md).

## Sibling repos

Mirror the GitHub `meshcloud/` org layout locally. This repo owns only runner-specific conventions;
for cross-cutting concerns, defer to the siblings instead of duplicating (and drifting from) them:
- **`../meshfed-release`** — the backend monorepo + house-wide conventions: its `PRINCIPLES.md` +
  `AGENTS.md` "Code Comments" (quality), `write-a-skill` / `writing-instructions` (authoring), and
  `local-dev-stack` (local backend bring-up, used by our `local-acceptance`).
- **`../terraform-provider-meshstack`** — the TF provider; its `acceptance-testing` skill drives runs
  against the local backend and is what `local-acceptance` builds on.

For any cross-cutting concern with no runner-repo skill, read the matching meshfed-release skill and
apply its language-agnostic guidance to Go, ignoring Kotlin/JVM/Gradle specifics.

## Code comments

Keep comments lean: a comment earns its place only by saying what the code cannot — the *why*, a
trade-off, a non-obvious constraint, or a surprise. Do not restate what a name, signature, or type
already conveys; prefer one sharp line over a paragraph. Full guidance: meshfed-release's
`PRINCIPLES.md` + `AGENTS.md` "Code Comments".

## Build, test, gates

- **Gates (all must pass before a change is done):** `task fmt && task lint && task test && task coverage`.
  `task test` runs with `-race`; `task coverage` enforces per-package thresholds in
  [`tools/coverage/thresholds.txt`](tools/coverage/thresholds.txt) (≥90% for gated packages).
- **Build:** `task build`. One `k8s` build tag switches `cmd/bbrunner`: **no tags** builds the in-process superset
  (every runner type via go-func, no k8s dispatch — the local dev binary); **`-tags k8s`** builds the lean
  Job-dispatching `run-controller` image (real Kubernetes-Job dispatcher, no in-process type handlers). `task test`
  runs both modes. See `cmd/bbrunner/registry.go` + `k8s_dispatcher{,_noop}.go` and the single
  multi-target root `Dockerfile` + `docker-bake.hcl` (`task images`).
- **Live end-to-end validation:** the `local-acceptance` skill.
- **Do not push** or open PRs unless asked; capture work in meaningful local commits (`commit-messages`).

## Key directories

- **`cmd/`** — one entrypoint per runner type, plus `cmd/bbrunner` (controller/superset).
- **`internal/`** — shared core: `dispatch` (claim/drain loop), `k8sjob` (Job dispatcher), `tf` /
  `manual` / `gitlab` / `azdevops` / `github` (per-type handlers), `meshapi` (backend client),
  `report`, `secret`, `config`, `observability` (mgmt listener + metrics), `httpclient`, `valuestring`.
- **Root `Dockerfile` + `docker-bake.hcl`** — one multi-target build for every shipped image
  (`task images`); per-type/controller configs live under `cmd/`, k8s manifests under `deploy/run-controller/`.
- **`docs/`** — `ARCHITECTURE.md` (design record) and `DEPRECATIONS.md` (compat surfaces + behavior changes).

## Conventions

- **"Runner type"**, not "persona".
- **Naming:** an acronym of 2+ letters gets only its first letter uppercased — `Id` not `ID`, `Uuid`
  not `UUID` (matches the TF provider).
- **Structured-log attribute keys are camelCase** (`runId`, `runnerUuid`, `serviceAccount`,
  `pollInterval`). Prometheus **metric label names stay snake_case** (`runner_uuid`, `controller_uuid`)
  — a public dashboard surface (`docs/DEPRECATIONS.md` §10), not a log key.
- **Timeouts/durations use `time.Duration`** (yaml keeps int-minute keys, converted at the boundary).
- **Behavior/breaking changes** get a [`docs/DEPRECATIONS.md`](docs/DEPRECATIONS.md) entry; config
  renames keep the old spelling as a deprecation-logged alias.
