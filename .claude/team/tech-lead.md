# Tech Lead — Orchestration Playbook

This file is not a subagent. It is the playbook for the main conversation (Claude Code) to follow when orchestrating the team. Read this at the start of every session.

## Session Startup (always, before anything else)

```
1. Read memory:  .claude/projects/.../memory/MEMORY.md → relevant memory files
2. Read plan:    docs/design/IMPLEMENTATION-PLAN-V1.md
3. Read state:   git status, git log --oneline -5, git branch
4. Read PM:      .claude/team/project-manager.md (phase status table)
5. Determine:    What phase are we in? What's the next unfinished task?
6. Resume or report: If work is in progress, continue. If a phase just completed, report.
```

If the user says nothing specific, summarize: "We're on Phase X, step Y. Continuing." Then continue.

If the user says "start", begin executing from wherever the plan is.

## Superpowers Skills — When to Use Each

These are invoked via the `Skill` tool. Use the right skill at the right time — they encode battle-tested workflows.

### Core Workflow Skills

| Skill | When to invoke | Sharko context |
|-------|---------------|----------------|
| `superpowers:brainstorming` | When a design decision isn't in the plan, or user says "let's think about..." | Use before adding anything not in IMPLEMENTATION-PLAN-V1.md |
| `superpowers:write-plan` | When breaking a phase into implementable tasks | Invoke at PLAN step of each phase |
| `superpowers:execute-plan` | When starting execution of a planned phase | Invoke at DO step — it handles task-by-task execution with checkpoints |
| `superpowers:finishing-a-development-branch` | When a phase is complete and ready for review | Invoke at NEXT step — handles verify/PR/cleanup |

### Quality Skills

| Skill | When to invoke | Sharko context |
|-------|---------------|----------------|
| `superpowers:requesting-code-review` | After each logical chunk or phase completion | Dispatch with our code-reviewer.md context |
| `superpowers:verification-before-completion` | Before declaring any task or phase done | Never say "should pass" — run and read output |
| `superpowers:test-driven-development` | When writing new packages (queue, remoteclient) or filling coverage gaps | Especially for Phase 1 (mutex tests) and Phase 3 (remote client tests) |

### Problem-Solving Skills

| Skill | When to invoke | Sharko context |
|-------|---------------|----------------|
| `superpowers:systematic-debugging` | When a test fails unexpectedly or behavior is wrong | Read error → reproduce → hypothesize → fix. Don't guess. |
| `superpowers:dispatching-parallel-agents` | When 2+ independent work streams exist | Phase 3+4 parallel, Go+UI within a phase |
| `superpowers:using-git-worktrees` | When parallel agents need isolated repos | Each parallel agent gets its own worktree |
| `superpowers:subagent-driven-development` | For complex phases with many files | Two-stage review (spec compliance, then code quality) per task |

### Skill Integration into Execution Loop

```
PLAN phase:
  → Invoke `superpowers:write-plan` to decompose the phase
  → Output: ordered task list with files, changes, expected output

DO phase:
  → Invoke `superpowers:execute-plan` to run through tasks
  → For new packages: invoke `superpowers:test-driven-development`
  → For parallel work: invoke `superpowers:dispatching-parallel-agents`
  → For complex multi-file work: invoke `superpowers:subagent-driven-development`

CHECK phase:
  → Invoke `superpowers:verification-before-completion` (run tests, read output)
  → Invoke `superpowers:requesting-code-review` (dispatch reviewer)

COMMIT phase:
  → Invoke `superpowers:finishing-a-development-branch` when phase is done

DEBUG (when things break):
  → Invoke `superpowers:systematic-debugging` before guessing
```

### Skills NOT Needed for Sharko

- `superpowers:writing-skills` — we're not writing new skills
- `superpowers:receiving-code-review` — we dispatch reviews, we don't receive external ones

## Execution Loop

For each phase, repeat this cycle until the phase is done:

```
PLAN    → Break phase into implementable tasks (invoke write-plan skill)
DO      → Implement each task (invoke execute-plan skill, dispatch agents)
CHECK   → Run quality gates, dispatch code-reviewer (invoke verification + review skills)
COMMIT  → Commit to feature branch, update progress
NEXT    → Move to next task or phase (invoke finishing-branch skill when phase done)
```

### PLAN — Task Decomposition

Invoke `superpowers:write-plan` to break the current phase into ordered tasks. Use TaskCreate to track them. Example for a phase:

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
1. The relevant .claude/team/{role}.md content
2. What specifically to build/review/test
3. Which files to read first
4. What the expected output is
5. Any constraints from the implementation plan
```

### CHECK — Quality Gates

After each task (not just each phase):
```bash
go build ./...           # Must pass
go vet ./...             # Must pass
```

After each phase (before presenting for review):
```bash
go test ./...            # All tests pass
cd ui && npm run build   # If UI was touched
cd ui && npm test        # If UI was touched
helm template sharko charts/sharko/  # If Helm was touched

# Security (always)
grep -rn "scrdairy\|merck\|msd\.com\|mahi-techlabs\|merck-ahtl" \
  --include="*.go" --include="*.ts" --include="*.yaml" . | \
  grep -v node_modules | grep -v .git/
```

Always invoke `superpowers:verification-before-completion` — run the commands and read the output. Never assume things pass.

Dispatch code-reviewer after each logical chunk of work (not every single file change, but after a feature is complete within a phase).

Dispatch security-auditor once per phase, after all code is written.

### COMMIT — Branch & Progress

- One feature branch per phase: `feat/phase-{N}-{short-name}`
- Commit after each completed task (small, focused commits)
- Never push to main. Branch → push → present for human review → human merges
- Update task status as you go (TaskUpdate → completed)

### NEXT — Phase Transitions

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
- A design decision isn't covered by the implementation plan (invoke `superpowers:brainstorming` if the user wants to explore)
- The plan contradicts itself or the existing code
- You need to change a settled decision (see product-manager.md)
- The phase scope seems wrong (too much or too little)
- You're about to delete or significantly restructure existing working code
- An external dependency is missing or broken

**DO NOT stop and ask when:**
- You need to choose between two equivalent implementations (pick the simpler one)
- A test is failing because of your new code (fix it — invoke `superpowers:systematic-debugging` if needed)
- A test reveals a bug in existing code — find it and fix it, don't ask. Bugs are bugs, fix them.
- You need to read more files to understand context (read them)
- You need to add a small helper function (add it)
- The plan says to do X and you know how (do it)
- A quality gate fails (fix the issue, don't ask)

## Parallel Execution

When the plan allows parallel work (e.g., Phase 3 + 4), invoke `superpowers:dispatching-parallel-agents` and use `superpowers:using-git-worktrees` for isolation. But:
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
