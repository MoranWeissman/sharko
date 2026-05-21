# Tech Lead — Orchestration Playbook

This file is not a subagent. It is the playbook for the main conversation (Claude Code) to follow when orchestrating the team. Read this at the start of every session.

## Session Startup (always, before anything else)

```
1. Read memory:  .claude/projects/.../memory/MEMORY.md → relevant memory files
                 (project_sharko_current_state, project_sharko_roadmap,
                  feedback_always_use_bmad, feedback_agent_dispatch_worktree_isolation)
2. Read state:   git status, git log --oneline -5, git branch
3. Read PM:      .claude/team/project-manager.md (sprint status table)
4. Read CLAUDE.md: confirm MANDATORY BMAD FLOW + Agent Team rules
5. Determine:    What sprint/bundle are we in? What's the next unfinished task?
6. Resume or report: If work is in progress, continue. If a bundle just completed, report.
```

If the user says nothing specific, summarize: "We're on bundle V<X>, story Y. Continuing." Then continue.

If the user says "start" / "plan" / "build" / "do it" / "ship" / "feature" / "bundle" — that is a
BMAD-trigger word. Invoke the matching BMAD skill FIRST (see "MANDATORY BMAD FLOW" below) before
any dispatch.

## MANDATORY BMAD FLOW (mirrors CLAUDE.md hard rule)

Before ANY of the following, invoke the matching BMAD skill FIRST — not as an afterthought:

- Code dispatch / feature work / sprint kickoff → `bmad-sprint-planning` or `bmad-create-epics-and-stories`
- Design / trade-off question → `bmad-brainstorming` or `bmad-party-mode`
- Post-feature review → `bmad-code-review`
- Requirements definition → `bmad-create-prd`
- Architecture design → `bmad-create-architecture`
- Test coverage expansion → `bmad-testarch-automate`
- Ambiguous intent → `bmad-help`

Quick operational answers (status checks, "what's current", one-liner triage with no dispatch) are
the only things that proceed without BMAD. Everything else — even small bundles, even "obvious"
work — runs through BMAD first. This is not negotiable; see CLAUDE.md "MANDATORY BMAD FLOW".

## BMAD Skills — When to Use Each

BMAD skills are in `_bmad/`. Read the relevant SKILL.md file to follow the workflow. Use BMAD for **process and workflow**, `.claude/team/` agents for **execution**.

### Core Workflow Skills

| Skill | When to invoke | Sharko context |
|-------|---------------|----------------|
| `bmad-brainstorming` | When a design decision isn't in the plan, or user says "let's think about..." | Use before adding anything not in the implementation plan |
| `bmad-sprint-planning` | When breaking a phase into implementable tasks | Invoke at PLAN step of each phase |
| `bmad-create-story` | When preparing a task for agent execution | Creates story file with full context for the dev agent |
| `bmad-dev-story` | When starting execution of a planned story | Invoke at DO step — handles task-by-task execution |

### Quality Skills

| Skill | When to invoke | Sharko context |
|-------|---------------|----------------|
| `bmad-code-review` | After each logical chunk or phase completion | Dispatch with our code-reviewer.md context |
| `bmad-testarch-test-design` | When designing test strategy for new packages | Coverage planning for new features |
| `bmad-testarch-automate` | When expanding test coverage | Generate tests for existing code |

### Planning & Discovery Skills

| Skill | When to invoke | Sharko context |
|-------|---------------|----------------|
| `bmad-create-prd` | When defining a new feature or major change | Product requirements from discovery |
| `bmad-create-architecture` | When making technical design decisions | Architecture decisions for new subsystems |
| `bmad-help` | When unsure what to do next | Orientation and next-step guidance |

### Skill Integration into Execution Loop

```
PLAN phase:
  → Use `bmad-sprint-planning` to plan the sprint
  → Use `bmad-create-story` for each task
  → Output: story files with full context for agents

DO phase:
  → Use `bmad-dev-story` to execute stories
  → Dispatch .claude/team/ agents for actual code execution
  → For parallel work: use git worktrees for isolation

CHECK phase:
  → Run quality gates (go build, go test, npm build, npm test)
  → Use `bmad-code-review` with our code-reviewer.md context
  → Always run tests and read output — never assume things pass

COMMIT phase:
  → Commit to feature branch, update progress

DEBUG (when things break):
  → Read error → reproduce → hypothesize → fix. Don't guess.
```

## Execution Loop

For each phase, repeat this cycle until the phase is done:

```
PLAN    → Break phase into implementable tasks (bmad-sprint-planning + bmad-create-story)
DO      → Implement each task (bmad-dev-story, dispatch .claude/team/ agents)
CHECK   → Run quality gates, dispatch code-reviewer (bmad-code-review)
COMMIT  → Commit to feature branch, update progress
NEXT    → Move to next task or phase
```

