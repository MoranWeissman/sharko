# Sharko Testing Runbook

> Systematic QA testing guide. Test every feature via CLI, API, and UI.
> Replace `<ARGOCD_URL>`, `<GIT_REPO>`, and `<TOKEN>` with your values.

## Prerequisites

- Sharko deployed and port-forwarded to localhost:8080
- `sharko` CLI built: `go build -o /usr/local/bin/sharko ./cmd/sharko`
- Logged in: `sharko login --server http://localhost:8080`
- ArgoCD token ready
- GitHub fine-grained token ready
- Empty test repo created

**Get your session token** (needed for raw curl calls):

```bash
curl -s -X POST http://localhost:8080/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"username": "<USERNAME>", "password": "<PASSWORD>"}' | jq -r '.token'
```

Store the result as `TOKEN` in your shell:

```bash
export TOKEN=<paste_token_here>
```

---

## 1. Connection Management

Connections wire Sharko to a specific Git repo + ArgoCD instance. There is always one active connection.

### 1.1 Create Connection (API)

```bash
curl -s -X POST "http://localhost:8080/api/v1/connections/" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "dev",
    "git_provider": "github",
    "git_repo": "<GIT_REPO>",
    "git_token": "<GITHUB_TOKEN>",
    "argocd_server_url": "<ARGOCD_URL>",
    "argocd_token": "<ARGOCD_TOKEN>",
    "argocd_namespace": "argocd"
  }' | jq .
```

Expected: 201 with connection details.

### 1.2 List Connections (API)

```bash
curl -s "http://localhost:8080/api/v1/connections/" \
  -H "Authorization: Bearer $TOKEN" | jq .
```

Expected: array containing the "dev" connection.

### 1.3 Test Connection (API)

```bash
curl -s -X POST "http://localhost:8080/api/v1/connections/test" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name": "dev"}' | jq .
```

Expected: status "ok" for both Git and ArgoCD.

### 1.4 Set Active Connection (API)

```bash
curl -s -X POST "http://localhost:8080/api/v1/connections/active" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name": "dev"}' | jq .
```

Expected: 200 confirming "dev" is now active.

### 1.5 Verify via UI

- Settings → Connections — should show "dev" as active with green ArgoCD/Git status indicators.

---

## 2. Repository Initialization

Init creates the bootstrap GitOps directory structure and opens a PR in the configured repo.

### 2.1 Init via CLI

```bash
sharko init
```

Expected: prints files created and a PR URL. The `--no-bootstrap` flag skips ArgoCD app registration.

### 2.2 Init via API

```bash
curl -s -X POST "http://localhost:8080/api/v1/init" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"bootstrap_argocd": true}' | jq .
```

Expected: 200 or 201 with `files_created` list and optional `argocd` object.

### 2.3 Verify

- Check the test repo on GitHub — should have `bootstrap/`, `charts/`, `configuration/` directories.
- UI Dashboard should load without errors after the PR is merged.

---

## 3. Addon Management

### 3.1 Add Addon (CLI)

```bash
sharko add-addon keda \
  --chart keda \
  --repo https://kedacore.github.io/charts \
  --version 2.16.1
```

Optional flags: `--namespace <ns>`, `--sync-wave <int>`.

Expected: prints "done" and a PR URL.

### 3.2 Add Addon (API)

```bash
curl -s -X POST "http://localhost:8080/api/v1/addons" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "argo-rollouts",
    "chart": "argo-rollouts",
    "repo_url": "https://argoproj.github.io/argo-helm",
    "version": "2.38.2",
    "namespace": "argo-rollouts"
  }' | jq .
```

Expected: 201 with `git.pr_url`.

### 3.3 List Addons — Catalog (API)

```bash
curl -s "http://localhost:8080/api/v1/addons/catalog" \
  -H "Authorization: Bearer $TOKEN" | jq '.addons[] | {name: .addon_name, version: .version}'
```

### 3.4 List Addons — Status (CLI)

```bash
sharko status
```

Expected: table of all clusters and their addon sync status.

### 3.5 Get Addon Detail (API)

```bash
curl -s "http://localhost:8080/api/v1/addons/keda" \
  -H "Authorization: Bearer $TOKEN" | jq .
```

For the full detail view including defaults:

```bash
curl -s "http://localhost:8080/api/v1/addons/keda/detail" \
  -H "Authorization: Bearer $TOKEN" | jq .
```

