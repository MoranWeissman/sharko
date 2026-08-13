import type {
  AddonCatalogResponse,
  AddonDetailResponse,
  AddonValuesSecretActionResult,
  AdoptClustersResponse,
  AIConfigResponse,
  APIToken,
  AuditEntry,
  AvailableVersionsResponse,
  ClusterChange,
  ClusterComparisonResponse,
  ClusterDetailResponse,
  ClusterProvider,
  ClusterResyncResponse,
  ClustersResponse,
  ConfigDiffResponse,
  ConnectionComparisonView,
  CredsSource,
  ConnectionsListResponse,
  DashboardStats,
  DiagnosticReport,
  DoctorClusterResponse,
  DropLegacyLabelsRequestBody,
  DropLegacyLabelsResponse,
  DryRunResult,
  ManagedSecretsResponse,
  OrphanedSecretDeleteResult,
  MigrateResult,
  MigrationMigrateRequest,
  MigrationPlan,
  MigrationStatus,
  RuntimeHandoffReport,
  NodeInfoResponse,
  ObservabilityOverviewResponse,
  PullRequestsResponse,
  RegisterClusterResult,
  SecretResource,
  SyncActivityEntry,
  SystemCapabilitiesResponse,
  TakeoverReport,
  TakeoverRequestBody,
  TakeoverResponse,
  TrackedPRsResponse,
  UnregisterConsequencesResponse,
  UpgradeCheckResponse,
  UpgradeRecommendations,
  V4AddonValidationErrorBody,
  V4GitResult,
  VerifyResult,
  VersionMatrixResponse,
} from './models'
import { getToken, clearSession } from '@/lib/authStorage'

const BASE_URL = '/api/v1'

/**
 * PRWriteResult — the PR fields every write endpoint returns when it opens
 * (or auto-merges) a pull request. Some handlers wrap the orchestrator result
 * under `result` when an attribution warning fired, and a few still emit the
 * legacy `pull_request_url` alias. Typing the write calls with this (instead
 * of `Promise<any>`) means a missing pr_url/pr_id/merged surfaces at compile
 * time and the UI can branch on `merged` consistently (V2-cleanup-24).
 */
export interface PRWriteResult {
  pr_url?: string
  pr_id?: number
  branch?: string
  merged?: boolean
  /** Legacy alias for pr_url emitted by older handlers. */
  pull_request_url?: string
  status?: string
  message?: string
  attribution_warning?: 'no_per_user_pat'
  result?: {
    pr_url?: string
    pr_id?: number
    branch?: string
    merged?: boolean
  }
}

/**
 * RemoveClusterResult — deregisterCluster wraps its PR fields under `git`
 * (RemoveClusterResult on the backend), so the UI can tell "cluster gone"
 * (PR merged) apart from "PR opened for review" (cluster stays listed).
 */
export interface RemoveClusterResult {
  status?: string
  git?: PRWriteResult
}

/**
 * DeployAddonResult — toggling/deploying an addon on a cluster returns PR
 * fields either under `git` or at the top level depending on the path.
 */
export interface DeployAddonResult extends PRWriteResult {
  git?: PRWriteResult
}

function authHeaders(): Record<string, string> {
  const token = getToken()
  return token ? { Authorization: `Bearer ${token}` } : {}
}

/**
 * Envelope shape internal/api/tiered_git.go's `withAttributionWarning`
 * wraps a Tier-2 write response in when the caller had no personal Git
 * token on file and the service token was used as a fallback:
 * `{result: <the normal payload>, attribution_warning: "no_per_user_pat"}`.
 * The no-fallback case is the payload unchanged — this envelope only ever
 * shows up on the fallback path.
 *
 * Checking the exact sentinel string (not just "has a `result` field")
 * keeps this from misfiring on a payload that legitimately carries its
 * own `result` key.
 */
function isAttributionEnvelope(
  json: unknown,
): json is { result: Record<string, unknown>; attribution_warning: string } {
  if (!json || typeof json !== 'object') return false
  const obj = json as { result?: unknown; attribution_warning?: unknown }
  return typeof obj.attribution_warning === 'string' && !!obj.result && typeof obj.result === 'object'
}

/**
 * Flattens the attribution envelope so every caller sees one consistent
 * shape regardless of whether the fallback fired:
 * `{...result, attribution_warning}` when wrapped, the payload unchanged
 * otherwise.
 *
 * Applied centrally in the shared fetch helpers below (plus the handful
 * of raw-fetch callers — addToCatalog, migrateRepoRequest,
 * annotateAddonValues — that build their own request for typed-error
 * handling and so bypass the helpers) so no individual endpoint wrapper
 * has to know the envelope exists.
 *
 * This closes the dead-Preview bug: MarketplaceAddonDetail's
 * handlePreview read only `res.dry_run`, never `res.result?.dry_run`, so
 * a user with no personal PAT got a silent no-op on Preview. The same
 * class of bug had been patched once, at one call site (handleSubmit's
 * `res.pr_id ?? res.result?.pr_id`) — this is the one-time fix for the
 * whole class instead of another one-off patch.
 */
function unwrapAttribution<T>(json: unknown): T {
  if (isAttributionEnvelope(json)) {
    return { ...json.result, attribution_warning: json.attribution_warning } as T
  }
  return json as T
}

/**
 * ApiErrorBody — the shape the server's error boundary emits (error review
 * package 2, backend counterpart in internal/api/router.go's
 * errorResponse). `error` is always populated; `cause`, `hint`, and `code`
 * are additive and simply absent when the server had nothing to say.
 * `problems` is a pre-existing field some coded refusals attach (see
 * internal/api/catalog_org.go's writeCodedError) — carried through
 * unchanged.
 */
export interface ApiErrorBody {
  error?: string
  cause?: string
  hint?: string
  code?: string
  problems?: unknown
  [key: string]: unknown
}

/**
 * ApiError — thrown by every fetch helper below in place of a bare Error,
 * so a call site that wants the server's full shape (cause/hint/code/
 * problems) can get it, while every existing call site that only ever did
 * `err instanceof Error ? err.message : '...'` keeps working unchanged:
 * `.message` is exactly the headline that a bare `new Error(err.error ||
 * res.statusText)` used to carry.
 *
 * `cause` here is intentionally a plain string, not the ES2022
 * `Error.cause` (which is typically an Error/unknown, not a display
 * string) — this is server-supplied technical detail meant for rendering,
 * not a JS-level error chain.
 */
export class ApiError extends Error {
  status: number
  code?: string
  cause?: string
  hint?: string
  problems?: unknown
  body: ApiErrorBody

  constructor(status: number, body: ApiErrorBody, fallbackMessage: string) {
    super(body.error || fallbackMessage)
    this.name = 'ApiError'
    this.status = status
    this.code = typeof body.code === 'string' ? body.code : undefined
    this.cause = typeof body.cause === 'string' ? body.cause : undefined
    this.hint = typeof body.hint === 'string' ? body.hint : undefined
    this.problems = body.problems
    this.body = body
  }
}

/**
 * throwApiError reads the failed response's JSON body (falling back to
 * `{error: res.statusText}` for a non-JSON or empty body — the exact
 * fallback the six call sites below all duplicated before this) and throws
 * an ApiError built from it. Never returns.
 */
async function throwApiError(res: Response): Promise<never> {
  const body = (await res.json().catch(() => ({ error: res.statusText }))) as ApiErrorBody
  throw new ApiError(res.status, body, res.statusText)
}

async function fetchJSON<T>(path: string): Promise<T> {
  const res = await fetch(`${BASE_URL}${path}`, {
    headers: authHeaders(),
  })
  if (res.status === 401) {
    clearSession()
    window.location.reload()
    throw new Error('Session expired')
  }
  if (!res.ok) {
    await throwApiError(res)
  }
  return unwrapAttribution<T>(await res.json())
}

/**
 * fetchJSONWithTotal — same GET contract as fetchJSON, but also reads the
 * X-Total-Count response header (S4: server-side pagination surfaces this
 * on list endpoints like /clusters, but plain fetchJSON has no way to
 * expose it). `total` is null when the header is absent or not a number,
 * so callers can fall back to `data`'s own length rather than trusting a
 * NaN.
 */
async function fetchJSONWithTotal<T>(path: string): Promise<{ data: T; total: number | null }> {
  const res = await fetch(`${BASE_URL}${path}`, {
    headers: authHeaders(),
  })
  if (res.status === 401) {
    clearSession()
    window.location.reload()
    throw new Error('Session expired')
  }
  if (!res.ok) {
    await throwApiError(res)
  }
  const totalHeader = res.headers.get('X-Total-Count')
  const total = totalHeader !== null && totalHeader !== '' && !Number.isNaN(Number(totalHeader))
    ? Number(totalHeader)
    : null
  const data = unwrapAttribution<T>(await res.json())
  return { data, total }
}

async function postJSON<T>(path: string, body?: unknown): Promise<T> {
  const res = await fetch(`${BASE_URL}${path}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...authHeaders() },
    body: body ? JSON.stringify(body) : undefined,
  })
  if (res.status === 401) {
    clearSession()
    window.location.reload()
    throw new Error('Session expired')
  }
  if (!res.ok) {
    await throwApiError(res)
  }
  return unwrapAttribution<T>(await res.json())
}

async function putJSON<T>(path: string, body?: unknown): Promise<T> {
  const res = await fetch(`${BASE_URL}${path}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json', ...authHeaders() },
    body: body ? JSON.stringify(body) : undefined,
  })
  if (res.status === 401) {
    clearSession()
    window.location.reload()
    throw new Error('Session expired')
  }
  if (!res.ok) {
    await throwApiError(res)
  }
  return unwrapAttribution<T>(await res.json())
}

