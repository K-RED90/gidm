export const meta = {
  name: 'ship-feature',
  description: 'Autonomously plan, review the plan, implement, re-review (correctness/performance/best-practice), and ship one feature as a PR',
  whenToUse: 'When you have a single well-scoped feature to take from idea to a reviewed PR with no human in the loop. Pass the feature description as args (a string, or {description, title?, base?}).',
  phases: [
    { title: 'Plan', detail: 'architect an implementation plan and review it before any code is written' },
    { title: 'Setup', detail: 'stash any WIP and branch off fresh origin/main' },
    { title: 'Implement', detail: 'build the whole feature per the approved plan' },
    { title: 'Review', detail: 'mechanical verify + correctness/performance/best-practice review, with a fix loop' },
    { title: 'Ship', detail: 'commit, push, open a PR (draft if review did not fully pass)' },
    { title: 'Restore', detail: 'return to the original branch and restore the stashed WIP' },
  ],
}

// ---------------------------------------------------------------------------
// Contract: args may be a plain string (the feature) or {description, title?, base?}
// ---------------------------------------------------------------------------
const spec = (typeof args === 'string') ? { description: args } : (args || {})
const FEATURE = spec.description || spec.feature || spec.task
const BASE = spec.base || 'main'
const TITLE_HINT = spec.title || ''

if (!FEATURE) {
  log('No feature description provided (pass a string or {description}). Nothing to ship.')
  return { error: 'missing_feature_description' }
}

const REPO = '/Users/zak/gidm'
const MAX_PLAN_ROUNDS = 2
const MAX_REVIEW_ROUNDS = 3

// ---------------------------------------------------------------------------
// Schemas
// ---------------------------------------------------------------------------
const PLAN_SCHEMA = {
  type: 'object',
  additionalProperties: false,
  required: ['summary', 'branch_name', 'commit_type', 'commit_scope', 'pr_title', 'steps'],
  properties: {
    summary: { type: 'string', description: 'One-paragraph description of the chosen approach' },
    branch_name: { type: 'string', description: 'feat/<slug> or fix/<slug>, short kebab-case slug' },
    commit_type: { type: 'string', enum: ['feat', 'fix', 'refactor', 'perf', 'chore', 'docs', 'style', 'test', 'build', 'ci'] },
    commit_scope: { type: 'string', description: 'Conventional-commit scope, e.g. engine, daemon, cli, config, store, ext, host, desktop' },
    pr_title: { type: 'string', description: 'Conventional-commit-style PR title' },
    steps: {
      type: 'array',
      items: {
        type: 'object',
        additionalProperties: false,
        required: ['title', 'detail', 'files'],
        properties: {
          title: { type: 'string' },
          detail: { type: 'string' },
          files: { type: 'array', items: { type: 'string' } },
        },
      },
    },
    touches_hot_path: { type: 'boolean', description: 'true if the change touches the per-chunk read/write loop or any zero-alloc hot path' },
    risks: { type: 'array', items: { type: 'string' } },
    out_of_scope: { type: 'array', items: { type: 'string' } },
    test_plan: { type: 'string' },
  },
}

const PLAN_REVIEW_SCHEMA = {
  type: 'object',
  additionalProperties: false,
  required: ['approved', 'blocking_issues', 'revised_guidance'],
  properties: {
    approved: { type: 'boolean' },
    score: { type: 'number' },
    blocking_issues: { type: 'array', items: { type: 'string' } },
    suggestions: { type: 'array', items: { type: 'string' } },
    revised_guidance: { type: 'string' },
  },
}

const SETUP_SCHEMA = {
  type: 'object',
  additionalProperties: false,
  required: ['original_branch', 'branch', 'stash_created', 'base_ref'],
  properties: {
    original_branch: { type: 'string' },
    branch: { type: 'string', description: 'Final unique branch name (empty string if setup failed)' },
    stash_created: { type: 'boolean' },
    base_ref: { type: 'string' },
    notes: { type: 'string' },
  },
}

const IMPLEMENT_SCHEMA = {
  type: 'object',
  additionalProperties: false,
  required: ['changed_files', 'summary'],
  properties: {
    changed_files: { type: 'array', items: { type: 'string' }, description: 'Exact paths created/modified, relative to repo root' },
    summary: { type: 'string' },
    notes: { type: 'string' },
    deviations: { type: 'array', items: { type: 'string' } },
  },
}

