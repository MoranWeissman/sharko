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

### 10. Concurrency Safety
- Global Git mutex on orchestrator prevents branch/merge race conditions
- Mutex held ONLY during Git operations — non-Git ops (provider, ArgoCD, remote secrets) run freely
- No deadlock risk: single mutex, no nesting, no cross-lock dependencies
- Reconcilers (`internal/clusterreconciler`, `internal/prtracker`, `internal/argosecrets`,
  `internal/secrets`) use a single-goroutine design with `sync.Once` for Start() idempotency.
  Test seams MUST be per-instance Deps fields, never package-level vars (race hazard under
  `t.Parallel()` — V125-1-8.0 lesson).

### 11. Ownership-Label Gate (V125-1-8)
- Every ArgoCD cluster Secret Sharko writes carries `app.kubernetes.io/managed-by: sharko`.
- Every cluster-Secret deletion MUST check `clusterreconciler.IsManagedBySharko(secret)` first.
  This is the canonical safety gate for V125-1-7 orphan-delete and V125-2 Adopt distinction.
  A delete code path that bypasses the predicate is a critical finding.
- Both the legacy `argosecrets.Manager` and the new `clusterreconciler.Reconciler` MUST emit
  byte-identical Secret payloads — they share `argosecrets.BuildSecretConfigJSON` +
  `argosecrets.BuildClusterSecretLabels` wrappers. Hand-rolled Secret payloads in either path
  are a critical finding.

### 12. Schema / Envelope Integrity (V125-1-9)
- Reads of `managed-clusters.yaml` and `addon-catalog.yaml` MUST go through the envelope-aware
  loaders (`models.LoadManagedClusters`, `catalog.LoadAddonCatalog`), which validate against the
  committed JSON Schema before unmarshalling. Direct `yaml.Unmarshal` over these files (especially
  remotely-fetched bytes) is a finding — it bypasses the validator and allows malformed input
  through the trust boundary.
- Writes MUST go through `models.SaveManagedClusters` / catalog equivalents (envelope-emitting
  writers).
- Schema regeneration is committed (`docs/schemas/*.v1.json` + `internal/schema/*.v1.json` —
  dual-write via `cmd/schema-gen`). The `schemas-up-to-date` CI gate catches drift; an audit
  that finds a way to silence the gate is a critical finding.

### 13. No-AVP / No-Redis-Leak Design Constraint
- Sharko intentionally has NO AVP (ArgoCD Vault Plugin) integration and NO Redis ingestion of
  secret material. Any code path that proposes to bridge secrets through ArgoCD's Redis cache
  or AVP's secret-injection model is a critical finding — the current design is push-based,
  Sharko-controlled, and audit-reviewable.

## Catalog signing surface (V123-2)

The v1.23 catalog-signing surface introduced a set of cosign/Sigstore concerns that every audit touching `internal/catalog/` MUST cover:

- **Cosign-keyless trust root TUF wiring.** The Sigstore verifier resolves the public-good Fulcio + Rekor root via TUF. The TUF cache must land on a writable path under read-only-rootfs pods — confirm the cache dir is overridable (env or constructor option) and is NOT pointed at a read-only mount. Regression that bit `v1.23.0-rc.0` in production.
- **Per-entry signature verification.** Every `CatalogEntry` with a `signature` sidecar URL must round-trip through `signing.LoadBytesWithVerifier` on load. Verify on fetch only, NOT on every API request — re-verifying per request is a perf footgun and was settled in OQ §7.2.
- **Trust-policy regex semantics.** Operator-supplied patterns in `SHARKO_CATALOG_TRUSTED_IDENTITIES` MUST be **anchored** (`^...$`) — an unanchored pattern matches substrings and silently widens trust. The `<defaults>` expansion token is the canonical way to keep CNCF + Sharko-release-workflow defaults; reject any audit finding that suggests dropping the defaults wholesale.
- **`workflow_run` cert SAN encoding.** GitHub Actions Sigstore certs encode the **`job_workflow_ref`** (e.g. `https://github.com/owner/repo/.github/workflows/release.yml@refs/tags/v1.23.0`) as the SAN, NOT the triggering tag string. The default trust regex must match `job_workflow_ref` form. Regression that bit `v1.23.0-rc.2` in production.
- **Modern Sigstore Bundle format.** The verifier consumes the **modern Sigstore Bundle** (`sigstore-go` Bundle type), NOT the legacy v1 format. Any code path that accepts/produces signature material must round-trip the modern Bundle. Regression that bit `v1.23.0-rc.1` in production.

Outcome surfaces: `verified` (bool) and `signature_identity` (string) on the `CatalogEntry` API model and JSON responses. Both fields are user-visible (Marketplace **Verified** badge + AddonDetail signature panel) — never silently flip them based on trust-policy edits without a re-fetch.

## Report Format
- **PASS**: no issues found (with brief summary of what was checked)
- **ISSUES**: list each with severity (critical/important/minor), file, line, description

## Update This File When
- New write endpoints are added (add to auth check list)
- New credential-handling code is added
- Auth model changes
- New security-sensitive features are added (remote clients, API keys, etc.)
