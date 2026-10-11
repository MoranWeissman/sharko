# Developing with AI

Sharko is built with the help of an AI coding agent (Claude Code). This page
describes what is public about that workflow, so a contributor can understand
how the project uses AI assistance and how to opt in if they choose to. None of
this is required to contribute: you can open a PR by hand like any other
open-source project.

## The BMAD skills

Planning work — breaking a feature into stories, locking down decisions before
code gets written, reviewing a finished change against the plan — uses a set of
AI agent skills from the
[BMAD-METHOD](https://github.com/bmad-code-org/BMAD-METHOD) project, committed
in this repo at `.claude/skills/bmad-*` (MIT license, see
[THIRD-PARTY-NOTICES.md](https://github.com/MoranWeissman/sharko/blob/main/THIRD-PARTY-NOTICES.md)).
The skill *content* is committed here; the engine that runs it (`_bmad/`) is a
separate install — see
[CONTRIBUTING.md](https://github.com/MoranWeissman/sharko/blob/main/CONTRIBUTING.md)
for the one-line setup.

The general loop:

```
PLAN    → Break the work into stories with the decisions already locked in.
DISPATCH → Hand each story to an agent with the plan and project context.
CHECK   → Run the quality gates. Review the change against the plan and
          against security/contract concerns.
COMMIT  → Merge once the gates and the review are clean.
```

The point of planning first is that an agent given a plan with the decisions
already made produces a focused, mergeable change. An agent given an open-ended
task tends to guess at implementation details it shouldn't be guessing at —
naming, scope, which file owns what — and that guesswork is expensive to unwind
later.

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

## Where to go next

- [CONTRIBUTING.md](https://github.com/MoranWeissman/sharko/blob/main/CONTRIBUTING.md) —
  the contribution guide, including the AI-agent disclosure expectations.
- [THIRD-PARTY-NOTICES.md](https://github.com/MoranWeissman/sharko/blob/main/THIRD-PARTY-NOTICES.md) —
  attribution for the BMAD-METHOD skills bundled in this repo.