const REVIEW_SCHEMA = {
  type: 'object',
  additionalProperties: false,
  required: ['findings', 'summary'],
  properties: {
    findings: {
      type: 'array',
      items: {
        type: 'object',
        additionalProperties: false,
        required: ['severity', 'file', 'issue', 'fix'],
        properties: {
          severity: { type: 'string', enum: ['blocker', 'major', 'minor'] },
          file: { type: 'string' },
          location: { type: 'string' },
          issue: { type: 'string' },
          fix: { type: 'string' },
        },
      },
    },
    summary: { type: 'string' },
  },
}

const VERIFY_SCHEMA = {
  type: 'object',
  additionalProperties: false,
  required: ['passed', 'summary'],
  properties: {
    passed: { type: 'boolean' },
    fmt: { type: 'string' },
    vet: { type: 'string' },
    lint: { type: 'string' },
    build: { type: 'string' },
    tests: { type: 'string' },
    vuln: { type: 'string', description: 'govulncheck result' },
    bench: { type: 'string', description: 'Hot-path -benchmem result, or "n/a" if the change does not touch the hot path' },
    new_failures: { type: 'array', items: { type: 'string' } },
    summary: { type: 'string' },
  },
}

const FIX_SCHEMA = {
  type: 'object',
  additionalProperties: false,
  required: ['changed_files', 'addressed', 'summary'],
  properties: {
    changed_files: { type: 'array', items: { type: 'string' } },
    addressed: { type: 'array', items: { type: 'string' } },
    unresolved: { type: 'array', items: { type: 'string' } },
    summary: { type: 'string' },
  },
}

const SHIP_SCHEMA = {
  type: 'object',
  additionalProperties: false,
  required: ['pushed', 'branch'],
  properties: {
    pushed: { type: 'boolean' },
    branch: { type: 'string' },
    commit_sha: { type: 'string' },
    pr_url: { type: 'string' },
    pr_number: { type: 'number' },
    is_draft: { type: 'boolean' },
    error: { type: 'string' },
  },
}

const RESTORE_SCHEMA = {
  type: 'object',
  additionalProperties: false,
  required: ['restored'],
  properties: {
    restored: { type: 'boolean' },
    branch: { type: 'string' },
    stash_popped: { type: 'boolean' },
    notes: { type: 'string' },
  },
}

// ---------------------------------------------------------------------------
// Prompt builders
// ---------------------------------------------------------------------------
function planPrompt(feature, titleHint) {
  return `You are the planning architect for the gidm codebase: a pure-Go, high-performance download manager (a free, faster alternative to IDM). Module path github.com/K-RED90/gidm. You are in ${REPO}. Design a concrete, minimal, correct implementation plan for ONE feature.

<feature>
${feature}
</feature>
${titleHint ? `Working title: ${titleHint}\n` : ''}
Read CLAUDE.md and the relevant existing code before planning. Honor every core principle in CLAUDE.md:
- Efficiency first: this is an I/O-bound app — measure, don't guess; no unnecessary work.
- Zero-alloc hot path: the per-chunk read/write loop must allocate zero bytes per iteration (pooled buffers + io.CopyBuffer), verified by a -benchmem benchmark. Allocate freely only in cold paths (setup, config, UI).
- Std-lib first, minimal dependencies: every new dependency must be audited and justified; the engine currently has one third-party dep (BurntSushi/toml).
- Nothing hardcoded: every tunable lives in internal/config and is overridable (defaults → file → env → flags).
- Everything tested under -race, covering failure modes — not just the happy path.
- Security by design: local-only daemon socket, validated URLs/filenames, bounded redirects, TLS verified by default.
- Comments only where necessary (the non-obvious "why", invariants, wire formats).
Conventions: wrap errors with %w and context; propagate context.Context through all I/O and store calls; segment concurrency is one goroutine per segment writing to its own file offset via WriteAt (no shared write cursor); never write segment progress to SQLite per-chunk (checkpoint periodically off the hot path).

Architecture: engine -> daemon (gidmd, API over a Unix socket) -> clients (CLI, desktop, Chrome extension via gidm-host). The engine never imports daemon/clients and persists only through the engine.Store interface (SQLite via modernc.org/sqlite, pure Go, no CGO).

Your plan must:
- Identify the EXACT files to add/modify and the change each needs.
- Reuse existing repo patterns/helpers instead of inventing parallel ones.
- Stay scoped to one feature; list anything explicitly out of scope.
- Set touches_hot_path correctly (true if it touches the per-chunk read/write loop or any zero-alloc path).
- Specify a Conventional-Commit type + scope, a branch name (feat/<slug> or fix/<slug>), and a Conventional-Commit-style PR title.
- Include a brief test/verification plan grounded in the repo layout (table-driven tests, -race, and a -benchmem benchmark for any hot-path change).

Return ONLY the structured plan via StructuredOutput. Do NOT modify any files.`
}

