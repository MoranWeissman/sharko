---
stepsCompleted: [1, 2, 3]
inputDocuments:
  - task #190 (sharko-dev.sh upgrade subcommand)
  - task #191 (sharko-dev.sh ready preflight)
  - scripts/sharko-dev.sh (~2300 lines, dispatch case around line 2207)
  - .local/scripts-pending/upgrade.sh (87 lines, source to integrate)
workflowType: 'epics-and-stories'
project_name: 'sharko'
version: 'v126-5-sharko-dev-dx'
user_name: 'Moran'
date: '2026-05-21'
---

# V126-5 — sharko-dev.sh DX bundle — Epic Breakdown

## Sprint goal (verbatim, locked 2026-05-21)

> "sharko-dev.sh bundle: upgrade subcommand (#190) + ready preflight (#191). Parallel-dispatchable with V126-4." — Moran

2 tasks → 1 single-file bash story → 1 agent → 1 PR. Auto-merge per `feedback_auto_merge_when_green`.

## Branch + release strategy

- **Branch:** `dev/v126-5-sharko-dev-dx` from current `main` HEAD (commit `0a746410`)
- Single worktree, single agent
- Single PR; auto-merge when CI green
- NO release / NO tag

## Quality gates

- `bash -n scripts/sharko-dev.sh` (syntax check — MUST pass)
- `./scripts/sharko-dev.sh upgrade --help` (renders without error)
- `./scripts/sharko-dev.sh help | grep -q upgrade` (discoverable in main help)
- `./scripts/sharko-dev.sh ready --help | grep -q force-clean` (new flag discoverable)
- `shellcheck scripts/sharko-dev.sh` if shellcheck is installed (best-effort; not a CI gate)
- NO `Co-Authored-By`, NO `--no-verify`, NO `git push` from worktree

## Role files

- `.claude/team/tech-lead.md`
- `.claude/team/devops-agent.md` (bash, Makefile-adjacent, sharko-dev.sh maintainer)

## MCP tools

- **Serena MCP** — navigate `scripts/sharko-dev.sh` (~2300 lines, locate dispatch case + help text + ready function)

## Scope guardrails

- ONLY: `scripts/sharko-dev.sh` + delete `.local/scripts-pending/upgrade.sh`
- NO Go. NO UI. NO Chart.yaml. NO release notes.
- DO NOT touch other scripts in `scripts/`.
- DO NOT introduce new dependencies (must run on stock macOS + Linux bash).

---

## Epic V126-5: sharko-dev.sh DX bundle

### Story V126-5.1 — `upgrade` subcommand + `ready` preflight (single bash file, single agent)

**Subtasks:**

#### #190 — `upgrade <version>` subcommand integration

1. Read `.local/scripts-pending/upgrade.sh` (87 lines, the original standalone script) to understand current logic.
2. Add `upgrade)` case in main subcommand dispatch (around line 2207 — `case "$cmd" in ... esac` block in `scripts/sharko-dev.sh`).
3. Add help-line under the "Lifecycle" section of the main help text (currently lists `ready, up, install, rebuild, reset, down`).
4. **Reuse existing helpers** (do NOT duplicate constants):
   - `helm_release_exists` — existing
   - `${SHARKO_NAMESPACE}` — existing
   - `${SHARKO_LOCAL_PORT}` — existing
   - Existing pf restart logic (the `pkill -f` + `kubectl port-forward &` dance — agent finds where it lives and calls it as a function, not as inlined commands)
5. Subcommand shape:
   ```
   ./scripts/sharko-dev.sh upgrade <version>    # explicit version
   ./scripts/sharko-dev.sh upgrade              # use Chart.yaml version (read via yq or grep)
   ./scripts/sharko-dev.sh upgrade --help       # per-subcommand help
   ```
6. Flow: poll until `oci://ghcr.io/moranweissman/sharko/charts/sharko:<version>` is queryable → `helm upgrade` against published chart → restart pod → restart port-forward → curl `/api/v1/health` → verify reported `version` matches requested.
7. After integration verified: delete `.local/scripts-pending/upgrade.sh`.

#### #191 — `ready` preflight resource check

