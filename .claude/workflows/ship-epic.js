export const meta = {
  name: 'ship-epic',
  description: 'Break a large epic into small, independently-shippable features and ship each as its own reviewed PR via ship-feature',
  whenToUse: 'When a big body of work should be decomposed into independently-achievable features, each taken to its own PR with no human in the loop. Pass the epic description as args (a string, or {description, base?}).',
  phases: [
    { title: 'Decompose', detail: 'split the epic into small, independently-shippable features in dependency order' },
    { title: 'Ship features', detail: 'run the ship-feature workflow for each slice, one independent PR per feature' },
  ],
}

// ---------------------------------------------------------------------------
// Contract: args may be a plain string (the epic) or {description, base?}
// ---------------------------------------------------------------------------
const epicSpec = (typeof args === 'string') ? { description: args } : (args || {})
const EPIC = epicSpec.description || epicSpec.epic || epicSpec.task
const BASE = epicSpec.base || 'main'

if (!EPIC) {
  log('No epic description provided (pass a string or {description}). Nothing to do.')
  return { error: 'missing_epic_description' }
}

const REPO = '/Users/zak/gidm'

const DECOMPOSE_SCHEMA = {
  type: 'object',
  additionalProperties: false,
  required: ['features'],
  properties: {
    overview: { type: 'string' },
    features: {
      type: 'array',
      items: {
        type: 'object',
        additionalProperties: false,
        required: ['title', 'description', 'order'],
        properties: {
          title: { type: 'string' },
          description: { type: 'string', description: 'A self-contained feature spec a downstream ship-feature run can implement without seeing the others' },
          rationale: { type: 'string' },
          order: { type: 'number', description: '1-based sequence; any feature depending on another comes later' },
          depends_on: { type: 'array', items: { type: 'string' }, description: 'Titles of features this one depends on' },
        },
      },
    },
  },
}

function decomposePrompt(epic) {
  return `Break this epic into the SMALLEST set of independently-shippable features for the gidm codebase: a pure-Go, high-performance download manager (module github.com/K-RED90/gidm). You are in ${REPO} — read CLAUDE.md and explore the relevant code first.

<epic>
${epic}
</epic>

Honor gidm's core principles when slicing: efficiency first, zero-alloc hot path, std-lib-first / minimal dependencies, nothing hardcoded (config-driven), everything tested under -race, security by design. Respect the layering engine -> daemon (gidmd) -> clients (CLI, desktop, Chrome extension) and the roadmap milestones (M1 core engine, M2 daemon+CLI, M3 dynamic segmentation/rate limiting/scheduler, M4 extension+host, M5 desktop).

Each feature must be:
- Independently reviewable and mergeable as its own PR off ${BASE} (small, coherent, one concern).
- Self-contained: its 'description' is a COMPLETE spec a downstream ship-feature workflow can implement WITHOUT seeing the others (each feature is built fresh off ${BASE}, so it cannot rely on another unmerged feature's code).
- Ordered: assign a 1-based 'order' so any feature depending on another comes later, and record depends_on by title.

Prefer 3-8 vertical slices. Keep them genuinely achievable and honor the user's preference for clean, minimal, fast code on a std-lib-first stack. Read-only: do not edit files.`
}

// ---------------------------------------------------------------------------
// Flow
// ---------------------------------------------------------------------------
phase('Decompose')
const decomposed = await agent(decomposePrompt(EPIC), { label: 'decompose', phase: 'Decompose', agentType: 'Plan', schema: DECOMPOSE_SCHEMA })
if (!decomposed || !decomposed.features || decomposed.features.length === 0) {
  log('Could not decompose the epic into features.')
  return { error: 'decompose_failed' }
}

const ordered = [...decomposed.features].sort((a, b) => (a.order || 0) - (b.order || 0))
log(`Epic decomposed into ${ordered.length} shippable feature(s). Shipping each as its own PR, in dependency order.`)

phase('Ship features')
const results = []
for (let i = 0; i < ordered.length; i++) {
  const feature = ordered[i]
  log(`Feature ${i + 1}/${ordered.length}: ${feature.title}`)
  let outcome
  try {
    outcome = await workflow('ship-feature', {
      description: `${feature.title}\n\n${feature.description}`,
      title: feature.title,
      base: BASE,
    })
  } catch (err) {
    outcome = { error: String((err && err.message) || err) }
  }
  results.push({ title: feature.title, order: feature.order, ...(outcome || { error: 'no_result' }) })
}

const shipped = results.filter((r) => r.pr_url)
const drafts = shipped.filter((r) => r.is_draft)
log(`Epic done: ${shipped.length}/${ordered.length} feature(s) opened a PR (${drafts.length} draft).`)

return {
  epic: EPIC,
  features: ordered.length,
  shipped: shipped.length,
  drafts: drafts.length,
  prs: shipped.map((r) => ({ title: r.title, pr_url: r.pr_url, is_draft: r.is_draft, review_passed: r.review_passed })),
  results,
}
