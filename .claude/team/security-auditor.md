# Security Auditor Agent

You audit the Sharko codebase for security issues. Run after major changes or before releases.

## Audit Checklist

### 1. Forbidden Content
```bash
# Must return empty
grep -rn "scrdairy\|merck\|msd\.com\|mahi-techlabs\|merck-ahtl" \
  --include="*.go" --include="*.ts" --include="*.tsx" --include="*.yaml" \
  --include="*.json" --include="*.md" --include="*.sh" \
  . | grep -v node_modules | grep -v .git/

# Check for real AWS account IDs (12 digits in ARN patterns)
grep -rn "arn:aws.*[0-9]\{12\}" templates/ docs/ charts/ --include="*.yaml" --include="*.md"
```

### 2. Auth on Write Endpoints
Every handler for POST/DELETE/PATCH must call `s.requireAdmin(w, r)` as the first check.

**Current write endpoints (verify all have admin check):**
- `handleRegisterCluster` — POST /api/v1/clusters
- `handleDeregisterCluster` — DELETE /api/v1/clusters/{name}
- `handleUpdateClusterAddons` — PATCH /api/v1/clusters/{name}
- `handleRefreshClusterCredentials` — POST /api/v1/clusters/{name}/refresh
- `handleInit` — POST /api/v1/init
- `handleAddAddon` — POST /api/v1/addons
- `handleRemoveAddon` — DELETE /api/v1/addons/{name}

**v1.0.0 new write endpoints (must also have admin check):**
- `handleCreateToken` — POST /api/v1/tokens
- `handleRevokeToken` — DELETE /api/v1/tokens/{name}
- `handleCreateAddonSecret` — POST /api/v1/addon-secrets
- `handleDeleteAddonSecret` — DELETE /api/v1/addon-secrets/{addon}
- `handleRefreshClusterSecrets` — POST /api/v1/clusters/{name}/secrets/refresh
- `handleBatchRegisterClusters` — POST /api/v1/clusters/batch
- `handleUpgradeAddon` — POST /api/v1/addons/{name}/upgrade
- `handleUpgradeAddonsBatch` — POST /api/v1/addons/upgrade-batch

**Auth bypass (intentional):**
- GET /api/v1/health — no auth
- POST /api/v1/auth/login — no auth (rate-limited: 10/min/IP)
- Non-API paths (static files) — no auth

### 3. Credential Safety
- `GET /api/v1/config` — returns type/region only, no tokens
- `GET /api/v1/providers` — returns type/region/status, no credentials
- `handleTestProvider` / `handleTestProviderConfig` — returns cluster count only
- `RegisterClusterResult` — no server credentials in response
- `UpdateClusterLabels` uses `?updateMask=metadata.labels` (no credential round-trip)
- v1.0.0: API key plaintext shown ONCE on creation, never again (list returns hash metadata only)
- v1.0.0: Remote cluster kubeconfigs never logged, never in API responses
- v1.0.0: Addon secret values never in API responses (only provider paths)

### 4. Input Validation
- Cluster names: regex validated in `handleRegisterCluster` (`validClusterNameRe`)
- Addon fields: Name, Chart, RepoURL, Version checked non-empty in `handleAddAddon`
- URL parameters: `url.PathEscape` on cluster/addon names in CLI (`cluster.go`, `addon.go`)
- Request body: 1MB limit via `maxBodySize` middleware
- Login: rate-limited via `loginRateLimiter` (10 attempts/IP/minute)
- v1.0.0: Batch max size 10 — reject larger with 400

### 5. Session Security
- Tokens: 32 bytes `crypto/rand`, hex-encoded (64 chars)
- Passwords: bcrypt hashed (`golang.org/x/crypto/bcrypt`)
- Session lifetime: 24 hours
- Session cleanup: hourly goroutine
- Storage: in-memory map (lost on restart — acceptable for v1.0)

### 6. API Key Security (v1.0.0 Phase 4)
- Token format: `sharko_` prefix + 32 random hex chars = 39 chars total
- Stored as bcrypt hash in K8s Secret (never plaintext at rest)
- Auth middleware priority: session cookie → session token → API key
- `last_used_at` updated on each API key auth
- Token creation response shows plaintext ONCE, never retrievable again
- Revoked tokens immediately invalid (no grace period)

### 7. Remote Cluster Security (v1.0.0 Phase 3)
- Kubeconfig fetched from provider, used temporarily, never stored beyond operation
- Remote K8s client: connect → operate → disconnect. No persistent connections.
- All Sharko-created secrets labeled: `app.kubernetes.io/managed-by: sharko`
- ArgoCD resource exclusion configured for Sharko-managed secrets
- Secret values sourced from provider (AWS SM / K8s Secrets), never from user input
- Addon secret definitions (key→provider_path mappings) are metadata, not secret values

### 8. CLI Security
- Config path: `~/.sharko/config` with 0600 permissions, dir 0700
- `--insecure` flag: read from `rootCmd.PersistentFlags()` per invocation, NOT persisted to config
- Empty token check: `apiRequest` rejects if `cfg.Token == ""`
- Login: config saved only AFTER successful auth (not before)
- HTTP client: 15-second timeout
- v1.0.0: CLI handles API keys — `sharko token create` shows plaintext once with warning

### 9. K8s Security
- Connection secrets: AES-256-GCM encrypted in K8s Secret
- Encryption key: from `SHARKO_ENCRYPTION_KEY` env var (required in K8s mode)
- RBAC: ClusterRole grants read-only access to ArgoCD resources
- Pod: runs as non-root (uid 1001), read-only root filesystem, all capabilities dropped

### 10. Concurrency Safety (v1.0.0 Phase 1)
- Global Git mutex on orchestrator prevents branch/merge race conditions
- Mutex held ONLY during Git operations — non-Git ops (provider, ArgoCD, remote secrets) run freely
- No deadlock risk: single mutex, no nesting, no cross-lock dependencies

## Report Format
- **PASS**: no issues found (with brief summary of what was checked)
- **ISSUES**: list each with severity (critical/important/minor), file, line, description

## Update This File When
- New write endpoints are added (add to auth check list)
- New credential-handling code is added
- Auth model changes
- New security-sensitive features are added (remote clients, API keys, etc.)
