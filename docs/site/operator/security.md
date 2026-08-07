# Security

This page documents Sharko's security posture and hardening recommendations for production deployments.

## Security Headers

Sharko sets the following HTTP security headers on every response:

| Header | Value |
|--------|-------|
| `Content-Security-Policy` | Restricts sources for scripts, styles, and frames |
| `Strict-Transport-Security` | `max-age=31536000; includeSubDomains` (HTTPS enforced for 1 year) |
| `X-Content-Type-Options` | `nosniff` |
| `X-Frame-Options` | `DENY` |
| `Referrer-Policy` | `strict-origin-when-cross-origin` |

HSTS is only effective when Sharko is served over HTTPS. Configure TLS termination at the ingress layer and ensure the ingress controller forwards the `X-Forwarded-Proto` header.

## Rate Limiting

Sharko applies rate limiting to both authentication endpoints and admin write endpoints:

| Scope | Limit |
|-------|-------|
| Auth endpoints (`/api/v1/auth/*`) | Per-IP burst limit |
| Write endpoints (admin POST/DELETE/PATCH) | 30 requests/minute per IP |

Rate limiting relies on the client's real IP address, which requires correct **trusted proxy** configuration.

If Sharko is behind a reverse proxy or ingress controller, set the `SHARKO_TRUSTED_PROXIES` environment variable to the proxy's IP CIDR or `"*"` to trust all proxies (only safe in controlled environments):

```yaml
extraEnv:
  - name: SHARKO_TRUSTED_PROXIES
    value: "10.0.0.0/8"
```

!!! warning
    Without a trusted proxy configuration, the rate limiter sees the proxy's IP instead of the real client IP, which means a single attacker could exhaust the rate limit for all users.

## Authentication

### Admin Password

The initial admin password is randomly generated and stored as a bcrypt hash in a Kubernetes Secret. Retrieve it once and change it immediately after first login.

### No Users Configured

!!! danger "Risk: Auth disabled"
    If the `sharko-users` ConfigMap is deleted or contains no users, and no `SHARKO_AUTH_USER` / `SHARKO_AUTH_PASSWORD` env vars are set, **authentication may be bypassed**. Always ensure at least one user account exists.

Check user configuration:

```bash
kubectl get configmap sharko-users -n sharko -o yaml
```

### Login Sessions

A person logging in gets a session that **lasts 24 hours**. That window is the
default and it is not configurable. When it runs out the session is gone: the
next request comes back `401` and the person logs in again. There is no refresh
token and no "remember me", so a stolen session token is useful to an attacker
for at most 24 hours.

Sessions live in memory, so a pod restart signs everyone out. A background
sweep clears expired sessions every hour, but the sweep is only housekeeping —
every single request re-checks the expiry, so an expired session is refused
immediately whether or not the sweep has run.

### API Keys

API keys use bcrypt hashing — the server never stores plaintext keys. The plaintext key is shown only once at creation time. Treat API keys as secrets; store them in your CI/CD vault (e.g., GitHub Actions secrets, Vault).

**Keys expire after 90 days by default.** You can ask for anything from 1 to
365 days when you create one. A key past its date is refused with a `401` that
names the key and says it expired — the caller already holds that key, so
naming it gives nothing away. A key that was revoked or never existed gets a
flat `401` instead, so nobody can probe for real key names.

**Renewing** a key pushes its expiry out without changing the key value, so
pipelines holding it keep working with nothing to redeploy. **Revoking** takes
effect immediately, with no grace period.

**Keys created before expiry dates existed** have no expiry. Sharko does NOT
force-expire them — they keep working, and they show up in the key list as
`legacy-no-expiry` so you can spot them. Recreate them when convenient so they
pick up an expiry date.

