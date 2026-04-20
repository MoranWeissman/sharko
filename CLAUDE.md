# Sharko — Claude Code Instructions

## Git Rules

- **Never add Co-Authored-By trailers** to commits. All commits are authored solely by the repo owner.
- **Never use --no-verify or skip hooks.**
- **Never retag an existing version.** Every code change — no matter how small — gets a new semver version. Retagging (deleting a tag and recreating it on a different commit) is forbidden. Patch bump for fixes, minor for features, major for breaking changes.
- Git user: `Moran Weissman <moran.weissman@gmail.com>`

## Code Rules

- **Every new API endpoint must have swagger annotations AND regenerated docs.** After adding or modifying any handler with `@Router` annotations, run `swag init -g cmd/sharko/serve.go -o docs/swagger --parseDependency --parseInternal` and commit the result. CI will reject PRs with stale swagger docs.

## Content Policy

This project was extracted from an internal codebase. No references to the original organization, internal domains, employee emails, or real AWS account IDs should appear anywhere in the code, commits, or documentation. If any are found, remove them immediately.

## Project Context

Sharko is an addon management server for Kubernetes clusters, built on ArgoCD.

## Session Startup

Every new session: read `.claude/team/tech-lead.md` and follow the startup procedure. This gets you oriented — current phase, next task, what to do — without the user needing to explain context.

## MCP Tools

Use these whenever available to save tokens and improve reasoning:

- **Serena MCP** — prefer for code operations (reading, searching, navigating code) when available
- **Sequential Thinking MCP** — use for complex reasoning, multi-step decisions, architectural analysis
- **Context7 MCP** — use whenever working with libraries, frameworks, or tools (React, Vite, Tailwind, shadcn/ui, Helm, Cobra, client-go, etc.). Fetch current docs instead of relying on training data. Include in agent dispatch prompts.

## MANDATORY BMAD FLOW — NO EXCEPTIONS

**This is a hard rule. Violating it = violating the user's explicit instruction.**

Before ANY of the following, you MUST invoke the matching BMAD skill FIRST — not as an afterthought, not as the second step, FIRST:

- **Code dispatch** (spawning agents to write code) → `bmad-sprint-planning` or `bmad-create-epics-and-stories` first
- **Feature work** (starting a new feature/bundle/version) → `bmad-sprint-planning` first
- **Planning task** (deciding scope, sequencing work) → `bmad-sprint-planning` or `bmad-brainstorming` first
- **Design question** (architecture, trade-offs) → `bmad-brainstorming` or `bmad-party-mode` first
- **Post-feature review** (after completing implementation) → `bmad-code-review`
- **Requirements definition** → `bmad-create-prd`
- **Test coverage expansion** → `bmad-testarch-automate`
- **Ambiguous user intent that matches multiple skills** → `bmad-help`

**Excuses that are NOT valid exceptions:**
- "Scope is clear from the roadmap" — invoke BMAD anyway to formalize
- "Lean workflow / one agent per bundle" — applies to *execution*, not to skipping BMAD *planning*
- "Small bundle, not worth the ceremony" — small bundles still get BMAD; BMAD ceremony scales down proportionally
- "User said 'do it' — that's an execute signal" — kickoff of any feature/bundle is a planning signal regardless of the user's wording
- "It's obvious what to build" — if it's obvious, BMAD planning takes 2 minutes. Do it.
- "Already did planning in my head / in a memory file" — not a substitute for producing BMAD artifacts (epics.md, stories, etc.)

If you catch yourself rationalizing around this rule → STOP and ask the user. Do not silently proceed without BMAD.

**Quick operational answers** (one-line questions, status checks, "what's the current state", bug triage questions with no code dispatch) are the only things that are fine without BMAD.

### BMAD skill map

- `bmad-sprint-planning` — start a new feature / bundle / version / any multi-story work
- `bmad-create-epics-and-stories` — break a design doc into formal epics + stories
- `bmad-brainstorming` — open-ended design / trade-off exploration
- `bmad-party-mode` — multi-persona discussion for complex decisions
- `bmad-create-prd` — formal requirements doc
- `bmad-create-architecture` — system architecture design
- `bmad-quick-dev` / `bmad-dev-story` — execute a single story
- `bmad-code-review` — post-implementation review against plan
- `bmad-testarch-automate` — test strategy + coverage expansion
- `bmad-help` — unsure which skill applies

### BMAD + Sharko team agents

BMAD drives the *workflow* (epics, stories, acceptance criteria, review gates). `.claude/team/` agents provide Sharko-specific *domain knowledge* (K8s/ArgoCD/security/DevOps) that generic BMAD personas lack.

**Every** agent dispatch MUST include the relevant `.claude/team/*.md` role file(s) as embedded context in the prompt. This is non-negotiable. An agent dispatch without a role file will produce generic output that misses Sharko-specific constraints (tiered Git attribution, two-operation catalog/deploy model, quality gates, etc.).

If the rule feels like friction, that's the point — the friction prevents the drift that produces flip-flopping recommendations and wasted agent runs.

## Agent Team

**The tech lead NEVER writes code directly.** Every change — no matter how small — is dispatched to an agent with a role. Read the relevant role file from `.claude/team/` and include it as context. Update role files as the product evolves — they are living documents.

**Orchestration:**
- `.claude/team/tech-lead.md` — **read first every session**. Playbook for breaking down phases, dispatching agents, quality gates, when to stop vs. continue

**Execution:**
- `.claude/team/implementer.md` — writes code following plans, knows project patterns
- `.claude/team/go-expert.md` — complex Go work, interfaces, testing, stdlib patterns
- `.claude/team/k8s-expert.md` — ArgoCD, Helm, K8s providers, ApplicationSets
- `.claude/team/frontend-expert.md` — React UI, shadcn/ui, Vite, TypeScript
- `.claude/team/test-engineer.md` — writes tests, knows mock patterns, tracks coverage gaps

**Architecture & Infrastructure:**
- `.claude/team/architect.md` — package design, interface contracts, dependency direction, trade-offs
- `.claude/team/devops-agent.md` — CI/CD, Makefile, Docker, Helm packaging, GitHub Actions, releases

**Documentation:**
- `.claude/team/docs-writer.md` — all documentation: user guides, API refs, design specs, AND agent team files themselves

**Quality:**
- `.claude/team/code-reviewer.md` — reviews for bugs, security, contract compliance
- `.claude/team/security-auditor.md` — full security sweep, forbidden content, auth checks

**Leadership:**
- `.claude/team/product-manager.md` — product vision, user needs, feature prioritization
- `.claude/team/project-manager.md` — progress tracking, build sequence, quality gates
