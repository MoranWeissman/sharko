# Section 7 — UI Write Capabilities

> The UI is a full management interface, not a read-only viewer.

---

## Decision: Full UI — Everything the CLI/API Can Do

Every operation available via CLI and API is also available in the UI. The UI is a first-class citizen, not a secondary dashboard. Three interfaces, same capabilities:

| Operation | CLI | API | UI |
|-----------|-----|-----|-----|
| Initialize repo | `sharko init` | `POST /api/v1/init` | Setup wizard / Init button |
| Add cluster | `sharko add-cluster` | `POST /api/v1/clusters` | Add Cluster form |
| Remove cluster | `sharko remove-cluster` | `DELETE /api/v1/clusters/{name}` | Remove button + confirmation |
| Update cluster addons | `sharko update-cluster` | `PATCH /api/v1/clusters/{name}` | Toggle addons on cluster detail page |
| Refresh credentials | `sharko refresh-cluster` | `POST /api/v1/clusters/{name}/refresh` | Refresh button on cluster page |
| Add addon to catalog | `sharko add-addon` | `POST /api/v1/addons` | Add Addon form |
| Remove addon | `sharko remove-addon` | `DELETE /api/v1/addons/{name}` | Remove button + impact preview |
| Define addon secrets | `sharko add-addon-secret` | `POST /api/v1/addon-secrets` | Addon secret config form |
| View fleet status | `sharko status` | `GET /api/v1/fleet/status` | Fleet dashboard (already exists) |
| List clusters | `sharko list-clusters` | `GET /api/v1/clusters` | Clusters page (already exists) |
| Create API key | `sharko token create` | `POST /api/v1/tokens` | Settings → API Keys |
| Manage API keys | `sharko token list/revoke` | `GET/DELETE /api/v1/tokens` | Settings → API Keys |

---

## Permissions — Role-Based UI

The existing auth system has three roles: admin, operator, viewer. The UI respects these:

| Role | Can See | Can Do |
|------|---------|--------|
| **viewer** | Fleet dashboard, cluster details, addon catalog, version matrix, drift detection, observability | Nothing. Read-only. No action buttons visible. |
| **operator** | Everything viewer sees | Refresh credentials, trigger sync. Limited write operations. |
| **admin** | Everything | Everything. Add/remove clusters, add/remove addons, manage secrets, create API keys, manage users, initialize repo. |

**UI behavior by role:**
- **Viewer:** Action buttons (Add Cluster, Remove, Toggle Addon) are hidden. The UI looks like a pure dashboard.
- **Operator:** Some action buttons visible (Refresh, Sync). Destructive actions hidden.
- **Admin:** All action buttons visible. Full management capabilities.

The role comes from the authenticated user's session. The UI fetches it on login and conditionally renders action elements. The API enforces roles server-side regardless — a viewer who somehow crafts a POST request gets 403.

---

## UI Write Features — Design

### Add Cluster

**Where:** Clusters page → "Add Cluster" button (top right, admin only)

**Form fields:**
- Cluster name (required, validated against regex)
- Region (optional, metadata)
- Addons (multi-select checkboxes from the addon catalog)

**Behavior:**
1. User fills form, clicks "Add Cluster"
2. UI shows progress: "Fetching credentials... Creating secrets... Opening PR..."
3. On success: shows PR URL with "View PR" link, cluster appears in the list
4. On partial success: shows what succeeded and what failed, with guidance
5. On error: shows clear error message

**No manual entry of kubeconfig or credentials.** The cluster name is all the user provides — Sharko fetches credentials from the configured provider. If the cluster doesn't exist in the provider, the user sees "Cluster not found in secrets provider."

### Remove Cluster

**Where:** Cluster detail page → "Remove Cluster" button (admin only, red/destructive styling)

**Behavior:**
1. Click "Remove Cluster"
2. Confirmation modal: "This will remove prod-eu from ArgoCD. ArgoCD will stop managing addons on this cluster. Are you sure?"
3. If addon secrets exist: "Sharko will also delete managed secrets (datadog-keys) from this cluster."
4. User confirms → UI shows progress → PR opened (or auto-merged)

### Toggle Addons on a Cluster

**Where:** Cluster detail page → addon list → toggle switches (admin only)

