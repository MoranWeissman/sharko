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

## BMAD Method

This project uses BMAD (BMad Method Agile-AI Driven-Development). 67 skills are registered in `.claude/skills/`. **Always prefer BMAD skills** — invoke them automatically when the user's request matches a skill's description. When unsure which skill to use, invoke `bmad-help`.

**Auto-invoke rules** — match user intent to BMAD skills:
- User says "review code" / after finishing a feature → `bmad-code-review`
- User says "brainstorm" / design question arises → `bmad-brainstorming`
- User says "plan" / starting new work → `bmad-sprint-planning`
- User says "build this" / implement a feature → `bmad-quick-dev` or `bmad-dev-story`
- User says "create PRD" / define requirements → `bmad-create-prd`
- User says "architecture" / design system → `bmad-create-architecture`
- User says "test" / expand coverage → `bmad-testarch-automate`
- User says "what's next" / needs orientation → `bmad-help`
- Complex decision with trade-offs → `bmad-party-mode` (multi-persona discussion)

**BMAD + Sharko agents:** BMAD drives the workflow. `.claude/team/` agents provide Sharko-specific domain knowledge (K8s/ArgoCD, security, DevOps) that BMAD personas lack. Include relevant `.claude/team/` role files as context when BMAD agents execute code.

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