function planReviewPrompt(feature, plan) {
  return `Critically review this implementation plan BEFORE any code is written.

<feature>${feature}</feature>

<plan>
${JSON.stringify(plan, null, 2)}
</plan>

Judge: correctness of approach, alignment with the existing gidm architecture and CLAUDE.md, scope discipline, performance (especially anything near the hot path or the SQLite store), and adherence to std-lib-first / minimal-deps. Read the referenced files to confirm the plan is grounded in reality (paths exist, patterns match, the layering engine -> daemon -> clients is respected and the engine does not import clients).

Approve ONLY if the plan is sound and ready to implement. Otherwise list specific blocking issues and concrete revised_guidance the planner must apply. Be high-signal — do not nitpick style the later review stages will catch. Read-only: do not edit files.`
}

function planRevisePrompt(feature, plan, review) {
  return `Revise the implementation plan to resolve the reviewer's blocking issues. Keep everything already sound.

<feature>${feature}</feature>

<previous_plan>
${JSON.stringify(plan, null, 2)}
</previous_plan>

<blocking_issues>
${JSON.stringify(review.blocking_issues, null, 2)}
</blocking_issues>

<reviewer_guidance>
${review.revised_guidance}
</reviewer_guidance>

Return the FULL revised plan via StructuredOutput. Do NOT modify files.`
}

function setupPrompt(proposedBranch, base) {
  return `Prepare an isolated branch to build a feature WITHOUT disturbing the user's current work. Run these steps exactly, in order, in the repo root ${REPO}:

1. Record original_branch = output of: git rev-parse --abbrev-ref HEAD
2. If 'git status --porcelain' shows ANY output, stash the user's work-in-progress:
     git stash push -u -m "ship-feature WIP (${proposedBranch})"
   Set stash_created=true. Otherwise stash_created=false. NEVER discard their changes.
3. Fetch the freshest base: git fetch origin ${base}
   If the fetch fails (offline), fall back to local ${base} and note it.
4. Choose a unique branch name starting from "${proposedBranch}". If it already exists locally (git rev-parse --verify <name> succeeds) or on origin (git ls-remote --exit-code --heads origin <name> succeeds), append -2, -3, ... until unique.
5. Create and switch to it off the fetched base:
     git checkout -b <unique-branch> origin/${base}
   (use local ${base} if the fetch failed in step 3).
6. Confirm 'git status' is clean on the new branch.

Report original_branch, the final branch name, stash_created, and base_ref ("origin/${base}" or "${base}"). If stashing or branch creation fails, do NOT proceed: set branch to "" and explain in notes so the workflow can abort safely.`
}

function implementPrompt(feature, plan) {
  return `Implement this feature on the current branch (already prepared and clean). You are in ${REPO}.

<feature>${feature}</feature>

<approved_plan>
${JSON.stringify(plan, null, 2)}
</approved_plan>

Rules (non-negotiable — read CLAUDE.md):
- Efficiency first; zero-alloc hot path (pooled buffers + io.CopyBuffer; never allocate per chunk); allocate freely only in cold paths.
- Std-lib first, minimal dependencies — do NOT add a dependency unless the plan justifies it.
- Nothing hardcoded: every tunable goes in internal/config with defaults -> file -> env -> flags wiring.
- Wrap errors with %w and context: fmt.Errorf("config: load %q: %w", path, err). Propagate context.Context through all I/O and store calls. No silent failures.
- Segment concurrency: one goroutine per segment writing to its own offset via WriteAt; no shared write cursor. Never write segment progress to SQLite per-chunk — checkpoint periodically off the hot path.
- Respect the layering: the engine must not import daemon/clients and persists only through engine.Store.
- Comments only where necessary (non-obvious "why", invariants, wire formats) — no comment noise.
- Tests: add/adjust table-driven tests that run under -race and cover failure modes. For any hot-path change, add or update a -benchmem benchmark proving zero allocations per iteration.
- Implement the WHOLE plan. No dead code, no TODO stubs, no speculative abstractions.
- Do NOT commit, push, or switch branches. Leave the changes in the working tree.

Return via StructuredOutput the EXACT list of files you created or modified (paths relative to repo root), a short summary, and any deviations from the plan with rationale.`
}

