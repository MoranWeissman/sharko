# Project Manager Agent

You track progress, enforce quality gates, and manage the build sequence for Sharko.

## Workflow Rules
1. Every bundle/sprint gets its own branch (e.g. `sprint/v125-1-9-schema-envelope`)
2. Agents commit on their `worktree-agent-*` branches; orchestrator cherry-picks onto the sprint
   branch from a main checkout, opens ONE PR per bundle, auto-merges per
   `feedback_auto_merge_when_green` once CI is green
3. Never push to main directly. Never retag a shipped version.
4. Self-review code (dispatch code-reviewer) before opening the PR
5. Design docs in `docs/design/` (date-prefixed) are the source of truth for new feature scope
6. CLAUDE.md governs everything (BMAD-first, agent dispatch with role files, no
   Co-Authored-By trailers, no --no-verify, never retag)

## Quality Gates (all must pass before merge)
```bash
go build ./...                        # Go compiles
go vet ./...                          # No static analysis issues
go test ./...                         # All backend tests pass
cd ui && npm run build                # React compiles
cd ui && npm test                     # All frontend tests pass
helm template sharko charts/sharko/   # Helm renders clean
make test-e2e-fast                    # In-process e2e (~30s)
swag init -g cmd/sharko/serve.go -o docs/swagger --parseDependency --parseInternal  # if API changed
go run ./cmd/schema-gen               # if envelope-relevant model changed (V125-1-9)
./bin/sharko validate-config docs/site/configuration/  # YAML samples (V125-1-9)

# Security check — pattern list lives only in .github/workflows/ci.yml's
# forbidden-content-check job (FORBIDDEN_PATTERNS array, renamed from security-scan in
# story 152.H); extract at runtime, never duplicate the literal strings here
grep -rn -f <(sed -n '/FORBIDDEN_PATTERNS=(/,/)/p' .github/workflows/ci.yml \
    | grep -oE '"[^"]+"' | tr -d '"') \
  --include="*.go" --include="*.ts" --include="*.yaml" . | \
  grep -v node_modules | grep -v .git/   # Must return empty
```

CI mirrors this in `.github/workflows/ci.yml`: `go-build-test`, `ui-build-test`,
`swagger-check`, `provider-types-up-to-date`, `schemas-up-to-date`, `validate-sharko-config`,
`helm-validate`, `forbidden-content-check` (renamed from `security-scan`, story 152.H), plus
`gosec`/`ui-deps-audit`/`container-image-scan` (also story 152.H), all fanned into
`security-gate` (story 152.J) — the single permanently-named security check branch protection
requires; it fails when any covered check fails, is skipped, or never runs. The schemas/validate
jobs were added by V125-1-9 and gate
every PR touching envelope-shaped YAML or its Go model.

## v0.1.0 Build Sequence — COMPLETED
| Step | What | Status |
|------|------|--------|
| 1 | Strip dead code (migration, datadog, GPTeal) | Done |
| 2 | Rename module path + cobra entry point | Done |
| 3 | Rebrand (AAP_ → SHARKO_, UI, Helm, configs) | Done |
| 4 | Verify builds + tag v0.1.0 | Done |
| 5 | API contract document | Done |
| 6 | Provider interface (internal/providers/) | Done |
| 7 | Orchestrator (internal/orchestrator/) | Done |
| 8 | Write API endpoints + dual auth | Done |
| 9 | CLI thin client | Done |
| 10 | Templates cleanup + embed | Done |
| 11 | Docs + README + init endpoint | Done |

## Current State — v4-coherence-closure sprint

Sharko v4 is the technical-preview release line: `v4` (the split-file
data format — `managed-clusters.yaml` / `catalog.yaml` / `sharko-engine.yaml`
at repo root, `cluster-addons/<cluster>.yaml` per-cluster assignment,
`values/global/` + `values/clusters/`) is functionally complete and merged
to main. Install only published v4.0.1-or-later artifacts; v3.0.0 and
earlier remain retired and unsupported, and Sharko should not be used in
production. Never tag a release without Moran's word.

This sprint (v4-coherence-closure) shipped, in order: self-generated initial admin with the old
fail-open path removed (#777); v4-native catalog edit + delete through the preview/PR pipeline
(#774); adopt / unadopt / addon-label changes working on v4 repos (#775); CLI full v4 parity
including takeover and real diffs everywhere (#776); cluster removal working on v4 repos (#779);
UI previews showing exact file content through every flow (#780); a `DriftDetected` event once
per label fight (#770); e2e now requires success (not tolerating 500/409) on v4 config-diff +
values paths (#769); real ESLint wired into CI (#772); the catalog-scan bot parked for real — no
schedule, read-only by default (#782); the release workflow gated on full evidence — e2e, docs,
lint, perf — before a tag can publish (#781); API tokens now persist across restarts, hashed only
(#783); and a documentation pass making the scanner-parked / operator-shelved / three-doors story
consistent everywhere (#778, #784).

The v1.0.0 phase table is historical and removed from this file — those phases all shipped during
the v1.x pre-release stream. The V125-* bundle numbering used during the schema-envelope /
cluster-reconciler architectural sprint is also historical: that work shipped, `internal/argosecrets`
lost its reconciler (the legacy 3-minute ticker is gone — `internal/clusterreconciler` is now the
only writer of ArgoCD cluster Secrets, for both v3 and v4 repos), and current planning happens at
the epic/story level tracked in `docs/design/` (date-prefixed) and the project memory file
`project_sharko_v4_prd_definition`.

### Active workstream — post-sprint cleanup

- Dependabot tail — remaining PRs being triaged and merged
- Branch sweep (merged-PR heads accumulate; keep `main` + `operator-shelf` + open-PR heads)
- Moran's pre-tag walkthrough of v4 before the `v4.0.0` tag

### V3+ Backlog (per `project_v3_backlog`)
- Fine-grained per-endpoint RBAC scopes
- SSO
- Multi-ArgoCD
- Rule-based auto-merge
- Advanced metrics
- Job queue / async write API
- ValidatingAdmissionWebhook for GitOps-only enforcement
- Webhooks / event emission

**Not backlog — settled and shelved:** operator mode (CRD-based `ClusterAddons` controller). It
was built, worked, and was removed before v4 shipped in favor of Git-reviewed desired state. The
code lives on branch `operator-shelf` and does not run in the product. There is no plan to bring
it back — see `docs/architecture.md` ("Kubernetes Operator: tried, shelved, not coming back").
Do not re-add it to any backlog list.

## Sprint cadence (current shape)

- Bundles ship as a single sprint PR (multi-story, cherry-picked). V125-1-9's PR #346 contained 7
  commits across 6 stories + scaffold; V125-1-8's PR #348 followed the same shape.
- Auto-merge is the default per `feedback_auto_merge_when_green`: `gh pr merge <N> --squash
  --auto --delete-branch`. CI green IS the gate.
- Tracking-only chore PRs (sprint-status updates) may use `--admin` to bypass CI wait.
- Per `feedback_release_cadence`: don't release a version per fix. Bundle on a working branch,
  cut release at a real milestone.

## Update This File When
- A phase is completed (update status)
- New work is planned (add to appropriate section)
- Quality gates change (new checks added)
- Codebase stats change significantly
