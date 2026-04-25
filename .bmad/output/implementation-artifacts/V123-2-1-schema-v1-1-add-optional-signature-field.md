---
story_key: V123-2-1-schema-v1-1-add-optional-signature-field
epic: V123-2 (Per-entry cosign signing)
status: done
effort: S
dispatched: 2026-04-26
merged: 2026-04-26 (PR #284 → main @ b06eee1)
---

# Story V123-2.1 — Schema v1.1: add optional `signature:` field

## Brief (from epics-v1.23.md §V123-2.1)

As the **catalog subsystem**, I want the schema to accept an optional
`signature:` object on each entry without breaking v1.0 catalogs, so that
verified and unverified entries coexist.

## Acceptance criteria

**Given** `catalog/schema.json` is updated (v1.1)
**Then** each entry accepts optional `signature: {bundle: <URL>}`.

**Given** a catalog missing the `signature` field
**When** loaded
**Then** the entry is accepted (backward-compatible).

**Given** a catalog with `signature: {bundle: "not-a-url"}`
**When** loaded
**Then** the loader rejects the entry with a clear error naming the offending entry.

**Given** a v1.0 schema catalog (no signature anywhere)
**When** loaded by v1.23
**Then** it loads without warning.

**Given** a v1.1 schema catalog (with signature objects)
**When** loaded by v1.21 (older Sharko)
**Then** the `signature` field is tolerated as "unknown field" per v1.21 §4.2; no runtime error.

## Design constraints

### Forward/backward compatibility model

- The Go YAML decoder in `internal/catalog/loader.go` does NOT use a catch-all
  map — unknown YAML keys are silently dropped during unmarshal (current
  comment on `CatalogEntry` confirms this is intentional). v1.21 sees
  `signature:` and ignores it. ✅ backward compat.
- Adding a new optional field to the struct is forward compat — older catalogs
  without `signature:` deserialize cleanly because the field is the zero value
  (nil pointer). ✅ forward compat.

### What "v1.1" means here

The current schema does NOT have an explicit `schema_version` field. The
`v1.1` marker is **documentation-level** — bump the JSON Schema's `description`
and the loader's package-doc comment to note "schema v1.1 adds the optional
`signature` block." Don't introduce a runtime `schema_version` field unless
V123-2.2 needs it (it likely won't — verification is per-entry, not per-file).

### Signature shape

```yaml
signature:
  bundle: https://example.com/cert-manager.bundle
```

- `bundle` → URL to a Sigstore bundle file (cert + sig + Rekor entry).
- This URL convention matches the V123-1.2 fetcher's `.bundle` sidecar probe
  (which is per-catalog-file, not per-entry — V123-2.2 will wire per-entry
  verification through the existing `SidecarVerifier` interface).
- Field is optional → `*Signature` pointer in Go (nil when absent).

### Loader validation

- If `signature` is absent → no validation. Pass.
- If `signature.bundle` is absent or empty when `signature` is present →
  reject (clear error: `entry %q: signature.bundle is required when signature is present`).
- If `signature.bundle` is present but not a valid URL (no scheme, malformed) → reject.
- URL scheme MUST be `https://` (or `oci://` if the design ever extends — for now
  HTTPS only; matches the security posture of V123-1.1 SSRF guard).

## Implementation plan

### 1. `catalog/schema.json`

Update the description / title to mention v1.1.

Add inside `$defs.CatalogEntry.properties`:

```json
"signature": {
  "type": "object",
  "additionalProperties": false,
  "required": ["bundle"],
  "properties": {
    "bundle": {
      "type": "string",
      "format": "uri",
      "pattern": "^https://",
      "description": "URL to a Sigstore bundle (cert + sig + Rekor entry) for this entry. Verified at load by V123-2.2 SidecarVerifier."
    }
  },
  "description": "Optional cosign-keyless signature attestation. Schema v1.1+. Older Sharko (v1.21) tolerates this field as unknown."
}
```

### 2. `internal/catalog/loader.go`

Add the type:

```go
// Signature is the optional per-entry cosign-keyless attestation
// (schema v1.1+, V123-2.1). When present, V123-2.2 verifies the bundle
// before exposing the entry as verified.
type Signature struct {
    Bundle string `yaml:"bundle" json:"bundle"`
}
```

Add field to `CatalogEntry` (group with other optional metadata):

```go
// Signature is the optional cosign-keyless attestation pointer
// (schema v1.1+; V123-2.1). nil when the entry is unsigned.
Signature *Signature `yaml:"signature,omitempty" json:"signature,omitempty"`
```

Extend `validateEntry` with one new branch:

```go
if e.Signature != nil {
    if strings.TrimSpace(e.Signature.Bundle) == "" {
        return fmt.Errorf("signature.bundle is required when signature is present")
    }
    if !strings.HasPrefix(e.Signature.Bundle, "https://") {
        return fmt.Errorf("signature.bundle must be an https:// URL: %q", e.Signature.Bundle)
    }
    // Use net/url to validate it's a parseable URL.
    if _, err := url.Parse(e.Signature.Bundle); err != nil {
        return fmt.Errorf("signature.bundle is not a valid URL: %w", err)
    }
}
```

Update the `CatalogEntry` package doc comment to note schema v1.1 and the
`signature` field.

### 3. UI model mirror — `ui/src/services/models.ts`

Add to `CatalogEntry` (after `signature` should rarely surface in UI today,
but mirror the shape so future V123-2.4 doesn't have to scramble):

```ts
export interface CatalogEntrySignature {
  bundle: string
}

export interface CatalogEntry {
  // ... existing fields ...
  /**
   * Optional cosign-keyless attestation (schema v1.1+; V123-2.1).
   * Present only when the entry was signed.
   */
  signature?: CatalogEntrySignature
}
```

This is a tiny additive change — backward compat for any consumer that doesn't
read the field. UI rendering of the verified pill is V123-2.4.

## Test plan

### Unit — loader (`internal/catalog/loader_test.go`)

Extend with new test functions (group near existing `validateEntry`-related tests):

1. `TestLoadBytes_AcceptsSignatureField` — YAML with `signature: {bundle: "https://example.com/x.bundle"}` loads cleanly; entry's `Signature.Bundle` matches.
2. `TestLoadBytes_AcceptsAbsentSignature` — YAML without signature loads cleanly; `Signature` is nil.
3. `TestValidateEntry_RejectsSignatureWithoutBundle` — YAML with `signature: {}` rejected with error message naming `signature.bundle`.
4. `TestValidateEntry_RejectsSignatureWithMalformedURL` — `signature: {bundle: "not-a-url"}` rejected with clear error.
5. `TestValidateEntry_RejectsSignatureWithHTTPScheme` — `signature: {bundle: "http://insecure.example.com/x.bundle"}` rejected (HTTPS-only enforcement).
6. `TestLoadBytes_BackwardCompat_v1_0_Catalog` — load the existing pre-v1.1 catalog YAML (no signature anywhere) — loads cleanly.

### Schema validation (catalog-validate CI workflow)

If `scripts/validate-catalog.mjs` (or equivalent) exists, the schema bump must
not regress its output. Run it locally if available; otherwise CI handles it.

### UI — `ui/src/services/models.ts`

Type-only change. Verify `npm run build` passes (`tsc` strict mode).

## Quality gates

- `go build ./...`
- `go vet ./...`
- `go test ./internal/catalog/... -race -count=1`
- `cd ui && npm run build` (TypeScript change is type-only).
- `cd ui && npm test -- --run` (no UI behavior change; tests should stay green).
- `golangci-lint run ./internal/catalog/...` (silent skip if missing).
- No swagger regen (no API surface change — the `signature` field surfaces via
  existing `GET /catalog/addons` JSON automatically).

## Explicit non-goals

- Actual cosign verification logic — V123-2.2.
- Trust policy env var — V123-2.3.
- UI verified badge — V123-2.4.
- Embedded catalog signing in release pipeline — V123-2.5.
- Network fetch of the bundle URL at load — V123-2.2 + V123-1.2 fetcher.

## Dependencies

- None (this is the first story of Epic V123-2).

## Gotchas

1. **Pointer field, not value.** `*Signature` (not `Signature`) so nil
   distinguishes "no signature" from "empty signature object".
2. **HTTPS-only scheme check.** Even though `bundle` URL fetch happens later
   (V123-2.2), reject http:// at schema validation so a malicious or careless
   PR can't ship a downgraded scheme.
3. **`additionalProperties: false`** on the signature object in the JSON
   schema — only `bundle` is allowed for now. V123-2.2 may add an optional
   `cert_identity` override; that's a future schema bump.
4. **Embedded catalog stays unsigned for now.** `catalog/addons.yaml` is NOT
   modified in this story — adding signatures to embedded entries is V123-2.5
   (release pipeline signs them). This story just opens the schema door.
5. **Don't break the existing catalog-validate CI workflow.** If it exists at
   `.github/workflows/catalog-validate.yml`, confirm the new optional field
   doesn't trip it.
6. **UI tsc strict mode.** Adding optional fields is safe; just don't make
   `signature` required.

## Role files (MUST embed in dispatch)

- `.claude/team/architect.md` — schema versioning + compat reasoning.
- `.claude/team/go-expert.md` — struct field + validation + tests.

## PR plan

- Branch: `dev/v1.23-signature-schema` off main.
- Commits:
  1. `feat(catalog): schema v1.1 — add optional signature field (V123-2.1)`
  2. `feat(ui): mirror CatalogEntrySignature type (V123-2.1)`
  3. `chore(bmad): mark V123-2.1 for review`
- No tag.

## Next story

V123-2.2 — Load-time verification via cosign library + implements the
`SidecarVerifier` interface (resolves open question §7.2). Reads
`entry.Signature.Bundle` and verifies cert identity against a trust policy.

## Tasks completed

- [x] **Schema (`catalog/schema.json`):**
  - Top-level `description` extended to mark schema as v1.1 (the brief's
    "documentation-level" version marker — no runtime `schema_version`
    field added).
  - New `signature` property on `$defs.CatalogEntry.properties` —
    `additionalProperties: false`, `required: ["bundle"]`, with
    `bundle` typed as `string`/`format: uri`/`pattern: ^https://`.
    Matches the brief verbatim.
  - Placed alongside `deprecated` / `superseded_by` (other optional
    metadata) per the brief's placement guidance.
- [x] **Loader (`internal/catalog/loader.go`):**
  - New `Signature` struct with a single `Bundle string` field tagged
    `yaml:"bundle" json:"bundle"`. Lives above `CatalogEntry` per
    the brief.
  - `CatalogEntry.Signature *Signature` pointer field added near
    `Deprecated` / `SupersededBy` (other optional metadata). Tagged
    `yaml:"signature,omitempty" json:"signature,omitempty"` so absent
    entries serialize with no key and round-trip cleanly.
  - `CatalogEntry` package-doc updated to note the v1.1 schema bump
    and the rationale (forward+backward compat through the existing
    "unknown fields tolerated" path).
  - `validateEntry` extended with one new branch (after the
    `curated_by` loop, before `return nil`): three guards on
    `*Signature` — non-empty bundle, https:// prefix, parseable URL.
    `net/url` added to the import list.
- [x] **Loader tests (`internal/catalog/loader_test.go`):** 6 new
  test functions covering the full AC matrix:
  1. `TestLoadBytes_AcceptsSignatureField` — happy path; asserts
     `Signature != nil` and `Signature.Bundle` round-trips verbatim.
  2. `TestLoadBytes_AcceptsAbsentSignature` — pointer stays nil when
     the YAML omits `signature:`.
  3. `TestValidateEntry_RejectsSignatureWithoutBundle` — empty
     `signature: {}` rejected with error mentioning `signature.bundle`.
  4. `TestValidateEntry_RejectsSignatureWithMalformedURL` — non-https
     `bundle: "not-a-url"` rejected; asserts the error mentions
     `https://` (not "not a valid URL", because `url.Parse` actually
     accepts relative refs — the failure path is the prefix check, as
     the brief flagged).
  5. `TestValidateEntry_RejectsSignatureWithHTTPScheme` —
     `http://insecure.example.com/x.bundle` rejected with the same
     `https://` error message. Confirms HTTPS-only posture matches
     V123-1.1 SSRF stance.
  6. `TestLoadBytes_BackwardCompat_v1_0_Catalog` — multi-entry v1.0
     YAML (no `signature:` anywhere) loads cleanly with all
     `Signature == nil`. Final smoke check that the schema bump is
     additive only.
- [x] **UI types (`ui/src/services/models.ts`):** new
  `CatalogEntrySignature` interface (`bundle: string`) inserted after
  `CatalogSecurityTier`; `signature?: CatalogEntrySignature` field
  added on `CatalogEntry` next to the existing `source?: string`
  (V123-1.4 sibling). Type-only — no rendering code yet (V123-2.4
  lands the badge).
- [x] **Quality gates:** `go build ./...` clean, `go vet ./...`
  clean, `go test ./internal/catalog/... -race -count=1` PASS
  (catalog + sources packages both green), 6 new tests confirmed
  via `-v -run` filter, `npm run build` clean (TypeScript strict),
  `npm test -- --run` 181/181 PASS. `golangci-lint` not installed
  locally → silent skip per brief; CI handles. No swagger regen
  (the new struct field surfaces automatically through existing
  `/catalog/addons` JSON encoders — no `@Router` annotation
  changes needed).
- [x] **BMAD tracking:** sprint-status.yaml — `last_updated`
  comment refreshed to mark Epic V123-1 closed and Epic V123-2
  started, `epic-V123-2: backlog → in-progress`,
  `V123-2-1-…: backlog → review`. Story frontmatter —
  `status: review`, `dispatched: 2026-04-26`. `.bmad/` is
  gitignored, so commits use `git add -f`.

## Files touched

- `catalog/schema.json` — `+15 LOC`, `-2 LOC`. Top-level description
  bump + new `signature` property block on `$defs.CatalogEntry.properties`.
  No structural changes to the schema's outer shape.
- `internal/catalog/loader.go` — `+~30 LOC`. New `Signature` type,
  new pointer field on `CatalogEntry`, expanded package-doc comment,
  `net/url` import, and new validation branch in `validateEntry`.
  No existing fields or functions modified.
- `internal/catalog/loader_test.go` — `+~165 LOC`. Six new test
  functions appended under a `--- V123-2.1 schema v1.1 / signature
  field cases ---` section banner just before the existing
  `TestUpdateScore`. No existing tests reordered or modified.
- `ui/src/services/models.ts` — `+~10 LOC`. New
  `CatalogEntrySignature` interface + new `signature?` optional
  field on `CatalogEntry`. Type-only.
- `.bmad/output/implementation-artifacts/sprint-status.yaml` — epic
  + story status flipped, `last_updated` refreshed.
- `.bmad/output/implementation-artifacts/V123-2-1-schema-v1-1-add-optional-signature-field.md`
  — frontmatter (`status: review`, `dispatched: 2026-04-26`) +
  retrospective sections (this file).

## Tests

Targeted run (catalog package only):

```bash
go test ./internal/catalog/ -run 'TestLoadBytes_AcceptsSignatureField|TestLoadBytes_AcceptsAbsentSignature|TestValidateEntry_RejectsSignatureWithoutBundle|TestValidateEntry_RejectsSignatureWithMalformedURL|TestValidateEntry_RejectsSignatureWithHTTPScheme|TestLoadBytes_BackwardCompat_v1_0_Catalog' -race -count=1 -v
# 6/6 PASS in ~0.00s each (~4.3s total incl. compile)
```

Full package suites (no regressions):

```bash
go test ./internal/catalog/... -race -count=1
# ok  github.com/MoranWeissman/sharko/internal/catalog          ~3.8s
# ok  github.com/MoranWeissman/sharko/internal/catalog/sources  ~6.1s
```

UI tests:

```bash
cd ui && npm test -- --run
# Test Files  32 passed (32)
# Tests      181 passed (181)
```

## Decisions

- **Pointer-optional, not value-optional.** `Signature *Signature` (not
  `Signature Signature` with an `IsZero()` check). Nil cleanly distinguishes
  "no signature" from "signature object present but empty" — and the latter
  is exactly the case the AC requires we reject. A value-typed field would
  collapse those two states into one.
- **HTTPS-only enforcement at validation time, not at fetch time.** Even
  though the bundle URL isn't actually fetched until V123-2.2, rejecting
  http:// at schema load means a malicious or careless catalog PR can't
  ship a downgraded scheme that survives until runtime. Matches the V123-1.1
  SSRF guard's defense-in-depth posture.
- **`url.Parse` kept in the validation chain even though it's permissive.**
  Brief explicitly called out that `url.Parse("not-a-url")` succeeds (parses
  as a relative reference). The prefix check is the real gate. `url.Parse`
  stays as a belt-and-suspenders catch for control-character or
  null-byte URLs that `strings.HasPrefix` wouldn't reject — small cost,
  symmetric with the existing `repo` field's lack of a parse step (the
  brief's example signature handler is the more rigorous one). The bonus
  control-char test was skipped because `url.Parse` doesn't actually
  reject `\n` in a URL value via yaml.v3's unmarshal — the YAML decoder
  resolves `\n` as a literal newline in the string and `url.Parse` accepts
  it as a path char. Adding a stricter check (e.g., `strings.ContainsAny`)
  would be scope-creep; the V123-2.2 fetcher will reject control chars at
  the HTTP-request stage anyway.
- **No runtime `schema_version` field.** The brief was explicit:
  "v1.1 is documentation-only here." The schema's top-level description
  carries the marker, and the loader's package-doc comment carries the
  Go-side annotation. Adding a runtime field would be a breaking change
  to the YAML shape that older Sharko binaries would silently ignore but
  newer Sharko binaries would have to handle (default to "1.0" when
  missing, etc.) — pure surface area for no benefit until V123-2.2 needs
  per-version dispatch (which the per-entry verification model means it
  won't).
- **No mutation of `catalog/addons.yaml`.** Embedded entries staying
  unsigned is intentional — the release pipeline will sign them in
  V123-2.5. This story opens the schema door; signing the embedded
  catalog is a separate concern with its own CI workflow.
- **Tests appended at the end of `loader_test.go`, not interleaved.**
  V123-1.4's `TestLoadBytes_SourceAlwaysEmbedded_IgnoresYAMLForgery`
  set the precedent of appending V123-feature-specific tests under a
  banner comment rather than weaving them into `TestLoadBytes_ErrorCases`'s
  table-driven block. Keeping that convention so the loader test file
  stays scannable as "smoke + happy + error cases + per-feature blocks."
- **Single section banner `--- V123-2.1 schema v1.1 / signature field
  cases ---`** matches the V123-1.9 retrospective's pattern of banner
  comments before per-story test blocks. Helps the next reader (or
  V123-2.6's test-engineer dispatch) find related cases at a glance.
- **No swagger regen.** The `signature` field surfaces automatically via
  the existing `GET /catalog/addons` handler because `CatalogEntry` is
  already in the swagger output and the new field has standard `json`
  tags. Verified by inspection of the existing handler's response type.
  Brief explicitly waived swagger regen for this reason.

## Gotchas / constraints addressed

1. **Pointer field, not value.** `*Signature` per Gotcha #1 — nil
   distinguishes absent from empty.
2. **HTTPS-only scheme check at validation time** per Gotcha #2 —
   guards against downgrade attacks even though fetch happens later.
3. **`additionalProperties: false`** on the signature object in
   `catalog/schema.json` per Gotcha #3 — only `bundle` is allowed
   for now; future fields (e.g., `cert_identity`) require an explicit
   schema bump.
4. **`catalog/addons.yaml` unmodified** per Gotcha #4 — embedded
   catalog signing is V123-2.5 (release pipeline).
5. **catalog-validate CI workflow** — schema change is purely additive
   (new optional property), so the validator's existing pass on
   `catalog/addons.yaml` continues to hold (the embedded catalog
   doesn't yet use the new field; older entries remain valid against
   the v1.1 schema).
6. **TypeScript strict mode** — new field is optional (`signature?:`)
   so existing consumers that don't read it stay type-safe.

## Quality-gate summary

| Gate | Command | Result |
|---|---|---|
| Build | `go build ./...` | clean |
| Vet | `go vet ./...` | clean |
| Catalog tests | `go test ./internal/catalog/... -race -count=1` | PASS (catalog + sources both green) |
| New tests focus | `go test ./internal/catalog/ -run 'TestLoadBytes_AcceptsSignatureField\|...' -v` | 6/6 PASS |
| UI build | `cd ui && npm run build` | clean (TypeScript strict) |
| UI tests | `cd ui && npm test -- --run` | 181/181 PASS |
| Lint | `golangci-lint run ./internal/catalog/...` | **skipped** — binary not installed locally; CI runs it on the PR |
| Swagger | n/a — new struct field auto-surfaces through existing handler | n/a |

## Deviations from the brief

- **None substantive.** Bonus
  `TestLoadBytes_RejectsSignatureWithControlChars` test was skipped
  per the brief's "low priority — skip if `url.Parse` doesn't actually
  reject it" guidance: yaml.v3 resolves `\n` in a quoted string as a
  literal newline, and `url.Parse` accepts it as a path character —
  so the test would have to assert the *opposite* of what the brief
  expected. Out of scope for this story; revisit in V123-2.2 if
  control-char rejection becomes a bundle-fetcher requirement.
