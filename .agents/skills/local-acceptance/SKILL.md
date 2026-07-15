---
name: local-acceptance
description: Validate the single-binary building-block runners live against a real local meshStack — either by running bbrunner as the ALL runner (in-process superset) in place of the compose mux, or by deploying the run-controller into minikube to exercise the Kubernetes Job-dispatch path with locally-built images. Use when live-testing runner behavior end-to-end, reproducing a runner bug against a real backend, validating tf in-process dispatch or the k8s Job dispatcher, or before flipping a runner default.
---

# local-acceptance — live-validate the runners against a real meshStack

Unit tests are hermetic; this skill covers the **end-to-end** path a run actually takes: meshStack
schedules a run → a runner claims it → clones/executes/decrypts → reports status back. It exercises
real OpenTofu, git-over-HTTP, and RSA sensitive-input decryption — the class of bug unit tests miss
(e.g. a zero-valued timeout that only bites the live `tofu init`).

There are **two dispatch paths** to validate, and this skill covers both:
- the **in-process superset** (`RUNNER_DISPATCHER=inprocess`) — [§ Procedure](#procedure) below;
- the **Kubernetes Job dispatcher** (`internal/k8sjob`, one single-run Job per run) — only runs in a
  cluster, so [§ Variant: minikube](#variant-validate-the-kubernetes-job-dispatch-path-in-minikube).

Relies on the sibling-repo layout (`../meshfed-release`, `../terraform-provider-meshstack`).

## The idea: one bbrunner ALL runner replaces the mux

The meshfed `local-dev-stack` topology runs a `multiplexing-block-runner` (mux) plus a separate
runner per type. For this repo we validate the **single binary** instead: `bbrunner` with
`RUNNER_DISPATCHER=inprocess` registers under the seeded magic ALL UUID
(`98520496-627d-43e6-82da-ce499179ff3f`) and dispatches **all five types in-process** — so it stands
in for the whole mux + fan-out. This is also the live evidence for mux retirement
(`CROSS_REPO_TODO.md` X4).

## Procedure

1. **Backend up.** Follow meshfed's `local-dev-stack` skill (`../meshfed-release`) for infra
   (`docker compose up -d`) + the three services (`meshfed-api :8080`, `block-coordinator`,
   `replicator`). **Stop after that — do not start the per-type runners.**
2. **Retire the mux, start bbrunner as ALL.** The mux runs as a compose service; shut it down and
   put our binary in its place (it polls `:8080` directly, so it needs no mux port):
   ```bash
   docker stop multiplexing-block-runner
   cd <building-block-runner>
   # api.url must resolve on the host — see Gotcha 1. Copy the shipped controller config and
   # point api.url at http://localhost:8080:
   sed 's#host\..*internal:8080#localhost:8080#' containers/run-controller/runner-config.yml > /tmp/bbrunner-all-config.yml
   RUNNER_DISPATCHER=inprocess \
     RUNNER_API_CLIENT_ID=00000000-0000-0000-0000-000000000001 \
     RUNNER_API_CLIENT_SECRET=eUp1jPMfM2RyNOjdVRuLmHGOYCvzZrN5 \
     RUNNER_CONFIG_FILE=/tmp/bbrunner-all-config.yml \
     nohup go run ./cmd/bbrunner > /tmp/bbrunner-all.log 2>&1 &
   ```
   Verify in `/tmp/bbrunner-all.log`: it registers as `ALL` and the dispatch loop claims from `:8080`.
3. **Drive runs** with the terraform-provider-meshstack `acceptance-testing` skill. `set -a && source
   .env && set +a`, then target ONE subtest and **kill it hard** rather than waiting on the suite:
   ```bash
   TF_ACC=1 go test -count=1 ./internal/provider/ \
     -run 'TestAccBuildingBlockV2$/04_sensitive_user_input' -v -timeout 240s
   ```
   `TestAccBuildingBlockV2` is the runner-driving test. Good coverage picks: `03_sensitive_input`
   (STATIC secret), `04_sensitive_user_input` (STRING+CODE decrypt end-to-end), `01_workspace` /
   `02_tenant` (basic non-sensitive apply+destroy). Each clones a bare fixture over git-HTTP and
   asserts real decryption — a no-op runner cannot pass them.
4. **Correlate on failure.** bbrunner run log: `/tmp/bbrunner-all.log` (grep `runId=<id>` — see
   Gotcha 3). Backend: `/tmp/meshstack-api.log` (mark the line count before the run, `tail -n +<mark>`
   after). Grep the runner log for `STATUS FAILED`, `error`, `panic`, `context deadline exceeded`.

## Gotchas

1. **`api.url` has no env override on the controller/superset.** Unlike the fit runners, the
   controller/superset does not read `RUNNER_API_URL` for its upstream — it takes `api.url` from the
   config file only. The shipped config points at a minikube/docker host alias, which won't resolve
   from a host process, so you must edit it to `http://localhost:8080` (step 2). Tracked as an open
   question in `PLAN.md`.
2. **All three tf timeouts must be non-zero.** A handler built with only the command timeout set
   leaves init/workspace timeouts at 0 → `context.WithTimeout(ctx, 0)` is born-expired → `tofu init`
   fails "context deadline exceeded" in ~1ms. The superset sets all three; if you wire a tf handler
   by hand, do the same. (This was a real bug this suite caught.)
3. **Grep logs by `runId=`.** Structured log keys are camelCase; the run identifier is `runId`
   (not `run`/`run_id`). Prometheus metric *labels* stay snake_case (`runner_uuid`) — that's a
   separate public surface, not a log key.
4. **RAM.** Gradle build daemons are notorious RAM hogs. Watch `free -h`; `docker compose down && up`
   between runs to clear the ephemeral DB (and stale-data `409`s) and reclaim memory. Kill a hung
   `go test` hard rather than waiting on the timeout.

## Variant: validate the Kubernetes Job-dispatch path in minikube

The procedure above exercises the **in-process** dispatcher. The controller's *other* path — claiming
a run and creating one single-run Kubernetes **Job** per run (`internal/k8sjob`) — only runs in a
cluster. Validate it in local minikube. **Only the `run-controller` and the per-run Jobs run in
minikube; meshStack (meshfed-api, block-coordinator, replicator) stays a local host process.** Build
images **locally and load them into minikube — never push to a registry.**

### How a pod reaches the host-local meshStack

This is the crux of keeping meshfed on the host while the controller/Jobs run in-cluster:

- **`host.minikube.internal`** is a hostname minikube resolves (via CoreDNS's NodeHosts, not the pod's
  `/etc/hosts`) to the **host** from inside every pod — the minikube analogue of `host.docker.internal`.
  So `http://host.minikube.internal:8080` from a pod reaches the gradle meshfed-api on the laptop.
  `localhost`/`127.0.0.1` from a pod is the **pod itself** and will `Connection refused`. (Verified:
  from the controller pod, `wget host.minikube.internal:8080/...` gets an HTTP response, `localhost:8080`
  is refused.)
- **The host service must listen on `0.0.0.0`**, not `127.0.0.1`, or `host.minikube.internal` can't
  reach it. Spring Boot binds `0.0.0.0` by default, so meshfed-api works out of the box.
- **Wiring:** the controller's `api.url` is `http://host.minikube.internal:8080` (baked config). The
  controller stamps that same value into every dispatched Job as `RUNNER_API_URL`, and the single-run
  runners (`manual`/`gitlab`/`azdevops`/`github`) report against `RUNNER_API_URL` (`cfg.Api.Url`,
  `internal/*/loadconfig.go`) — so both the controller **and** the Jobs reach host-meshfed the same way.

### Procedure

1. **Backend up** — meshfed infra (`docker compose up -d`) + the three host services on `:8080`
   (`local-dev-stack` skill). The minikube controller must be the **sole** poller of the magic UUID —
   do NOT also run the mux or a host bbrunner (double-claim).
2. **Start the cluster:** `minikube start` (docker driver). Watch RAM — minikube + gradle stack + image
   builds together are heavy.
3. **Build images into minikube (no push):** `minikube image build` compiles directly inside the
   cluster's container runtime, so nothing is pushed/pulled from ghcr. `imagePullPolicy` is already
   `IfNotPresent` and images are `:main`-tagged, so the loaded local image is used. Tag exactly as the
   config expects:
   ```bash
   minikube image build -t ghcr.io/meshcloud/run-controller:main      -f containers/run-controller/Dockerfile .
   minikube image build -t ghcr.io/meshcloud/manual-block-runner:main -f containers/manual-block-runner/Dockerfile .
   minikube image build -t ghcr.io/meshcloud/tf-block-runner:main     -f containers/tf-block-runner/Dockerfile .
   # build the gitlab/azdevops/github images too only if you will exercise those types
   ```
4. **Deploy the controller:**
   ```bash
   kubectl apply -f containers/run-controller/k8s/rbac.yaml
   kubectl apply -f containers/run-controller/k8s/deployment.yaml
   kubectl set env deployment/run-controller \
     RUNNER_API_CLIENT_ID=00000000-0000-0000-0000-000000000001 \
     RUNNER_API_CLIENT_SECRET=eUp1jPMfM2RyNOjdVRuLmHGOYCvzZrN5
   kubectl rollout status deployment/run-controller --timeout=90s
   ```
   The baked `/app/runner-config.yml` supplies `api.url: http://host.minikube.internal:8080`, the magic
   UUID, crypto keys, and the per-type Job images; the env vars override the API creds.
5. **Confirm registration:** `kubectl logs -l app=run-controller` shows
   `dispatcher auto-detect ... detected=k8sjob`, `controller registered successfully`, and
   `dispatch loop running`.
6. **Drive the full suite** from `../terraform-provider-meshstack` (its `acceptance-testing` skill).
   `TestAccBuildingBlockV2` exercises **both** dispatch types: `01_workspace`/`02_tenant` are
   `manual`-implementation BBs, `03_sensitive_input`/`04_sensitive_user_input` are `terraform` (+ real
   sensitive-input decryption). The tf subtests clone a git fixture the test serves on the host — set
   `MESHSTACK_TF_FIXTURE_HOST` so it is advertised at `host.minikube.internal` (Terraform-fixture note below):
   ```bash
   set -a && source .env && set +a
   MESHSTACK_TF_FIXTURE_HOST=host.minikube.internal \
     TF_ACC=1 go test -count=1 ./internal/provider/ -run 'TestAccBuildingBlockV2' -v -timeout 400s
   ```
   Watch the controller dispatch a Job per run and each Job run to completion:
   ```bash
   kubectl get jobs,pods                              # runner-<runId> Jobs → Complete 1/1
   kubectl logs -l app=run-controller --tail=30       # dispatching run (MANUAL|TERRAFORM) → SA → Secret → Job
   kubectl logs <tf-runner-pod>                       # clone from host.minikube.internal → tf init → STATUS SUCCEEDED
   ```
   **Validated end-to-end:** every `TestAccBuildingBlockV2` subtest PASSes through the minikube
   controller — manual *and* terraform, including real sensitive-input decryption. The tf Job clones
   the fixture via `http://host.minikube.internal:<port>/tf-building-block` and drives tofu against the
   meshStack http backend, reporting `STATUS SUCCEEDED` back to host-meshfed.

### Minikube gotchas

- **Terraform fixture reachability from pods.** The tf-provider acc test serves its terraform fixture
  over git smart-HTTP on the host. The listener already binds `0.0.0.0` (reachable from pods) but
  *advertises* `127.0.0.1` by default, which a pod reads as itself. Set
  `MESHSTACK_TF_FIXTURE_HOST=host.minikube.internal` — an env-guard in the tf-provider's
  `git_http_server_test.go` (default `127.0.0.1`, so host/CI runs are unchanged) that swaps the
  advertised host, so the clone URL handed to the tf Job resolves from the pod (this env-guard is
  merged in the tf-provider). The meshStack http backend the tf run uses resolves via the Job's
  `RUNNER_API_URL` (= `host.minikube.internal:8080`), so no `localhost` callback leaks into the pod.
  (A `manual` BB like `01_workspace` clones nothing, so it needs none of this and is the simplest first
  smoke test.)
- **Job/Secret cleanup.** Each run creates a `run-json-<runId>` Secret owned by its Job (owner-ref →
  cascades on Job delete); the ServiceAccount is per-BBD and reused. The example Deployment sets no Job
  TTL, so finished Jobs linger until deleted.
- **Teardown:** `minikube delete` removes the cluster and all loaded images in one shot.

## Restore the normal topology

`docker start multiplexing-block-runner` and stop the bbrunner process (`pkill -f 'cmd/bbrunner' ||
true`) to hand type-fan-out back to the mux.