async function patchJSON<T>(path: string, body?: unknown): Promise<T> {
  const res = await fetch(`${BASE_URL}${path}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json', ...authHeaders() },
    body: body ? JSON.stringify(body) : undefined,
  })
  if (res.status === 401) {
    clearSession()
    window.location.reload()
    throw new Error('Session expired')
  }
  if (!res.ok) {
    await throwApiError(res)
  }
  return unwrapAttribution<T>(await res.json())
}

async function deleteJSON<T>(path: string): Promise<T> {
  const res = await fetch(`${BASE_URL}${path}`, {
    method: 'DELETE',
    headers: authHeaders(),
  })
  if (res.status === 401) {
    clearSession()
    window.location.reload()
    throw new Error('Session expired')
  }
  if (!res.ok) {
    await throwApiError(res)
  }
  return unwrapAttribution<T>(await res.json())
}

async function fetchJSONMethod<T>(path: string, method: string, body?: unknown): Promise<T> {
  const res = await fetch(`${BASE_URL}${path}`, {
    method,
    headers: { ...authHeaders(), 'Content-Type': 'application/json' },
    body: body ? JSON.stringify(body) : undefined,
  })
  if (res.status === 401) {
    clearSession()
    window.location.reload()
    throw new Error('Session expired')
  }
  if (!res.ok) {
    await throwApiError(res)
  }
  return unwrapAttribution<T>(await res.json())
}

// --- Write operations (Phase 7) ---

export async function registerCluster(data: {
  name: string;
  addons?: Record<string, boolean>;
  region?: string;
  secret_path?: string;
  provider?: ClusterProvider;
  role_arn?: string;
  auto_merge?: boolean;
  dry_run?: boolean;
  // Required when the inline-kubeconfig creds source is chosen. Bearer-token
  // authentication only — see internal/providers/kubeconfig_parser.go.
  kubeconfig?: string;
  // Primary credential signal (creds-reframe story 1). When set it WINS over
  // `provider` on the backend. The dialog always sends this alongside a
  // consistent `provider` for backward-compat with anything still reading
  // `provider`.
  creds_source?: CredsSource;
  // Who manages the ArgoCD cluster secret (V2-cleanup-57.2). Omitted or
  // 'sharko' = Sharko writes and rotates it (default). 'user' = the caller
  // creates the secret by hand; Sharko never writes it and only syncs
  // addon labels onto it. Credentials become optional in that mode.
  connection_managed_by?: 'sharko' | 'user';
}) {
  return postJSON<RegisterClusterResult>('/clusters', data)
}

/**
 * Structured 503 body returned by `POST /api/v1/clusters/{name}/test` when
 * the test feature is unavailable for a known reason. The UI renders
 * branch-specific copy off the stable `error_code` field so each scenario
 * gets a clear, action-oriented message instead of a generic "test failed".
 *
 * Codes:
 *  - `no_secrets_backend`: the active connection has no secrets backend
 *    configured (Vault / AWS Secrets Manager / file-store / ArgoCDProvider).
 *    Configure one in Settings → Connections.
 *
 *  - `argocd_provider_iam_required`: the active backend is the built-in
 *    ArgoCDProvider, the cluster's ArgoCD Secret has the AWS-IAM
 *    `awsAuthConfig` shape, and the Sharko pod has no AWS credentials.
 *    The test cannot exec aws-iam-authenticator from inside the pod —
 *    grant the Sharko pod's role the right permissions (IRSA / EC2
 *    instance profile / Pod Identity).
 *
 *  - `argocd_provider_exec_unsupported`: the cluster's ArgoCD Secret has
 *    an `execProviderConfig` shape — auth is provided by an external exec
 *    plugin (gcloud, azure-cli, aws-iam-authenticator, etc.). Not
 *    supported in v1.x; tracked for v2.
 *
 *  - `argocd_provider_unsupported_auth`: the cluster's ArgoCD Secret has
 *    an unrecognized auth shape. Surface as "inspect the Secret manually
 *    in the argocd namespace" — there is no automatic remediation.
 *
 * The backend may also return 503 with NO `error_code` for unexpected
 * failures; those are surfaced as a thrown Error so the UI's generic
 * error path renders them.
 */
export type TestClusterErrorCode =
  | 'no_secrets_backend'
  | 'argocd_provider_iam_required'
  | 'argocd_provider_exec_unsupported'
  | 'argocd_provider_unsupported_auth'

export interface TestClusterUnavailable {
  unavailable: true
  error_code: TestClusterErrorCode
  error: string
  hint?: string
}

const KNOWN_TEST_ERROR_CODES: ReadonlySet<TestClusterErrorCode> = new Set<TestClusterErrorCode>([
  'no_secrets_backend',
  'argocd_provider_iam_required',
  'argocd_provider_exec_unsupported',
  'argocd_provider_unsupported_auth',
])

export function isTestClusterUnavailable(
  v: unknown,
): v is TestClusterUnavailable {
  if (typeof v !== 'object' || v === null) return false
  if ((v as { unavailable?: boolean }).unavailable !== true) return false
  const code = (v as { error_code?: string }).error_code
  return typeof code === 'string' && KNOWN_TEST_ERROR_CODES.has(code as TestClusterErrorCode)
}

export async function testClusterConnection(
  name: string,
): Promise<
  | (VerifyResult & { reachable?: boolean; platform?: string; suggestions?: string[] })
  | TestClusterUnavailable
> {
  const res = await fetch(`${BASE_URL}/clusters/${encodeURIComponent(name)}/test`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...authHeaders() },
    body: JSON.stringify({}),
  })
  if (res.status === 401) {
    clearSession()
    window.location.reload()
    throw new Error('Session expired')
  }
  // Detect the structured 503 envelope and surface it as a typed
  // "unavailable" result instead of throwing. Unknown / absent error_code
  // on a 503 falls through to the generic throw-Error path so the UI shows
  // whatever message the backend sent.
  if (res.status === 503) {
    const body = (await res.json().catch(() => ({}))) as {
      error?: string
      error_code?: string
      hint?: string
    }
    if (
      typeof body.error_code === 'string' &&
      KNOWN_TEST_ERROR_CODES.has(body.error_code as TestClusterErrorCode)
    ) {
      return {
        unavailable: true,
        error_code: body.error_code as TestClusterErrorCode,
        error: body.error || 'Cluster test unavailable.',
        hint: body.hint,
      }
    }
    throw new Error(body.error || 'Service unavailable')
  }
  if (!res.ok) {
    const body = await res.json().catch(() => ({ error: res.statusText }))
    throw new Error((body as { error?: string }).error || res.statusText)
  }
  return res.json()
}

export async function diagnoseCluster(name: string) {
  return postJSON<DiagnosticReport>(`/clusters/${encodeURIComponent(name)}/diagnose`, {})
}

/**
 * reconcileCluster — POST /clusters/{name}/reconcile.
 * "Refresh": asks the cluster-secret engine to CHECK right now instead of
 * waiting for its regular pass — it reads git, reads the live secrets,
 * compares them, and records what it found. Nothing is created, changed or
 * deleted. To actually apply what a check found, call resyncClusterLabels
 * below. The check covers every cluster, not only the one named. Returns 202
 * as soon as it is accepted — the check is async, so callers should refetch
 * shortly after to pick up the updated last_reconcile.
 */
export async function reconcileCluster(name: string) {
  return postJSON<{ status: string; message: string }>(`/clusters/${encodeURIComponent(name)}/reconcile`, {})
}

/**
 * resyncClusterLabels — POST /clusters/{name}/resync (v4-8-5).
 * "Re-sync now" from the drift view: re-applies Sharko's own addon-label
 * keys for this ONE cluster, ONCE, to match git — regardless of the
 * managed_cluster_self_heal setting, and without changing that setting.
 * Unlike reconcileCluster (a fleet-wide pass nudge that returns 202 and
 * runs async), this is a targeted, synchronous, single-cluster action that
 * returns 200 with the label diff it applied.
 */
export async function resyncClusterLabels(name: string) {
  return postJSON<ClusterResyncResponse>(`/clusters/${encodeURIComponent(name)}/resync`, {})
}

/**
 * getSystemCapabilities — GET /api/v1/system/capabilities (V2-cleanup-88.1).
 * What Sharko has auto-detected about its own runtime: whether it's running
 * with an AWS identity, and (best-effort) whether the hub cluster looks
 * like EKS. Read-tier — any authenticated user may call this. Standalone
 * (not under `api.`) so the two-layer registration dialog's Layer 1 can be
 * mocked the same way diagnoseCluster / testClusterConnection are.
 */
export async function getSystemCapabilities() {
  return fetchJSON<SystemCapabilitiesResponse>('/system/capabilities')
}

/**
 * getManagedSecrets — GET /api/v1/system/managed-secrets. Every secret
 * Sharko manages (connection secrets + addon values secrets) plus each
 * reconciler engine's cadence, last run, and last error. Read-tier, same
 * convention as getSystemCapabilities above.
 */
export async function getManagedSecrets() {
  return fetchJSON<ManagedSecretsResponse>('/system/managed-secrets')
}

/**
 * triggerSecretsReconcile — POST /api/v1/secrets/reconcile. Nudges the
 * addon-values secrets engine to run immediately instead of waiting for its
 * 5-minute tick. Returns 202 as soon as the trigger is accepted — the
 * reconcile itself runs in the background, so callers should refetch
 * getManagedSecrets shortly after to see the updated engine stats.
 */
export async function triggerSecretsReconcile() {
  return postJSON<{ status: string }>('/secrets/reconcile', {})
}

/**
 * checkAllAddonValuesSecrets — POST /api/v1/secrets/check. Checks EVERY
 * addon-values secret against its source right now, WITHOUT writing
 * anything. This is what "Refresh all" drives on this engine — deliberately
 * not triggerSecretsReconcile above, which runs the pass that creates and
 * rotates secrets. Returns 202 as soon as the check is accepted; the check
 * runs in the background, so callers should refetch getManagedSecrets
 * shortly after.
 */
export async function checkAllAddonValuesSecrets() {
  return postJSON<{ status: string }>('/secrets/check', {})
}

/**
 * refreshAddonValuesSecret — POST /clusters/{name}/addons/{addon}/secret/refresh
 * (S4). Re-checks ONE addon-values secret against its source (the vault)
 * right now, WITHOUT writing anything — the row-scoped "Refresh" action on
 * the Managed Secrets page. Throws with the server's plain-English error
 * text when the check itself could not run (no Git connection, cluster
 * unreachable, no secret definition for this pair, etc.).
 */
export async function refreshAddonValuesSecret(cluster: string, addon: string) {
  return postJSON<AddonValuesSecretActionResult>(
    `/clusters/${encodeURIComponent(cluster)}/addons/${encodeURIComponent(addon)}/secret/refresh`,
    {},
  )
}

/**
 * syncAddonValuesSecret — POST /clusters/{name}/addons/{addon}/secret/sync
 * (S4). Re-pushes ONE addon-values secret from its source (the vault) to
 * the remote cluster right now — the row-scoped "Sync" action on the
 * Managed Secrets page. Throws with the server's plain-English error text
 * when the push itself could not run or failed.
 */
export async function syncAddonValuesSecret(cluster: string, addon: string) {
  return postJSON<AddonValuesSecretActionResult>(
    `/clusters/${encodeURIComponent(cluster)}/addons/${encodeURIComponent(addon)}/secret/sync`,
    {},
  )
}

/**
 * deleteOrphanedSecret — DELETE
 * /clusters/{name}/orphaned-secrets/{namespace}/{secret} (leftover-secrets
 * S1.2). Operator-or-higher only. Deletes ONE orphaned leftover secret
 * Sharko once wrote — never automatic, only this explicit call behind the
 * UI's confirm dialog. On success the body echoes the server's own
 * `message`. On failure (404 not on record, 409 reclaimed by the catalog
 * or its ownership marker changed — nothing deleted, 422, 503) the shared
 * fetch helper throws an ApiError carrying the server's plain-English
 * `error` sentence, safe to show verbatim.
 */
export async function deleteOrphanedSecret(cluster: string, namespace: string, name: string) {
  return deleteJSON<OrphanedSecretDeleteResult>(
    `/clusters/${encodeURIComponent(cluster)}/orphaned-secrets/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}`,
  )
}

/**
 * getConnectionSecretResource — GET /clusters/{name}/secret/resource (S3).
 * Reads the cluster's ArgoCD connection Secret from Sharko's own cluster
 * as it is right now, for read-only display.
 *
 * COST: one live read per call. Call it ON A CLICK and only on a click —
 * never while the list renders, never on a timer, never in a loop over
 * rows. The server says the same thing in the handler's own comment.
 *
 * The response NEVER carries a secret value: the server blanks every
 * value before the body is built. Do not ask it for one.
 */
export async function getConnectionSecretResource(cluster: string) {
  return fetchJSON<SecretResource>(`/clusters/${encodeURIComponent(cluster)}/secret/resource`)
}

/**
 * getAddonValuesSecretResource — GET
 * /clusters/{name}/addons/{addon}/secret/resource (S3). Reads one addon's
 * values Secret from the remote cluster it was pushed to, as it is right
 * now, for read-only display. Same cost rule and same blanking guarantee
 * as getConnectionSecretResource above.
 */
export async function getAddonValuesSecretResource(cluster: string, addon: string) {
  return fetchJSON<SecretResource>(
    `/clusters/${encodeURIComponent(cluster)}/addons/${encodeURIComponent(addon)}/secret/resource`,
  )
}

/**
 * doctorCluster — POST /api/v1/clusters/{name}/doctor (V2-cleanup-88.4).
 * Runs the connection doctor's four real-attempt checks against the named
 * cluster and returns a structured pass/fail/not-applicable verdict per
 * check, each with a plain-English fix on failure.
 */
export async function doctorCluster(name: string) {
  return postJSON<DoctorClusterResponse>(`/clusters/${encodeURIComponent(name)}/doctor`, {})
}

/**
 * takeoverPreflight — GET /clusters/{name}/takeover/preflight (v4 Wave 2,
 * Epic 6, stories 6.1 + 6.2). Four read-only checks on a cluster ArgoCD
 * already manages. Writes nothing, so it is safe to call on every render
 * of the takeover dialog and safe to re-run after the user fixes something
 * — that re-run is exactly how a blocked check turns green.
 */
export async function takeoverPreflight(name: string) {
  return fetchJSON<TakeoverReport>(`/clusters/${encodeURIComponent(name)}/takeover/preflight`)
}

/**
 * takeoverCluster — POST /clusters/{name}/takeover (story 6.3). Makes
 * Sharko the owner of an existing cluster's ArgoCD connection: same name,
 * same address, and by default every label the previous owner left on it.
 * Refuses without `yes: true`. The checks are re-run server-side on this
 * call, and every warning they raise has to be named by its finding id in
 * `acknowledged_findings` — anything left out comes back as a 409 listing
 * it. Pass `dry_run: true` for the plan.
 */
export async function takeoverCluster(name: string, body: TakeoverRequestBody) {
  return postJSON<TakeoverResponse>(`/clusters/${encodeURIComponent(name)}/takeover`, body)
}

/**
 * dropLegacyLabels — POST /clusters/{name}/takeover/legacy-labels/drop
 * (story 6.4). Removes the labels the takeover carried over. The response
 * carries `warnings` naming any ApplicationSet that still picks clusters
 * using one of them, and `warning_ids` alongside them — the caller must
 * show the warnings and send those ids back in `acknowledged_findings`
 * before the removal is allowed.
 */
export async function dropLegacyLabels(name: string, body: DropLegacyLabelsRequestBody) {
  return postJSON<DropLegacyLabelsResponse>(
    `/clusters/${encodeURIComponent(name)}/takeover/legacy-labels/drop`,
    body,
  )
}

/**
 * unregisterConsequences — GET /clusters/{name}/unregister/consequences
 * (story 6.5). Reads out what unregistering this cluster will actually do.
 * Deletes nothing; it is the read that comes before the removal.
 */
export async function unregisterConsequences(name: string) {
  return fetchJSON<UnregisterConsequencesResponse>(
    `/clusters/${encodeURIComponent(name)}/unregister/consequences`,
  )
}

/**
 * previewEnableAddon — dry-run of POST /clusters/{name}/addons/{addon}
 * (dry_run: true). Used by the pre-warn flow (V2-cleanup-88.5, addon_secrets_ready):
 * before the user commits to enabling an addon on a cluster with no
 * resolvable connection credentials, this call surfaces the SAME pre-flight
 * credentials-gate rejection EnableAddon would give — without writing
 * anything — so the 422 (when it applies) is shown as an inline warning
 * instead of a surprise after "Apply Changes".
 */
export async function previewEnableAddon(clusterName: string, addonName: string): Promise<DeployAddonResult> {
  return postJSON<DeployAddonResult>(
    `/clusters/${encodeURIComponent(clusterName)}/addons/${encodeURIComponent(addonName)}`,
    { dry_run: true },
  )
}

/**
 * V4AddonValidationError — thrown by enableAddonV4 on ANY 422 (v4 wave 2.5
 * review fix round, B-2). Two shapes share this error: the "sharpened
 * pipeline" semantic-validation 422 (code `incomplete_entry` /
 * `validation_failed`, plain-English `problems` list, checked BEFORE any
 * branch or PR exists) and the plain coded 422 the catalog gate returns
 * (code `not_in_catalog` / `empty_catalog_file`, no `problems`). Callers
 * branch on `code` — never on the message text, which is the exact bug
 * this closes: the catalog-gate combo used to fire on any problem sentence
 * containing the word "catalog", which both false-fired on unrelated 422s
 * and never fired on the real not-in-catalog error (that body has no
 * `problems` array at all).
 */
export class V4AddonValidationError extends Error {
  code?: string
  cluster: string
  addon: string
  problems: string[]

  constructor(body: V4AddonValidationErrorBody) {
    super(body.error)
    this.name = 'V4AddonValidationError'
    this.code = body.code
    this.cluster = body.cluster ?? ''
    this.addon = body.addon ?? ''
    this.problems = body.problems ?? []
  }
}

/**
 * CatalogAddError — thrown by addToCatalog on any 4xx from POST
 * /api/v1/catalog/addons. Every 4xx body carries a machine-readable `code`
 * next to the plain-English `error` (v4 wave 2.5 review fix round, B-2/B-3):
 * `incomplete_entry` and `validation_failed` additionally carry `problems`
 * (`validation_failed` also carries `cluster`/`addon`). Callers branch on
 * `code`, never on the message text. A 502 (genuine upstream failure) has
 * no code — `addToCatalog` throws a plain Error for that case.
 */
export class CatalogAddError extends Error {
  code?: string
  problems: string[]
  cluster?: string
  addon?: string

  constructor(body: {
    error?: string
    code?: string
    problems?: string[]
    cluster?: string
    addon?: string
  }) {
    super(body.error || 'Failed to add to catalog')
    this.name = 'CatalogAddError'
    this.code = body.code
    this.problems = body.problems ?? []
    this.cluster = body.cluster
    this.addon = body.addon
  }
}

/**
 * MigrationPRAlreadyOpenError — thrown by migrateRepo when the server
 * returns 409 (a previous migrate call already opened a pull request that
 * is still open). Distinct from a plain Error so MigrationBanner can link
 * straight to the existing PR instead of showing a generic failure and
 * inviting a retry that would just 409 again.
 */
export class MigrationPRAlreadyOpenError extends Error {
  prUrl: string
  prNumber?: number

  constructor(body: { error?: string; migration_pr_url?: string; migration_pr_number?: number }) {
    super(body.error || 'a migration pull request is already open')
    this.name = 'MigrationPRAlreadyOpenError'
    this.prUrl = body.migration_pr_url || ''
    this.prNumber = body.migration_pr_number
  }
}

export interface EnableAddonV4Request {
  version?: string
  values?: Record<string, unknown>
  settings?: Record<string, unknown>
  dry_run?: boolean
  yes?: boolean
  auto_merge?: boolean
}

export interface DisableAddonV4Request {
  remove?: boolean
  dry_run?: boolean
  yes?: boolean
  auto_merge?: boolean
}

/**
 * enableAddonV4 — POST /api/v1/v4/clusters/{name}/addons/{addon} (v4 Wave
 * 1 Story 4.3). Writes cluster-addons/{name}.yaml (kind ClusterAddons) and,
 * when values are supplied, values/clusters/{name}/{addon}.yaml — the v4
 * data-file format, distinct from the v3 per-addon endpoint
 * (previewEnableAddon / enableAddonOnCluster above). A 422 throws
 * V4AddonValidationError with the full plain-English problems list rather
 * than the generic postJSON Error (which would drop everything but the
 * summary sentence).
 */
export async function enableAddonV4(
  clusterName: string,
  addonName: string,
  body: EnableAddonV4Request,
): Promise<V4GitResult> {
  const res = await fetch(
    `${BASE_URL}/v4/clusters/${encodeURIComponent(clusterName)}/addons/${encodeURIComponent(addonName)}`,
    {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', ...authHeaders() },
      body: JSON.stringify(body),
    },
  )
  if (res.status === 401) {
    clearSession()
    window.location.reload()
    throw new Error('Session expired')
  }
  if (res.status === 422) {
    // Every 422 from this endpoint carries a machine-readable `code`
    // (not_in_catalog / empty_catalog_file / incomplete_entry /
    // validation_failed) — thrown as V4AddonValidationError regardless of
    // whether this particular code carries a `problems` array, so callers
    // always get a typed error to branch on (v4 wave 2.5 review B-2).
    const errBody = await res.json().catch(() => ({ error: 'Validation failed' }))
    throw new V4AddonValidationError({
      error: errBody?.error || 'Validation failed',
      code: errBody?.code,
      cluster: errBody?.cluster ?? clusterName,
      addon: errBody?.addon ?? addonName,
      problems: errBody?.problems,
    })
  }
  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: res.statusText }))
    throw new Error(err.error || res.statusText)
  }
  return res.json()
}

/**
 * addToCatalog — POST /api/v1/catalog/addons. Raw fetch (not postJSON) so a
 * 4xx becomes a typed CatalogAddError carrying the machine-readable `code`
 * instead of postJSON's generic Error (message only) — the combo/wizard
 * doors need the code to branch correctly (v4 wave 2.5 review B-2/B-3).
 */
export async function addToCatalog(
  req: import('./models').AddToCatalogRequest,
): Promise<import('./models').AddToCatalogResult> {
  const res = await fetch(`${BASE_URL}/catalog/addons`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...authHeaders() },
    body: JSON.stringify(req),
  })
  if (res.status === 401) {
    clearSession()
    window.location.reload()
    throw new Error('Session expired')
  }
  if (!res.ok) {
    const errBody = await res.json().catch(() => ({ error: res.statusText }))
    throw new CatalogAddError(errBody)
  }
  return unwrapAttribution<import('./models').AddToCatalogResult>(await res.json())
}

/**
 * disableAddonV4 — DELETE /api/v1/v4/clusters/{name}/addons/{addon} (v4
 * Wave 1 Story 4.3). Sets enabled=false in cluster-addons/{name}.yaml — the
 * entry (version pin + settings) is kept by default so re-enabling is a
 * one-word change; pass remove:true to delete it entirely. No semantic
 * validation runs (disabling never needs required values or secrets), so
 * this never throws V4AddonValidationError.
 */
export async function disableAddonV4(
  clusterName: string,
  addonName: string,
  body: DisableAddonV4Request,
): Promise<V4GitResult> {
  const res = await fetch(
    `${BASE_URL}/v4/clusters/${encodeURIComponent(clusterName)}/addons/${encodeURIComponent(addonName)}`,
    {
      method: 'DELETE',
      headers: { 'Content-Type': 'application/json', ...authHeaders() },
      body: JSON.stringify(body),
    },
  )
  if (res.status === 401) {
    clearSession()
    window.location.reload()
    throw new Error('Session expired')
  }
  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: res.statusText }))
    throw new Error(err.error || res.statusText)
  }
  return res.json()
}

export async function fetchAuditLog(filters?: {
  user?: string
  action?: string
  source?: string
  result?: string
  cluster?: string
  since?: string
  limit?: number
}) {
  const params = new URLSearchParams()
  if (filters) {
    if (filters.user) params.set('user', filters.user)
    if (filters.action) params.set('action', filters.action)
    if (filters.source) params.set('source', filters.source)
    if (filters.result) params.set('result', filters.result)
    if (filters.cluster) params.set('cluster', filters.cluster)
    if (filters.since) params.set('since', filters.since)
    if (filters.limit) params.set('limit', String(filters.limit))
  }
  const qs = params.toString()
  return fetchJSON<{ entries: AuditEntry[] }>(`/audit${qs ? `?${qs}` : ''}`)
}

export function createAuditStream(): EventSource {
  const token = getToken()
  const url = `/api/v1/audit/stream${token ? `?token=${encodeURIComponent(token)}` : ''}`
  return new EventSource(url)
}

// The backend handler at `orchestrator.RemoveCluster` rejects
// confirmation-required operations with HTTP 400 "confirmation required:
// set yes: true in request body" when the body doesn't include
// `{"yes": true}`. Include the confirmation flag in the DELETE body.
export async function deregisterCluster(name: string, autoMerge: boolean | undefined, dryRun: true): Promise<DryRunResult>
export async function deregisterCluster(name: string, autoMerge?: boolean, dryRun?: false): Promise<RemoveClusterResult>
export async function deregisterCluster(name: string, autoMerge?: boolean, dryRun = false): Promise<RemoveClusterResult | DryRunResult> {
  // auto_merge mirrors init/register: when set it overrides the connection's
  // PRAutoMerge default for this removal PR only; when omitted the backend
  // falls back to the connection default. Omitting the key (rather than
  // sending null) keeps the wire shape identical to the legacy call.
  const body: { yes: boolean; auto_merge?: boolean; dry_run?: boolean } = { yes: true }
  if (autoMerge !== undefined) body.auto_merge = autoMerge
  if (dryRun) body.dry_run = true
  const res = await fetch(`${BASE_URL}/clusters/${encodeURIComponent(name)}`, {
    method: 'DELETE',
    headers: { 'Content-Type': 'application/json', ...authHeaders() },
    body: JSON.stringify(body),
  })
  if (res.status === 401) {
    clearSession()
    window.location.reload()
    throw new Error('Session expired')
  }
  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: res.statusText }))
    throw new Error(err.error || res.statusText)
  }
  if (dryRun) {
    const envelope = await res.json() as { dry_run?: DryRunResult }
    return (envelope.dry_run ?? (envelope as unknown as DryRunResult))
  }
  return res.json() as Promise<RemoveClusterResult>
}

export async function adoptClusters(data: { clusters: string[]; auto_merge?: boolean; dry_run?: boolean }) {
  // AdoptClustersRequest on the backend does NOT have a `Yes` field —
  // `cluster.adopt` is gated on RBAC + per-cluster Stage1 verification,
  // not on a confirmation flag. Do NOT send `yes: true` here even though
  // the AdoptClustersDialog is a confirmation flow from the user's
  // perspective.
  return postJSON<AdoptClustersResponse>('/clusters/adopt', data)
}

// The unadopt handler is `POST /clusters/{name}/unadopt` and requires
// `yes: true` in the body.
export async function unadoptCluster(name: string, dryRun: true): Promise<DryRunResult>
export async function unadoptCluster(name: string, dryRun?: false): Promise<PRWriteResult>
export async function unadoptCluster(name: string, dryRun = false): Promise<PRWriteResult | DryRunResult> {
  const body: { yes: boolean; dry_run?: boolean } = { yes: true }
  if (dryRun) body.dry_run = true
  if (dryRun) {
    const envelope = await postJSON<{ dry_run?: DryRunResult }>(`/clusters/${encodeURIComponent(name)}/unadopt`, body)
    return (envelope.dry_run ?? (envelope as unknown as DryRunResult))
  }
  return postJSON<PRWriteResult>(`/clusters/${encodeURIComponent(name)}/unadopt`, body)
}

// Orphan cluster Secret cleanup. The BE returns 204 No Content on
// success (no body), so we can't use the generic deleteJSON helper which
// assumes a JSON body. The BE refuses with 400 if the cluster is
// genuinely managed (in git) or pending (open register PR) — the caller
// surfaces the error message in a toast.
export async function deleteOrphanCluster(name: string): Promise<void> {
  const res = await fetch(`${BASE_URL}/clusters/${encodeURIComponent(name)}/orphan`, {
    method: 'DELETE',
    headers: authHeaders(),
  })
  if (res.status === 401) {
    clearSession()
    window.location.reload()
    throw new Error('Session expired')
  }
  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: res.statusText }))
    throw new Error(err.error || res.statusText)
  }
  // 204 No Content — nothing to parse.
}

export async function updateClusterAddons(name: string, addons: Record<string, boolean>, dryRun: true): Promise<DryRunResult>
export async function updateClusterAddons(name: string, addons: Record<string, boolean>, dryRun?: false): Promise<DeployAddonResult>
export async function updateClusterAddons(name: string, addons: Record<string, boolean>, dryRun = false): Promise<DeployAddonResult | DryRunResult> {
  const body: { addons: Record<string, boolean>; dry_run?: boolean } = { addons }
  if (dryRun) body.dry_run = true
  if (dryRun) {
    const envelope = await patchJSON<{ dry_run?: DryRunResult }>(`/clusters/${encodeURIComponent(name)}`, body)
    return (envelope.dry_run ?? (envelope as unknown as DryRunResult))
  }
  return patchJSON<DeployAddonResult>(`/clusters/${encodeURIComponent(name)}`, body)
}

export async function updateClusterSettings(name: string, settings: { secret_path?: string; dry_run: true }): Promise<DryRunResult>
export async function updateClusterSettings(name: string, settings: { secret_path?: string; dry_run?: false }): Promise<PRWriteResult>
export async function updateClusterSettings(name: string, settings: { secret_path?: string; dry_run?: boolean }): Promise<PRWriteResult | DryRunResult> {
  if (settings.dry_run) {
    const envelope = await patchJSON<{ dry_run?: DryRunResult }>(`/clusters/${encodeURIComponent(name)}`, settings)
    return (envelope.dry_run ?? (envelope as unknown as DryRunResult))
  }
  return patchJSON<PRWriteResult>(`/clusters/${encodeURIComponent(name)}`, settings)
}

export interface AddAddonResponse {
  // Top-level fields when no attribution warning fired (raw orchestrator result).
  pr_url?: string
  pr_id?: number
  branch?: string
  merged?: boolean
  attribution_warning?: 'no_per_user_pat'
  // Legacy alias — kept on the type so existing callers compile cleanly.
  pull_request_url?: string
  // When attribution_warning is set, the orchestrator result is wrapped under `result`.
  result?: {
    pr_url?: string
    pr_id?: number
    branch?: string
    merged?: boolean
  }
  // Preview returned when the request set dry_run=true. No PR was created and
  // no catalog change was committed. Mirrors the register-cluster dry-run
  // shape so the Marketplace preview reuses the same DryRunResult render.
  dry_run?: DryRunResult
}

export async function removeAddon(name: string, dryRun: true): Promise<DryRunResult>
export async function removeAddon(name: string, dryRun?: false): Promise<PRWriteResult>
export async function removeAddon(name: string, dryRun = false): Promise<PRWriteResult | DryRunResult> {
  if (dryRun) {
    // Dry-run: pass dry_run in body (prefer body over query for consistency)
    const res = await fetch(`${BASE_URL}/addons/${encodeURIComponent(name)}?confirm=true`, {
      method: 'DELETE',
      headers: { 'Content-Type': 'application/json', ...authHeaders() },
      body: JSON.stringify({ dry_run: true }),
    })
    if (res.status === 401) {
      clearSession()
      window.location.reload()
      throw new Error('Session expired')
    }
    if (!res.ok) {
      const err = await res.json().catch(() => ({ error: res.statusText }))
      throw new Error(err.error || res.statusText)
    }
    const envelope = await res.json() as { dry_run?: DryRunResult }
    return (envelope.dry_run ?? (envelope as unknown as DryRunResult))
  }
  return deleteJSON<PRWriteResult>(`/addons/${encodeURIComponent(name)}?confirm=true`)
}

export async function upgradeAddon(name: string, data: { version: string; cluster?: string }): Promise<PRWriteResult> {
  return postJSON<PRWriteResult>(`/addons/${encodeURIComponent(name)}/upgrade`, data)
}

// upgradeAddonClusters — Epic 7 Story 7.2 (v4 Wave 2): bump one addon's
// version pin on a CHOSEN SUBSET of clusters in a single PR
// (POST /addons/{name}/upgrade-clusters). Pass dry_run: true to preview
// every file the PR would touch before opening it.
export async function upgradeAddonClusters(
  name: string,
  data: { clusters: string[]; version: string; dry_run?: boolean; yes?: boolean; auto_merge?: boolean },
): Promise<V4GitResult> {
  return postJSON<V4GitResult>(`/addons/${encodeURIComponent(name)}/upgrade-clusters`, data)
}

// Overloads for configureAddon
export async function configureAddon(
  name: string,
  config: {
    version?: string
    self_heal?: boolean
    sync_options?: string[]
    extra_helm_values?: Record<string, string>
    ignore_differences?: Record<string, unknown>[]
    additional_sources?: Record<string, unknown>[]
    dry_run: true
  },
): Promise<DryRunResult>
export async function configureAddon(
  name: string,
  config: {
    version?: string
    self_heal?: boolean
    sync_options?: string[]
    extra_helm_values?: Record<string, string>
    ignore_differences?: Record<string, unknown>[]
    additional_sources?: Record<string, unknown>[]
    dry_run?: false
  },
): Promise<PRWriteResult>
export async function configureAddon(
  name: string,
  config: {
    version?: string
    self_heal?: boolean
    sync_options?: string[]
    extra_helm_values?: Record<string, string>
    ignore_differences?: Record<string, unknown>[]
    additional_sources?: Record<string, unknown>[]
    dry_run?: boolean
  },
): Promise<PRWriteResult | DryRunResult> {
  // The backend handler is registered at PATCH /api/v1/addons/{name}
  // (see internal/api/router.go).
  const body = { name, ...config }
  if (config.dry_run) {
    const envelope = await patchJSON<{ dry_run?: DryRunResult }>(`/addons/${encodeURIComponent(name)}`, body)
    return (envelope.dry_run ?? (envelope as unknown as DryRunResult))
  }
  return patchJSON<PRWriteResult>(`/addons/${encodeURIComponent(name)}`, body)
}

/**
 * Response shape for token creation is not fully pinned down server-side —
 * callers (see ApiKeys.tsx) probe `token` / `api_token` / `value` in that
 * order, so all three stay optional here rather than picking one and
 * casting the rest away with `any`.
 */
export interface TokenCreateResult {
  token?: string
  api_token?: string
  value?: string
  name?: string
  role?: string
  [key: string]: unknown
}

/**
 * Create an API token. Leave expires_in_days out for the default of 90 days;
 * anything else must be between 1 and 365.
 */
export async function createToken(data: { name: string; role: string; expires_in_days?: number }) {
  return postJSON<TokenCreateResult>('/tokens', data)
}

export async function listTokens() {
  return fetchJSON<APIToken[]>('/tokens')
}

/**
 * Push a token's expiry out by a fresh window. The token value does not
 * change, so anything already using it keeps working.
 */
export async function renewToken(name: string, expiresInDays?: number) {
  const body = expiresInDays ? { expires_in_days: expiresInDays } : {}
  return postJSON<APIToken>(`/tokens/${encodeURIComponent(name)}/renew`, body)
}

export async function revokeToken(name: string) {
  return deleteJSON<unknown>(`/tokens/${encodeURIComponent(name)}`)
}

// Not called from the UI today — the response shape is intentionally left
// as `unknown` (not `any`) so any future caller has to narrow it before
// use instead of silently getting untyped access.
export async function batchRegisterClusters(clusters: Array<{ name: string; addons?: Record<string, boolean>; region?: string }>) {
  return postJSON<unknown>('/clusters/batch', { clusters })
}

export async function discoverClusters() {
  return fetchJSON<unknown>('/clusters/available')
}

export interface OperationStep {
  name: string
  status: 'pending' | 'running' | 'completed' | 'failed' | 'waiting'
  detail?: string
}

export interface OperationSession {
  id: string
  status: 'pending' | 'running' | 'completed' | 'failed' | 'cancelled' | 'waiting'
  steps: OperationStep[]
  wait_payload?: string
  wait_detail?: string
  error?: string
}

/**
 * Either the async-init shape (`operation_id`, poll it via getOperation)
 * or the legacy synchronous shape (`pr_url` / `pull_request_url`) — see
 * the "Legacy synchronous response fallback" branch in FirstRunWizard.tsx
 * and ConnectionSection.tsx, which is why both stay optional here.
 */
export interface InitRepoResult {
  operation_id?: string
  pr_url?: string
  pull_request_url?: string
  [key: string]: unknown
}

export async function initRepo(data?: { bootstrap_argocd?: boolean; auto_merge?: boolean }): Promise<InitRepoResult> {
  const res = await fetch(`${BASE_URL}/init`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...authHeaders() },
    body: JSON.stringify(data || { bootstrap_argocd: true }),
  })
  if (res.status === 401) {
    clearSession()
    window.location.reload()
    throw new Error('Session expired')
  }
  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: res.statusText }))
    throw new Error(err.error || res.statusText)
  }
  return res.json()
}

/**
 * Repo-state probe returned by GET /api/v1/init/status.
 *
 *   - "empty"        — the GitOps repo has not been initialized yet.
 *   - "initialized"  — repo is set up AND the ArgoCD bootstrap is healthy.
 *   - "partial"      — repo files exist but the ArgoCD bootstrap is genuinely
 *                      missing or degraded; `detail` carries the ArgoCD
 *                      diagnostic. Check `repairable` (w2-q2) before promising
 *                      a fix: true means the bootstrap app was simply never
 *                      created and POST /init can repair it with no PR; false
 *                      means the app already exists but is degraded, and
 *                      re-running Initialize cannot fix a live app.
 *   - "unreachable"  — repo files exist but ArgoCD simply CAN'T reach/compare
 *                      the repo right now (a connection/network problem, e.g. a
 *                      corporate Zscaler proxy). Re-initializing won't fix it;
 *                      the fix lives in Settings → Connections. `detail` carries
 *                      the ArgoCD diagnostic (e.g. "sync=Unknown health=Error").
 *   - "forbidden"    — repo files exist but ArgoCD rejected the read with a
 *                      403: the token lacks RBAC permission. `detail` carries
 *                      the server's actionable permission message.
 *   - "auth_failed"  — repo files exist but ArgoCD rejected the read with a
 *                      401: the token itself is invalid or expired. `detail`
 *                      carries the actionable token message. (error review
 *                      package 1)
 *   - "unknown"      — repo files exist but Sharko could not determine the
 *                      bootstrap app's health at all (the ArgoCD read failed
 *                      for a reason that is neither a permission problem nor
 *                      an invalid token, or no ArgoCD connection is
 *                      configured). This state must NEVER be treated like
 *                      "partial" — it does not mean the app is missing or
 *                      degraded, only that Sharko couldn't check. (error
 *                      review package 1)
 */
export interface InitStatus {
  state: 'empty' | 'initialized' | 'partial' | 'unreachable' | 'forbidden' | 'auth_failed' | 'unknown'
  detail: string
  /** Only meaningful when state is "partial" — see the state doc above. */
  repairable?: boolean
}

/**
 * Machine-readable `reason` tags on GET /api/v1/repo/status — mirrors the Go
 * doc comment on RepoStatusResponse (internal/api/repo_status.go). Typed as
 * a union (review findings r1, L15) instead of a bare string so
 * ConnectionErrorBanner's switch and repoHealth.ts's arrow derivations get
 * compile-time checking against the server's actual vocabulary.
 *
 *   - "no_connection"       — no active Git connection is configured.
 *   - "not_bootstrapped"    — the repo is reachable but genuinely not set up.
 *   - "connection_error"    — the Git repo could not be reached/verified.
 *   - "error"               — the HTTP probe itself failed client-side.
 *   - "bootstrap_unreachable" — ArgoCD can't reach/evaluate the Git repo.
 *   - "bootstrap_degraded"  — the engine app is missing or genuinely degraded.
 *   - "argocd_auth_failed"  — ArgoCD rejected Sharko's token (401).
 *   - "argocd_unreachable"  — Sharko never got a usable answer from ArgoCD.
 *   - "argocd_forbidden"    — ArgoCD rejected Sharko's token with a 403 (the
 *     token is valid but lacks permission to read applications).
 */
export type RepoStatusReason =
  | 'no_connection'
  | 'not_bootstrapped'
  | 'connection_error'
  | 'error'
  | 'bootstrap_unreachable'
  | 'bootstrap_degraded'
  | 'argocd_auth_failed'
  | 'argocd_unreachable'
  | 'argocd_forbidden'

export interface RepoStatusResponse {
  initialized: boolean
  bootstrap_synced?: boolean
  reason?: RepoStatusReason
  format?: string
}

/**
 * Read-only probe used by the first-run wizard's Step 4 before it offers to
 * initialize the repo. Unlike most helpers here it does NOT auto-reload on
 * 401 — the wizard owns that flow — and on any failure the caller falls back
 * to the Initialize offer so the user is never blocked.
 */
export async function getInitStatus(): Promise<InitStatus> {
  const res = await fetch(`${BASE_URL}/init/status`, { headers: authHeaders() })
  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: res.statusText }))
    throw new Error(err.error || res.statusText)
  }
  return res.json()
}

/**
 * Typed error thrown by `getOperation` so the wizard can distinguish a 401
 * (session expired — fatal, stop polling) from transient network errors
 * (swallow + retry).
 *
 * `getOperation` intentionally does NOT call `window.location.reload()`
 * (unlike most other helpers in this file). The wizard polls every 2s; a
 * silent reload mid-init would interrupt the in-progress operation display
 * before the user understands what happened, and a queued reload during a
 * 401 storm produces a "wizard appears frozen" symptom. The wizard owns
 * the error UX instead.
 */
export class OperationApiError extends Error {
  status: number
  constructor(message: string, status: number) {
    super(message)
    this.name = 'OperationApiError'
    this.status = status
  }
}

export function isUnauthorizedError(err: unknown): boolean {
  if (err instanceof OperationApiError) return err.status === 401
  if (err instanceof Error) {
    // Defense in depth: `getOperation` always throws OperationApiError, but
    // other layers (e.g. a fetch wrapper) might surface 401 as a plain
    // Error with the status in the message. Match the few common shapes.
    return /\b401\b|unauthorized|unauthenticated|session expired/i.test(
      err.message,
    )
  }
  return false
}

export async function getOperation(id: string): Promise<OperationSession> {
  const res = await fetch(`${BASE_URL}/operations/${id}`, { headers: authHeaders() })
  if (res.status === 401) {
    // Don't auto-reload — let the wizard surface a "Session expired"
    // error and provide a Log in again button. See OperationApiError above.
    throw new OperationApiError('Unauthorized: session expired', 401)
  }
  if (!res.ok) {
    throw new OperationApiError(`Failed to get operation (HTTP ${res.status})`, res.status)
  }
  return res.json()
}

export async function operationHeartbeat(id: string): Promise<void> {
  await fetch(`${BASE_URL}/operations/${id}/heartbeat`, {
    method: 'POST',
    headers: authHeaders(),
  })
}

// --- Tracked PRs ---

// `operation` is a CSV string of canonical Operation codes; `limit` is the
// server-side cap (default 100, hard cap 500). PullRequestsPanel uses
// these for the filter chips and the "View all on GitHub →" escape hatch.
export async function fetchTrackedPRs(filters?: {
  status?: string
  cluster?: string
  user?: string
  addon?: string
  operation?: string
  limit?: number
}) {
  const params = new URLSearchParams()
  if (filters) {
    if (filters.status) params.set('status', filters.status)
    if (filters.cluster) params.set('cluster', filters.cluster)
    if (filters.user) params.set('user', filters.user)
    if (filters.addon) params.set('addon', filters.addon)
    if (filters.operation) params.set('operation', filters.operation)
    if (filters.limit) params.set('limit', String(filters.limit))
  }
  const qs = params.toString()
  return fetchJSON<TrackedPRsResponse>(`/prs${qs ? `?${qs}` : ''}`)
}

export async function getAddonPRs(addonName: string) {
  return fetchJSON<TrackedPRsResponse>(`/prs?addon=${encodeURIComponent(addonName)}`)
}

export async function refreshPR(id: number) {
  return postJSON<{ status: string }>(`/prs/${id}/refresh`)
}

// ─── Merged PRs ────────────────────────────────────────────────────────────
//
// /api/v1/prs only returns OPEN PRs (the prtracker drops PRs once they merge).
// /api/v1/prs/merged goes back to the Git provider directly and lists merged
// PRs. Cached server-side for 60s. Used by the Dashboard Pending/Merged toggle.
export interface MergedPRItem {
  pr_id: number
  pr_url: string
  pr_title: string
  pr_branch: string
  description?: string
  author?: string
  cluster?: string
  addon?: string
  operation?: string
  created_at?: string
  merged_at?: string
}

export interface MergedPRsResponse {
  prs: MergedPRItem[]
  limit: number
}

export async function fetchMergedPRs(filters?: { cluster?: string; addon?: string; limit?: number }) {
  const params = new URLSearchParams()
  if (filters?.cluster) params.set('cluster', filters.cluster)
  if (filters?.addon) params.set('addon', filters.addon)
  if (filters?.limit) params.set('limit', String(filters.limit))
  const qs = params.toString()
  return fetchJSON<MergedPRsResponse>(`/prs/merged${qs ? `?${qs}` : ''}`)
}

// HealthResponse mirrors the shape returned by GET /api/v1/health.
// `cluster_test_available` is the capability flag the UI uses to gate the
// per-cluster Test button when no secrets backend is wired up on the
// active connection. The server (internal/api/health.go) sends exactly
// status/version/mode/cluster_test_available — no host_cluster_name or
// cluster_name field ever ships here (an earlier GitOpsSection.tsx read of
// those was dead code and has been removed).
//
// The index signature is kept for forward-compatibility with fields the
// server may add later that aren't yet pinned into this contract.
export interface HealthResponse {
  status: string
  version?: string
  mode?: string
  cluster_test_available?: boolean
  [key: string]: unknown
}

/**
 * migrateRepoRequest — POST /api/v1/migration/migrate. A dedicated fetch
 * (rather than the generic postJSON) so a 409 (a previous attempt's PR is
 * still open) throws MigrationPRAlreadyOpenError with the existing PR's
 * link, instead of the generic postJSON Error that would drop everything
 * but the summary sentence.
 */
async function migrateRepoRequest(req: MigrationMigrateRequest): Promise<MigrateResult> {
  const res = await fetch(`${BASE_URL}/migration/migrate`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...authHeaders() },
    body: JSON.stringify(req),
  })
  if (res.status === 401) {
    clearSession()
    window.location.reload()
    throw new Error('Session expired')
  }
  if (res.status === 409) {
    const errBody = await res.json().catch(() => ({}))
    throw new MigrationPRAlreadyOpenError(errBody)
  }
  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: res.statusText }))
    throw new Error(err.error || res.statusText)
  }
  return unwrapAttribution<MigrateResult>(await res.json())
}

// ArgoCD's /api/version returns a full info map (internal/argocd/client.go
// GetVersion), and internal/api/system.go's handleGetConfig puts that whole
// map into `argocd.version` verbatim — it does not extract just the version
// string. So the raw wire shape for `argocd.version` is either that object
// or absent, never a plain string. This type is only the fields we care
// about; the server sends more (BuildDate, Compiler, GitCommit, GitTag,
// GitTreeState, GoVersion, HelmVersion, JsonnetVersion, KubectlVersion,
// KustomizeVersion, Platform), which we ignore.
interface RawArgocdVersionInfo {
  Version?: string
}

// Pulls a plain version string out of whatever `argocd.version` turned out
// to be. Keeping this in one place means every caller of getConfig() only
// ever sees a string (or undefined) — never the raw object — so a stray
// `{version}` in JSX can't white-screen the app again.
export function extractArgocdVersionString(raw: unknown): string | undefined {
  if (typeof raw === 'string') return raw
  if (raw && typeof raw === 'object') {
    const version = (raw as RawArgocdVersionInfo).Version
    if (typeof version === 'string') return version
  }
  return undefined
}

// CLUSTERS_PAGE_SIZE is the per_page value the clusters list requests —
// the server's own max (internal/api/pagination.go's maxPerPage), so a
// single request covers any realistic estate (S4: the server silently
// truncated to its 20-per-page default before this, hiding every cluster
// past #20 at scale with no indication anything was cut).
export const CLUSTERS_PAGE_SIZE = 100

export const api = {
  // Health
  health: () => fetchJSON<HealthResponse>('/health'),

  // Clusters
  getClusters: () => fetchJSON<ClustersResponse>(`/clusters?per_page=${CLUSTERS_PAGE_SIZE}`),
  // getClustersPage — page-aware variant that also surfaces the total
  // count (X-Total-Count), for continuing past the first page when an
  // estate has more clusters than CLUSTERS_PAGE_SIZE. getClusters() above
  // covers every realistic case in one call; this exists for the rare
  // overflow the ClustersOverview "Load more" control handles.
  getClustersPage: (page: number, perPage: number = CLUSTERS_PAGE_SIZE) =>
    fetchJSONWithTotal<ClustersResponse>(`/clusters?page=${page}&per_page=${perPage}`),
  getDiscoveredClusters: () => fetchJSON<ClustersResponse>('/clusters?managed=false'),
  getCluster: (name: string) => fetchJSON<ClusterDetailResponse>(`/clusters/${name}`),
  getClusterComparison: (name: string) => fetchJSON<ClusterComparisonResponse>(`/clusters/${name}/comparison`),
  getConnectionComparison: (name: string) => fetchJSON<ConnectionComparisonView>(`/clusters/${name}/connection-comparison`),
  repairConnection: (name: string, reviewedCommit: string) =>
    fetchJSON<ConnectionRepairView>(`/clusters/${name}/connection-repair?reviewed_commit=${encodeURIComponent(reviewedCommit)}`, {
      method: 'POST',
    }),
  getClusterValues: (name: string) => fetchJSON<{ cluster_name: string; values_yaml: string }>(`/clusters/${name}/values`),
  getConfigDiff: (name: string) => fetchJSON<ConfigDiffResponse>(`/clusters/${name}/config-diff`),
  getClusterHistory: (name: string) => fetchJSON<{ cluster_name: string; history: SyncActivityEntry[] }>(`/clusters/${name}/history`),
  getClusterChanges: (name: string) => fetchJSON<{ cluster_name: string; changes: ClusterChange[] }>(`/clusters/${name}/changes`),

  // Addons
  getAddonCatalog: () => fetchJSON<AddonCatalogResponse>('/addons/catalog'),
  getAddonDetail: (name: string) => fetchJSON<AddonDetailResponse>(`/addons/${name}`),
  getAddonValues: (name: string) => fetchJSON<{ addon_name: string; values_yaml: string }>(`/addons/${name}/values`),
  getVersionMatrix: () => fetchJSON<VersionMatrixResponse>('/addons/version-matrix'),

  // Dashboard
  getDashboardStats: () => fetchJSON<DashboardStats>('/dashboard/stats'),
  getPullRequests: () => fetchJSON<PullRequestsResponse>('/dashboard/pull-requests'),

  // Connections
  getConnections: () => fetchJSON<ConnectionsListResponse>('/connections/'),
  createConnection: (data: unknown) => postJSON('/connections/', data),
  updateConnection: (name: string, data: unknown) => putJSON(`/connections/${encodeURIComponent(name)}`, data),
  deleteConnection: (name: string) => deleteJSON(`/connections/${encodeURIComponent(name)}`),
  setActiveConnection: (name: string) => postJSON('/connections/active', { connection_name: name }),
  testConnection: () => postJSON<{ git: { status: string; message?: string }; argocd: { status: string; message?: string } }>('/connections/test'),
  // `data` may include `use_saved: true` + `name` to instruct the backend
  // to fetch the named saved connection's stored credentials and test with
  // those (instead of the request body's tokens). Unlocks the wizard's
  // "leave blank to keep, or enter new value to replace" contract end-to-
  // end. Backend returns 400 if use_saved=true but no matching saved
  // connection.
  testCredentials: (data: unknown) => postJSON<{ git: { status: string; message?: string; auth?: string }; argocd: { status: string; message?: string; auth?: string } }>('/connections/test-credentials', data),
  discoverArgocd: (namespace?: string) => fetchJSON<{ server_url: string; has_env_token: boolean; namespace: string }>(`/connections/discover-argocd${namespace ? `?namespace=${namespace}` : ''}`),

  // Observability
  getObservability: () => fetchJSON<ObservabilityOverviewResponse>('/observability/overview'),

  // Upgrade
  getUpgradeVersions: (addonName: string) => fetchJSON<AvailableVersionsResponse>(`/upgrade/${addonName}/versions`),
  getUpgradeRecommendations: (addonName: string) => fetchJSON<UpgradeRecommendations>(`/upgrade/${addonName}/recommendations`),
  checkUpgrade: (addonName: string, targetVersion: string) => postJSON<UpgradeCheckResponse>('/upgrade/check', { addon_name: addonName, target_version: targetVersion }),
  getAddonChangelog: (name: string, from?: string, to?: string) => {
    const params = new URLSearchParams()
    if (from) params.set('from', from)
    if (to) params.set('to', to)
    return fetchJSON<{
      addon_name: string
      current_version: string
      target_version: string
      versions: { version: string; app_version: string; created: string; description: string }[]
      total_versions_between: number
    }>(`/addons/${name}/changelog?${params.toString()}`)
  },

  // AI
  getAIStatus: () => fetchJSON<{ enabled: boolean }>('/upgrade/ai-status'),
  getAISummary: (addonName: string, targetVersion: string) => postJSON<{ summary: string }>('/upgrade/ai-summary', { addon_name: addonName, target_version: targetVersion }),
  getAIConfig: () => fetchJSON<AIConfigResponse>('/ai/config'),
  saveAIConfig: (data: { provider: string; api_key?: string; model?: string; base_url?: string; ollama_url?: string; annotate_on_seed?: boolean }) => postJSON<{ status: string }>('/ai/config', data),
  setAIProvider: (provider: string) => postJSON<{ status: string; provider: string }>('/ai/provider', { provider }),
  testAI: () => postJSON<{ status: string; response: string }>('/ai/test', {}),
  testAIConfig: (data: { provider: string; api_key?: string; model?: string; base_url?: string; ollama_url?: string }) => postJSON<{ status: string; message?: string; response?: string }>('/ai/test-config', data),


  // Auth
  updatePassword: (currentPassword: string, newPassword: string) => postJSON<{ status: string }>('/auth/update-password', { current_password: currentPassword, new_password: newPassword }),

  // My account (tiered attribution)
  getMe: () => fetchJSON<import('./models').MeResponse>('/users/me'),
  setMyGitHubToken: (token: string) => putJSON<{ status: string; has_github_token: boolean }>('/users/me/github-token', { token }),
  clearMyGitHubToken: () => deleteJSON<{ status: string; has_github_token: boolean }>('/users/me/github-token'),
  testMyGitHubToken: () => postJSON<{ status: string; github_login: string }>('/users/me/github-token/test', {}),

  // Values editor
  getAddonValuesSchema: (addonName: string) =>
    fetchJSON<import('./models').AddonValuesSchemaResponse>(
      `/addons/${encodeURIComponent(addonName)}/values-schema`,
    ),
  setAddonValues: ((addonName: string, valuesYAML: string, dryRun = false) => {
    const body: { values: string; dry_run?: boolean } = { values: valuesYAML }
    if (dryRun) body.dry_run = true
    if (dryRun) {
      return putJSON<{ dry_run?: DryRunResult }>(
        `/addons/${encodeURIComponent(addonName)}/values`,
        body,
      ).then(envelope => (envelope.dry_run ?? (envelope as unknown as DryRunResult)))
    }
    return putJSON<import('./models').ValuesEditResult>(
      `/addons/${encodeURIComponent(addonName)}/values`,
      body,
    )
  }) as {
    (addonName: string, valuesYAML: string, dryRun: true): Promise<DryRunResult>
    (addonName: string, valuesYAML: string, dryRun?: false): Promise<import('./models').ValuesEditResult>
  },
  // Refresh-from-upstream uses the SAME endpoint as setAddonValues.
  // Backend ignores `values` when `refresh_from_upstream` is true,
  // fetches the chart's upstream values.yaml, runs the smart-values
  // pipeline, and overwrites the global file.
  refreshAddonValuesFromUpstream: ((addonName: string, dryRun = false) => {
    const body: { values: string; refresh_from_upstream: boolean; dry_run?: boolean } = {
      values: '',
      refresh_from_upstream: true,
    }
    if (dryRun) body.dry_run = true
    if (dryRun) {
      return putJSON<{ dry_run?: DryRunResult }>(
        `/addons/${encodeURIComponent(addonName)}/values`,
        body,
      ).then(envelope => (envelope.dry_run ?? (envelope as unknown as DryRunResult)))
    }
    return putJSON<import('./models').ValuesEditResult>(
      `/addons/${encodeURIComponent(addonName)}/values`,
      body,
    )
  }) as {
    (addonName: string, dryRun: true): Promise<DryRunResult>
    (addonName: string, dryRun?: false): Promise<import('./models').ValuesEditResult>
  },
  // Preview an additive merge of upstream values into the user's current
  // file. Returns a candidate body the UI shows in a diff modal; applying
  // calls setAddonValues with that body (no dedicated "apply merge"
  // endpoint — same PR flow as a manual edit).
  previewMergeAddonValues: (addonName: string) =>
    postJSON<import('./models').PreviewMergeResponse>(
      `/addons/${encodeURIComponent(addonName)}/values/preview-merge`,
      {},
    ),
  getClusterAddonValues: (clusterName: string, addonName: string) =>
    fetchJSON<import('./models').ClusterAddonValuesResponse>(
      `/clusters/${encodeURIComponent(clusterName)}/addons/${encodeURIComponent(addonName)}/values`,
    ),
  setClusterAddonValues: ((clusterName: string, addonName: string, valuesYAML: string, dryRun = false) => {
    const body: { values: string; dry_run?: boolean } = { values: valuesYAML }
    if (dryRun) body.dry_run = true
    if (dryRun) {
      return putJSON<{ dry_run?: DryRunResult }>(
        `/clusters/${encodeURIComponent(clusterName)}/addons/${encodeURIComponent(addonName)}/values`,
        body,
      ).then(envelope => (envelope.dry_run ?? (envelope as unknown as DryRunResult)))
    }
    return putJSON<import('./models').ValuesEditResult>(
      `/clusters/${encodeURIComponent(clusterName)}/addons/${encodeURIComponent(addonName)}/values`,
      body,
    )
  }) as {
    (clusterName: string, addonName: string, valuesYAML: string, dryRun: true): Promise<DryRunResult>
    (clusterName: string, addonName: string, valuesYAML: string, dryRun?: false): Promise<import('./models').ValuesEditResult>
  },

  // Manual AI annotate. Returns 200 (AnnotateAddonValuesResponse), 422
  // (AIAnnotateBlockedResponse) when the secret guard fires, or 503 when
  // AI is not configured. Uses a dedicated wrapper (not shared postJSON)
  // so the caller can inspect the typed 422 body via `(err as { body }).body`
  // — the shared wrapper drops the body to a plain message string which
  // would lose the secret-leak match list.
  annotateAddonValues: async (addonName: string) => {
    const res = await fetch(`${BASE_URL}/addons/${encodeURIComponent(addonName)}/values/annotate`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', ...authHeaders() },
      body: '{}',
    })
    if (res.status === 401) {
      clearSession()
      window.location.reload()
      throw new Error('Session expired')
    }
    const body = await res.json().catch(() => ({}))
    if (!res.ok) {
      const err = new Error((body as { error?: string; message?: string }).error || (body as { message?: string }).message || res.statusText) as Error & { body?: unknown; status?: number }
      err.body = body
      err.status = res.status
      throw err
    }
    return unwrapAttribution<import('./models').AnnotateAddonValuesResponse>(body)
  },
  // Per-addon AI opt-out toggle. Idempotent — flipping to the current
  // state returns 200 with `status: "noop"`.
  setAddonAIOptOut: (addonName: string, optOut: boolean) =>
    putJSON<{ status: string; opt_out: boolean; addon: string; pr_url?: string; pr_id?: number; merged?: boolean }>(
      `/addons/${encodeURIComponent(addonName)}/values/ai-opt-out`,
      { opt_out: optOut },
    ),

  // Legacy `<addon>:` wrap migration. Pass `addon` to migrate a single
  // file (used by the per-addon "Migrate this file" banner button); omit
  // it to migrate every wrapped file in the repo.
  unwrapGlobalValues: ((addonName?: string, dryRun = false) => {
    const params = new URLSearchParams()
    if (addonName) params.set('addon', addonName)
    if (dryRun) params.set('dry_run', 'true')
    const qs = params.toString()
    if (dryRun) {
      return postJSON<{ dry_run?: DryRunResult }>(`/addons/unwrap-globals${qs ? `?${qs}` : ''}`, {})
        .then(envelope => (envelope.dry_run ?? (envelope as unknown as DryRunResult)))
    }
    return postJSON<{
      migrated: number
      skipped: number
      files: Array<{ file: string; addon: string; status: string; message?: string }>
      message?: string
      pr_url?: string
      pr_id?: number
      branch?: string
      merged?: boolean
      attribution_warning?: string
    }>(`/addons/unwrap-globals${qs ? `?${qs}` : ''}`, {})
  }) as {
    (addonName: string | undefined, dryRun: true): Promise<DryRunResult>
    (addonName?: string, dryRun?: false): Promise<{
      migrated: number
      skipped: number
      files: Array<{ file: string; addon: string; status: string; message?: string }>
      message?: string
      pr_url?: string
      pr_id?: number
      branch?: string
      merged?: boolean
      attribution_warning?: string
    }>
  },

  getAddonValuesRecentPRs: (addonName: string, limit = 5) =>
    fetchJSON<import('./models').RecentPRsResponse>(
      `/addons/${encodeURIComponent(addonName)}/values/recent-prs?limit=${limit}`,
    ),
  getClusterAddonValuesRecentPRs: (clusterName: string, addonName: string, limit = 5) =>
    fetchJSON<import('./models').RecentPRsResponse>(
      `/clusters/${encodeURIComponent(clusterName)}/addons/${encodeURIComponent(addonName)}/values/recent-prs?limit=${limit}`,
    ),

  // Catalog editor — same endpoint as existing configureAddon() but a
  // typed wrapper that understands the ValuesEditResult shape with the
  // attribution_warning field.
  setAddonCatalog: (
    addonName: string,
    body: {
      version?: string
      self_heal?: boolean
      sync_options?: string[]
      ignore_differences?: Record<string, unknown>[]
      additional_sources?: Record<string, unknown>[]
      extra_helm_values?: Record<string, string>
    },
  ) =>
    fetchJSONMethod<import('./models').ValuesEditResult>(
      `/addons/${encodeURIComponent(addonName)}`,
      'PATCH',
      { name: addonName, ...body },
    ),

  // Agent Chat
  agentChat: (sessionId: string, message: string, pageContext?: string) => postJSON<{ session_id: string; response: string }>('/agent/chat', { session_id: sessionId, message, page_context: pageContext }),
  agentReset: (sessionId: string) => postJSON<{ status: string }>('/agent/reset', { session_id: sessionId }),

  // Users
  listUsers: () => fetchJSON<{ username: string; enabled: boolean; role: string }[]>('/users'),
  createUser: (data: { username: string; role: string }) => postJSON<{ username: string; temp_password: string; message: string }>('/users', data),
  updateUser: (username: string, data: { enabled?: boolean; role?: string }) =>
    fetchJSONMethod<{ username: string; enabled: boolean; role: string }>(`/users/${encodeURIComponent(username)}`, 'PUT', data),
  deleteUser: (username: string) => deleteJSON<void>(`/users/${encodeURIComponent(username)}`),
  resetPassword: (username: string) => postJSON<{ username: string; temp_password: string }>(`/users/${encodeURIComponent(username)}/reset-password`),

  // Embedded dashboards (persisted in K8s ConfigMap)
  getEmbeddedDashboards: () => fetchJSON<{ id: string; name: string; url: string; provider: string }[]>('/embedded-dashboards'),
  saveEmbeddedDashboards: (dashboards: { id: string; name: string; url: string; provider: string }[]) =>
    postJSON<unknown>('/embedded-dashboards', dashboards),

  // Cluster nodes
  getNodeInfo: () => fetchJSON<NodeInfoResponse>('/cluster/nodes'),

  // Home cluster (where Sharko & ArgoCD run)
  getHomeCluster: () => fetchJSON<{
    available: boolean;
    message?: string;
    kubernetes_version?: string;
    node_count?: number;
    nodes_ready?: number;
    nodes_not_ready?: number
  }>('/cluster/home'),

  // Server config — repo paths, GitOps settings, and ArgoCD connection
  // status/version (internal/api/system.go handleGetConfig). The home-
  // cluster identity card (dashboard facelift, Package 3) reads
  // `argocd.version` here; `argocd.connected` is false (and `version`
  // absent) whenever Sharko can't reach the active ArgoCD. The server
  // actually sends `argocd.version` as ArgoCD's full version-info object,
  // not a string — normalize it here so every caller only ever sees a
  // plain string (see extractArgocdVersionString above).
  getConfig: async () => {
    const raw = await fetchJSON<{
      repo_paths: { cluster_values: string; global_values: string; charts: string; bootstrap: string };
      gitops: { pr_auto_merge: boolean; branch_prefix: string; commit_prefix: string; base_branch: string };
      provider?: { type: string; region: string };
      argocd: { connected: boolean; version?: RawArgocdVersionInfo | string };
    }>('/config')
    return {
      ...raw,
      argocd: {
        connected: raw.argocd.connected,
        version: extractArgocdVersionString(raw.argocd.version),
      },
    }
  },

  // Fleet status overview (internal/api/fleet.go handleGetFleetStatus) —
  // the home-cluster identity card reads `uptime` (a server-formatted
  // human string, e.g. "3h12m") for its footer.
  getFleetStatus: () => fetchJSON<{
    server_version: string;
    uptime: string;
    git_unavailable?: boolean;
    argo_unavailable?: boolean;
    total_clusters: number;
    healthy_clusters: number;
    degraded_clusters: number;
    disconnected_clusters: number;
    total_addons: number;
    total_deployments: number;
    healthy_deployments: number;
    degraded_deployments: number;
    out_of_sync_deployments: number;
    addon_data_unavailable?: boolean;
  }>('/fleet/status'),

  // Dashboard
  getAttentionItems: () => fetchJSON<{ app_name: string; addon_name: string; cluster: string; health: string; sync: string; error?: string; error_type?: string }[]>('/dashboard/attention'),

  // Providers — `type` is narrowed to the generated `ProviderType` union
  // (sourced from internal/providers/provider.go via cmd/gen-provider-types)
  // so callers see a compile error if they accept a value the backend
  // factory would reject. Consumers that need to handle unknowns should
  // narrow with a type guard rather than upcasting.
  getProviders: () =>
    fetchJSON<{
      configured_provider:
        | {
            type: import('./models').ProviderType
            region: string
            prefix?: string
            status: string
            error?: string
            addon_secret_status?: 'ok' | 'missing' | 'invalid_argocd'
            addon_secret_message?: string
          }
        | null
      available_types: import('./models').ProviderType[]
    }>('/providers'),

  // Tests the configured secrets/cluster-credentials provider ("vault" in
  // plain terms — AWS Secrets Manager, Kubernetes Secrets, or the ArgoCD
  // cluster-secret backend) by listing cluster credential entries. Mirrors
  // testConnection()/testCredentials() for git+ArgoCD — same plain-words
  // pass/fail contract, never a raw Go/SDK error (v4-wave2 8.1).
  testProvider: () =>
    postJSON<{ status: string; message?: string; clusters_found?: number }>('/providers/test', {}),

  // Repo status — `bootstrap_synced` reports whether the canonical ArgoCD
  // application `cluster-addons-bootstrap` exists AND is Sync=Synced AND
  // Health=Healthy. App.tsx's wizard gate combines
  // (!initialized || !bootstrap_synced) so a missing/degraded bootstrap
  // auto-opens the wizard. Backend returns `bootstrap_synced=false`
  // defensively whenever the ArgoCD client is unavailable or the probe
  // fails — the wizard exists to recover that state.
  getRepoStatus: () => fetchJSON<RepoStatusResponse>('/repo/status'),

  // Cluster addons
  enableAddonOnCluster: (clusterName: string, addonName: string): Promise<DeployAddonResult> =>
    postJSON<DeployAddonResult>(`/clusters/${encodeURIComponent(clusterName)}/addons/${encodeURIComponent(addonName)}`, { yes: true }),

  restartAddonSync: (clusterName: string, addonName: string): Promise<{ terminated: boolean; synced: boolean }> =>
    postJSON<{ terminated: boolean; synced: boolean }>(
      `/clusters/${encodeURIComponent(clusterName)}/addons/${encodeURIComponent(addonName)}/restart-sync`,
    ),

  // Notifications
  getNotifications: () => fetchJSON<{
    notifications: { id: string; type: string; title: string; description: string; timestamp: string; read: boolean }[]
    unread_count: number
  }>('/notifications'),

  markAllNotificationsRead: () => postJSON<unknown>('/notifications/read-all'),

  markNotificationRead: (id: string) => postJSON<unknown>(`/notifications/${encodeURIComponent(id)}/read`),

  // ─── Curated catalog (Marketplace) ──────────────────────────────────────
  // Three reads:
  //   1. listCuratedCatalog       — Browse tab grid (server-side filters)
  //   2. getCuratedCatalogEntry   — single-entry detail (Configure modal)
  //   3. listCuratedCatalogVersions — chart versions for the version picker
  //
  // All three are GET-only; no audit/tier coverage required.

  listCuratedCatalog: (filters?: import('./models').CatalogListFilters) => {
    const params = new URLSearchParams()
    if (filters?.q) params.set('q', filters.q)
    if (filters?.category && filters.category.length > 0) {
      // Backend handler only honours a single `category` query param today.
      // Multi-category is handled client-side in MarketplaceBrowseTab so we
      // just send the first when there's exactly one — otherwise let the
      // client filter the full list. Same approach for license below.
      if (filters.category.length === 1) params.set('category', filters.category[0])
    }
    if (filters?.curated_by && filters.curated_by.length > 0) {
      // The backend takes a comma-separated list and ANDs them; that maps
      // exactly to multi-select semantics.
      params.set('curated_by', filters.curated_by.join(','))
    }
    if (filters?.license && filters.license.length === 1) {
      params.set('license', filters.license[0])
    }
    if (filters?.min_score && filters.min_score > 0) {
      params.set('min_score', String(filters.min_score))
    }
    const qs = params.toString()
    return fetchJSON<import('./models').CatalogListResponse>(
      `/marketplace/addons${qs ? `?${qs}` : ''}`,
    )
  },

  getCuratedCatalogEntry: (name: string) =>
    fetchJSON<import('./models').CatalogEntry>(
      `/marketplace/addons/${encodeURIComponent(name)}`,
    ),

  // ─── The org's approved catalog (v4 wave 2.5 — "catalog = the approved
  // list") ───────────────────────────────────────────────────────────────
  // Reads/writes catalog.yaml ONLY — no curated auto-seeding. A fresh repo
  // returns zero entries. Replaces the old v4-wave-1 "delta" model
  // (listMergedCatalog/getMergedCatalogAddon/addInternalAddon, which used
  // to seed from the curated list — that's exactly the bug that made a
  // fresh org's catalog show 45 addons nobody approved).

  /** List every approved addon: catalog.yaml, nothing else. */
  listCatalogApproved: () =>
    fetchJSON<import('./models').CatalogAddonListResponse>('/catalog/addons'),

  /** Single approved-addon lookup. 404 means "not approved" — even for a
   *  name the Marketplace knows well. */
  getCatalogApprovedAddon: (name: string) =>
    fetchJSON<import('./models').CatalogAddon>(
      `/catalog/addons/${encodeURIComponent(name)}`,
    ),

  /**
   * Add one or more addons to the approved catalog, committed via a pull
   * request — one element = a single add, N elements = ONE batch PR,
   * never N. Passing `enable_on_cluster` makes it the combo: one PR that
   * touches both catalog.yaml and cluster-addons/<name>.yaml.
   */
  addToCatalog,

  /**
   * List configured catalog sources (embedded + third-party). Powers the
   * source-badge tooltip (last-fetched / status) on Browse tiles and the
   * "Source" section on the addon detail page.
   */
  listCatalogSources: () =>
    fetchJSON<import('./models').CatalogSourceRecord[]>('/marketplace/sources'),

  /**
   * Force-refresh all configured catalog sources. Tier-2 (admin); backend
   * emits an audit entry. Returns the fresh record list. Powers the
   * "Refresh now" button in Settings → Catalog Sources.
   */
  refreshCatalogSources: () =>
    postJSON<import('./models').CatalogSourceRecord[]>('/marketplace/sources/refresh', {}),

  /**
   * v4 wave 1 Story 3.4 — catalog-wide version-freshness summary (when
   * Sharko last checked chart versions across the curated catalog + the
   * engine pin). Powers the Marketplace Browse tab's "Last checked" line.
   */
  getCatalogFreshness: () =>
    fetchJSON<import('./models').CatalogFreshnessResponse>('/catalog/freshness'),

  /**
   * Request an out-of-cycle freshness refresh (the Browse tab's "Refresh"
   * action). Non-blocking — the pass runs in the background; callers
   * re-fetch getCatalogFreshness / listCuratedCatalogVersions afterward to
   * see updated timestamps. Tier-2 (operator); backend emits an audit
   * entry.
   */
  refreshCatalogFreshness: () =>
    postJSON<{ message: string }>('/catalog/freshness/refresh', {}),

  listCuratedCatalogVersions: (
    name: string,
    options?: { includePrereleases?: boolean },
  ) => {
    const params = new URLSearchParams()
    if (options?.includePrereleases === false) {
      params.set('include_prereleases', 'false')
    }
    const qs = params.toString()
    return fetchJSON<import('./models').CatalogVersionsResponse>(
      `/marketplace/addons/${encodeURIComponent(name)}/versions${qs ? `?${qs}` : ''}`,
    )
  },

  /**
   * Paste-URL validator. Confirms an arbitrary `<repo>/index.yaml` is
   * reachable and contains the named chart. Returns 200 in both the happy
   * and structured-failure paths — branch on `resp.valid` and
   * `resp.error_code`.
   */
  validateCatalogChart: (repo: string, chart: string) => {
    const params = new URLSearchParams({ repo, chart })
    return fetchJSON<import('./models').CatalogValidateResponse>(
      `/catalog/validate?${params.toString()}`,
    )
  },

  /**
   * List every chart name in a Helm repo's index.yaml. Powers the chart-
   * name dropdown in the manual "Add Addon" form. Same `valid` +
   * `error_code` envelope as validateCatalogChart so the UI can reuse its
   * existing error switch table.
   */
  listRepoCharts: (repo: string) => {
    const params = new URLSearchParams({ repo })
    return fetchJSON<import('./models').CatalogRepoChartsResponse>(
      `/catalog/repo-charts?${params.toString()}`,
    )
  },

  // ─── ArtifactHub proxy (Search tab) ────────────────────────────────────
  // Server-side proxy: the browser never calls ArtifactHub directly. The
  // backend handles caching, rate-limit backoff, and stale-serve.

  /**
   * Blended search across the Sharko-curated catalog and ArtifactHub. Returns
   * curated hits and ArtifactHub hits in one envelope; if ArtifactHub is
   * unreachable, curated hits are still returned and `artifacthub_error` is
   * populated so the UI can show the unreachable banner.
   */
  searchCatalog: (q: string, limit = 20) => {
    const params = new URLSearchParams({ q, limit: String(limit) })
    return fetchJSON<import('./models').CatalogSearchResponse>(
      `/marketplace/search?${params.toString()}`,
    )
  },

  /**
   * Per-package detail (proxied from ArtifactHub). Used to pre-fill the
   * Configure modal for an external chart. Returns the trimmed package shape
   * (description, maintainers, available versions, links).
   */
  getRemoteCatalogPackage: (repo: string, name: string) =>
    fetchJSON<import('./models').CatalogRemotePackageResponse>(
      `/marketplace/remote/${encodeURIComponent(repo)}/${encodeURIComponent(name)}`,
    ),

  /**
   * README markdown for a curated catalog addon. The backend resolves the
   * curated chart → ArtifactHub package (best-match heuristic), fetches
   * the package detail, and returns just the README. Empty `readme` means
   * the chart was found but doesn't ship a README — render an empty
   * state, not an error.
   */
  getCuratedCatalogReadme: (name: string) =>
    fetchJSON<import('./models').CatalogReadmeResponse>(
      `/marketplace/addons/${encodeURIComponent(name)}/readme`,
    ),

  /**
   * Force ArtifactHub connectivity re-check. Resets the in-memory backoff and
   * purges the search/package caches; returns whether ArtifactHub is currently
   * reachable from the Sharko process. Tier 1 (admin) — used by the "Retry"
   * button on the unreachable banner.
   */
  reprobeArtifactHub: () =>
    postJSON<import('./models').CatalogReprobeResponse>(`/marketplace/reprobe`),

  // ─── Server-wide settings (V2-cleanup-85.4) ────────────────────────────

  /**
   * Current server-wide connectivity probe mode ('check-app' | 'api-test').
   * Powers the Settings → Connectivity Probe toggle.
   */
  getProbeMode: () =>
    fetchJSON<import('./models').ProbeModeResponse>('/settings/probe-mode'),

  /**
   * Set the server-wide connectivity probe mode. Admin-only on the backend
   * (403 for non-admins) — gate the control with isAdmin before calling.
   */
  setProbeMode: (probe_mode: import('./models').ProbeMode) =>
    putJSON<import('./models').ProbeModeResponse>('/settings/probe-mode', { probe_mode }),

  // ─── Server-wide settings (V2-cleanup-89.6) ────────────────────────────

  /**
   * Whether the "Paste a kubeconfig" registration path is enabled
   * server-wide (default true). Powers the Settings → Inline Credentials
   * toggle and the Register dialog's Connection source select.
   */
  getAllowInlineCredentials: () =>
    fetchJSON<import('./models').AllowInlineCredentialsResponse>('/settings/allow-inline-credentials'),

  /**
   * Set the server-wide allow_inline_credentials switch. Admin-only on the
   * backend (403 for non-admins) — gate the control with isAdmin before
   * calling.
   */
  setAllowInlineCredentials: (allow_inline_credentials: boolean) =>
    putJSON<import('./models').AllowInlineCredentialsResponse>('/settings/allow-inline-credentials', {
      allow_inline_credentials,
    }),

  // ─── Addon values engine off switch (gitops-proud P4-I, D2) ───────────

  /**
   * Whether the addon-values secrets engine is switched on (default true).
   * Powers the Settings → Addon Values Engine toggle, and the setting is
   * what the Secret Sync page's engine strip reads to decide whether it
   * shows "Addon values engine is switched off."
   */
  getAddonValuesEngineEnabled: () =>
    fetchJSON<import('./models').AddonValuesEngineEnabledResponse>('/settings/addon-values-engine-enabled'),

  /**
   * Set the server-wide addon_values_engine_enabled switch. Admin-only on
   * the backend (403 for non-admins) — gate the control with isAdmin
   * before calling.
   */
  setAddonValuesEngineEnabled: (addon_values_engine_enabled: boolean) =>
    putJSON<import('./models').AddonValuesEngineEnabledResponse>('/settings/addon-values-engine-enabled', {
      addon_values_engine_enabled,
    }),

  // ─── Default Addons (V3-P2.2) ──────────────────────────────────────────

  /**
   * Get the current default addon set (reads git file, falls back to
   * connection string). Returns `{addons: string[]}`.
   */
  getDefaultAddons: () =>
    fetchJSON<{ addons: string[] }>('/default-addons'),

  /**
   * Update default addons via PR. Writes git file and opens (or updates)
   * a PR. Does NOT mutate the connection. Returns `{pr_url: string,
   * pr_id: number, message?: string}` or DryRunResult when dry_run is true.
   */
  putDefaultAddons: ((addons: string[], dryRun = false) => {
    const body: { addons: string[]; dry_run?: boolean } = { addons }
    if (dryRun) body.dry_run = true
    if (dryRun) {
      return putJSON<{ dry_run?: DryRunResult }>('/default-addons', body)
        .then(envelope => (envelope.dry_run ?? (envelope as unknown as DryRunResult)))
    }
    return putJSON<{ pr_url: string; pr_id: number; message?: string }>('/default-addons', body)
  }) as {
    (addons: string[], dryRun: true): Promise<DryRunResult>
    (addons: string[], dryRun?: false): Promise<{ pr_url: string; pr_id: number; message?: string }>
  },

  // v3 -> v4 repo migration (v4 Wave 2, Epic 5 / migration-ui). Three
  // doors, in the order a person uses them: status (is there anything to
  // migrate?), preview (show me every file), migrate (do it, one PR).
  getMigrationStatus: () => fetchJSON<MigrationStatus>('/migration/status'),
  previewMigration: () => postJSON<MigrationPlan>('/migration/preview', {}),
  migrateRepo: (req: MigrationMigrateRequest = { yes: true }) => migrateRepoRequest(req),
  // The fourth door, which a person normally never presses: finish the
  // ArgoCD side when the automatic run after the merge did not happen.
  // Idempotent, so pressing it twice costs nothing.
  completeMigration: () => postJSON<RuntimeHandoffReport>('/migration/complete', {}),
}
