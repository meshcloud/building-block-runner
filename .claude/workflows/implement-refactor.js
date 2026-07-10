export const meta = {
  name: 'implement-refactor',
  description:
    'Drive the single-Go-binary refactor phase by phase: verify each phase\'s STOP-gate assumptions, fan out parallel sub-agents (haiku/sonnet) partitioned to disjoint files, then reconcile with an opus consolidation agent into one always-green single-commit PR. Every gate outcome is recorded in a decision ledger; a tripped STOP halts (never coded around) and a final opus agent writes a quirks/surprises run-log for human review. Runs all phases by default; pass args.phase to run one.',
  phases: [
    { title: 'Verify STOP gates' },
    { title: 'Fan-out implement' },
    { title: 'Consolidate' },
    { title: 'Run-log' },
  ],
}

// ---------------------------------------------------------------------------
// This workflow orchestrates ONLY. It never edits the planned gates, STOP
// markers or exit criteria in the PLAN_DETAIL_*.md files — it verifies them,
// records the outcome, and halts on a real failure (§5: "STOP: do not code
// around it"). The gate *meaning* lives in the plans; this script obeys it.
// ---------------------------------------------------------------------------

const ROOT = '/home/agr/git/meshcloud/building-block-runner'

// Shared standing rules injected into every implementer/verifier prompt.
// Mirrors PLAN_HIGH_LEVEL.md §3 (P1–P8) + §5 (stacked PRs, STOP rule).
const STANDING = (spec) => `
You are implementing part of the single-Go-binary refactor in ${ROOT}.
BEFORE any code: (1) read PLAN_HIGH_LEVEL.md §3 (prime directives P1–P8) and §4
(design decisions D1–D16) — they OVERRIDE convenience; (2) read the detail plan
${spec.plan} in full, including its "Assumptions from prior phases" and STOP markers.
Binding: always-green checkpoints (the suite compiles + passes after every checkpoint;
in-scope coverage rises monotonically); scenario/integration tests over unit armadas
(D16); package layout D11; naming P6/P8; misuse-resistant types P8. Quote file:line
evidence for non-obvious claims. Never rename a customer-facing image / env var /
metric / config key without an alias + deprecation (D7/D8/D12).
Autonomy rule: this is a FULL-AUTONOMOUS run — the plans' §5 STOP ("halt for human
replanning") is REPLACED here by: resolve the issue yourself as best you can and keep
going. Record ONLY the calls you are genuinely UNSURE about as uncertainties (with what
you did and why), so a human can look closer afterward — do not log routine resolutions.
Hard limit: never SILENTLY weaken a frozen contract (D9/D10) or a customer-facing surface;
if you must touch one, do it correctly, update every dependent, and record it as a
BLOCKER-severity uncertainty. Repo boundary: never edit files outside ${ROOT}; capture any
required cross-repo change in CROSS_REPO_TODO.md inside this repo as a hand-off note.
Work on branch ${spec.branch} (base ${spec.base}).`.trim()

const UNCERTAINTY = {
  type: 'array',
  description: 'ONLY the judgment calls you are genuinely unsure about — the human review list. Empty is good.',
  items: {
    type: 'object',
    additionalProperties: false,
    properties: {
      severity: { type: 'string', enum: ['blocker', 'judgment-call'], description: 'blocker = touched a frozen/customer-facing contract' },
      item: { type: 'string', description: 'the assumption/conflict/decision in question' },
      whatIDid: { type: 'string', description: 'how you resolved or interpreted it autonomously, and why' },
    },
    required: ['severity', 'item', 'whatIDid'],
  },
}

const GATE_SCHEMA = {
  type: 'object',
  additionalProperties: false,
  properties: {
    verified: { type: 'boolean', description: 'true if every assumption checked out; false if any needed resolving' },
    uncertainties: UNCERTAINTY,
    notes: { type: 'string' },
  },
  required: ['verified', 'uncertainties'],
}

