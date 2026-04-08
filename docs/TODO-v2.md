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
- [x] **Wizard has no escape/skip option** — X button added to wizard header; dispatches `close-wizard` event to parent Layout.
- [x] **Bootstrap resume should show "continuing" message** — wizard/CLI now detects an existing in-progress bootstrap and displays "Resuming previous initialization…" instead of starting fresh.
- [x] **Auto-merge doesn't delete the branch after merging** — `DeleteBranch` is now called after successful `MergePullRequest` in the orchestrator.
- [ ] **Addon detail missing AppSet info** — each addon is an ApplicationSet but the detail page doesn't show AppSet status (synced/errored, how many apps generated, which clusters matched)
- [x] **Advanced config fields lack context** — click-to-toggle HelpText panels added to all advanced config fields in AddonDetail. Clicking the HelpCircle icon expands an inline explanation.
- [x] **ignoreDifferences not editable + no example** — now editable via YAML textarea with placeholder example; saved via `PATCH /api/v1/addons/{name}`.
- [x] **additionalSources not editable + no example** — now editable via YAML textarea with placeholder example; saved via `PATCH /api/v1/addons/{name}`.
- [ ] **Upgrade advisor not showing data** — the version comparison and upgrade recommendations section appears empty. May be because no clusters have the addon enabled, or the Helm repo fetch isn't returning versions. Need to verify the upgrade version check works end-to-end
- [ ] **"20+ latest versions" and "Compare versions" unclear** — these UI sections need better labels and explanation of what they do (show available chart versions from Helm repo, compare changelogs between versions)
- [x] **AWS SM secret format — structured JSON, not raw kubeconfig** — provider auto-detects format (raw kubeconfig vs structured JSON with `server`/`ca`/`token` keys). STS-based short-lived token generation via `role_arn` field is supported.
- [ ] **Multi-cloud provider support (V1.x/V2)** — interface is pluggable but only AWS is implemented. GCP (oauth2 token), Azure (AD token) need their own implementations. Community can contribute — the interface is "given cluster info, return a short-lived token."
- [ ] **Settings page UX — organize sections in side nav** — currently one long form with everything crammed together. Should use DetailNavPanel with separate sections: Connection (Git + ArgoCD), Secrets Provider, GitOps Settings, Users, API Keys, AI Provider. Each section is its own page, not a scrollable form.
- [ ] **Auto-detect host cluster name** — Sharko runs on the cluster, it can detect its own name. Don't ask the user.
- [ ] **Default addons should be a dynamic checklist** — show checkboxes from the current catalog, not a text input. Users pick from what's available.
- [ ] **Branch prefix is too niche for main settings** — most users never change it. Hide under an "Advanced" toggle or remove entirely.
- [ ] **Settings has too many fields** — even with side nav, 6 sections is overwhelming. Goal: fewer settings, smarter defaults, auto-detection. Less config = better product.

---

## Design Decisions (Next Phases)

Full design spec: `docs/design/2026-04-07-sharko-v1-design-decisions.md`

### Action Items from Design Session

- [x] Remove `secrets` block from Helm values.yaml — Done in PR #102
- [x] Remove env var credential fallback from serve.go — Done in PR #102
- [x] Remove `config.devMode` flag — Done in PR #102
- [x] Single connection — remove connection list UI, edit-only Settings page — Done in PR #102
- [x] Operations endpoint (`/api/v1/operations`) with session model — Done in PR #102
- [x] Heartbeat-based session keep-alive (no timeout clock) — Done in PR #102
- [x] PR merge polling in init flow (UI shows status, CLI watches live) — Done in PR #102
- [x] Resume support for interrupted init sessions — Done in PR #114
- [x] Add `secrets:` field to AddonCatalogEntry model — Done in PR #105
- [x] SecretProvider interface with k8s-secrets and aws-sm backends — Done in PR #105
- [x] Background secrets reconciler goroutine — Done in PR #105
- [x] Remote cluster secret push via existing remoteclient — Done in PR #105
- [ ] ArgoCD resource exclusion config for Sharko-managed secrets
- [ ] GitOps-only ESO reference template (separate repo/deliverable)
- [ ] V2: Full operator with CRDs (SharkoAddon, SharkoSecret, SharkoCluster)

---

## Improvements

- N/A **Auto-detect GitHub token from Helm secret** — removed; Sharko no longer uses a Helm-managed GitHub token secret. Connection credentials are entered once in Settings and stored encrypted.
- [x] **Security advisory detection** — `GET /api/v1/notifications` now surfaces `security_advisory` type notifications when a major version bump is detected in the Helm repo index.
- [x] **Filtering/sorting on list endpoints** — `GET /api/v1/clusters` and `GET /api/v1/addons/catalog` support `?sort=<field>` and `?filter=<predicate>` query params.
- [ ] **Audit log for manual changes** — webhook exists but no audit trail persisted
- [ ] **E2E tests** — test against real ArgoCD (Kind + ArgoCD in CI)
- [ ] **AI-parsed release notes** — use AI provider to summarize chart changelogs
- [ ] **Addon dependency ordering** — declare addon B depends on addon A
- [x] **Separate managed vs discovered clusters in dashboard** — ClustersOverview now shows two sections: Managed (full Sharko features) and Discovered (ArgoCD-only, with Adopt button).
- [x] **Adopt existing ArgoCD cluster secrets** — `POST /api/v1/clusters/{name}/adopt` implemented. UI shows "Adopt" button on discovered cluster cards.
- [x] **Cluster connectivity check** — `POST /api/v1/clusters/{name}/test` implemented. CLI: `sharko test-cluster`. UI: Test Connectivity button on cluster detail page.

---

## Tech Debt

- [ ] **Test coverage** — orchestrator ~78%, overall ~23%. Target 50%+
- [ ] **Storybook** — deferred, low ROI for current team size