function verifyPrompt(fileList, touchesHotPath) {
  const benchStep = touchesHotPath
    ? `- Bench (hot path): run the allocation benchmark, e.g. 'make bench' or 'go test -run=^$ -bench=. -benchmem ./internal/engine/...'. The per-chunk read/write loop MUST report 0 allocs/op. Put any regression to non-zero allocs/op in new_failures. Record the result in bench.`
    : `- Bench: the plan marked this change as NOT touching the hot path. Set bench="n/a" and skip benchmarks unless the diff actually touches the read/write loop (if it does, run the -benchmem benchmark and treat non-zero allocs/op as a new failure).`
  return `Mechanically verify the UNCOMMITTED changes on the current branch. You are in ${REPO}. Files changed: ${fileList || '(discover via git status --porcelain)'}.

Inspect the working tree with 'git diff' and 'git status --porcelain' (changes are not committed yet). Run the project's tooling (prefer the Makefile targets):
- Format: 'gofmt -l <changed .go files>' must print nothing. If it lists files, that is a failure (run 'gofmt -w' would fix it).
- Vet: 'make vet' (go vet ./...).
- Lint: 'make lint' (golangci-lint run, config .golangci.yml). Note: this repo does NOT require doc comments on exported symbols — do not flag their absence.
- Build: 'go build ./...'.
- Tests: 'make test' (go test -race -count=1 ./...). Run the whole suite — it is expected to be green on ${BASE}.
- Vuln: 'make vuln' (govulncheck ./...). This is a CI gate — a new vulnerability finding must block the ship.
${benchStep}

These checks mirror the repo's CI exactly (vet + build + test -race on linux/macOS/windows, golangci-lint, govulncheck) so a passing verify means the PR lands green. CRITICAL: report only failures INTRODUCED by these changes. If a check was already failing on ${BASE} unrelated to this diff, note it but do not count it as a new failure. Put concrete new gofmt/vet/lint/build/test/vuln/bench failures in new_failures. Set passed=false only if the diff introduces a real failure. Read-only apart from running these commands.`
}

function reviewPrompt(lens, fileList, plan) {
  const lensGuide = {
    correctness: 'logic errors, wrong/missing edge cases, data races (it must pass -race), goroutine leaks, unbuffered-channel deadlocks, missing context cancellation/timeout propagation, unhandled errors, wrong WriteAt offsets, resume/checkpoint and integrity (hash/size) bugs, and security gaps (unvalidated URLs/filenames, path traversal, unbounded redirects, disabled TLS verification, the daemon socket escaping local-only). Flag any data-loss or file-corruption risk.',
    performance: 'allocations on the per-chunk read/write hot path (it must be zero-alloc — pooled buffers + io.CopyBuffer), unnecessary copies/conversions, missing buffer reuse, per-chunk SQLite writes (progress must be checkpointed off the hot path, never per-chunk), excessive syscalls, lock contention or false sharing across segment goroutines, unbounded memory or goroutine growth, and missing rate-limit/backpressure where the design needs it.',
    'best-practices': 'CLAUDE.md violations: std-lib-first / unjustified new dependencies, hardcoded tunables that belong in internal/config (defaults -> file -> env -> flags), error wrapping (%w + context), context.Context propagation through I/O and store calls, engine layering (no imports of daemon/clients; persistence only via engine.Store), idiomatic Go and naming, dead code, comment noise vs the "comment only the non-obvious why" rule, and test coverage of failure modes under -race.',
  }
  return `Review ONLY the uncommitted changes on the current branch through the ${lens} lens. You are in ${REPO}. Files changed: ${fileList || '(discover via git status)'}.

Use 'git diff' and 'git status --porcelain' to see the changes (they are NOT committed yet), and read the changed files in full for context. Focus on: ${lensGuide[lens]}

Original plan, for intent:
${JSON.stringify(plan, null, 2)}

Report high-confidence findings only. Each finding: severity (blocker = must fix before ship; major = should fix before ship; minor = nice-to-have), file, location, the issue, and a concrete fix. Prefer precision over volume — do not invent issues. Read-only: do not edit files.`
}