const CONSOLIDATE_SCHEMA = {
  type: 'object',
  additionalProperties: false,
  properties: {
    green: { type: 'boolean', description: 'task test + task lint pass on the consolidated branch' },
    coverage: { type: 'string', description: 'in-scope statement coverage number, or n/a' },
    exitCriteriaMet: { type: 'boolean', description: 'the phase exit criteria from the detail plan all hold' },
    conflictsResolved: { type: 'array', items: { type: 'string' }, description: 'conflicts/rebases resolved autonomously (routine — not the review list)' },
    crossPlanIssues: { type: 'array', items: { type: 'string' }, description: 'inconsistencies vs other PLAN_DETAIL files' },
    sanctionedDeltas: { type: 'array', items: { type: 'string' }, description: 'deliberate flagged behavior changes this phase made (e.g. maxConcurrentJobs 20→10)' },
    uncertainties: UNCERTAINTY,
    prUrls: { type: 'array', items: { type: 'string' }, description: 'draft PR URL(s) opened for this phase' },
    prReady: { type: 'boolean' },
  },
  required: ['green', 'exitCriteriaMet', 'prReady', 'uncertainties'],
}

// --- Phase specs. Model tiering: haiku = mechanical/measurement, sonnet =
// spec-driven bulk implementation, opus = hard reasoning + consolidation. ----
const B = 'refactor/single-go-binary'
const SPECS = {
  '0': {
    plan: 'PLAN_DETAIL_00_guardrails.md', branch: `${B}/phase-0-guardrails`, base: 'main',
    consolidateEffort: 'medium',
    tasks: [
      { label: 'coverage-baseline', model: 'haiku', focus: 'Measure per-package statement coverage across all 3 Go modules and document the real numbers (§3/§4).' },
      { label: 'golangci-v2', model: 'sonnet', focus: 'Add .golangci.yml v2 mirroring the provider repo: gci ordering, govet-in-lint (drop the separate vet target), depguard skeleton (§5.2/§5.3).' },
      { label: 'taskfile', model: 'sonnet', focus: 'Replace the Makefile with Taskfile.yml (task lint/test), behavior-neutral (§5.1).' },
      { label: 'ci-coverage', model: 'sonnet', focus: 'CI coverage publish + threshold plumbing, NOT gating yet; GitHub Actions otherwise untouched (§5.4/§5.5).' },
      { label: 'd9-inventory', model: 'sonnet', focus: 'Seed the D9 untested-behavior inventory (§8) and verify local-dev-stack acceptance runs against this branch (§10).' },
    ],
  },
  '1': {
    plan: 'PLAN_DETAIL_01_tf_characterization_tests.md', branch: `${B}/phase-1-characterization-tests`, base: `${B}/phase-0-guardrails`,
    consolidateEffort: 'high',
    first: { label: 'cp1-fixtures', model: 'sonnet', focus: 'CP1 only: hermetic fixtures_test.go + shared builders/helpers (local git repo, runDetails builder, encrypted-input helper). This is the shared dependency every parallel CP below reuses — land it green first.' },
    tasks: [
      { label: 'cp2-failpaths', model: 'sonnet', focus: 'CP2: worker fetch/register failure paths + header pins (disjoint _test.go).' },
      { label: 'cp3-singlerun', model: 'sonnet', focus: 'CP3: SingleRunWorker suite mirroring WorkerTestSuite (disjoint _test.go).' },
      { label: 'cp4-inputs-crypto', model: 'sonnet', focus: 'CP4: inputs / crypto / artifact-cap pins (disjoint _test.go).' },
      { label: 'cp-usecases', model: 'sonnet', focus: 'Remaining CPs: the APPLY/DETECT/DESTROY × polling/single-run use-case matrix (§4), saved-plan, backend fallback, env whitelist, workspace naming — as t.Run subtests, disjoint _test.go.' },
      { label: 'bug-inventory', model: 'sonnet', focus: 'Maintain the D13 bug inventory (§6): pin buggy behavior verbatim with // FIXME(bug): markers, NEVER fix here.' },
    ],
  },
  '2': {
    plan: 'PLAN_DETAIL_02_tf_ddd_refactor.md', branch: `${B}/phase-2-tf-ddd-refactor`, base: `${B}/phase-1-characterization-tests`,
    consolidateEffort: 'high',
    // Deliberately low-parallelism: the ≤15-step migration is serial by
    // construction (always-compiling). One opus implementer owns the sequence.
    tasks: [
      { label: 'ddd-migration', model: 'opus', effort: 'high', focus: 'Execute the whole §6 migration sequence (≤15 always-compiling steps): extract domain/application/ports/adapters (D4), eliminate the AppConfig + meshcrypto.Crypto globals via injection, collapse Worker+SingleRunWorker, replace RunContextInfo-in-context. Fix ONLY the two data races structurally (B6/B10, §5.5); every other bug still waits for 2b. Coverage gate stays ≥90%. Turn on go test -race.' },
    ],
  },
  '2b': {
    plan: 'PLAN_DETAIL_02_tf_ddd_refactor.md', branch: `${B}/phase-2b-bugfix`, base: `${B}/phase-2-tf-ddd-refactor`,
    consolidateEffort: 'medium',
    tasks: [
      { label: 'bugfix-pass', model: 'sonnet', focus: 'Phase 2b (§7): work the phase-1 bug inventory — flip each FIXME(bug) pin to assert correct behavior and fix the code. Sequential per bug; keep the suite green throughout.' },
    ],
  },
  '3': {
    plan: 'PLAN_DETAIL_03_shared_core.md', branch: `${B}/phase-3-shared-core`, base: `${B}/phase-2b-bugfix`,
    consolidateEffort: 'high',
    tasks: [
      { label: 'meshapi-consolidate', model: 'opus', effort: 'high', focus: 'The hard one: consolidate the client (§5.2) — diff controller/runapi.go + registration.go vs meshapi.Client, merge, adopt the provider retry/backoff + Logger seam (D3). Judgment-heavy; keep DTOs/naming aligned for the future SDK merge.' },
      { label: 'config-loader', model: 'sonnet', focus: 'Shared config package (§5.3): two-file deep-merge, ${VAR} interpolation, typed struct fields, fail-fast on unconsumed legacy-prefixed env (D7).' },
      { label: 'report-facility', model: 'sonnet', focus: 'Shared reporting facility (§5.4): the single Reporter interface, Observer 10s ticker (tf-only), RunStatus/StepStatus — races already fixed in phase 2 shapes.' },
      { label: 'meshapitest', model: 'sonnet', focus: 'meshapitest package (§5.7): httptest-based meshfed-API mock server reused by phases 5/6/7.' },
      { label: 'registration', model: 'sonnet', focus: 'Registration consolidation outlook (§5.5) — what this phase builds vs defers to D5.' },
    ],
  },
  '4': {
    plan: 'PLAN_DETAIL_04_single_binary.md', branch: `${B}/phase-4-single-binary`, base: `${B}/phase-3-shared-core`,
    consolidateEffort: 'high',
    first: { label: 'module-move', model: 'opus', effort: 'high', focus: 'Atomic layout migration (§4.6/§7.1): move go.mod to repo root, drop ./runner, delete go.work + go.work.sum. Preserve the coverage gate across the git mv (§7.1) — this is the fragile sequential prerequisite; everything below bases on it.' },
    tasks: [
      { label: 'cmd-tf-bbrunner', model: 'opus', effort: 'high', focus: 'cmd/tf (fit binary) + cmd/bbrunner (the superset = run-controller, k8s dispatch, behavior-preserving; auto-detect + InProcess land in phase 5) — wiring only, D11 (§4.1).' },
      { label: 'mgmt-package', model: 'sonnet', focus: 'internal/mgmt: unify /healthz + /metrics on MANAGEMENT_PORT with per-persona defaults + new standalone-runner metrics (D12, §4.3).' },
      { label: 'dockerfiles', model: 'sonnet', focus: 'Per-app Dockerfiles, direct entrypoints, no symlinks (D8, §4.4).' },
      { label: 'ci-release', model: 'sonnet', focus: 'Release/CI matrix building binaries via go build ./cmd/... → N images (§4.5).' },
      { label: 'build-identity', model: 'haiku', focus: 'Single build/identity+version package (§4.2).' },
      { label: 'meshfed-handoff', model: 'sonnet', focus: 'Cross-repo touch points affect meshfed-release local-dev-stack (§9), but this workflow MUST NOT edit files outside this repo. Instead write/append CROSS_REPO_TODO.md IN THIS REPO listing the exact meshfed-release doc edits needed (files, before/after, why) as a hand-off for that repo\'s owner.' },
    ],
  },
  '5': {
    plan: 'PLAN_DETAIL_05_dispatcher.md', branch: `${B}/phase-5-dispatcher`, base: `${B}/phase-4-single-binary`,
    consolidateEffort: 'high',
    tasks: [
      { label: 'dispatcher-inproc', model: 'opus', effort: 'high', focus: 'Dispatcher/RunHandler interfaces (§4.1/4.2), extract KubernetesJobDispatcher, add InProcessDispatcher (per-run decrypt → runToken-only reporting, per-run workdirs, TfBinaries version-download lock). In-process secret/auth model (risk #5, §8). Judgment-heavy.' },
      { label: 'concurrency-tests', model: 'sonnet', focus: 'One named test per concurrency hazard in the §7 inventory (risk #4). Disjoint _test.go.' },
      { label: 'capability-config', model: 'sonnet', focus: 'Explicit capability config (concrete type or ALL) + claim-and-fail-fast for unhandled types using process creds (D5, §4.4/§10.1).' },
      { label: 'controller-dissolve', model: 'sonnet', focus: 'Dissolve internal/controller into the dispatch package per D11 (§5).' },
    ],
  },
  // Phase 6: 06A is the template (must land first, defines shared interfaces);
  // 06B/06C/06D are authored in parallel worktrees off 06A, then the opus
  // consolidator stacks them in order (rebase B→C→D) and runs the fit-checks.
  '6': {
    plan: 'PLAN_DETAIL_06_kotlin_ports_umbrella.md', branch: `${B}/phase-6a-manual`, base: `${B}/phase-5-dispatcher`,
    consolidateEffort: 'high',
    consolidateNote:
      'These three ports were built in parallel worktrees off phase-6a-manual (already committed). Stack them AUTONOMOUSLY in the frozen order manual→gitlab→azdevops→github: rebase 6b onto 6a, 6c onto 6b, 6d onto 6c, resolving every rebase conflict yourself (they will mostly be in shared wiring — registration, config, cmd/*). Run each sub-plan\'s Template fit-check (umbrella §6). If a SHARED interface must change to make the ports cohere, change it and propagate the change to the 06A/plan-05 artifacts and ALL dependents so the tree stays green — then record it as a judgment-call uncertainty (blocker if it touches a frozen contract). Do NOT halt. Verify the per-runner secret-leak tests and the Gradle-shrink/Kotlin-removal steps. Run go test ./... + task lint after each rebase step; a green stack at 6d is the bar. Open FOUR draft PRs with the stacked base chain: 6a base phase-5-dispatcher, 6b base phase-6a-manual, 6c base phase-6b-gitlab, 6d base phase-6c-azdevops.',
    first: { label: '6a-manual-template', model: 'opus', effort: 'high', commit: true, focus: 'Port PLAN_DETAIL_06A_manual.md AND establish every template artifact the other three inherit (umbrella §6): block-runner-core wire pins, the event-driven reporting seam over report, config compat mechanics (blockrunner: yaml, private-key order, SPRING_PROFILES_ACTIVE alias), the external-API error type, Dockerfile/Gradle-shrink recipe, and the fit review (STOP-D). Kotlin-tests-first then port truthfully (D6/D16).' },
    tasks: [
      { label: '6b-gitlab', model: 'sonnet', isolation: 'worktree', branch: `${B}/phase-6b-gitlab`, plan: 'PLAN_DETAIL_06B_gitlab.md', focus: 'Port gitlab per PLAN_DETAIL_06B_gitlab.md reusing the 06A template unchanged; async handover, one external POST, secret asymmetry (DecryptInputs, leak test), ExternalCallError first consumer. Include the Template fit-check table (§4).' },
      { label: '6c-azdevops', model: 'sonnet', isolation: 'worktree', branch: `${B}/phase-6c-azdevops`, plan: 'PLAN_DETAIL_06C_azdevops.md', focus: 'Port azure-devops per PLAN_DETAIL_06C_azdevops.md: sync polling loop + stage-step fan-out, on the 06A template. Include the Template fit-check table.' },
      { label: '6d-github', model: 'opus', effort: 'high', isolation: 'worktree', branch: `${B}/phase-6d-github`, plan: 'PLAN_DETAIL_06D_github.md', focus: 'Port github per PLAN_DETAIL_06D_github.md — the hardest: App-JWT auth chain (stdlib), two token exchanges, dual input modes, unsupported-input heuristics, sync poller. Include the Template fit-check table.' },
    ],
  },
  '7': {
    plan: 'PLAN_DETAIL_07_cleanup.md', branch: `${B}/phase-7-cleanup`, base: `${B}/phase-6a-manual`,
    consolidateEffort: 'high',
    tasks: [
      { label: 'docs-rewrite', model: 'sonnet', focus: 'Rewrite README + docs/ARCHITECTURE.md for the final architecture (§6/§9.1).' },
      { label: 'ci-reshape', model: 'sonnet', focus: 'Reshape GitHub Actions into Go-only CI incl. docker image builds; the JVM/Gradle legs die here (§5).' },
      { label: 'slog-migration', model: 'opus', effort: 'high', focus: 'Migrate tf/tfrun to slog and handle the SystemMessage hazard (§8) — the one real risk in cleanup; judgment-heavy.' },
      { label: 'deprecation-ledger', model: 'sonnet', focus: 'Accumulated alias inventory + operator-facing deprecation timeline (§7).' },
      { label: 'plan-disposition', model: 'haiku', focus: 'Move PLAN_*.md into docs/plans/ (do not delete) per §9.2.' },
    ],
  },
}