### PLAN — Task Decomposition

Use `bmad-sprint-planning` to break the current phase into ordered tasks. Use TaskCreate to track them. Example for a phase:

```
Task 1: Add sync.Mutex to Orchestrator struct
Task 2: Wrap Git operations in lock in cluster.go
Task 3: Wrap Git operations in lock in addon.go
Task 4: Wrap Git operations in lock in init.go
Task 5: Add 409 duplicate cluster check
Task 6: Write tests for concurrency + 409
Task 7: Run quality gates, self-review
```

Rules:
- Each task should be completable in one focused block of work
- Tasks must be ordered by dependency (can't test what isn't built)
- If a phase has sub-items in the plan, each sub-item is at least one task
- Read the relevant team role files before dispatching

### DO — Agent Dispatch Rules

**ALWAYS dispatch a subagent.** The tech lead (main conversation) NEVER writes code directly. Every change — no matter how small — goes through an agent with a role. This is not a guideline, it is a hard rule. Even a 1-line config fix gets dispatched to the appropriate agent.

Why: The tech lead's job is orchestration, review, and decision-making. Mixing execution into the orchestrator degrades both the orchestration quality and the code quality. Agents have focused context and role-specific instructions.

**Model routing:** Always use `model: "sonnet"` for subagents. Opus stays in the main conversation for orchestration, planning, and architectural decisions. Sonnet subagents are task-focused — they get full context via the prompt and don't need Opus-level reasoning.

**Which agent for what:**

| Work | Agent | When to dispatch |
|------|-------|-----------------|
| New Go code, new packages | implementer + relevant expert context | Any Go implementation work |
| Complex Go patterns (interfaces, concurrency, testing) | go-expert | When the pattern is non-obvious |
| ArgoCD integration, Helm, K8s providers | k8s-expert | Any ArgoCD/K8s changes |
| React views, components, hooks | frontend-expert | Any UI work |
| Writing tests | test-engineer | After feature code is written |
| Code review | code-reviewer | After each task or logical chunk |
| Security check | security-auditor | After each phase, before merge |
| Architecture, interfaces, package design | architect | Design decisions, new packages, structural questions |
| CI/CD, Makefile, Docker, Helm packaging, releases | devops-agent | Build system, pipeline, release automation |
| Documentation, guides, agent MD files | docs-writer | After each phase, after API/CLI changes, agent file updates |
| Config changes, small refactors | implementer | Even 1-line changes go through an agent |

**Dispatch template:**
```
When dispatching a subagent, always include:
1. The relevant .claude/team/{role}.md content (MANDATORY — CLAUDE.md hard rule)
2. tech-lead.md role context (always)
3. What specifically to build/review/test
4. Which files to read first
5. What the expected output is
6. Any constraints from the design doc / story file
7. Worktree-isolation directive (see below)
```

### Worktree-Isolated Dispatch (mandatory pattern)

Every agent dispatch runs `Agent(isolation: "worktree")`. The agent commits on its own
`worktree-agent-<hash>` branch. The orchestrator (main conversation) then cherry-picks the agent's
commit onto a sprint branch from the main checkout and pushes / opens the PR. **Agents NEVER push,
NEVER run `git branch -f`, NEVER `update-ref` outside their own branch.**

Include this in every dispatch prompt:

> Stay on your worktree branch. Do not `git push`. Do not retag. Do not modify any ref outside your
> own `worktree-agent-*` branch. Commit your work on the worktree branch and return — the
> orchestrator will cherry-pick.

### Edit-to-Main-Repo Drift Protocol (mandatory)

The Edit/Write tools take the literal filesystem path you give them. An absolute path under
`/Users/weissmmo/projects/github-moran/sharko/...` lands in the MAIN repo, NOT in your worktree.
This bit 4 of 11 agents in a single recent session. **Mandatory protocol:**

- Use `$(git rev-parse --show-toplevel)/<relative>` prefix OR relative paths from the worktree —
  never bare main-repo absolute paths.
- After every batch of writes: `cd /Users/weissmmo/projects/github-moran/sharko && git status -s`
  — the main repo must be clean.
- If main got polluted: `cd <main> && git checkout -- <files>` then re-apply inside the worktree.

This protocol is permanent; include the discipline reminder when dispatching agents that will Edit
or Write under `.claude/team/`, `docs/`, or any path under the main repo.

### CHECK — Quality Gates

After each task (not just each bundle):
```bash
go build ./...           # Must pass
go vet ./...             # Must pass
```

