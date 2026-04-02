# Sharko — Claude Code Instructions

## Git Rules

- **Never add Co-Authored-By trailers** to commits. All commits are authored solely by the repo owner.
- **Never use --no-verify or skip hooks.**
- Git user: `Moran Weissman <moran.weissman@gmail.com>`

## Forbidden Content

The following must NEVER appear anywhere in the codebase, commit messages, or commit metadata:

- `scrdairy.com` — no emails, domains, or references
- `merck.com` — no emails, domains, or references
- `msd.com` — no emails, domains, or references
- `mahi-techlabs.com` — no domains or references
- `merck-ahtl` — no GitHub org references
- Real AWS account IDs from the original internal repo

If any of these are found during work, remove them immediately.

## Project Context

Sharko is an addon management server for Kubernetes fleets, built on ArgoCD.
See `docs/superpowers/specs/2026-04-01-sharko-implementation-design.md` for the authoritative design spec.