const DEFAULT_ORDER = ['0', '1', '2', '2b', '3', '4', '5', '6', '7']

function verifyPrompt(spec) {
  return `${STANDING(spec)}

TASK: STOP-gate check for this phase — verification, minimal edits only.
Open ${spec.plan}, read its "Assumptions from prior phases" section, and for EACH
assumption run its stated verification step (the command / the file:line to read / the
test to run). Set verified=false if any failed. Where one fails, resolve or reinterpret
it autonomously so the phase can proceed, and record it in uncertainties[] ONLY if you
are genuinely unsure your resolution is right (severity=judgment-call, or =blocker if it
touches a frozen/customer-facing contract). Do not halt; do not log assumptions that
verified cleanly.`
}

function gitSetupPrompt(spec) {
  return `${STANDING(spec)}

TASK: git plumbing only, no code. Ensure branch ${spec.branch} exists and is checked out
off its base ${spec.base} (fetch/create if missing; if it already exists, check it out).
Do not commit anything. Report the current branch + HEAD.`
}

function taskPrompt(spec, t, prefaceNote) {
  const plan = t.plan || spec.plan
  // Git discipline: same-tree parallel siblings must NOT commit (they'd race on
  // the shared branch) — the consolidator commits. A worktree-isolated task owns
  // its branch, so it commits there. The `first` prereq commits only when its
  // output must be visible to forked worktrees (commit:true).
  let git
  if (t.isolation === 'worktree') {
    git = `\nYou run in an isolated worktree. Create branch ${t.branch} off ${spec.base} inside it and commit your slice there (multiple commits ok; the PR squash-merges). Do NOT touch other personas' packages.`
  } else if (t.commit) {
    git = `\nCommit your work as one commit on ${spec.branch} before returning — forked worktrees depend on it being visible.`
  } else {
    git = `\nDo NOT commit — edit the working tree only; the consolidation agent owns the commit so parallel siblings don't race on the branch.`
  }
  return `${STANDING({ ...spec, plan })}

YOUR SLICE (label ${t.label}): ${t.focus}
Stay inside your slice; write to disjoint files so parallel siblings don't collide.
Land your slice always-green.${git}
Return a terse report: files touched, checkpoints landed, coverage delta, any STOP/quirk.${prefaceNote ? `\n\nShared prerequisite already landed: ${prefaceNote}` : ''}`
}