### 3.6 Describe Addon (CLI)

```bash
sharko describe-addon keda
sharko describe-addon argo-rollouts
```

Expected: prints chart, repo, version, namespace, sync wave, self-heal, sync options, ignore-differences, extra Helm values. Fields show `(default)` when they match the system default.

### 3.7 Configure Addon (CLI)

```bash
sharko configure-addon keda --sync-wave 1
sharko configure-addon argo-rollouts --self-heal=false
sharko configure-addon keda --sync-option ServerSideApply=true
sharko configure-addon keda --version 2.16.2
```

Each flag is applied independently — only changed flags are sent.

### 3.8 Configure Addon (API)

```bash
curl -s -X PATCH "http://localhost:8080/api/v1/addons/keda" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"sync_wave": 2}' | jq .
```

Expected: 200 with optional `git.pr_url`.

### 3.9 Verify via UI

- Addons Catalog — both addons should appear as cards.
- Click an addon → Detail page with left nav (Overview / Clusters / Upgrade / Config).
- Overview → Advanced Configuration accordion shows sync wave, self-heal settings.

---

## 4. Cluster Management

### 4.1 Add Cluster (CLI)

```bash
sharko add-cluster staging \
  --addons keda,argo-rollouts \
  --region us-east-1
```

Expected: prints registered cluster details and PR URL.

### 4.2 Add Cluster (API)

```bash
curl -s -X POST "http://localhost:8080/api/v1/clusters" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "staging",
    "addons": {"keda": true, "argo-rollouts": true},
    "region": "us-east-1"
  }' | jq .
```

Expected: 201 or 207 (partial). Check `git.pr_url`.

### 4.3 List Clusters (CLI)

```bash
sharko list-clusters
```

Expected: table with NAME, STATUS, REGION, ADDONS columns.

### 4.4 List Clusters (API)

```bash
curl -s "http://localhost:8080/api/v1/clusters" \
  -H "Authorization: Bearer $TOKEN" | jq '.clusters[] | {name: .name, status: .connection_status}'
```

### 4.5 Get Cluster Detail (API)

```bash
curl -s "http://localhost:8080/api/v1/clusters/staging" \
  -H "Authorization: Bearer $TOKEN" | jq .
```

### 4.6 Update Cluster Addons (CLI)

```bash
sharko update-cluster staging --add-addon keda --remove-addon argo-rollouts
```

### 4.7 Update Cluster Addons (API)

```bash
curl -s -X PATCH "http://localhost:8080/api/v1/clusters/staging" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"addons": {"keda": true, "argo-rollouts": false}}' | jq .
```

### 4.8 Discover Available Clusters (API)

```bash
curl -s "http://localhost:8080/api/v1/clusters/available" \
  -H "Authorization: Bearer $TOKEN" | jq .
```

Expected: clusters registered in ArgoCD but not yet in Sharko.

### 4.9 Remove Cluster (CLI)

```bash
sharko remove-cluster staging
```

### 4.10 Verify via UI

- Clusters page — all ArgoCD-registered clusters appear with status badges.
- Click a cluster → detail view with addon list and sync status.

---

## 5. Upgrade Checking

### 5.1 List Available Versions (API)

```bash
curl -s "http://localhost:8080/api/v1/upgrade/keda/versions" \
  -H "Authorization: Bearer $TOKEN" | jq '.versions[:5]'
```

### 5.2 Check Upgrade Impact (API)

```bash
curl -s -X POST "http://localhost:8080/api/v1/upgrade/check" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"addon": "keda", "from_version": "2.16.1", "to_version": "2.16.2"}' | jq .
```

### 5.3 Addon Changelog (API)

```bash
curl -s "http://localhost:8080/api/v1/addons/keda/changelog?from=2.14.0&to=2.16.1" \
  -H "Authorization: Bearer $TOKEN" | jq .
```

### 5.4 Upgrade Addon — Global (CLI)

```bash
sharko upgrade-addon keda --version 2.16.2
```

### 5.5 Upgrade Addon — Per-Cluster Override (CLI)

```bash
sharko upgrade-addon keda --version 2.16.2 --cluster staging
```

### 5.6 Upgrade Addon (API)

```bash
curl -s -X POST "http://localhost:8080/api/v1/addons/keda/upgrade" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"version": "2.16.2"}' | jq .
```

### 5.7 Batch Upgrade (CLI)

```bash
sharko upgrade-addons keda=2.16.2,argo-rollouts=2.38.3
```

