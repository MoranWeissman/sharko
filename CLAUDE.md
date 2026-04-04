# Sharko — Claude Code Instructions

## Git Rules

- **Never add Co-Authored-By trailers** to commits. All commits are authored solely by the repo owner.
- **Never use --no-verify or skip hooks.**
- Git user: `Moran Weissman <moran.weissman@gmail.com>`

## Content Policy

This project was extracted from an internal codebase. No references to the original organization, internal domains, employee emails, or real AWS account IDs should appear anywhere in the code, commits, or documentation. If any are found, remove them immediately.

## Project Context

Sharko is an addon management server for Kubernetes fleets, built on ArgoCD.
See `docs/superpowers/specs/2026-04-01-sharko-implementation-design.md` for the authoritative design spec.

## Agent Team

When dispatching subagents, read the relevant role file from `.claude/team/` and include it as context. Update role files as the product evolves — they are living documents.

**Execution:**
- `.claude/team/implementer.md` — writes code following plans, knows project patterns
- `.claude/team/go-expert.md` — complex Go work, interfaces, testing, stdlib patterns
- `.claude/team/k8s-expert.md` — ArgoCD, Helm, K8s providers, ApplicationSets
- `.claude/team/frontend-expert.md` — React UI, shadcn/ui, Vite, TypeScript
- `.claude/team/test-engineer.md` — writes tests, knows mock patterns, tracks coverage gaps

**Quality:**
- `.claude/team/code-reviewer.md` — reviews for bugs, security, contract compliance
- `.claude/team/security-auditor.md` — full security sweep, forbidden content, auth checks

**Leadership:**
- `.claude/team/product-manager.md` — product vision, user needs, feature prioritization
- `.claude/team/project-manager.md` — progress tracking, build sequence, quality gates