function consolidatePrompt(spec, results, prefaceResult) {
  const parts = results.filter(Boolean).map((r, i) => `--- sibling ${i + 1} ---\n${typeof r === 'string' ? r : JSON.stringify(r)}`).join('\n\n')
  return `${STANDING(spec)}

TASK: You are the CONSOLIDATION agent (opus) for this phase. The parallel sub-agents
below each landed a disjoint slice. Reconcile them into ONE always-green, single-commit,
reviewable PR on ${spec.branch}:
1. Merge the slices; de-duplicate helpers/fixtures; fix any interface drift between them.
2. Run the gates: 'task test' (with -race where enabled), 'task lint', and the coverage
   threshold. The phase counts as done only when all pass.
3. Check cross-plan consistency: this phase's outputs must honor the contracts other
   PLAN_DETAIL files depend on (their "Assumptions from prior phases" reference this
   phase's promises). List any inconsistency in crossPlanIssues.
4. Confirm each exit criterion from ${spec.plan}. Record every deliberate flagged
   behavior change in sanctionedDeltas.
5. Resolve conflicts, interface drift and rebases AUTONOMOUSLY — do not halt. Record in
   uncertainties[] ONLY the calls you are genuinely unsure about (item + whatIDid + why),
   flagging any frozen/customer-facing contract touch as severity=blocker. Routine
   conflict/rebase resolutions go in conflictsResolved, not the uncertainty list.
6. Only once green: commit the working-tree slices as the phase commit(s) on
   ${spec.branch} (the PR squash-merges to one), then open a DRAFT PR:
   'gh pr create --draft --base ${spec.base} --head ${spec.branch}' with a body summarizing
   the phase + its sanctioned deltas + any uncertainties. Put the URL(s) in prUrls. Do not merge.

CLOSE-GAPS DIRECTIVE (do not just report gaps — close them): the failure mode this
workflow keeps hitting is a green but INCOMPLETE PR — a consolidator that reconciles only
the slices the fan-out happened to produce and defers the phase's remaining plan steps to
uncertainties[] instead of finishing them. Do NOT do that. If a slice left steps from
${spec.plan} unimplemented, IMPLEMENT the missing steps yourself so this phase's stated EXIT
CRITERIA actually hold before you open the PR — as long as the tree stays green ('task test'
and 'task lint' pass, -race where enabled, coverage gates hold). Reinterpreting an
illustrative signature against the code that truly exists is fine; shipping a half-built
phase is not. Record an item in uncertainties[] ONLY when you genuinely cannot complete it
within reason (needs a live meshStack/cluster/Gradle, a frozen customer-facing contract you
must not touch, etc.) and state precisely why. Set exitCriteriaMet=true ONLY when the plan's
exit criteria truly hold. A longer, complete, green PR beats a fast green-but-partial one.
${spec.consolidateNote ? `\nPHASE-SPECIFIC (overrides the single-PR step above): ${spec.consolidateNote}` : ''}
${prefaceResult ? `\nSequential prerequisite result:\n${typeof prefaceResult === 'string' ? prefaceResult : JSON.stringify(prefaceResult)}` : ''}

Sub-agent reports:
${parts}`
}