Format: comma-separated `addon=version` pairs. Opens a single PR for all upgrades.

### 5.8 Batch Upgrade (API)

```bash
curl -s -X POST "http://localhost:8080/api/v1/addons/upgrade-batch" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"upgrades": {"keda": "2.16.2", "argo-rollouts": "2.38.3"}}' | jq .
```

### 5.9 Verify via UI

- Addon Detail → Upgrade tab — version list, diff/compare view, upgrade button.

---

## 6. Version Matrix / Drift

### 6.1 Version Matrix (API)

```bash
curl -s "http://localhost:8080/api/v1/addons/version-matrix" \
  -H "Authorization: Bearer $TOKEN" | jq '.addons[] | {addon: .addon_name, cells: .cells}'
```

Expected: per-cluster version status showing which clusters are in sync vs. drifted.

### 6.2 Fleet Status (API)

```bash
curl -s "http://localhost:8080/api/v1/fleet/status" \
  -H "Authorization: Bearer $TOKEN" | jq .
```

### 6.3 Verify via UI

- Dashboard → Version Drift summary widget showing drifted clusters.

---

## 7. Dashboard & Observability

### 7.1 Dashboard Stats (API)

```bash
curl -s "http://localhost:8080/api/v1/dashboard/stats" \
  -H "Authorization: Bearer $TOKEN" | jq .
```

### 7.2 Attention Items (API)

```bash
curl -s "http://localhost:8080/api/v1/dashboard/attention" \
  -H "Authorization: Bearer $TOKEN" | jq .
```

### 7.3 Open Pull Requests (API)

```bash
curl -s "http://localhost:8080/api/v1/dashboard/pull-requests" \
  -H "Authorization: Bearer $TOKEN" | jq .
```

### 7.4 Observability Overview (API)

```bash
curl -s "http://localhost:8080/api/v1/observability/overview" \
  -H "Authorization: Bearer $TOKEN" | jq '.control_plane'
```

### 7.5 Verify via UI

- Dashboard — stats cards (clusters, addons, drift), health bars, problem cluster list.
- Observability page — control plane health, sync activity timeline.

---

## 8. Addon Secrets

Addon secrets allow injecting sensitive Helm values (e.g. licence keys) from remote cluster secrets.

### 8.1 Create Addon Secret Definition (API)

```bash
curl -s -X POST "http://localhost:8080/api/v1/addon-secrets" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "addon": "keda",
    "secret_name": "keda-operator-secret",
    "namespace": "keda",
    "key": "license-key"
  }' | jq .
```

### 8.2 List Addon Secrets (API)

```bash
curl -s "http://localhost:8080/api/v1/addon-secrets" \
  -H "Authorization: Bearer $TOKEN" | jq .
```

### 8.3 List Secrets on a Cluster (API)

```bash
curl -s "http://localhost:8080/api/v1/clusters/staging/secrets" \
  -H "Authorization: Bearer $TOKEN" | jq .
```

### 8.4 Refresh Secrets on a Cluster (API)

```bash
curl -s -X POST "http://localhost:8080/api/v1/clusters/staging/secrets/refresh" \
  -H "Authorization: Bearer $TOKEN" | jq .
```

### 8.5 Delete Addon Secret Definition (API)

```bash
curl -s -X DELETE "http://localhost:8080/api/v1/addon-secrets/keda" \
  -H "Authorization: Bearer $TOKEN" | jq .
```

---

## 9. Notifications

### 9.1 List Notifications (API)

```bash
curl -s "http://localhost:8080/api/v1/notifications" \
  -H "Authorization: Bearer $TOKEN" | jq .
```

### 9.2 Mark All Read (API)

```bash
curl -s -X POST "http://localhost:8080/api/v1/notifications/read-all" \
  -H "Authorization: Bearer $TOKEN" | jq .
```

### 9.3 Verify via UI

- Notification bell in the top bar — badge count decreases after mark-all-read.

---

## 10. Settings & Users

### 10.1 Get System Config (API)

```bash
curl -s "http://localhost:8080/api/v1/config" \
  -H "Authorization: Bearer $TOKEN" | jq .
```

### 10.2 List Users (API — admin only)

```bash
curl -s "http://localhost:8080/api/v1/users" \
  -H "Authorization: Bearer $TOKEN" | jq .
```

### 10.3 Create User (API — admin only)

