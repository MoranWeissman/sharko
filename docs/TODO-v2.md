# Sharko — v1.8.0 Final State

> All items completed as of v1.8.0. This file is archived for historical reference.
> Active work tracked in GitHub Issues.

---

> _Original: Issues found during QA testing on real K8s cluster (v1.2.0)._

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

- [x] **GitHub token not auto-detected from Helm secret** — resolved: Sharko no longer uses a Helm-managed GitHub token secret. Connection credentials are entered once in Settings and stored encrypted. The Helm `secrets.GITHUB_TOKEN` value is now only a fallback for legacy deployments and will be removed in v2.0.
- [x] **Wizard ArgoCD auto-discovery inconsistent** — Fixed: `autoDiscoverArgoCD()` now lists all services in the ArgoCD namespace and probes each with `GET /api/v1/version`. First successful response wins. Falls back to env var if none respond.
- [x] **Init from Settings doesn't show ArgoCD bootstrap progress** — Fixed in v1.8.0: Settings page now displays a live progress log panel when init is running, showing each step as it completes, including ArgoCD sync polling.
- [x] **Cluster secrets not managed via GitOps** — design decision settled: imperative push via `internal/remoteclient/` is sufficient for v1.x. GitOps-only ESO reference template is tracked as a v2.0 deliverable. The secrets reconciler (5min polling + webhook + manual trigger) keeps secrets fresh without requiring GitOps round-trips.
- [x] **No bootstrap-config.yaml was being generated** — Fixed: `bootstrap/values.yaml` now included in init scaffold with `repoURL` and `targetRevision` placeholders replaced at init time.
- [x] **Wizard has no escape/skip option** — X button added to wizard header; dispatches `close-wizard` event to parent Layout.
- [x] **Bootstrap resume should show "continuing" message** — wizard/CLI now detects an existing in-progress bootstrap and displays "Resuming previous initialization…" instead of starting fresh.
- [x] **Auto-merge doesn't delete the branch after merging** — `DeleteBranch` is now called after successful `MergePullRequest` in the orchestrator.
- [x] **Addon detail missing AppSet info** — Fixed: addon detail page now shows ApplicationSet status (synced/errored), the number of generated Applications, and which clusters matched the AppSet generator.
- [x] **Advanced config fields lack context** — click-to-toggle HelpText panels added to all advanced config fields in AddonDetail. Clicking the HelpCircle icon expands an inline explanation.
- [x] **ignoreDifferences not editable + no example** — now editable via YAML textarea with placeholder example; saved via `PATCH /api/v1/addons/{name}`.
- [x] **additionalSources not editable + no example** — now editable via YAML textarea with placeholder example; saved via `PATCH /api/v1/addons/{name}`.
- [x] **Upgrade advisor not showing data** — Fixed: Helm repo fetcher now handles empty cluster lists gracefully (shows available chart versions even when no clusters have the addon enabled). Version comparison works end-to-end.
- [x] **"20+ latest versions" and "Compare versions" unclear** — Fixed: section headers renamed to "Available chart versions from Helm repository" and "Compare changelogs". Descriptive subtitles added.
- [x] **AWS SM secret format — structured JSON, not raw kubeconfig** — provider auto-detects format (raw kubeconfig vs structured JSON with `server`/`ca`/`token` keys). STS-based short-lived token generation via `role_arn` field is supported.
- [x] **Multi-cloud provider support** — GCP and Azure provider stubs added in v1.8.0 (`internal/providers/gcp.go`, `internal/providers/azure.go`). Both implement the interface and return `ErrNotImplemented`. The interface is defined and ready for community contributions.
- [x] **Settings page UX — organize sections in side nav** — Fixed: Settings uses `DetailNavPanel` with sections: Connections, Secrets Provider, GitOps, Users, API Keys, AI Provider. Each is a separate panel, not a scrollable form.
- [x] **Auto-detect host cluster name** — Fixed: `SHARKO_HOST_CLUSTER_NAME` can be set via Helm value `hostClusterName`. A future improvement will auto-detect from the node's cloud provider metadata, but the manual override is sufficient for v1.x.
- [x] **Default addons should be a dynamic checklist** — Fixed: the wizard's addon selection step uses a multi-select checklist populated from `GET /api/v1/addons/catalog`.
- [x] **Branch prefix is too niche for main settings** — Fixed: moved to an "Advanced" collapsible section in Settings > GitOps. Hidden by default.
- [x] **Settings has too many fields** — Addressed for v1.x: advanced fields hidden under toggles, smarter defaults, branch prefix and commit prefix moved to Advanced. Further simplification planned for v2.0 alongside auto-detection work.

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
- [x] ArgoCD resource exclusion config for Sharko-managed secrets — documented in operator/configuration.md; ArgoCD restart note included
- [ ] GitOps-only ESO reference template (separate repo/deliverable) — **v2.0 scope**
- [ ] V2: Full operator with CRDs (SharkoAddon, SharkoSecret, SharkoCluster) — **v2.0 scope**

---

## Improvements

- N/A **Auto-detect GitHub token from Helm secret** — removed; Sharko no longer uses a Helm-managed GitHub token secret. Connection credentials are entered once in Settings and stored encrypted.
- [x] **Security advisory detection** — `GET /api/v1/notifications` now surfaces `security_advisory` type notifications when a major version bump is detected in the Helm repo index.
- [x] **Filtering/sorting on list endpoints** — `GET /api/v1/clusters` and `GET /api/v1/addons/catalog` support `?sort=<field>` and `?filter=<predicate>` query params.
- [x] **Audit log for manual changes** — `GET /api/v1/audit` implemented in v1.8.0. In-memory ring buffer recording all write operations with actor, action, target, result, and timestamp.
- [x] **E2E tests** — E2E framework added in v1.8.0 (`e2e/` package). `make e2e-setup`, `make e2e`, `make e2e-teardown`. Tests against real ArgoCD in Kind cluster.
- [x] **AI-parsed release notes** — AI SimplePrompt used to summarize chart changelogs. `POST /api/v1/addons/{name}/ai-summary` endpoint. Summary shown in AddonDetail overview tab.
- [x] **Addon dependency ordering** — `dependsOn` field added to `AddonCatalogEntry` in v1.8.0. `--depends-on` CLI flag on `add-addon`. Cycle detection via topological sort. Generates ArgoCD sync waves.
- [x] **Separate managed vs discovered clusters in dashboard** — ClustersOverview now shows two sections: Managed (full Sharko features) and Discovered (ArgoCD-only, with Adopt button).
- [x] **Adopt existing ArgoCD cluster secrets** — `POST /api/v1/clusters/{name}/adopt` implemented. UI shows "Adopt" button on discovered cluster cards.
- [x] **Cluster connectivity check** — `POST /api/v1/clusters/{name}/test` implemented. CLI: `sharko test-cluster`. UI: Test Connectivity button on cluster detail page.

---

## Tech Debt

- [ ] **Test coverage** — orchestrator ~78%, overall ~23%. Target 50%+. **v2.0 priority.**
- [ ] **Storybook** — deferred, low ROI for current team size. **Not planned.**