Full request and response shapes are in the
[API endpoints reference](../api/endpoints.md#api-keys-tokens).

## Application Roles

Every user and every API key carries one of three roles. The role decides
which write actions (and a few read actions) are allowed — it is checked on
**every** request, not just at login.

| Role | Who it's for | Can do |
|------|---------------|--------|
| `viewer` | Anyone who should be able to look, not touch | See clusters, addons, connections, pull requests, the audit log, and metrics. Manage their own profile (change their own token, clear their own GitHub token). Cannot create, change, or delete anything shared. |
| `operator` | The people who run day-to-day operations | Everything a viewer can do, plus: register/adopt/test/diagnose clusters, enable/disable addons, restart a stuck sync, create/update connections, test connections and credential providers, edit the addon catalog, create API tokens and renew *their own* tokens, trigger the secrets reconciler, and run the first-time init wizard. |
| `admin` | Whoever owns the Sharko install | Everything an operator can do, plus the actions with blast radius beyond the caller's own work: delete a connection, remove or unadopt a cluster, remove an addon from the catalog, create/delete/change the role of other users, revoke any token, renew *someone else's* token, clear the audit log, change AI provider settings, save dashboard layouts, edit ArgoCD resource exclusions, create/delete addon secrets, delete a pull request, refresh third-party catalog sources, and flip the security-relevant settings toggles (probe mode, inline credentials, self-heal). |

**New users and new API tokens default to `viewer`** if no role is given —
the caller (an admin) has to deliberately opt a person up to `operator` or
`admin`, both at `POST /api/v1/users` and `POST /api/v1/tokens`.

**A token can never carry a role higher than the person who created it.** An
operator asking for an `admin` token is refused with a 403; only an admin can
create an admin token. Without that ceiling, "create your own API token"
would be a one-request way for an operator to hand themselves every
permission their own account is refused.

**Renewing a token follows who owns it.** An operator may renew a token they
created (or a token renewing itself, since an API key authenticates under its
own name); renewing anyone else's — including a token created before Sharko
started recording who asked for it — takes an admin. Renewing keeps a live
credential alive, so it sits on the same own/other line as revoking.

An action Sharko's code doesn't recognize is treated as **admin-only**
(fail-closed) rather than open to everyone — a bug that adds a new write path
without registering its action shows up as "nobody but admin can use this
yet," never as "anyone can use this."

### The honest limit: roles are coarse, not scoped

As of v4, permissions are **not** scoped to a specific cluster or a specific
addon. An `operator` who can enable addons can enable them on *any*
registered cluster — there is no "operator for cluster X only" or "operator
for addon Y only." The three roles are the entire permission model.

This is a known gap, not an oversight: finer-grained, per-cluster or
per-addon permissions are tracked as future work (see the
[roadmap](../community/roadmap.md)) and are intentionally out of scope for
v4. If your team needs cluster-level isolation today, the practical
workaround is separate Sharko installs per team/cluster-group rather than
one shared install with mixed trust levels.

### Where the mapping lives

The full action → role table is `internal/authz/authz.go`
(`authz.ActionRequirements`). It is enforced identically on two surfaces:

- **REST API** — every mutating handler (`POST`/`PUT`/`PATCH`/`DELETE`) calls
  `authz.RequireWithResponse` naming its action before doing anything else.
- **AI assistant tools** — the handful of tools that write (enable/disable an
  addon, bump a catalog version, trigger an ArgoCD sync/refresh) check the
  SAME action table before touching Git or ArgoCD, so a viewer asking the
  assistant to "enable datadog on prod" gets the identical refusal a direct
  API call would get.

Both surfaces are covered by a test that mechanically re-derives the route
and tool inventory from the source (rather than trusting a hand-maintained
list to stay in sync): `internal/api/authz_coverage_test.go` and
`internal/api/ai_tools_authz_parity_test.go`.

## Pod Security

Sharko's default security context enforces a hardened pod configuration:

```yaml
podSecurityContext:
  runAsNonRoot: true
  runAsUser: 1001
  runAsGroup: 1001
  fsGroup: 1001

securityContext:
  allowPrivilegeEscalation: false
  readOnlyRootFilesystem: true
  runAsNonRoot: true
  capabilities:
    drop:
      - ALL
```

This is compliant with the Kubernetes **Restricted** Pod Security Standard. No privileged containers, no root access, no capability escalation.

## RBAC

Sharko creates a `ClusterRole` granting read access to ArgoCD resources:

```yaml
rbac:
  create: true
  argocdNamespace: argocd
```

The ClusterRole grants `get`, `list`, and `watch` on ArgoCD CRDs (Applications, AppProjects, ApplicationSets). It does not grant write access to the Kubernetes API.

Read access to Kubernetes Nodes (`get`, `list` on `v1/nodes`) is granted by default so the Dashboard node-count widget works out of the box. Node metadata is low-sensitivity — no pod, secret, or workload data is exposed. To disable it on clusters where cluster-wide node reads are restricted, set:

```yaml
config:
  nodeAccess: false
```

When disabled, the `/api/v1/cluster/nodes` endpoint returns an empty list with a `"Node info only available when running in-cluster"` style message and the Dashboard widget degrades gracefully.

## Secret Encryption

Connection credentials (ArgoCD tokens, Git tokens) stored in the `sharko-connections` Secret are encrypted at rest using **AES-256-GCM** with a randomly generated encryption key. The encryption key is stored in the Helm release Secret.

!!! tip
    To rotate the encryption key, update the `SHARKO_ENCRYPTION_KEY` env var and re-save all connections in the Settings UI.

### A stored credential only ever goes to its own address

`POST /connections/test-credentials` lets you re-test a saved connection
without re-typing its tokens: leave the token fields blank, name the
connection, and Sharko fills in what it has stored.

Sharko will only do that for the address the connection already points at. If
the request names a different Git repository URL, a different Git provider, or
a different ArgoCD server, the stored token is **not** used and the call is
refused with a 422 telling you to submit that address's credentials
explicitly. Otherwise "test this connection" would be a way to have Sharko
post its stored secrets to any address the caller cares to name. All four
connectivity tests (`/connections/test`, `/connections/test-credentials`,
`/providers/test`, `/providers/test-config`) also require `operator` — they
reach out with real credentials, which is not a read.

## Network Policy

Sharko does not ship a NetworkPolicy by default. For production, create one that restricts inbound traffic to your ingress controller and ArgoCD, and restricts outbound traffic to ArgoCD and your Git provider:

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: sharko
  namespace: sharko
spec:
  podSelector:
    matchLabels:
      app.kubernetes.io/name: sharko
  policyTypes:
    - Ingress
    - Egress
  ingress:
    - from:
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: ingress-nginx
  egress:
    - to:
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: argocd
    - ports:
        - port: 443
          protocol: TCP
```

## Webhook Security

`POST /api/v1/webhooks/git` accepts push events from your Git provider to trigger secrets reconciliation. Protect this endpoint with HMAC-SHA256 signature verification:

1. Generate a random secret: `openssl rand -hex 32`
2. Configure it in Sharko: `secrets.webhookSecret: "<secret>"` (or `SHARKO_WEBHOOK_SECRET` env var)
3. Configure the same secret in your Git provider's webhook settings

Sharko verifies the `X-Hub-Signature-256` header. Requests without a valid signature return `401 Unauthorized`.

!!! warning
    If `SHARKO_WEBHOOK_SECRET` is empty, HMAC verification is skipped. Always set a webhook secret in production.

## Secrets Provider Security Model

Sharko's secrets reconciler uses a push-based model:

- Sharko fetches secrets from the provider (AWS SM or K8s Secrets) at reconcile time
- Values are **never cached** in memory or on disk between reconcile cycles
- Secrets are pushed directly to remote clusters via temporary kubeconfig connections
- All Sharko-managed secrets are labeled `app.kubernetes.io/managed-by: sharko`
- ArgoCD must exclude these secrets from management (see [Configuration](configuration.md#secrets-reconciler))

This means the blast radius of a Sharko compromise is limited to the window between reconcile cycles — there is no persistent plaintext store on the Sharko pod.

## Secrets Management Recommendations

- Use `existingSecret` with **Sealed Secrets** or **External Secrets Operator** instead of passing tokens as Helm values
- Enable **RBAC audit logging** in your cluster to track Sharko's API calls
- Rotate GitHub PATs and ArgoCD tokens periodically via the Settings UI
- Do not set `SHARKO_DEV_MODE=true` in production — it allows credential fallback via environment variables
- Set `SHARKO_WEBHOOK_SECRET` when exposing the webhook endpoint to the internet
- **Turn off `allow_inline_credentials` in production** (Settings → same section as Connectivity Probe, default `true`). This closes the one registration path where sensitive kubeconfig bytes travel inside the request itself — with it off, registration only accepts a pointer to an already-stored secret, an EKS token mint, or no credentials at all, enforcing GitOps-clean secret-store pointers for every cluster. Every other registration path (and enabling addons) is unaffected. Once scoped RBAC ships (see the [roadmap](../community/roadmap.md)), this is planned to become a per-role permission rather than one server-wide switch — until then, it's all-or-nothing for every admin.

## Tiered Git Attribution (v1.20+)

Sharko classifies every mutating endpoint as **Tier 1** (operational) or **Tier 2** (configuration) and resolves the Git author accordingly:

| Tier | Examples | Token used | Commit author | Trailer |
|---|---|---|---|---|
| **Tier 1** | cluster register/remove, addon enable/disable, addon upgrade, PR refresh, connection CRUD, AI config | Service token | `Sharko Bot` | `Co-authored-by: <user>` |
| **Tier 2** | edit addon catalog metadata, edit values | Per-user PAT if configured, else service token | The user (per-user PAT) or `Sharko Bot` (fallback) | None (per-user) or `Co-authored-by: <user>` (fallback) |
| **Personal / Auth / Webhook** | login, set-own-PAT, inbound webhooks | n/a | n/a | n/a |

Each user can configure a personal GitHub PAT under **Settings → My Account**. PATs are stored encrypted at rest with `SHARKO_ENCRYPTION_KEY` (AES-256-GCM, the same key used by the connection store) under the `<username>.github_token` key in the auth Secret.

The audit log records the resolved attribution mode on every mutating entry:

| `attribution_mode` | Meaning |
|---|---|
| `service` | Service token used; no human identified on the commit (e.g. webhooks) |
| `co_author` | Service token used; user listed in `Co-authored-by:` trailer |
| `per_user` | Per-user PAT used; commit `Author` IS the user |

When a user performs a Tier 2 action without a personal PAT configured, the response includes `attribution_warning: "no_per_user_pat"` and the UI renders a banner pointing to **Settings → My Account**.

For the full design rationale and the V2.x roadmap that builds on this foundation, see `docs/design/2026-04-16-attribution-and-permissions-model.md`.

## v2.0.0 threat model

The structured threat model for the v2.0.0 production-launch baseline lives at [`docs/design/2026-06-02-threat-model-v2.md`](https://github.com/MoranWeissman/sharko/blob/main/docs/design/2026-06-02-threat-model-v2.md). It documents the seven assets, six primary trust boundaries (with STRIDE per boundary), OWASP Top 10 (2021) mapping, CNCF / SLSA supply-chain analysis, the comprehensive 40-row existing-mitigation table, and the 11 known gaps tracked for v3+. The companion review-prep bundle scoped for an external security consultant is tracked in `.bmad/output/reviews/v2-security-review-prep.md`.

## SSRF guard on URL-fetching endpoints

Several endpoints fetch from a user-supplied URL (e.g. `GET /api/v1/catalog/validate` pulls `<repo>/index.yaml` from a Helm repo URL the user pastes into the Marketplace). To prevent an authenticated user from coaxing the server into hitting cluster-internal addresses (the K8s API, ArgoCD, the cloud-provider metadata service), Sharko ships a built-in SSRF guard that runs in front of every such handler.

The guard rejects URLs that resolve to:

| Range | Reason |
|---|---|
| `127.0.0.0/8`, `::1` | Loopback |
| `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16` | RFC1918 private |
| `169.254.0.0/16`, `fe80::/10` | Link-local (cloud metadata services) |
| `fc00::/7` | IPv6 ULA |
| Multicast / unspecified | Defense in depth |

A blocked request returns HTTP 200 with `error_code: "ssrf_blocked"` (matches the rest of the catalog-validate failure taxonomy so the UI's switch table doesn't need to branch on HTTP status).

### Optional allowlist

For higher-assurance deployments, set `SHARKO_URL_ALLOWLIST` to restrict outbound fetches to a fixed set of hostnames:

```yaml
extraEnv:
  - name: SHARKO_URL_ALLOWLIST
    value: "charts.jetstack.io,charts.bitnami.com,api.scorecard.dev"
```

When set, only the listed hostnames pass the guard — every other host is rejected with `ssrf_blocked: not_in_allowlist`. When unset, the guard falls back to the default deny-list above (RFC1918 + loopback + link-local + ULA), which is appropriate for self-hosted Sharko behind a network policy.

The guard runs in addition to (not instead of) any Kubernetes NetworkPolicy fronting the Sharko pod. Treat it as defense-in-depth — operators of production clusters should still apply egress NetworkPolicy that pins Sharko to its required external endpoints.

## Secret-leak guard on AI annotation

When AI annotation is enabled (V121-7), Sharko scans every upstream `values.yaml` for secret-like patterns (AWS keys, GitHub PATs, JWTs, PEM blocks, Slack tokens, Google API keys, generic API key/password assignments, high-entropy base64 blobs). On a match the LLM call is **hard-blocked** — there is no override.

Every block emits a dedicated audit-log entry with the event name `secret_leak_blocked` so security review can grep one stable token across the audit log:

```bash
curl -H "Authorization: Bearer $SHARKO_TOKEN" \
  "https://sharko.example.com/api/v1/audit?action=block&limit=200" \
  | jq '.[] | select(.event == "secret_leak_blocked")'
```

The audit `Detail` field carries the source handler (`addon_add`, `ai_annotate`, or `values_refresh`), the chart + version, the match count, and the deduplicated list of pattern names that fired. The actual matched bytes are never logged, never stored, and never returned in API responses.
