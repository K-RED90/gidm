# Autonomous workflows

Deterministic multi-agent pipelines that take work from an idea to a reviewed
PR with no human in the loop. Each is a JavaScript orchestration script run by
Claude Code's `Workflow` tool — the script controls the flow (loops, fan-out,
fix-loops); subagents do the reading, coding, and reviewing.

## What's here

| Workflow        | Does                                                                                  |
| --------------- | ------------------------------------------------------------------------------------- |
| `ship-feature`  | Plan → review the plan → branch off fresh `origin/main` → implement → multi-lens review + verify (with a fix loop) → commit, push, open a PR → restore your WIP. |
| `ship-epic`     | Decompose a large epic into small, independently-shippable features, then run `ship-feature` for each (one PR per feature, in dependency order). |

## How to run

Just ask Claude in this repo, e.g.:

- "Run the **ship-feature** workflow to add per-segment retry with exponential backoff."
- "Use **ship-epic** to build out M1: segmented download, resume, retries, integrity, and the SQLite store."

Claude calls the `Workflow` tool with the feature/epic text as `args`. You can
watch live progress with `/workflows`.

## What `ship-feature` guarantees

- **Never touches your WIP.** It stashes uncommitted work, branches off fresh
  `origin/main`, and pops the stash back at the end. You're returned to your
  original branch.
- **Enforces CLAUDE.md.** Every agent is told the core principles — zero-alloc
  hot path, std-lib-first, nothing hardcoded, `-race` everywhere, security by
  design — and the review stage checks for violations.
- **Verifies mechanically, mirroring CI.** `gofmt`, `make vet`, `make lint`,
  `go build`, `make test` (`-race`), `make vuln` (`govulncheck`), and for any
  change flagged `touches_hot_path`, a `-benchmem` benchmark proving the
  read/write loop stays **0 allocs/op**. The gate is a superset of
  `.github/workflows/ci.yml`, so a passing verify means the PR lands green.
- **Reviews from three lenses** (correctness, performance, best-practices) in
  parallel, fixes blockers in a loop (up to 3 rounds), and ships as a **draft**
  if anything is still unresolved.
- **Opens a clean PR** with a Conventional-Commit title/message (no bot
  attribution trailer).

## Requirements

- `origin` remote set (→ `github.com/K-RED90/gidm`) and `gh` authenticated.
- `.claude/settings.local.json` grants the permissions agents need to run
  without prompts. It's personal (gitignored) — each contributor keeps their
  own.

## Tuning

Edit the workflow files directly:

- `MAX_PLAN_ROUNDS` / `MAX_REVIEW_ROUNDS` in `ship-feature.js` — how many
  revise/fix rounds before shipping the best effort (as a draft).
- The `lensGuide` strings — what each review lens hunts for.
- The `shipPrompt` step — flip the attribution-trailer rule if you want it.