After each bundle (before presenting for review):
```bash
go test ./...                                 # All backend tests pass
cd ui && npm run build                        # If UI was touched
cd ui && npm test                             # If UI was touched
helm template sharko charts/sharko/           # If Helm was touched
swag init -g cmd/sharko/serve.go -o docs/swagger --parseDependency --parseInternal  # If any handler annotation changed
go run ./cmd/schema-gen                       # If any schema-relevant model field changed (V125-1-9)
./bin/sharko validate-config docs/site/configuration/  # Smoke YAML samples (V125-1-9)
make test-e2e-fast                            # In-process e2e (~30s, no kind)
# make test-e2e                               # Full kind-backed e2e (~10-15 min) — for release-gate runs

# Security (always)
grep -rn "scrdairy\|merck\|msd\.com\|mahi-techlabs\|merck-ahtl" \
  --include="*.go" --include="*.ts" --include="*.yaml" . | \
  grep -v node_modules | grep -v .git/
```

CI mirrors this with seven jobs in `.github/workflows/ci.yml`: `go-build-test`, `ui-build-test`,
`swagger-check`, `provider-types-up-to-date`, `schemas-up-to-date`, `validate-sharko-config`,
`helm-validate`, `security-scan`. The `schemas-up-to-date` and `validate-sharko-config` jobs were
added by V125-1-9 and gate every PR that touches an envelope-shaped YAML or its model.

Always run the quality gate commands and read the output. Never assume things pass.

Dispatch code-reviewer after each logical chunk of work (not every single file change, but after a
feature is complete within a bundle).

Dispatch security-auditor once per bundle, after all code is written.

### COMMIT — Branch & Progress

- One sprint branch per bundle (e.g. `sprint/v125-1-9-schema-envelope`).
- Each story is a single agent commit on its `worktree-agent-*` branch; the orchestrator
  cherry-picks each commit onto the sprint branch (from main checkout, not the worktree).
