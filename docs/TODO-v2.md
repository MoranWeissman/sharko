# Sharko — Post v1.2.0 TODO

> Issues found during QA testing on real K8s cluster.

---

## Bugs

- [x] ~~CSP blocks Google Fonts stylesheets~~ — Fixed in PR #90
- [x] ~~Dashboard crashes on uninitialized repo~~ — Fixed in PR #95
- [x] ~~Wizard skips Step 4 (Init)~~ — Fixed in PR #96
- [x] ~~ArgoCD defaults to HTTPS for in-cluster~~ — Fixed in PR #96
- [x] ~~Init fails on empty repos~~ — Fixed in PR #96
- [x] ~~Init writes files one-by-one (slow)~~ — Fixed in PR #96 (batch Git tree API)
- [x] ~~Version shows 1.0.0~~ — Fixed in PR #94

---

## Open Issues

- [ ] **GitHub token not auto-detected from Helm secret** — user passes `--set secrets.GITHUB_TOKEN` during install, but wizard asks for token again. The connection system uses its own encrypted secret and doesn't read the Helm-managed one.
- [ ] **Wizard ArgoCD auto-discovery inconsistent** — tries common service names but may not match all installations. Needs to list services in the ArgoCD namespace and probe them.
- [ ] **Init from Settings doesn't show ArgoCD bootstrap progress** — when initializing from Settings (not wizard), the UI doesn't show sync status polling. The API does the work but the UI doesn't reflect it.
- [ ] **Cluster secrets not managed via GitOps** — the old ESO-based approach (Git → ExternalSecret → AWS SM → K8s Secret) was removed. Sharko now creates secrets imperatively via API (`internal/remoteclient/`). This means cluster registration only works through `sharko add-cluster`, not by editing Git directly. Design decision needed: is imperative sufficient, or do we need a declarative (GitOps) secret mechanism?
- [ ] **No bootstrap-config.yaml was being generated** — root-app.yaml references `{{ .Values.repoURL }}` but the values file with repoURL/targetRevision was missing from the bootstrap template. Fixed in this PR.
- [ ] **Wizard has no escape/skip option** — says "You can always update connections later in Settings" but there's no way to close or skip the wizard. Need a "Skip to Dashboard" link or X button.
- [ ] **Bootstrap resume should show "continuing" message** — when Sharko detects a previously started bootstrap (e.g., PR exists but not merged), the wizard/CLI should indicate it's resuming an existing process, not starting fresh

---

## Design Decisions (Next Phases)

Full design spec: `docs/design/2026-04-07-sharko-v1-design-decisions.md`

### Action Items from Design Session

- [ ] Remove `secrets` block from Helm values.yaml
- [ ] Remove env var credential fallback from serve.go
- [ ] Remove `config.devMode` flag
- [ ] Single connection — remove connection list UI, edit-only Settings page
- [ ] Operations endpoint (`/api/v1/operations`) with session model
- [ ] Heartbeat-based session keep-alive (no timeout clock)
- [ ] PR merge polling in init flow (UI shows status, CLI watches live)
- [ ] Resume support for interrupted init sessions
- [ ] Add `secrets:` field to AddonCatalogEntry model
- [ ] SecretProvider interface with k8s-secrets and aws-sm backends
- [ ] Background secrets reconciler goroutine
- [ ] Remote cluster secret push via existing remoteclient
- [ ] ArgoCD resource exclusion config for Sharko-managed secrets
- [ ] GitOps-only ESO reference template (separate repo/deliverable)
- [ ] V2: Full operator with CRDs (SharkoAddon, SharkoSecret, SharkoCluster)

---

## Improvements

- [ ] **Auto-detect GitHub token from Helm secret** — connection setup should check if GITHUB_TOKEN exists in the Sharko pod's env vars and pre-fill
- [ ] **Security advisory detection** — parse Helm chart release notes for CVE mentions
- [ ] **Filtering/sorting on list endpoints** — only pagination was done
- [ ] **Audit log for manual changes** — webhook exists but no audit trail persisted
- [ ] **E2E tests** — test against real ArgoCD (Kind + ArgoCD in CI)
- [ ] **AI-parsed release notes** — use AI provider to summarize chart changelogs
- [ ] **Addon dependency ordering** — declare addon B depends on addon A

---

## Tech Debt

- [ ] **Test coverage** — orchestrator ~78%, overall ~23%. Target 50%+
- [ ] **Storybook** — deferred, low ROI for current team size
