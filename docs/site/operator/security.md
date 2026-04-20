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

### API Keys

API keys use bcrypt hashing — the server never stores plaintext keys. The plaintext key is shown only once at creation time. Treat API keys as secrets; store them in your CI/CD vault (e.g., GitHub Actions secrets, Vault).

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
- Do not enable `config.devMode: true` in production — it allows credential fallback via environment variables
- Set `SHARKO_WEBHOOK_SECRET` when exposing the webhook endpoint to the internet

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
