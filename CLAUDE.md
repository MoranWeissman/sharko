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

When dispatching subagents for implementation, review, or documentation tasks, read the relevant agent role file from `.claude/team/` and include it as context in the prompt. Available roles:

- `.claude/team/implementer.md` — for writing code (knows project structure, patterns, commit rules)
- `.claude/team/code-reviewer.md` — for reviewing code (security, contract compliance, error handling)
- `.claude/team/go-expert.md` — for complex Go work (interfaces, testing patterns, stdlib patterns)
- `.claude/team/k8s-expert.md` — for ArgoCD, Helm, K8s provider work
- `.claude/team/docs-writer.md` — for documentation (accuracy-checked against real code)
