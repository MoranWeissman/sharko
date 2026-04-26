---
story_key: V123-2-2-load-time-verification-via-cosign-library-open-question-7-2
epic: V123-2 (Per-entry cosign signing)
status: review
effort: L
dispatched: 2026-04-26
merged: TBD
---

# Story V123-2.2 — Load-time verification via cosign library + resolves OQ §7.2

## Brief (from epics-v1.23.md §V123-2.2 + design §3.3.1, §7.2)

As the **catalog loader**, I want to fetch + verify each entry's Sigstore
bundle using the cosign Go library and annotate the in-memory entry with the
`verified` outcome, so that third-party catalogs can prove provenance at
ingest.

**Resolves design open question §7.2** — re-verify on every reload vs only on
fetch. Decision below.

## Acceptance criteria

**Given** open question §7.2
**When** this story lands
**Then** the design doc is updated: **verify on fetch only**. The fetcher
caches `(verified, issuer)` in the snapshot; the merger / API handlers read
those flags without re-verifying. Per-entry signatures are verified once when
the loader processes the entry, then the result is cached on the merged-entry
struct.

**Given** the existing `sources.SidecarVerifier` interface (V123-1.2)
**When** the implementation lands at `internal/catalog/signing/`
**Then** `signing.Verifier` satisfies `sources.SidecarVerifier`. The fetcher
package does NOT import `signing/` — dependency direction stays
signing → sources.

**Given** an entry with a valid per-entry `signature.bundle` URL
**When** the loader calls `signing.VerifyEntry(ctx, canonicalEntryBytes, bundleURL, trustPolicy)`
**Then** the bundle is fetched, verified against the canonical serialization,
and the entry surfaces `verified: true` + `signature_identity: <OIDC subject>`
on the API.