8. Add `preflight()` function (~80-120 lines) called at the top of `ready` / `up` / `install` code paths.
9. Checks performed (in order, with clear log output):
   - **Stale kind clusters:** `kind get clusters` returns anything other than `${KIND_CLUSTER_NAME}` → log cluster ages (`docker ps --filter "name=<cluster>-control-plane"`); check each for CrashLoopBackOff in kube-system; if any unhealthy, flag red; **interactive prompt:** `[y/n] Delete N stale clusters?` (unless `--force-clean` is set → auto-delete).
   - **Docker memory headroom:** parse `docker info` → if `(running kind clusters × 2GB) + 2GB > total memory` → warn "Docker has X GB; running Y clusters needs ~Z GB; ArgoCD apply likely to timeout. Free resources before continuing." Non-blocking warning.
   - **Orphan `k8s_*` containers:** `docker ps --filter "name=k8s_"` outside of any active kind cluster's control-plane → "Stale pod containers from a deleted cluster. Run `docker container prune -f` to clean." Non-blocking warning.
   - **Existing `sharko-e2e` in degraded state:** if `${KIND_CLUSTER_NAME}` exists, probe control-plane health BEFORE assuming `--reuse`. If any kube-system pod is CrashLoopBackOff or ContainerCreating >2 min → **interactive prompt:** `[y/n] Existing ${KIND_CLUSTER_NAME} is unhealthy. Delete and recreate?` (unless `--force-clean` → auto-delete + recreate).
10. Add `--force-clean` flag (non-interactive; skips all prompts, applies recommended fixes automatically). Surface in `ready --help`.
11. Preflight prints a one-line "preflight: OK" or "preflight: N warning(s)" summary at the end so it's clear when it ran.

**Acceptance criteria:**

- `bash -n scripts/sharko-dev.sh` clean
- `./scripts/sharko-dev.sh upgrade --help` renders the new subcommand's help
- `./scripts/sharko-dev.sh help | grep -q upgrade` succeeds (main help lists `upgrade`)
- `./scripts/sharko-dev.sh ready --help | grep -q force-clean` succeeds
- `./scripts/sharko-dev.sh upgrade` (no arg) reads Chart.yaml version + runs successfully against a published version
- `./scripts/sharko-dev.sh ready` triggers preflight before any kind action; with no stale state, prints "preflight: OK" + continues
- `.local/scripts-pending/upgrade.sh` deleted from working tree
- No regression: existing `ready` / `up` / `install` / `rebuild` / `reset` / `down` subcommands unchanged in behavior

**Commit:**

```bash
git commit -m "$(cat <<'EOF'
feat(sharko-dev): upgrade subcommand + ready preflight resource check (V126-5.1)

Two maintainer DX items integrated into scripts/sharko-dev.sh:

#190 — upgrade <version> subcommand integrates the previously
standalone .local/scripts-pending/upgrade.sh into the main dispatch.
Polls the published OCI chart, runs helm upgrade against it, restarts
pod + port-forward, and verifies the reported version matches.
Catches values defaults bugs, image manifest issues, version-stamping
bugs, and OCI publish race conditions that only show with the
published artifact.

#191 — preflight() runs at the top of ready / up / install:
- Stale kind cluster detection (interactive cleanup prompt)
- Docker memory headroom warning (clusters × 2GB + 2GB vs total)
- Orphan k8s_* container detection
- Degraded sharko-e2e detection (CrashLoopBackOff or stuck ContainerCreating)
- --force-clean flag skips prompts for CI / scripted use

Closes the 2026-05-13 incident where ready failed silently after 5
minutes of ArgoCD apply because 4 leaked kind clusters + 1
crashlooping control-plane starved Docker (15.6 GB used). User now
gets a clear pre-flight warning instead of a context deadline exceeded
deep in the apply.

Reuses existing helpers (helm_release_exists, SHARKO_NAMESPACE,
SHARKO_LOCAL_PORT, pf restart logic). No new dependencies.

Refs: tasks #190 #191, epics-v126-5-sharko-dev-dx.md V126-5.1
EOF
)"
```

---

## Sequencing + dispatch order

| Wave | Stories | Mode |
|------|---------|------|
| A | V126-5.1 | Serial (single bash story, one agent) |

Total: 1 story, ~45-75 min agent runtime.

## Parallel-to-V126-4

This bundle dispatches in parallel with V126-4 (e2e harness QoL). Surfaces are independent — no merge conflict.

---

## All OQs PRE-RESOLVED

Per `feedback_decide_dont_ask_technical_oqs`:

1. Branch → `dev/v126-5-sharko-dev-dx`
2. `upgrade` no-arg → reads Chart.yaml version (consistent with how `rebuild` already works)
3. Preflight default → interactive prompts ON; `--force-clean` opts into non-interactive
4. Preflight non-blocking warnings → memory + orphan container checks are warn-only (don't block); stale cluster + degraded sharko-e2e prompts CAN block (user can decline + bail)
5. shellcheck → best-effort if installed; NOT a CI gate
6. Auto-merge → YES once CI green

## Done definition

- [ ] V126-5.1 shipped on `dev/v126-5-sharko-dev-dx`
- [ ] All quality gates green
- [ ] PR opened
- [ ] CI green
- [ ] PR auto-merged
- [ ] `.local/scripts-pending/upgrade.sh` deleted from main
- [ ] Tasks #190, #191 marked done
