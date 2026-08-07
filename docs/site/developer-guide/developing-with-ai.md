# Developing with AI

Sharko is built with the help of an AI coding agent (Claude Code). This page
describes how that works in practice, so a new contributor — human or AI —
can be productive without having to reverse-engineer the setup from git
history. None of this is required to contribute: you can open a PR by hand
like any other open-source project. If you do want to use an AI agent on
this repo, this is the workflow it follows.

## The team role files

`.claude/team/*.md` are small, focused instruction files. Each one describes
a role: what it's allowed to touch, what it should never touch, and the
project-specific patterns it needs to know (file layout, coding
conventions, quality gates). When an agent is dispatched to do a piece of
work, the relevant role file is included in its prompt, so the agent starts
with Sharko-specific context instead of generic assumptions.

| Role file | What it's for |
|---|---|
| `tech-lead.md` | The orchestration playbook — how work gets broken down, dispatched, checked, and merged. Read this first. |
| `implementer.md` | General feature code, both Go and simple UI changes, following a plan. |
| `go-expert.md` | Non-obvious Go: interfaces, concurrency, generics, stdlib patterns. |
| `k8s-expert.md` | ArgoCD integration, Helm charts, Kubernetes providers, ApplicationSets. |
| `frontend-expert.md` | React views, components, hooks, and the UI's design conventions. |
| `test-engineer.md` | Writing tests, tracking coverage gaps, mock patterns. |
| `architect.md` | Package design, interface contracts, dependency direction. |
| `devops-agent.md` | CI/CD, Makefile, Docker, Helm packaging, release automation. |
| `docs-writer.md` | User guides, API references, design specs, and the role files themselves. |
| `code-reviewer.md` | Reviewing a change for bugs, security issues, and contract compliance. |
| `security-auditor.md` | A full security sweep: forbidden content, auth checks, secret handling. |
| `product-manager.md` | Product direction, settled decisions, feature prioritization. |
| `project-manager.md` | Sprint/phase status, build sequence, quality gate tracking. |

These are living documents — when a pattern changes (a new package, a
renamed route, a new UI convention), the role file that covers it gets
updated in the same change.

## The BMAD skill flow

Planning work — breaking a feature into stories, locking down decisions
before code gets written, reviewing a finished change against the plan —
uses a set of AI agent skills from the
[BMAD-METHOD](https://github.com/bmad-code-org/BMAD-METHOD) project,
committed in this repo at `.claude/skills/bmad-*` (MIT license, see
[THIRD-PARTY-NOTICES.md](https://github.com/MoranWeissman/sharko/blob/main/THIRD-PARTY-NOTICES.md)).
The skill *content* is committed here; the engine that runs it (`_bmad/`) is
a separate install — see
[CONTRIBUTING.md](https://github.com/MoranWeissman/sharko/blob/main/CONTRIBUTING.md)
for the one-line setup. If the engine isn't installed, or a skill otherwise
isn't available, fall back to the team role files above and proceed by
hand — the role files carry the same project-specific constraints.

The loop looks like this:

```
PLAN    → Break the work into stories with the decisions already locked in
          (a sprint-planning skill, or the tech-lead role file by hand).
DISPATCH → Hand each story to the right role agent, worktree-isolated
          (see below), with the plan and the role file as context.
CHECK   → Run the quality gates. Review the change against the plan and
          against security/contract concerns.
COMMIT  → Merge once the gates and the review are clean.
```

The point of planning first is that an agent given a plan with the
decisions already made produces a focused, mergeable change. An agent given
an open-ended task tends to guess at implementation details it shouldn't be
guessing at — naming, scope, which file owns what — and that guesswork is
expensive to unwind later. A two-minute plan is cheaper than a bad hour of
agent work.

## Worktree-isolated agent dispatch

Every agent that writes code runs in its own git worktree, on its own
branch. This means an agent can never accidentally commit to `main`, push
to a shared branch, or leave the main checkout in a dirty state — it
physically cannot reach those refs from inside its worktree.

The pattern:

1. The agent is dispatched into a fresh worktree on a fresh branch.
2. It does its work and commits there — one focused commit per story is the
   norm.
3. It never pushes and never opens a PR itself.
4. Whoever dispatched it (a human, or the tech-lead role in an
   orchestrating session) cherry-picks the commit onto a clean branch cut
   from current `main`, and opens the PR from there.

This also solves a subtler problem: an agent's working directory and the
main checkout are different absolute paths. Writing to the wrong one is an
easy mistake to make by hand, and worktree isolation prevents it by
construction rather than by discipline.

## Quality gates every change must pass

CI runs the same checks locally available to you before pushing:

```bash
go build ./...
go vet ./...
go test ./...

# If you touched a handler with @Router annotations:
swag init -g cmd/sharko/serve.go -o docs/swagger --parseDependency --parseInternal

# If you touched an envelope-shaped model (managed-clusters.yaml, addon-catalog.yaml):
go run ./cmd/schema-gen

# If you touched charts/sharko/:
helm template sharko charts/sharko/

# If you touched ui/:
cd ui && npm run build && npm test

# If you touched docs/site/ or mkdocs.yml:
mkdocs build --strict

# Fast in-process e2e (no kind cluster needed):
make test-e2e-fast
```

A change is not done until its relevant gates are green. See
[CONTRIBUTING.md](https://github.com/MoranWeissman/sharko/blob/main/CONTRIBUTING.md#building-and-testing)
for the full list and the forbidden-content sweep that runs alongside these.

## Checkpoint and resume habits

An AI agent session doesn't have a person's memory of what happened
yesterday, so this repo keeps state that lets any session — a fresh one, or
one recovering from a context reset — get oriented from what's actually in
the repo, not from a private history nobody else can see:

- **`git log`, `git status`, and open PRs** are the ground truth for "what's
  currently in flight." A session should start by reading these, not by
  assuming.
- **`.claude/checkpoints/`** (gitignored, per-machine) can hold a short
  snapshot written before a context reset — current branch, recent
  commits, what's mid-flight. If `.claude/checkpoints/LATEST.md` exists,
  read it first; if it doesn't, orient from git history and open PRs
  instead. Either path gets you to the same place.
- **`.claude/team/project-manager.md`** tracks sprint/phase status for
  anyone (or anything) picking up work mid-stream.

Nothing about resuming work depends on one particular machine or one
particular person's local setup — that's on purpose, so the workflow works
the same for any contributor.

## Where to go next

- [CLAUDE.md](https://github.com/MoranWeissman/sharko/blob/main/CLAUDE.md) —
  the top-level instructions an AI agent reads at the start of a session in
  this repo.
- [CONTRIBUTING.md](https://github.com/MoranWeissman/sharko/blob/main/CONTRIBUTING.md) —
  the human-facing contribution guide, including the AI-agent disclosure
  expectations.
- [THIRD-PARTY-NOTICES.md](https://github.com/MoranWeissman/sharko/blob/main/THIRD-PARTY-NOTICES.md) —
  attribution for the BMAD-METHOD skills bundled in this repo.
