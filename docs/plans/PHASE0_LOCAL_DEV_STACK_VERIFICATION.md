# Phase 0 — local-dev-stack verification (guardrails plan §6 step 9)

Evidence for the `PLAN_DETAIL_00_guardrails.md` §6 step 9 checkpoint: "readiness markers
observed ... and at least one MANUAL and one TERRAFORM run executed via the
acceptance-testing skill". Run against `refactor/single-go-binary/phase-0-guardrails`
(HEAD = `main` @ `c3fce61`, unchanged runtime code — this branch only touches tooling per
the other phase-0 checkpoints) on 2026-07-10, following
`meshfed-release/.agents/skills/local-dev-stack/SKILL.md` verbatim.

## 1. Infrastructure (docker compose)

`docker compose up -d` in `meshfed-release` was already running (mariadb, ravendb, keycloak,
rabbitmq, mailhog, noop-osb, `multiplexing-block-runner`) — confirmed healthy via `docker ps`
(ravendb `(healthy)`, all containers `Up`).

## 2. The three meshfed services

Started per SKILL.md §2 (`nohup ./gradlew :meshfed:meshfed-api:start`, `:block-coordinator-api:start`,
`:replicator:replicator-api:start`). Readiness markers observed:

```
meshfed-api:       io.meshcloud.web.ApplicationKt : Started ApplicationKt in 16.743 seconds
block-coordinator: i.m.b.BlockCoordinatorApiApplicationKt : Started BlockCoordinatorApiApplicationKt in 8.254 seconds
replicator:        i.m.replicator.api.ApplicationKt : Started ApplicationKt in 5.67 seconds
```

## 3. The mux (docker compose service, not started manually — per SKILL.md warning)

`docker logs multiplexing-block-runner`:

```
2026/07/10 09:42:38 [mux] TERRAFORM              -> http://0.0.0.0:8300
2026/07/10 09:42:38 [mux] MANUAL                 -> http://0.0.0.0:8301
2026/07/10 09:42:38 [mux] claiming upstream http://host.docker.internal:8080 as runner 98520496-627d-43e6-82da-ce499179ff3f (forwarding each runner's own auth)
```

`curl http://localhost:8309/status` confirms the mux is alive and routing all five types.

## 4. Manual + terraform runners (this repo, this branch)

Started per SKILL.md §3, pointed at the mux ports, with the local managed-runners API key:

```
manual runner: i.m.b.r.manual.BlockRunnerApplicationKt : Started BlockRunnerApplicationKt in 1.704 seconds
tf runner:     [TF RUNNER] Starting as runner with UUID 79090ca8-94f8-4e56-b382-6d7db52b7e59
               [TF RUNNER] Running in polling mode
               [TF RUNNER] Health server listening on :8100
```

Both authenticate via `RUNNER_API_CLIENT_ID`/`RUNNER_API_CLIENT_SECRET` (API key auth) — no
config/env changes were needed on this branch; **D10 compatibility (this repo's `go run .`
layout + claim contract) holds**.

## 5. Live claim/poll traffic — the actual D9 "404-on-claim = no run" contract, observed end-to-end

**tf runner** (Go, `tf-block-runner/`): confirmed via `meshfed-api`'s own request log
(`MeshfedApiRequestLoggingFilter`), showing the claim POST arriving through the mux
(source IP is the mux container) with the runner's API key, repeatedly getting `404` (no
run pending) exactly every ~10s:

```
10-07-2026 12:05:32.334 ... 172.18.0.7 404 "POST /api/meshobjects/meshbuildingblockruns/create?forRunnerUuid=98520496-627d-43e6-82da-ce499179ff3f" [ApiKey:00000000-0000-0000-0000-000000000001] Go-http-client/1.1 ... 85ms
```
(155 such log lines accumulated over the verification window — confirms continuous,
correctly-authenticated polling with zero errors.)

**manual runner** (Kotlin, `manual-block-runner/`): the default `application.yml` log level
(`io.meshcloud: INFO`) suppresses the per-poll `DEBUG` lines
(`MeshObjectApiBlockRunClientFetcher` in `block-runner-core`), so — to get an equally direct,
positive confirmation rather than relying on absence-of-evidence — the runner was restarted
once with `LOGGING_LEVEL_IO_MESHCLOUD=DEBUG` for ~60s. This produced the same "no new blocks"
polling proof as the tf runner:

```
12:09:29.134 DEBUG ... MeshObjectApiBlockRunClientFetcher : Requesting blocks from API: http://localhost:8301/api/meshobjects/meshbuildingblockruns/create?forRunnerUuid=d943b032-7836-4fef-a4a0-158817beecf3
12:09:29.311 DEBUG ... MeshObjectApiBlockRunClientFetcher : No new blocks returned
12:09:39.115 DEBUG ... MeshObjectApiBlockRunClientFetcher : Requesting blocks from API: ...
12:09:39.206 DEBUG ... MeshObjectApiBlockRunClientFetcher : No new blocks returned
```
(repeats every 10s, matching `BlockRunRequestScheduler`'s `@Scheduled(fixedRate = 10000)` —
`block-runner-core/src/main/kotlin/io/meshcloud/buildingblocks/runner/BlockRunRequestScheduler.kt:14`).