**Behavior:**
1. Each addon has a toggle: enabled/disabled
2. Toggling an addon doesn't immediately fire — changes accumulate
3. "Apply Changes" button appears when toggles have been changed
4. Click "Apply Changes" → confirmation showing what will be enabled/disabled
5. If enabling an addon that has addon-secrets defined: "Sharko will create secrets on this cluster"
6. If disabling: "Sharko will remove the addon and its secrets from this cluster"
7. Confirm → progress → PR opened

### Add Addon to Catalog

**Where:** Addons page → "Add Addon" button (admin only)

**Form fields:**
- Addon name (required)
- Helm chart name (required)
- Helm repo URL (required)
- Chart version (required)
- Namespace (optional, defaults to addon name)
- Sync wave (optional, for ordered deployment)

**Behavior:**
1. Fill form, click "Add Addon"
2. Sharko validates the chart exists in the repo (optional — could try to fetch chart metadata)
3. PR opened with the new addon catalog entry and global values file
4. After merge, the addon is available for clusters to enable

### Remove Addon

**Where:** Addon detail page → "Remove Addon" button (admin only, red/destructive)

**Behavior:**
1. Click "Remove Addon"
2. Impact preview (the dry-run response): "This addon is deployed on 12 clusters. Removing it will delete deployments from: prod-eu, prod-us, staging-eu, ..."
3. User must type the addon name to confirm (GitHub-style destructive confirmation)
4. Confirm → PR opened to remove catalog entry
5. After merge, ArgoCD removes the addon from all clusters

### Addon Secret Configuration

**Where:** Addon detail page → "Secrets" tab (admin only)

**Form:**
- Secret name (what the K8s Secret will be called)
- Namespace (where to create it)
- Key mappings: key name → provider path
  - `api-key` → `secrets/datadog/api-key`
  - `app-key` → `secrets/datadog/app-key`
  - [+ Add Key]

**Behavior:**
1. Define the secret template for this addon
2. Save → stored in server config
3. From now on, when any cluster enables this addon, Sharko automatically creates the secret

### API Keys Management

**Where:** Settings → API Keys (admin only)

**Features:**
- List all API keys (name, role, created date, last used)
- "Create API Key" → modal with name + role → shows token ONCE → copy button
- "Revoke" button on each key → confirmation → key deleted

### Initialize Repo (Setup Wizard)

**Where:** Connections page, shown when no repo is initialized

**Already discussed in Section 1.** Green checks for configured connections, red X for missing. "Initialize" button when everything is ready. Calls `POST /api/v1/init`.

---

## What Already Exists vs What's New

| UI Component | Status |
|-------------|--------|
| Fleet dashboard | Exists — read-only, keep as-is |
| Cluster list + detail | Exists — add: "Add Cluster" button, "Remove" button, addon toggles |
| Addon catalog + detail | Exists — add: "Add Addon" button, "Remove" button, secrets tab |
| Version matrix | Exists — read-only, keep as-is |
| Drift detection | Exists — read-only, keep as-is |
| Observability | Exists — read-only, keep as-is |
| Connections page | Exists — add: provider config, init button, status indicators |
| AI Assistant | Exists — keep as-is |
| Embedded dashboards | Exists — keep as-is |
| User management | Exists — keep as-is |
| Add Cluster form | NEW |
| Remove Cluster confirmation | NEW |
| Addon toggle on cluster | NEW |
| Add Addon form | NEW |
| Remove Addon with impact preview | NEW |
| Addon secrets config | NEW |
| API Keys management | NEW |
| Init wizard / setup flow | NEW (extends existing connections page) |

---

## Implementation Notes

- All UI write operations call the same API endpoints as the CLI. No separate backend code.
- Every write action shows a progress indicator (stepper or spinner) since operations involve multiple backend steps.
- Every write action that results in a PR shows the PR URL with a direct link to GitHub/GitLab.
- Role-based rendering: check user role from session, conditionally show/hide action elements. Server enforces roles regardless.
- Destructive actions (remove cluster, remove addon) require explicit confirmation (modal + type-to-confirm for addon removal).
- Partial success (207) is displayed clearly: "Cluster added to ArgoCD but PR failed. View details."

---

## Open Questions for Later Sections

- Init sync verification (Section 8)
- Batch operations (Section 9)