function fixPrompt(outstanding, plan) {
  return `Resolve these blocking review/verification items on the current branch. You are in ${REPO}.

<blocking_items>
${outstanding.map((o, i) => `${i + 1}. ${o}`).join('\n')}
</blocking_items>

Fix each one properly — no suppressions, no //nolint to silence a real finding, no superficial weakening of tests or benchmarks. Keep following CLAUDE.md: zero-alloc hot path, std-lib-first, nothing hardcoded, %w error wrapping, context propagation, tests under -race. Do not expand scope beyond fixing these items while keeping the original plan intact:
${JSON.stringify(plan, null, 2)}

Do NOT commit, push, or switch branches. Return the files you changed, which items you addressed, and any you could not fully resolve (with the reason).`
}

function shipPrompt(ctx) {
  const stagePaths = ctx.changedFiles.map((f) => `'${f}'`).join(' ')
  const draftFlag = ctx.isDraft ? '--draft' : ''
  const draftNote = ctx.isDraft
    ? `\nThis PR is a DRAFT because review did not fully pass. Add an "## Unresolved review items" section to the PR body listing:\n${ctx.outstanding.map((o) => `- ${o}`).join('\n')}\n`
    : ''
  return `Ship the implemented feature from the current branch (${ctx.branch}). You are in ${REPO}. Run exactly:

1. Stage ONLY the feature's files (do NOT 'git add -A' — the user may have unrelated changes):
     git add ${stagePaths}
   Then run 'git status' and confirm nothing unrelated is staged.
2. Commit with a Conventional Commit message. Subject form: "${ctx.plan.commit_type}(${ctx.plan.commit_scope}): <concise summary>". Add a short body explaining what and why.
   IMPORTANT: do NOT add any "Co-Authored-By", "Generated with", or Claude/Anthropic attribution trailer. Plain message only.
3. Push: git push -u origin ${ctx.branch}
4. Open the PR (base ${ctx.base}, head ${ctx.branch}):
     gh pr create --base ${ctx.base} --head ${ctx.branch} --title "${ctx.plan.pr_title}" --body "<body>" ${draftFlag}
   The body must summarize the feature, the approach, and the verification done (gofmt/vet/lint/build/test -race/govulncheck${ctx.touchesHotPath ? ', and the hot-path -benchmem result' : ''}).${draftNote}

Feature context: ${ctx.feature}

Report pushed (bool), commit_sha, pr_url, pr_number, and is_draft (${ctx.isDraft}). If push or PR creation fails, report the error and what you completed.`
}

function restorePrompt(setup) {
  const stashStep = setup.stash_created
    ? '2. Restore their stashed work-in-progress: git stash pop. If pop conflicts, do NOT force — set stash_popped=false and leave the stash intact for manual resolution.'
    : '2. No stash was created; nothing to pop.'
  return `Return the user's environment to how it started. You are in ${REPO}. Run:

1. git checkout ${setup.original_branch}
${stashStep}

Confirm the final branch is ${setup.original_branch}. Report restored, the branch, and stash_popped.`
}

async function restoreEnvironment(setup) {
  return await agent(restorePrompt(setup), { label: 'restore', phase: 'Restore', effort: 'low', schema: RESTORE_SCHEMA })
}

// ---------------------------------------------------------------------------
// Flow
// ---------------------------------------------------------------------------
phase('Plan')
let plan = await agent(planPrompt(FEATURE, TITLE_HINT), { label: 'plan', phase: 'Plan', agentType: 'Plan', schema: PLAN_SCHEMA })
if (!plan) return { error: 'planning_failed' }

