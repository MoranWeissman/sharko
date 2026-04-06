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

Sharko applies rate limiting to authentication endpoints. Rate limiting relies on the client's real IP address, which requires correct **trusted proxy** configuration.

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

Optionally, read access to Kubernetes Nodes can be enabled for fleet dashboards that show node counts:

```yaml
config:
  nodeAccess: true
```

This adds a separate ClusterRole rule. Disable it if not needed.

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

## Secrets Management Recommendations

- Use `existingSecret` with **Sealed Secrets** or **External Secrets Operator** instead of passing tokens as Helm values
- Enable **RBAC audit logging** in your cluster to track Sharko's API calls
- Rotate GitHub PATs and ArgoCD tokens periodically via the Settings UI
- Do not enable `config.devMode: true` in production — it allows credential fallback via environment variables
