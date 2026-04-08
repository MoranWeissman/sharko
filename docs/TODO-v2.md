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
- [ ] **Auto-merge doesn't delete the branch after merging** — when Sharko auto-merges a PR, the source branch is left behind. Should call `DeleteBranch` after successful merge.
- [ ] **Addon detail missing AppSet info** — each addon is an ApplicationSet but the detail page doesn't show AppSet status (synced/errored, how many apps generated, which clusters matched)
- [ ] **Advanced config fields lack context** — syncWave, selfHeal, syncOptions etc. are shown without examples or explanation of what they do. Add inline help text or tooltips explaining each field and when to use it
- [ ] **ignoreDifferences not editable + no example** — field is read-only with no guidance on format. Should show an example like `group: apps, kind: Deployment, jsonPointers: [/spec/replicas]`
- [ ] **additionalSources not editable + no example** — same as above, needs format guidance and edit capability
- [ ] **Upgrade advisor not showing data** — the version comparison and upgrade recommendations section appears empty. May be because no clusters have the addon enabled, or the Helm repo fetch isn't returning versions. Need to verify the upgrade version check works end-to-end
- [ ] **"20+ latest versions" and "Compare versions" unclear** — these UI sections need better labels and explanation of what they do (show available chart versions from Helm repo, compare changelogs between versions)
- [ ] **AWS SM secret format — structured JSON, not raw kubeconfig** — Sharko's AWS SM provider expects a full kubeconfig YAML but real secrets have individual keys (host, caData, clusterName, region, etc.). Need to auto-detect format and build kubeconfig from structured JSON. Also need STS-based short-lived token generation (like argocd-k8s-auth) instead of static tokens.
- [ ] **Multi-cloud provider support (V1.x/V2)** — interface is pluggable but only AWS is implemented. GCP (oauth2 token), Azure (AD token) need their own implementations. Community can contribute — the interface is "given cluster info, return a short-lived token."
- [ ] **Settings page UX — organize sections in side nav** — currently one long form with everything crammed together. Should use DetailNavPanel with separate sections: Connection (Git + ArgoCD), Secrets Provider, GitOps Settings, Users, API Keys, AI Provider. Each section is its own page, not a scrollable form.

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

- [ ] **Auto-detect GitHub token from Helm secret** — connection setup should check if GITHUB_TOKEN exists in the Sharko pod's env vars and pre-fill
- [ ] **Security advisory detection** — parse Helm chart release notes for CVE mentions
- [ ] **Filtering/sorting on list endpoints** — only pagination was done
- [ ] **Audit log for manual changes** — webhook exists but no audit trail persisted
- [ ] **E2E tests** — test against real ArgoCD (Kind + ArgoCD in CI)
- [ ] **AI-parsed release notes** — use AI provider to summarize chart changelogs
- [ ] **Addon dependency ordering** — declare addon B depends on addon A
- [ ] **Separate managed vs discovered clusters in dashboard** — clusters in cluster-addons.yaml show as "Managed" with full Sharko features. Clusters only in ArgoCD show as "Discovered" with a "Start managing" button. Don't hide ArgoCD-only clusters.
- [ ] **Adopt existing ArgoCD cluster secrets** — when adding a cluster that ArgoCD already knows about, skip registration and just add it to cluster-addons.yaml + create values file. Show "This cluster is already in ArgoCD — add to Sharko management?"
- [ ] **Cluster connectivity check** — ability to test if a cluster is reachable, either via direct connection test or by deploying a lightweight hello-world addon (ConfigMap) and verifying ArgoCD can sync it

---

## Tech Debt

- [ ] **Test coverage** — orchestrator ~78%, overall ~23%. Target 50%+
- [ ] **Storybook** — deferred, low ROI for current team size