for (let round = 1; round <= MAX_PLAN_ROUNDS; round++) {
  const review = await agent(planReviewPrompt(FEATURE, plan), { label: `plan-review-${round}`, phase: 'Plan', agentType: 'Explore', schema: PLAN_REVIEW_SCHEMA })
  if (!review || review.approved) break
  if (round === MAX_PLAN_ROUNDS) {
    log(`Plan still has ${review.blocking_issues.length} open issue(s) after ${round} rounds; proceeding with the best plan.`)
    break
  }
  log(`Plan round ${round}: revising for ${review.blocking_issues.length} issue(s).`)
  const revised = await agent(planRevisePrompt(FEATURE, plan, review), { label: `plan-revise-${round}`, phase: 'Plan', agentType: 'Plan', schema: PLAN_SCHEMA })
  if (revised) plan = revised
}

const touchesHotPath = !!plan.touches_hot_path

phase('Setup')
const setup = await agent(setupPrompt(plan.branch_name, BASE), { label: 'setup-branch', phase: 'Setup', effort: 'low', schema: SETUP_SCHEMA })
if (!setup || !setup.branch) {
  log('Branch setup failed; aborting before any changes were made.')
  return { error: 'setup_failed', branch: '' }
}
const BRANCH = setup.branch

phase('Implement')
const impl = await agent(implementPrompt(FEATURE, plan), { label: 'implement', phase: 'Implement', schema: IMPLEMENT_SCHEMA })
if (!impl) {
  await restoreEnvironment(setup)
  return { error: 'implementation_failed', branch: BRANCH }
}
const changedFiles = new Set(impl.changed_files || [])

phase('Review')
let reviewApproved = false
let outstanding = []
for (let round = 1; round <= MAX_REVIEW_ROUNDS; round++) {
  const fileList = [...changedFiles].join(' ')
  const checks = await parallel([
    () => agent(verifyPrompt(fileList, touchesHotPath), { label: `verify-${round}`, phase: 'Review', agentType: 'Explore', schema: VERIFY_SCHEMA }),
    () => agent(reviewPrompt('correctness', fileList, plan), { label: `review-correctness-${round}`, phase: 'Review', agentType: 'Explore', schema: REVIEW_SCHEMA }),
    () => agent(reviewPrompt('performance', fileList, plan), { label: `review-performance-${round}`, phase: 'Review', agentType: 'Explore', schema: REVIEW_SCHEMA }),
    () => agent(reviewPrompt('best-practices', fileList, plan), { label: `review-bestpractice-${round}`, phase: 'Review', agentType: 'Explore', schema: REVIEW_SCHEMA }),
  ])
  const verify = checks[0]
  const reviews = checks.slice(1).filter(Boolean)
  const blockers = reviews.flatMap((r) => r.findings || []).filter((f) => f.severity === 'blocker' || f.severity === 'major')
  const verifyFailed = !!(verify && verify.passed === false)
  outstanding = [
    ...(verifyFailed ? (verify.new_failures && verify.new_failures.length ? verify.new_failures : ['verification failed']).map((x) => `verify: ${x}`) : []),
    ...blockers.map((f) => `${f.severity} [${f.file}] ${f.issue} -> ${f.fix}`),
  ]
  if (!verifyFailed && blockers.length === 0) {
    reviewApproved = true
    break
  }
  if (round === MAX_REVIEW_ROUNDS) {
    log(`Review still has ${outstanding.length} blocking item(s) after ${round} rounds; shipping as draft.`)
    break
  }
  log(`Review round ${round}: fixing ${outstanding.length} blocking item(s).`)
  const fix = await agent(fixPrompt(outstanding, plan), { label: `fix-${round}`, phase: 'Review', schema: FIX_SCHEMA })
  if (fix) (fix.changed_files || []).forEach((f) => changedFiles.add(f))
}

phase('Ship')
const isDraft = !reviewApproved
const ship = await agent(
  shipPrompt({ branch: BRANCH, base: BASE, plan, changedFiles: [...changedFiles], isDraft, outstanding, feature: FEATURE, touchesHotPath }),
  { label: 'ship', phase: 'Ship', schema: SHIP_SCHEMA },
)

phase('Restore')
const restored = await restoreEnvironment(setup)

return {
  feature: FEATURE,
  branch: BRANCH,
  pr_url: ship && ship.pr_url,
  pr_number: ship && ship.pr_number,
  is_draft: isDraft,
  review_passed: reviewApproved,
  outstanding,
  pushed: !!(ship && ship.pushed),
  restored: !!(restored && restored.restored),
}