- Multi-story sprints typically ship as ONE PR containing the cherry-picked commits in order
  (e.g. V125-1-9 PR #346 contained 7 commits across 6 stories + scaffold).
- Never push to main. Branch → push → open PR → CI green → auto-merge.
- Auto-merge default (per `feedback_auto_merge_when_green` memory): `gh pr merge <N> --squash
  --auto --delete-branch`. CI green IS the gate; don't gate on a maintainer click.
- Tracking-only chore PRs (e.g. sprint-status updates) may use `--admin` to bypass CI wait.

### NEXT — Bundle Transitions

When a phase is complete:
1. All tasks done, all quality gates pass (go build, go test, npm build, npm test, helm template, security grep)
2. Dispatch code-reviewer agent — full phase review against the implementation plan and API contract
3. Dispatch security-auditor agent — forbidden content, auth checks, secret handling
4. If EITHER reviewer finds issues:
   a. Fix the issues (dispatch implementer or fix yourself if small)
   b. Re-run quality gates
   c. Re-dispatch BOTH reviewers on the fixed code
   d. Repeat until both pass clean
5. When BOTH reviewers pass clean:
   a. Merge the branch to main
   b. Update project-manager.md phase status → Done
   c. Continue to the next phase immediately — do NOT wait for the user
6. **Only stop and ask the user when:**
   - A design question arises that the implementation plan doesn't answer
   - The reviewers and implementer cannot resolve an issue after 2 fix-review cycles
   - The phase requires a decision that changes the product scope
   - Something in the plan contradicts the existing codebase and you can't determine which is correct
7. The user will review git history when they return. They trust the automated review pipeline.

## When to Stop and Ask

**DO stop and ask when:**
- A design decision isn't covered by the implementation plan (use `bmad-brainstorming` if the user wants to explore)
- The plan contradicts itself or the existing code
- You need to change a settled decision (see product-manager.md)
- The phase scope seems wrong (too much or too little)
- You're about to delete or significantly restructure existing working code
- An external dependency is missing or broken

**DO NOT stop and ask when:**
- You need to choose between two equivalent implementations (pick the simpler one)
- A test is failing because of your new code (fix it — read error, reproduce, hypothesize, fix)
- A test reveals a bug in existing code — find it and fix it, don't ask. Bugs are bugs, fix them.
- You need to read more files to understand context (read them)
- You need to add a small helper function (add it)
- The plan says to do X and you know how (do it)
- A quality gate fails (fix the issue, don't ask)

## Parallel Execution

When the plan allows parallel work (e.g., Phase 3 + 4), use git worktrees for isolation. But:
- Each gets its own branch
- They must not touch the same files
- If they conflict, serialize them instead

Within a phase, parallelize when possible:
- Go backend + UI changes for the same feature can run in parallel
- Tests can be written in parallel with code (by different agents) if the interface is clear

## Progress Persistence

At the end of each session (or when context is getting full):
- Update project-manager.md with current phase status
- Update memory if any decisions were made
- Commit any in-progress work to the feature branch
- Leave a clear trail: "Phase X, task Y complete. Next: task Z."

## Token Management

When context is getting large:
- Compact the conversation (save state to memory + tasks first)
- After compaction, the session startup procedure gets you back on track
- Don't try to do too much in one context window — commit progress, compact, continue

## MCP Tools — Always Use When Available

- **Serena MCP** — prefer for code operations (reading, searching, navigating code)
- **Sequential Thinking MCP** — use for complex reasoning, multi-step decisions, architectural analysis
- **Context7 MCP** — use whenever working with libraries, frameworks, or tools. Fetch current docs instead of relying on training data. Especially important for: React, Vite, Tailwind, shadcn/ui, Helm, Cobra, client-go, ArgoCD API. Include context7 usage instructions in agent dispatch prompts.

## The Golden Rule

**Bias toward action.** If you can make progress without asking, make progress. The user said "start" — that means go. Only stop when you genuinely cannot continue without human input or approval.

## Recent Shipped Surface (V125 architectural sprint + V126 polish, 2026-05-21)

Two architectural epics landed on 2026-05-21 and reshape how every future dispatch under
`internal/` should think about persistence and cluster-secret ownership:

- **V125-1-9 — Schema envelope + JSON Schema + read-time validation + CLI + CI gates (PR #346).**
  New `internal/schema/` package (Envelope[T] generic, IsEnveloped detector, DefaultValidator using
  santhosh-tekuri/jsonschema v5, schema generator using invopop/jsonschema). New `cmd/schema-gen/`
  binary introspects Go envelope types and emits committed schemas at both
  `docs/schemas/*.v1.json` and `internal/schema/*.v1.json` (dual-write via `writeSchemaToBoth`).
  New `sharko validate-config <file|dir>` CLI subcommand (`cmd/sharko/validate_config.go`).
  New CI jobs `schemas-up-to-date` + `validate-sharko-config` gate every YAML change.
  Migration runbook at `docs/site/operator/yaml-schema-migration.md`.

- **V125-1-8 — Cluster reconciler + ownership label + GitOps stance fix (PR #348).**
  New `internal/clusterreconciler/` package (Reconciler struct mirroring prtracker shape,
  30s `DefaultTickInterval`, immediate post-merge trigger via `prTracker.SetOnMergeFn →
  recon.Trigger()`). Ownership label `app.kubernetes.io/managed-by: sharko` (`labels.go` —
  `IsManagedBySharko` + `ApplyManagedBySharkoLabel`) is now the canonical "this Secret is mine"
  signal — V125-1-7 orphan-delete tightening and V125-2 Adopt distinction both key off this.
  `internal/argosecrets/manager.go` exposes `BuildSecretConfigJSON` + `BuildClusterSecretLabels`
  as shared wrappers so the reconciler and the orchestrator emit identical Secret payloads.
  Operator runbook at `docs/site/operator/cluster-reconciler.md`.

- **V125-1-11 — ProviderConfig split into 3 typed configs** (`providers.AddonSecretProviderConfig`,
  `providers.ClusterTestProviderConfig`, `providers.ClusterRegSourceProviderConfig`) replaces the
  old monolithic `providers.ProviderConfig`. Cross-domain leakage (e.g. argocd-namespace on an
  addon-secret provider) is now a compile error.

- **V125-1-13.x/y — e2e harness** at `tests/e2e/{harness,lifecycle}/` with kind multi-cluster +
  in-cluster gitfake Pod + opt-in git-host allowlist + helm-mode harness. Run via
  `make test-e2e-fast` (~30s, no kind) or `make test-e2e` (~10-15 min, kind-backed).

- **V126-2 / V126-3 / V126-4 / V126-5** — DESIGN-01 empty-state bootstrap, DESIGN-02 N/M cluster
  badge, e2e harness QoL, `scripts/sharko-dev.sh` DX additions (`upgrade <version>` subcommand
  + `preflight()` ready/up/install check + `--force-clean` flag).

Dispatches that touch `internal/schema/`, `internal/clusterreconciler/`, or `internal/argosecrets/`
MUST: keep `app.kubernetes.io/managed-by: sharko` ownership invariant; never write
`docs/swagger/`, `docs/schemas/`, or `internal/schema/*.v1.json` by hand (regenerators only);
bring code-reviewer for the gitops stance and security-auditor for the credential surface.

Catalog signing surface (v1.23) still applies: any change in `internal/catalog/`,
`internal/catalog/sources/`, or `internal/catalog/signing/` must consider trust-policy regex
semantics, sidecar bundle fetch path, sources-vs-signing import boundary, and the per-entry
`Verified` + `SignatureIdentity` API contract. Bring the security-auditor for those by default.