async function runPhase(PHASE) {
  const spec = SPECS[PHASE]
  if (!spec) throw new Error(`unknown phase '${PHASE}'; known: ${Object.keys(SPECS).join(', ')}`)
  log(`Phase ${PHASE} — ${spec.plan} → ${spec.branch}`)

  const gate = await agent(verifyPrompt(spec), { label: `verify:${PHASE}`, phase: 'Verify STOP gates', model: 'sonnet', schema: GATE_SCHEMA })
  const gateU = (gate && gate.uncertainties && gate.uncertainties.length) || 0
  if (gateU) log(`Phase ${PHASE} gate: resolved ${gateU} uncertain assumption(s) autonomously — recorded for review.`)

  await agent(gitSetupPrompt(spec), { label: `git-setup:${PHASE}`, phase: 'Fan-out implement', model: 'haiku' })

  let prefaceResult = null
  if (spec.first) {
    prefaceResult = await agent(taskPrompt(spec, spec.first), { label: spec.first.label, phase: 'Fan-out implement', model: spec.first.model, effort: spec.first.effort })
  }
  const noteForSiblings = spec.first ? `${spec.first.label} landed (${spec.first.focus})` : null
  const results = await parallel(
    spec.tasks.map((t) => () =>
      agent(taskPrompt(spec, t, noteForSiblings), { label: t.label, phase: 'Fan-out implement', model: t.model, effort: t.effort, isolation: t.isolation }),
    ),
  )

  const consolidation = await agent(consolidatePrompt(spec, results, prefaceResult), {
    label: `consolidate:${PHASE}`, phase: 'Consolidate', model: 'opus', effort: spec.consolidateEffort || 'high', schema: CONSOLIDATE_SCHEMA,
  })
  return { phase: PHASE, branch: spec.branch, gate, consolidation }
}