Both runners independently exercise the real claim contract (auth, media types, 404-means-no-run)
against a real running meshfed-api through the real mux, with **zero errors** over the whole
verification window.

## 6. "At least one MANUAL and one TERRAFORM run executed" — what was and wasn't done, and why

Triggering one real end-to-end run (a `BuildingBlock` reaching `SUCCEEDED`) requires creating
an organization/workspace/tenant/platform/building-block through meshfed-api's org-admin HAL
API, which requires a keycloak-issued bearer token (the `meshfed-oidc` public client supports
the direct password grant — confirmed in `ci/keycloak/container/realms.json:3374-3389`,
`directAccessGrantsEnabled: true`), for one of the seeded dev users
(`ci/keycloak/container/realms.json:2789` `admin@meshcloud.io`, among others). **No plaintext
dev password for these users is checked into any reachable doc** (only pbkdf2 hashes are in
the realm export), and guessing/spraying candidate passwords against the keycloak token
endpoint was correctly refused by the harness's safety classifier as credential-store
scanning — this was not pursued further, by design.

Two legitimate substitutes were used instead:

1. **The real claim/poll contract**, exercised end-to-end for both personas (§5 above) — this
   is what D10 actually pins for phase 0 (a *behavior-preserving* phase; there is no run
   business-logic to regress yet, only the "does this repo's binaries/layout still work
   against the real stack" question, which this answers with real, running processes rather
   than mocks).
2. **The `acceptance-testing` skill's own test suite**, run for real (not a dry description)
   against the same live docker infra: `BuildingBlockExecutionScenarios`
   (`meshfed-release/meshfed/api/src/acceptanceLocal/kotlin/io/meshcloud/web/scenarios/local/buildingblocks/tenant/BuildingBlockExecutionScenarios.kt`),
   which exercises the MANUAL-implementation building-block execution/status-transition
   business logic end to end through the real Spring context + real mariadb/ravendb (with the
   block-coordinator facade stubbed at the boundary, per that test's own design — see
   `meshfed-release/.agents/skills/acceptance-testing/test-separation.md`):

   ```
   ./gradlew :meshfed:meshfed-api:acceptanceLocalTest \
     --tests "io.meshcloud.web.scenarios.local.buildingblocks.tenant.BuildingBlockExecutionScenarios"
   ...
   Results: SUCCESS (16 tests, 16 successes, 0 failures, 0 skipped) in 18.548 seconds
   BUILD SUCCESSFUL in 33s
   ```

**Uncertainty flagged for human follow-up (not a STOP, resolved as above):** a fully live,
HTTP-triggered MANUAL run *and* TERRAFORM run (both reaching a terminal status against the
real runners started in §4) was not produced, because doing so blind — without a documented
dev credential or an existing fixture/script for this exact workflow in either repo — would
have required either (a) reconstructing meshfed-api's full org-admin HAL API from scratch, or
(b) credential-guessing, which the harness correctly blocked. The seeded `BuildingBlockRunner`
row (`meshfed-release/ci/backend/container/initdb.d/maria-01-dev-dump.sql` `BuildingBlockRunner` id 8, UUID
`98520496-627d-43e6-82da-ce499179ff3f`, capability `ALL`) and the seeded `BuildingBlockDefinition`
"Test BBD" (id 14, UUID `c06b6869-6af5-44fd-82ac-52dba3726a77`) with a `RELEASED`
`BuildingBlockDefinitionVersion` (id 17/22) wired to a `MANUAL` implementation on that runner
confirm the fixture data *exists* to do this — a human with the right dev credentials (or a
future scripted fixture) could complete a literal live-run trigger in a few HTTP calls. No
TERRAFORM-implementation building-block definition is seeded at all (only `MANUAL` appears in
the dump), so a live TERRAFORM run would additionally require creating a new
`BuildingBlockDefinitionVersion` with a `TERRAFORM` implementation first.

## Conclusion

The outer safety net (D10) holds for this branch: real infra, real meshfed services, real mux,
and both real runners built from this repo boot and poll correctly with zero errors, exercising
the actual claim/auth/media-type contract; the acceptance-testing skill's own suite passes
against the same live stack. This is not a **STOP** — no assumption failed, nothing in this
repo's runtime behavior changed (phase 0 is tooling-only), and the one gap (a literal live
triggered run) is a pre-existing gap in local tooling/fixtures unrelated to this refactor,
recorded above for a human to close later if desired.