```bash
curl -s -X POST "http://localhost:8080/api/v1/users" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"username": "test-viewer", "password": "changeme123", "role": "viewer"}' | jq .
```

### 10.4 Update Password (API)

```bash
curl -s -X POST "http://localhost:8080/api/v1/auth/update-password" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"current_password": "<CURRENT>", "new_password": "<NEW_8_CHARS_MIN>"}' | jq .
```

### 10.5 Create API Key (CLI)

```bash
sharko token create --name ci-token --role admin
```

Roles: `admin`, `operator`, `viewer`. Token is shown once — copy it immediately.

### 10.6 Create API Key (API)

```bash
curl -s -X POST "http://localhost:8080/api/v1/tokens" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name": "ci-token", "role": "admin"}' | jq .
```

Expected: 201 with `token` field (prefixed `sharko_`).

### 10.7 List API Keys (CLI)

```bash
sharko token list
```

### 10.8 List API Keys (API)

```bash
curl -s "http://localhost:8080/api/v1/tokens" \
  -H "Authorization: Bearer $TOKEN" | jq .
```

### 10.9 Revoke API Key (CLI)

```bash
sharko token revoke ci-token
```

### 10.10 Revoke API Key (API)

```bash
curl -s -X DELETE "http://localhost:8080/api/v1/tokens/ci-token" \
  -H "Authorization: Bearer $TOKEN" | jq .
```

### 10.11 Verify via UI

- Settings → Users — list of users with roles.
- Settings → API Keys — list of tokens (values never re-shown).
- Settings → Connections — active connection highlighted.

---

## 11. Remove Addon

### 11.1 Dry Run (CLI)

```bash
sharko remove-addon argo-rollouts
```

Expected: prints impact report — affected clusters, total deployments to remove — without making any changes.

### 11.2 Confirm Remove (CLI)

```bash
sharko remove-addon argo-rollouts --confirm
```

Expected: prints "done" and removes the addon from the catalog.

### 11.3 Remove via API — Dry Run

```bash
curl -s -X DELETE "http://localhost:8080/api/v1/addons/keda" \
  -H "Authorization: Bearer $TOKEN" | jq .
```

Expected: 400 with `impact` object showing affected clusters.

### 11.4 Remove via API — Confirmed

```bash
curl -s -X DELETE "http://localhost:8080/api/v1/addons/keda?confirm=true" \
  -H "Authorization: Bearer $TOKEN" | jq .
```

Expected: 200 confirming removal.

---

## 12. Swagger UI

- Open `http://localhost:8080/swagger/index.html`
- Verify all major endpoint groups are listed (auth, clusters, addons, connections, tokens, users, notifications, upgrade, observability, dashboard).
- Expand a GET endpoint (e.g. GET /api/v1/clusters) → click "Try it out" → Execute.
- Expected: 200 response with valid JSON body.

---

## 13. AI Assistant

### 13.1 Configure AI Provider (API)

```bash
curl -s -X POST "http://localhost:8080/api/v1/ai/config" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"provider": "openai", "api_key": "<OPENAI_KEY>", "model": "gpt-4o"}' | jq .
```

### 13.2 Test AI Connection (API)

```bash
curl -s -X POST "http://localhost:8080/api/v1/ai/test" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{}' | jq .
```

### 13.3 Chat via API

```bash
curl -s -X POST "http://localhost:8080/api/v1/agent/chat" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"session_id": "test-1", "message": "What addons are deployed?"}' | jq .
```

### 13.4 Reset Session (API)

```bash
curl -s -X POST "http://localhost:8080/api/v1/agent/reset" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"session_id": "test-1"}' | jq .
```

### 13.5 AI Upgrade Summary (API)

```bash
curl -s -X POST "http://localhost:8080/api/v1/upgrade/ai-summary" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"addon": "keda", "from_version": "2.16.1", "to_version": "2.16.2"}' | jq .
```

### 13.6 Verify via UI

- Settings → AI — configure provider, test connection.
- Click the floating chat button (bottom-right) or "Ask AI" in the top bar.
- Ask: "What addons are deployed?" — expect a structured list response.
- Ask: "Which clusters are out of sync?" — expect cluster status summary.

---

## 14. Health Check

```bash
curl -s "http://localhost:8080/api/v1/health" | jq .
```

Expected: 200 with `{"status": "ok"}`. No auth required.

---

## Issue Tracking

Any bugs or UX issues found during testing — add to `docs/TODO.md`.