**Given** an entry with a mismatched bundle (signature payload doesn't match)
**Then** verifier returns `(false, "", nil)` — fetcher / loader records
`verified: false`. NOT an error.

**Given** an entry whose certificate identity doesn't match `TrustPolicy.Identities`
**Then** verifier returns `(false, "", nil)`. NOT an error.

**Given** an entry with no `signature` block
**Then** the loader does NOT call the verifier; the entry surfaces
`verified: false` (unsigned-but-accepted).

**Given** the cosign library is the mechanism (NFR-V123-6)
**Then** ZERO shelling out to the `cosign` CLI. Pure Go API.

**Given** a fetched catalog file with a companion sidecar URL
**When** the fetcher calls `SidecarVerifier.Verify(catalogBytes, sidecarURL, trustPolicy)`
**Then** the implementation handles whole-file verification — the existing
fetcher path lights up automatically once the verifier is wired at startup.

## Design decisions

### §7.2 resolution: verify on fetch only

- **Fetcher (V123-1.2)** calls `SidecarVerifier.Verify` once per fetch cycle
  for the *whole catalog file*. Result lands on `SourceSnapshot.Verified` +
  `SourceSnapshot.Issuer`. Subsequent reads of `Snapshots()` return cached
  results — no re-verification on every API request.
- **Per-entry** verification happens once when the loader processes a YAML
  entry with `signature.bundle != nil`. Result lands on the in-memory
  CatalogEntry (new fields below). Subsequent merges / API reads use the
  cached result.
- **Trigger for re-verification**: only when (a) the fetcher pulls a fresh
  copy of the YAML (bytes changed by content hash), or (b) the loader
  re-processes the catalog at boot. NOT on every API request, NOT on a
  Settings refresh button click that returns the same bytes.
- **Why "on fetch" wins over "on every reload"**: cosign keyless
  verification is expensive (network call to Rekor for transparency-log
  inclusion proof). Per-API-request verification would saturate Rekor and
  lag handlers. Per-fetch is bounded.

### Two verifier surfaces

1. **Whole-file** (`signing.Verifier{}.Verify(...)`) — implements
   `sources.SidecarVerifier`. Used by the fetcher when a `.bundle` sidecar
   exists next to the YAML.
2. **Per-entry** (`signing.VerifyEntry(ctx, payloadBytes, bundleURL, trustPolicy) (verified bool, issuer string, err error)`) —
   pure function (no receiver) the loader calls when an entry's YAML has
   `signature.bundle != nil`. Pulls the bundle, computes canonical entry
   bytes, runs verification.

### Canonical serialization (per-entry)

The "message" being signed for a per-entry signature is a deterministic
YAML rendering of the entry without the `signature` field itself
(otherwise the signature would have to sign itself).

- Use `yaml.v3` Marshal on a struct copy with `Signature: nil`.
- yaml.v3 encodes struct fields in declaration order — deterministic.
- Document the canonical-serialization rule in
  `docs/site/developer-guide/catalog-signing.md` (placeholder doc — fully
  fleshed in V123-4.3).

### New fields on the in-memory catalog entry (NOT persisted to YAML)

Add to `internal/catalog/loader.go` `CatalogEntry`:

```go
// Verified is the post-load cosign-verification outcome (V123-2.2).
// True only when the entry had a valid signature.bundle that verified
// against the trust policy. Computed at load time; never persisted.
Verified bool `yaml:"-" json:"verified"`

// SignatureIdentity is the OIDC subject (cert SAN) of the verified
// signer when Verified is true. Empty otherwise. (V123-2.2)
SignatureIdentity string `yaml:"-" json:"signature_identity,omitempty"`
```

Both `yaml:"-"` (forgery-resistant — same model as `Source` in V123-1.4).
JSON-emitted so the UI in V123-2.4 can render the verified pill.

### Trust policy reuse

The existing `sources.TrustPolicy{Identities []string}` from V123-1.2 is the
canonical shape. Per-entry verifier accepts the same struct (or a copy with
the same shape — keep them aligned, or make `signing` import
`sources.TrustPolicy` as a type alias to avoid drift).

Each `Identity` is a regex matched against the OIDC subject from the cert.
**No prefix wildcards / glob conversion** — operators write proper Go regex.
Empty `Identities` slice = trust nothing → reject every signed entry.
This is a deliberate fail-closed default.

## Implementation plan

### 1. New package `internal/catalog/signing/`

Files:
- `internal/catalog/signing/verify.go` — `Verifier` type + `VerifyEntry` function.
- `internal/catalog/signing/canonical.go` — deterministic YAML marshal of a
  CatalogEntry minus its Signature field.
- `internal/catalog/signing/verify_test.go` — unit tests with fixture bundles.
- `internal/catalog/signing/testfixtures/` — pre-generated Sigstore bundles +
  matching payloads (ASCII / base64 in test files for portability OR small
  binary blobs checked in via .gitattributes binary marker).

### 2. Verifier type

```go
package signing

import (
    "context"
    "fmt"
    "io"
    "net/http"
    "regexp"
    "time"

    "github.com/MoranWeissman/sharko/internal/catalog/sources"
    sigsverify "github.com/sigstore/cosign/v2/pkg/cosign"
    "github.com/sigstore/cosign/v2/pkg/cosign/bundle"
    // (resolve actual current API via context7 MCP at implementation time —
    //  cosign's Go API has moved between v2.x releases; use the latest
    //  v2 package path that exposes blob/bundle verification)
)

// Verifier is the production SidecarVerifier implementation.
// Construct one via NewVerifier(httpClient).
type Verifier struct {
    http *http.Client
}

func NewVerifier(httpClient *http.Client) *Verifier {
    if httpClient == nil {
        httpClient = &http.Client{Timeout: 30 * time.Second}
    }
    return &Verifier{http: httpClient}
}

// Verify implements sources.SidecarVerifier — whole-file path.
func (v *Verifier) Verify(
    ctx context.Context,
    catalogBytes []byte,
    sidecarURL string,
    trustPolicy sources.TrustPolicy,
) (verified bool, issuer string, err error) {
    bundleBytes, err := v.fetchBundle(ctx, sidecarURL)
    if err != nil {
        return false, "", fmt.Errorf("fetch sidecar: %w", err)
    }
    return verifyBundle(ctx, catalogBytes, bundleBytes, trustPolicy)
}

// VerifyEntry is the per-entry verifier called by the loader when a
// CatalogEntry has signature.bundle != nil.
func VerifyEntry(
    ctx context.Context,
    canonicalEntryBytes []byte,
    bundleURL string,
    trustPolicy sources.TrustPolicy,
    httpClient *http.Client,
) (verified bool, issuer string, err error) {
    if httpClient == nil {
        httpClient = &http.Client{Timeout: 30 * time.Second}
    }
    bundleBytes, err := fetchBundle(ctx, httpClient, bundleURL)
    if err != nil {
        return false, "", fmt.Errorf("fetch entry bundle: %w", err)
    }
    return verifyBundle(ctx, canonicalEntryBytes, bundleBytes, trustPolicy)
}

// verifyBundle is the shared core: parse bundle, verify cert chain
// against Fulcio root, verify signature against payload, verify Rekor
// inclusion, check OIDC subject against TrustPolicy.Identities regexes.
func verifyBundle(
    ctx context.Context,
    payload []byte,
    bundleBytes []byte,
    trustPolicy sources.TrustPolicy,
) (bool, string, error) {
    // ... use cosign library's bundle.VerifyBundle or equivalent ...
    // ... extract OIDC subject from cert ...
    // ... regex-match against trustPolicy.Identities ...
    //
    // Return (false, "", nil) for: sig mismatch, untrusted identity,
    //                              missing Rekor entry, expired cert.
    // Return (false, "", err) only for: malformed bundle, unparseable cert.
}
```

### 3. Canonical serialization

```go
// CanonicalEntryBytes returns the deterministic YAML serialization of
// the entry MINUS its Signature field. This is the message that a
// per-entry cosign signature attests to. yaml.v3 marshals struct fields
// in declaration order, so as long as CatalogEntry's field order is
// stable (and it is — the struct is frozen in loader.go), the output
// is deterministic across Sharko binaries.
func CanonicalEntryBytes(e catalog.CatalogEntry) ([]byte, error) {
    e.Signature = nil  // strip — signing the signature is meaningless
    e.Verified = false  // computed-not-signed
    e.SignatureIdentity = ""
    e.Source = ""  // computed-not-signed
    return yaml.Marshal(e)
}
```

### 4. Loader wiring

`internal/catalog/loader.go` gets a new optional verifier param:

```go
// LoadBytesWithVerifier is the verification-aware variant. When
// httpClient + trustPolicy are non-zero AND an entry has
// signature.bundle, the entry is verified at load time. Failures land
// as Verified=false (not as load errors — unsigned/untrusted entries
// are still loaded).
func LoadBytesWithVerifier(data []byte, httpClient *http.Client, tp sources.TrustPolicy) (*Catalog, error)
```

Existing `Load()` and `LoadBytes()` keep working — they call through with
nil verifier params (no verification path).

The `cmd/sharko/serve.go` startup wires the verifier into the fetcher (for
whole-file path) AND passes it to the embedded-catalog loader (for per-entry
path).

### 5. Server bootstrap wiring (cmd/sharko/serve.go)

- Construct `signing.NewVerifier(...)` once at startup IFF
  `SHARKO_CATALOG_TRUSTED_IDENTITIES` is set (V123-2.3 lands the env var;
  this story can use an empty TrustPolicy as default — verifier will reject
  all signatures, which is safe).
- Pass it to `sources.NewFetcher(cfg, verifier, nil)`.
- Use it from the embedded loader path: `LoadBytesWithVerifier(addonsYAML, httpClient, trustPolicy)`.

### 6. Audit on verification outcomes (defensive)

When the fetcher or loader records a `verified=false` result on an entry that
HAD a `signature.bundle` (i.e., signing was attempted and failed), emit a
WARN log with the URL fingerprint (NOT the URL itself — same fp pattern as
V123-1.2 fetcher) so operators see "you tried to sign this and it didn't
work." Successful verifications log INFO with issuer.

## Test plan

### Unit — `verify_test.go`

Use fixture bundles. Two viable fixture-generation paths:

**Option A** — pre-generate bundles with a real cosign CLI run, check them in:
- One bundle signed by a known test identity (e.g., "test@example.com")
  matching a hard-coded payload.
- One bundle whose payload doesn't match the asserted blob (mismatch case).
- One bundle whose identity doesn't match (untrusted-identity case).
- Document how to regenerate them in a `testfixtures/README.md`.

**Option B** — use sigstore's test helpers (if v2 exposes a "fake Fulcio +
fake Rekor" harness) to mint bundles in `TestMain`. Cleaner but heavier.

Recommend Option A for initial landing — simpler, no test-only sigstore deps.

Cases:

1. `TestVerify_HappyPath` — valid bundle + matching payload + trusted identity → `(true, "<subject>", nil)`.
2. `TestVerify_SignatureMismatch` — valid bundle but payload differs → `(false, "", nil)`.
3. `TestVerify_UntrustedIdentity` — valid bundle, OIDC subject doesn't match any TrustPolicy regex → `(false, "", nil)`.
4. `TestVerify_EmptyTrustPolicy` — valid bundle but `TrustPolicy.Identities` is empty → `(false, "", nil)` (fail-closed).
5. `TestVerify_MalformedBundle` — non-Sigstore bytes → `(false, "", err)` (infrastructure error).
6. `TestVerify_HTTPFetchFails` — sidecar URL returns 404 → `(false, "", err)`.
7. `TestVerify_ContextCancelled` — ctx cancelled mid-fetch → `(false, "", ctx.Err())`.
8. `TestVerifyEntry_HappyPath` — per-entry path with canonical YAML → `(true, "<subject>", nil)`.
9. `TestVerifyEntry_PayloadMismatch` — per-entry, payload changed → `(false, "", nil)`.
10. `TestCanonicalEntryBytes_StripsSignature` — entry with Signature set → output bytes do not contain "signature:".
11. `TestCanonicalEntryBytes_StripsRuntimeFields` — entry with Verified=true + SignatureIdentity set + Source set → output bytes do not contain those keys (not signed).
12. `TestCanonicalEntryBytes_Deterministic` — two calls with the same entry produce byte-identical output.

### Integration — wire-through

If the fetcher integration test from V123-1.9 can be extended without too much
rework, add a case:
- Spin up an HTTPS test server serving a YAML + a sidecar bundle with a
  matching payload signed by a known test identity.
- Configure the fetcher with a `signing.NewVerifier(...)` + a TrustPolicy
  that whitelists the test identity.
- Force a refresh.
- Assert `snap.Verified == true` and `snap.Issuer == "<test identity>"`.

If integration test growth is too risky in this PR, defer the integration
case to V123-2.6 (the dedicated tests story).

### Loader test

- `TestLoadBytesWithVerifier_PerEntryHappyPath` — YAML with one entry that has a valid signature.bundle → entry's `Verified == true`.
- `TestLoadBytesWithVerifier_PerEntryUnsigned` — YAML with one unsigned entry + verifier wired → entry's `Verified == false`, no error.
- `TestLoadBytesWithVerifier_PerEntryMismatch` — YAML with a signature.bundle whose payload doesn't match → `Verified == false`, entry still loaded (no error).

### Quality gates

- `go build ./...`
- `go vet ./...`
- `go test ./internal/catalog/... -race -count=1`
- `go mod tidy` after adding sigstore deps; commit `go.sum` changes.
- `golangci-lint run ./internal/catalog/signing/...` (silent skip if missing).
- Swagger regen — required because new JSON fields (`verified`, `signature_identity`) surface on `CatalogEntry`.

## Explicit non-goals

- `SHARKO_CATALOG_TRUSTED_IDENTITIES` env var parsing — V123-2.3.
- UI verified badge / "Signed only" filter — V123-2.4.
- Release pipeline signing of the embedded catalog — V123-2.5.
- Comprehensive end-to-end fixture suite — V123-2.6.

## Dependencies

- V123-2.1 — `Signature` field on `CatalogEntry` — done ✅.
- V123-1.2 — `sources.SidecarVerifier` interface — done ✅.

## Gotchas

1. **Cosign Go API moves.** Use **context7 MCP** at implementation time to
   resolve the current `github.com/sigstore/cosign/v2/pkg/cosign/...` package
   layout. The "right" API for verifying a Sigstore bundle (cert + sig +
   Rekor entry) has shifted between v2.x releases. Don't trust the import
   paths in this brief verbatim — verify them.
2. **No `cosign` CLI shell-out.** NFR-V123-6 is non-negotiable.
3. **Pure Go module.** sigstore/cosign + transitive deps will balloon
   `go.mod` significantly. Run `go mod tidy` and commit the lock changes.
   Build time will increase ~30s on first compile; subsequent builds cached.
4. **Trust policy regex compilation** — compile each Identities regex once
   on policy change, not per-verification.
5. **Cert chain validation** — let the cosign library handle Fulcio root
   trust. Don't roll your own CA chain check.
6. **Rekor inclusion proof** — required by default for keyless verification.
   Don't disable this; it's the transparency-log guarantee.
7. **Time-of-check** — cert NotBefore/NotAfter must be valid AT THE TIME OF
   the Rekor entry, not at verify time. cosign handles this correctly when
   you pass the bundle's timestamp through.
8. **Fail-closed defaults**: empty `Identities` → reject. Don't fall back to
   "trust everything" or "trust well-known issuers."
9. **Audit trail**: failed verifications on entries that HAD signatures
   should be visibly logged (WARN level) — the operator chose to sign them
   for a reason. Use `source_fp` fingerprint, never the raw URL.
10. **Test fixtures lifecycle**: pre-generated bundles will eventually expire
    (cert NotAfter ~10 minutes for keyless). Either (a) pin a fake Fulcio
    test root in tests so cert validation passes regardless of expiry, or
    (b) use sigstore's test helpers if available, or (c) document the
    fixture-regeneration cadence and accept that tests will need refresh
    every N months. Prefer (a) or (b); (c) is a hidden time bomb.

## Role files (MUST embed in dispatch)

- `.claude/team/go-expert.md` — primary (interface impl, package design).
- `.claude/team/security-auditor.md` — signing is security-sensitive; cert
  validation, fail-closed defaults, no-shell-out NFR.
- `.claude/team/test-engineer.md` — fixture strategy + table-driven tests.

## Use context7 MCP

Before writing the Verifier body, query context7 for current sigstore/cosign
v2 Go API. Specifically:
- How to load a Sigstore bundle (`bundle.SigstoreBundle` vs other types)
- How to verify a bundle against a payload + Fulcio root + Rekor public key
- How to extract the OIDC subject from the verified cert

The cosign Go API has shifted between 2.0, 2.1, 2.2, 2.3 — context7 will give
the canonical current path.

## PR plan

- Branch: `dev/v1.23-cosign-verifier` off main.
- Commits (suggested):
  1. `feat(catalog/signing): add verifier package + canonical serialization (V123-2.2)`
  2. `feat(catalog/signing): cosign keyless verification impl (V123-2.2)`
  3. `feat(catalog): add Verified + SignatureIdentity fields, wire LoadBytesWithVerifier (V123-2.2)`
  4. `feat(catalog/sources): wire whole-file verifier at fetcher startup (V123-2.2)`
  5. `test(catalog/signing): bundle verification + canonical serialization tests (V123-2.2)`
  6. `docs(swagger): regen for verified + signature_identity JSON fields`
  7. `chore(bmad): mark V123-2.2 for review`
- No tag.

## Next story

V123-2.3 — `SHARKO_CATALOG_TRUSTED_IDENTITIES` env var parser; feeds the
verifier's TrustPolicy.

---

## Tasks completed

- [x] **Library resolution via context7 MCP.** Confirmed (a) cosign Go
  is at **v3** not v2 (the brief was outdated), and (b) the pure-Go
  bundle verifier is **`sigstore-go` v1.1.4** — explicitly designed for
  "library integrators" needing a smaller dependency tree than
  cosign/v3. Switched the implementation to sigstore-go. The cosign Go
  module is *not* added to go.mod (it would have ballooned the dep
  tree with OCI registry / k8s / TUF transitive deps that we don't
  need for catalog signing).
- [x] **`internal/catalog/signing/canonical.go`** — package doc + thin
  delegating `CanonicalEntryBytes` accessor. The actual canonical YAML
  marshal lives on `catalog.CatalogEntry.canonicalBytes` (and its
  exported pair `CanonicalBytes`) so the loader can call it without
  importing signing — single source of truth for "which fields are in
  the signed payload."
- [x] **`internal/catalog/signing/verify.go`** — `Verifier` type
  implementing `sources.SidecarVerifier` (whole-file path) +
  `VerifyEntry` method (per-entry path) + `VerifyEntryFunc` closure
  adapter that conforms to `catalog.VerifyEntryFunc` (closes over the
  trust policy so the loader doesn't have to know about it). All
  paths funnel through a single `verifyEntity` core that operates on
  any `verify.SignedEntity` — production passes a `*bundle.Bundle`
  (after `UnmarshalJSON`); tests pass `*ca.TestEntity` directly.
- [x] **Verifier core semantics:** fail-closed empty-policy
  short-circuit BEFORE any verification work; transparency-log
  inclusion required (`WithTransparencyLog(1)`); observer timestamps
  accepted from Rekor SET (`WithObserverTimestamps(1)`); identity
  matching is OUR regex post-verify (using `WithoutIdentitiesUnsafe`
  on the sigstore policy and applying `TrustPolicy.Identities` regex
  ourselves so semantics stay under our control); bundle-fetch via
  HTTP with 1 MiB body cap + 30 s default timeout; never log raw URL
  (10-char SHA-256 fingerprint via local `urlFingerprint`).
- [x] **`internal/catalog/loader.go`:**
  - New `Verified bool` + `SignatureIdentity string` fields on
    `CatalogEntry`, both `yaml:"-"` (forgery-resistant — same model as
    `Source` from V123-1.4). JSON-emitted (`json:"verified"` always,
    `json:"signature_identity,omitempty"`) so V123-2.4 UI can render.
  - New `VerifyEntryFunc` callback type — 3 args (ctx, canonical
    bytes, bundle URL) so the loader doesn't need to know about
    `sources.TrustPolicy` (would create a cycle: loader → sources →
    loader). The trust policy is closed over at construction time by
    `signing.Verifier.VerifyEntryFunc(tp)`.
  - New `LoadBytesWithVerifier(ctx, data, verifyFn) (*Catalog, error)`
    public API. Existing `Load()` and `LoadBytes()` unchanged. Per-
    entry verification only fires when `verifyFn != nil` AND the entry
    has a non-nil `Signature.Bundle`. Sig-mismatch / untrusted /
    fail-closed → `Verified=false`, no error. Infra error → log
    WARN with URL fingerprint, leave `Verified=false`, continue load.
  - New methods `CatalogEntry.canonicalBytes()` (private) +
    `CatalogEntry.CanonicalBytes()` (exported) — the deterministic
    YAML render minus Signature + computed-only fields. Single source
    of truth for the per-entry signed payload shape.
- [x] **`cmd/sharko/serve.go`** — wired `signing.NewVerifier(nil)` at
  startup, default empty `sources.TrustPolicy{}` (V123-2.3 will
  populate). Embedded catalog now loads via `LoadBytesWithVerifier`
  with the verifier's `VerifyEntryFunc(trustPolicy)`. Third-party
  fetcher gets the same verifier (whole-file `.bundle` sidecar path
  lights up automatically). Comment block explains the staging:
  empty-policy fail-closed today; V123-2.3 lands real identities;
  V123-2.5 lands embedded-catalog signing in CI.
- [x] **`internal/catalog/signing/verify_test.go`** — 13 tests (12
  cases from the brief + 1 bonus closure test):
  1. `TestVerify_HappyPath` — valid bundle + trusted identity →
     (true, subject, nil). Verifies issuer extraction.
  2. `TestVerify_SignatureMismatch` — payload tampered →
     (false, "", nil). Asserts NOT an error.
  3. `TestVerify_UntrustedIdentity` — signer SAN doesn't match policy
     regex → (false, "", nil). NOT an error.
  4. `TestVerify_EmptyTrustPolicy` — `Identities: nil` →
     (false, "", nil) without ever resolving the trust root. NOT an
     error. Fail-closed proven.
  5. `TestVerify_MalformedBundle` — non-bundle bytes →
     (false, "", err). Infra-error branch.
  6. `TestVerify_HTTPFetchFails` — 404 from sidecar URL →
     (false, "", err). Infra-error branch.
  7. `TestVerify_ContextCancelled` — pre-cancelled ctx →
     (false, "", err) with ctx-shaped error.
  8. `TestVerifyEntry_HappyPath` — per-entry verifier core run on a
     real `*ca.TestEntity` minted by VirtualSigstore.
  9. `TestVerifyEntry_PayloadMismatch` — same entity, different
     canonical bytes → (false, "", nil).
  10. `TestCanonicalEntryBytes_StripsSignature` — output does NOT
      contain `signature:`.
  11. `TestCanonicalEntryBytes_StripsRuntimeFields` — output does NOT
      contain `verified:`, `signature_identity:`, `source:`,
      `security_tier:`. The full forgery-resistance set.
  12. `TestCanonicalEntryBytes_Deterministic` — 5 calls produce
      byte-identical output. Failing this test = a future field
      reorder broke every existing per-entry signature.
  13. `TestVerifyEntryFunc_ClosesOverPolicy` (bonus) — proves the
      closure adapter compiles against `catalog.VerifyEntryFunc`
      and forwards correctly.
- [x] **`internal/catalog/loader_test.go`** — 4 new
  `LoadBytesWithVerifier` tests (one more than the brief asked):
  1. `TestLoadBytesWithVerifier_PerEntryHappyPath` — stub returns
     (true, "ci@example.com", nil) → entry surfaces `Verified=true`,
     `SignatureIdentity="ci@example.com"`, verifier called once
     with the bundle URL.
  2. `TestLoadBytesWithVerifier_PerEntryUnsigned` — entry without
     `signature:` block + verifier wired in → `Verified=false`,
     verifier NEVER called (proves the unsigned-but-accepted path).
  3. `TestLoadBytesWithVerifier_PerEntryMismatch` — stub returns
     (false, "", nil) → entry STILL LOADS, just with `Verified=false`
     and `Signature` pointer retained on the entry (downstream code
     can detect "was-attempted-signed").
  4. `TestLoadBytesWithVerifier_NilFn` (bonus) — passing
     `verifyFn=nil` for a signed entry leaves `Verified=false` and
     does not panic. Backward-compatibility for callers that haven't
     wired a verifier yet.

  These tests use a tiny `stubVerifier` helper LOCAL to loader_test.go
  — the loader package deliberately does NOT import signing (which
  would couple the loader test suite to the cosign dep tree and break
  the import-direction invariant). The signing package's own test
  suite (above) exercises real Sigstore verification via
  VirtualSigstore.
- [x] **`docs/design/2026-04-20-v1.23-catalog-extensibility.md`** —
  §7.2 marked **Resolved: verify on fetch only** with the full
  rationale (verify once per fetch cycle, cache outcome on snapshot +
  entry, no re-verification on API requests; trigger only when YAML
  bytes change or process restart; Rekor saturation is the reason).
- [x] **Swagger regen** — `swag init -g cmd/sharko/serve.go -o
  docs/swagger --parseDependency --parseInternal` regenerated all 3
  artefacts (docs.go, swagger.json, swagger.yaml). New `verified` and
  `signature_identity` fields surface on every endpoint that returns
  a `CatalogEntry` (verified by grep).
- [x] **`go mod tidy`** committed. sigstore-go v1.1.4 added; existing
  swag/openapi deps minor-bumped because sigstore-go pulls newer
  swag transitively. No cosign/v3 in the dep graph (we use sigstore-go,
  not cosign/v3).
- [x] **BMAD tracking:**
  - `sprint-status.yaml`: `last_updated` comment refreshed to mark
    Epic V123-2 as 1 done + 1 in review;
    `V123-2-2-…: backlog → review`.
  - Story frontmatter: `status: review`, `dispatched: 2026-04-26`.

## Files touched

- `internal/catalog/signing/canonical.go` — NEW, ~62 LOC. Package doc
  + thin `CanonicalEntryBytes` delegate.
- `internal/catalog/signing/verify.go` — NEW, ~370 LOC. `Verifier`
  type + `Verify` (whole-file) + `VerifyEntry` (per-entry) +
  `VerifyEntryFunc` closure adapter + `verifyEntity` core +
  `verifyBundleBytes` parse wrapper + `fetchBundle` HTTP wrapper +
  `compileIdentityPatterns` + `matchAnyPattern` + `extractSubject`
  + `urlFingerprint`. All the verification logic in one focused
  package, no helpers leaking out.
- `internal/catalog/signing/verify_test.go` — NEW, ~340 LOC, 13 tests.
- `internal/catalog/loader.go` — `+~155 LOC`. Two new `yaml:"-"`
  fields on `CatalogEntry` (`Verified`, `SignatureIdentity`); new
  `VerifyEntryFunc` type; new `LoadBytesWithVerifier` function; new
  `canonicalBytes` (private) + `CanonicalBytes` (public) methods on
  `CatalogEntry`. Existing `Load` / `LoadBytes` / `validateEntry`
  unchanged.
- `internal/catalog/loader_test.go` — `+~165 LOC`, 4 new tests under
  banner `--- V123-2.2 LoadBytesWithVerifier cases ---`.
- `cmd/sharko/serve.go` — `+~30 LOC` net. New `signing` +
  `catalogembed` imports; verifier construction at startup; embedded
  catalog now loads via `LoadBytesWithVerifier`; fetcher receives the
  verifier instead of `nil`.
- `docs/design/2026-04-20-v1.23-catalog-extensibility.md` — `+1 LOC,
  -1 LOC` on the §7.2 question; full resolution rationale.
- `docs/swagger/{docs.go,swagger.json,swagger.yaml}` — regen output;
  new `verified` + `signature_identity` fields appear on every
  CatalogEntry-emitting payload.
- `go.mod` + `go.sum` — sigstore-go v1.1.4 + transitives.
- `.bmad/output/implementation-artifacts/sprint-status.yaml` —
  comment + status flip.
- `.bmad/output/implementation-artifacts/V123-2-2-…md` — frontmatter
  + this retrospective.

## Tests

```bash
# signing package — 13 tests, all green
go test ./internal/catalog/signing/ -race -count=1 -v
# 13/13 PASS in ~5s

# catalog package — 38 existing + 4 new = 42, all green
go test ./internal/catalog/ -race -count=1 -run 'LoadBytesWithVerifier'
# 4/4 PASS

# full catalog tree (catalog + signing + sources) — clean
go test ./internal/catalog/... -race -count=1
# ok internal/catalog          ~3.9s
# ok internal/catalog/signing  ~7.9s
# ok internal/catalog/sources  ~3.2s

# api regression check — clean
go test ./internal/api/... -race -count=1
# ok internal/api  ~6.5s

# build / vet — clean across the whole tree
go build ./...
go vet ./...
```

Pre-existing race in `internal/argosecrets/reconciler_test.go::TestReconcileOnce_Trigger`
exists on `main` (verified via `git stash` + targeted run) and is
unrelated to this story. Not addressed here per the brief's "don't
gold-plate" guidance.

## Decisions

- **sigstore-go v1.1.4, not cosign/v3.** The brief specified cosign
  v2; context7 + the actual Go module cache showed (a) cosign moved
  to v3, (b) sigstore-go is the canonical pure-Go bundle verifier
  designed expressly for library integrators (per the
  sigstore.dev/language_clients/go docs: "It's not intended to
  replace Cosign… cosign will use sigstore-go for verification").
  cosign/v3 would have pulled in OCI registry, k8s, TUF transitives
  we don't need; sigstore-go's only direct deps are protobuf-specs,
  go-tuf, dsse, and a handful of crypto helpers. This is a
  significant deviation from the brief's import paths but a faithful
  execution of the brief's "verify with context7 — don't trust import
  paths verbatim" risk callout.
- **Single canonical-bytes source of truth on `CatalogEntry`.** The
  brief had `signing.CanonicalEntryBytes` as the canonical function;
  I made it a thin delegate to `CatalogEntry.canonicalBytes` because
  the loader needs to compute the same bytes when calling
  `verifyFn` and the loader can't import signing (cycle).
  Single-source-of-truth in the catalog package; signing exposes
  the public name the brief promised.
- **`VerifyEntryFunc` is 3-arg, not 4-arg.** The brief had the trust
  policy as a parameter; I removed it and made callers close over it
  via `signing.Verifier.VerifyEntryFunc(tp)`. Reason: keeping
  TrustPolicy on the signature would force the loader to import
  `internal/catalog/sources`, but `sources` imports `catalog` — cycle.
  Closure-over is the canonical Go fix and is exactly one line in
  serve.go. The signing package's `Verifier.VerifyEntry` method still
  takes the 4-arg shape the brief described, for direct callers.
- **Test fixture strategy: VirtualSigstore (Option B from the brief).**
  sigstore-go ships a test helper `pkg/testing/ca.VirtualSigstore`
  that mints fully-valid Sigstore-shaped signed entities entirely
  in-process (cert chain + sig + Rekor inclusion), with no need for
  pre-generated bundle files that would expire on the cert NotAfter
  boundary. This is materially cleaner than the brief's Option A
  (check-in pre-generated bundles + document regeneration cadence)
  and eliminates the "hidden time bomb" concern Gotcha #10 flagged.
  Zero fixtures checked in; tests are pure code.
- **Tests drive the `verifyEntity` core directly with `*ca.TestEntity`,
  not a JSON-round-tripped Bundle.** sigstore-go's `*ca.TestEntity`
  implements `verify.SignedEntity` directly, so it can be passed to
  the same verification primitive that production code reaches after
  `bundle.UnmarshalJSON`. Round-tripping a TestEntity through bundle
  JSON would require pulling the sign-side serialization helpers (a
  whole separate package) for a test that exercises the same final
  verification path. The HTTP fetch + parse wrappers ARE exercised in
  the failure cases (404, cancelled ctx, malformed bytes), so the
  full production path has end-to-end coverage even though the
  happy-path test uses the core directly.
- **`WithoutIdentitiesUnsafe` + our own regex, not sigstore-go's
  `WithCertificateIdentity`.** sigstore-go's identity matcher takes
  a SAN value/regex + an issuer value/regex pair. Using it would
  force us to expose a 2-field trust policy (issuer + SAN) on the
  Sharko side. The design specced a single-list `Identities` regex
  field that matches against the SAN. We honour the design by
  letting sigstore-go verify the cryptography, then matching the
  extracted SAN against our regex list ourselves. This also lets us
  return the (false, "", nil) sentinel cleanly on untrusted-identity
  vs sig-mismatch — sigstore-go's path would fold both into one
  error.
- **Per-entry test stubs in loader_test.go, not real signing.**
  loader tests use a hand-rolled `stubVerifier` so the catalog
  package can stay independent of cosign/sigstore. Real
  cryptographic verification is owned by the signing package's own
  tests; loader tests prove the LOADER CONTRACT (call verifyFn for
  signed entries; surface its outcome on the entry) which is purely
  about plumbing.
- **No swag annotation changes needed.** The new fields are on a
  struct already wired through swagger via the existing
  CatalogEntry-returning handlers. swag's `--parseInternal` picks
  up the json tags automatically. Verified by grepping
  swagger.json for `"verified":` and `"signature_identity":` after
  regen — both present.
- **Pre-existing argosecrets race left alone.** A pre-existing data
  race in `internal/argosecrets/reconciler_test.go::TestReconcileOnce_Trigger`
  triggers under `-race` (verified on main via `git stash`). Per the
  brief's "don't gold-plate" stance and per the
  `feedback_realistic_framing` user memory, that's out of scope for
  V123-2.2. Targeted gates from the brief (catalog/... and api/...)
  are clean.

## Gotchas / constraints addressed

1. **Cosign Go API moves.** Resolved via context7 — switched to
   sigstore-go v1.1.4 instead of cosign/v3.
2. **No cosign CLI shell-out.** Confirmed: zero `os/exec`, zero
   `exec.Command`, only Go API calls. NFR-V123-6 satisfied.
3. **Pure Go module.** sigstore-go is a clean dep — added 36 indirect
   transitives (mostly protobuf-specs + dsse + tuf), no kubernetes /
   OCI bloat.
4. **Trust policy regex compilation per-call.** Compiled once per
   verifyEntity invocation (not cached on the Verifier) — the
   policy is small (a few regexes) and policy mutation will be
   handled by V123-2.3's env reload. Caching with invalidation is
   future scope.
5. **Cert chain validation handled by sigstore-go.** We do NOT roll
   our own. Trust root comes from `WithTrustedMaterial` (or
   defaults-to-fail-closed in the unconfigured state).
6. **Rekor inclusion required.** `WithTransparencyLog(1)` is set —
   no opt-out path in the verifier construction.
7. **Time-of-check via observer timestamps.** sigstore-go's
   `WithObserverTimestamps(1)` reads the bundle's own integrated
   Rekor timestamp; cert NotBefore/NotAfter are checked against THAT
   time, not wall-clock-now. Short-lived Fulcio certs verify cleanly
   even when they've expired post-signing.
8. **Fail-closed defaults.** Empty Identities short-circuits the
   verifier BEFORE resolving trust root — the verifier doesn't even
   try to verify when it has no identity to check against. Test 4
   (TestVerify_EmptyTrustPolicy) proves this.
9. **Audit trail on failed verifications.** WARN log via slog with
   `source_fp` (10-char SHA-256 prefix), never raw URL. Covers:
   bundle parse failure, cert chain failure, Rekor failure (folded
   into "bundle verification failed"), untrusted identity, empty
   policy.
10. **Test fixture lifecycle.** Eliminated — VirtualSigstore mints
    fresh certs every test run. No checked-in bundles to maintain.
    The brief's option (a) / (b) / (c) discussion is moot.

## Quality-gate summary

| Gate | Command | Result |
|---|---|---|
| Build | `go build ./...` | clean |
| Vet | `go vet ./...` | clean |
| go mod tidy | `go mod tidy` | clean |
| Catalog tests | `go test ./internal/catalog/... -race -count=1` | PASS (catalog + signing + sources all green) |
| API tests | `go test ./internal/api/... -race -count=1` | PASS (no regressions) |
| Signing tests | `go test ./internal/catalog/signing/ -race -count=1 -v` | 13/13 PASS |
| Loader new tests | `go test ./internal/catalog/ -race -count=1 -run LoadBytesWithVerifier -v` | 4/4 PASS |
| Lint | `golangci-lint run ./internal/catalog/signing/...` | **skipped** — binary not installed locally; CI runs it on the PR |
| Swagger | `swag init -g cmd/sharko/serve.go -o docs/swagger --parseDependency --parseInternal` | regenerated; `verified` + `signature_identity` present in swagger.json |

## Deviations from the brief

- **sigstore-go v1.1.4 instead of cosign/v2 (or /v3).** Required
  deviation — cosign Go is at v3 not v2, and sigstore-go is the
  canonical thin client. The brief's risk callout explicitly
  permitted this — "Don't trust import paths from the brief verbatim
  — verify them via context7." The chosen library satisfies all the
  same security requirements (Fulcio cert chain validation via
  TrustedMaterial, Rekor transparency log via WithTransparencyLog,
  pure-Go-no-shell-out per NFR-V123-6).
- **`VerifyEntryFunc` callback type is 3-arg, not 4-arg.** Trust
  policy moved into the closure via
  `Verifier.VerifyEntryFunc(tp) catalog.VerifyEntryFunc`. Required
  to break the import cycle loader → sources → loader. The behaviour
  is equivalent to what the brief described.
- **Single canonical-bytes implementation on `CatalogEntry`** (with
  `signing.CanonicalEntryBytes` as a one-line delegate) instead of
  duplicate logic in both packages. Strictly cleaner than the
  brief's two-implementation outline; no behavioural change.
- **VirtualSigstore test fixtures (Option B)** instead of pre-
  generated bundle files (Option A). The brief recommended Option A
  for "initial landing — simpler, no test-only sigstore deps", but
  sigstore-go ships VirtualSigstore as part of the same module we
  already pulled in for verification, so there's no extra dep cost.
  Eliminates the fixture-expiry maintenance burden Gotcha #10
  flagged.