// --- Drive: one phase if args.phase given, else the whole ordered spine. -----
const single = args && (args.phase !== undefined && args.phase !== null) ? String(args.phase) : null
const order = single ? [single] : DEFAULT_ORDER

const ledger = []
for (const p of order) {
  ledger.push(await runPhase(p))
}

phase('Run-log')
const summary = await agent(
  `You are the run-log author. Write ${ROOT}/PLAN_IMPL_RUN_LOG.md: a well-written,
human-facing record of this full-autonomous run. The run did NOT halt — it resolved
issues autonomously and recorded only the uncertain calls. Your job is to make "where to
look closer" obvious.
LEAD with a "Review this first" section: every uncertainty across all phases (gate +
consolidation), BLOCKER-severity first, each as: phase · item · what the agent did · why
it was unsure. Then per phase, briefly: exit criteria met?, coverage number, draft PR
URL(s), sanctioned/flagged deltas, cross-plan inconsistencies. Keep routine
conflict/rebase resolutions to a one-line count — they are not the review list. Be honest:
surface anything that merely "made it fit". Do not restate the plans.

Ledger (JSON):
${JSON.stringify(ledger, null, 2)}`,
  { label: 'run-log', phase: 'Run-log', model: 'opus', effort: 'medium' },
)

log(`All phases completed. Review PLAN_IMPL_RUN_LOG.md — start with the uncertainties.`)
return { phasesRun: ledger.map((e) => e.phase), runLog: 'PLAN_IMPL_RUN_LOG.md', summary }
