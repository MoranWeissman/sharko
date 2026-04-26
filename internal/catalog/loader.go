// Package catalog provides the loader, search index, and OpenSSF Scorecard
// refresh for the curated addon catalog. The embedded YAML + schema live in
// the top-level `catalog` package; this package consumes them.
//
// The loader is strict about required fields and about enum membership, and
// tolerant of unknown fields so older binaries can parse newer catalog
// versions (per §4.2 of the v1.21 design doc).
package catalog

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	catalogembed "github.com/MoranWeissman/sharko/catalog"
)

// allowedCategories mirrors catalog/schema.json / design §4.2.1. Mutating the
// list is a schema PR, not a silent addition.
var allowedCategories = map[string]struct{}{
	"security":         {},
	"observability":    {},
	"networking":       {},
	"autoscaling":      {},
	"gitops":           {},
	"storage":          {},
	"database":         {},
	"backup":           {},
	"chaos":            {},
	"developer-tools":  {},
}

// allowedCuratedBy mirrors catalog/schema.json / design §4.2.
var allowedCuratedBy = map[string]struct{}{
	"cncf-graduated":       {},
	"cncf-incubating":      {},
	"cncf-sandbox":         {},
	"aws-eks-blueprints":   {},
	"azure-aks-addon":      {},
	"gke-marketplace":      {},
	"artifacthub-verified": {},
	"artifacthub-official": {},
}

// SourceEmbedded is the sentinel value for CatalogEntry.Source on entries that
// were loaded from the binary-shipped (embedded) catalog. Third-party entries
// carry their full catalog URL instead. Kept as a typed constant so callers
// can compare/emit without magic-string drift (matches the OriginEmbedded
// constant in internal/catalog/sources/merger.go — which is re-used from the
// merger package to keep a single source of truth for the string value;
// this const mirrors it for ergonomics within the catalog package).
const SourceEmbedded = "embedded"

// Signature is the optional per-entry cosign-keyless attestation pointer
// (schema v1.1+; V123-2.1). When present, V123-2.2's load-time verifier
// checks the bundle against the configured trust policy.
//
// `bundle` is a URL to a Sigstore bundle file (cert + sig + Rekor entry).
// Convention matches the V123-1.2 fetcher's `.bundle` sidecar probe but is
// per-entry, not per-catalog-file.
type Signature struct {
	Bundle string `yaml:"bundle" json:"bundle"`
}

// CatalogEntry is the Sharko-native curated catalog shape. Fields match the
// YAML keys in catalog/addons.yaml and the JSON Schema in catalog/schema.json.
//
// Unknown YAML fields are tolerated by the loader (forward-compatibility); they
// are silently dropped during unmarshal because this struct does not have a
// catch-all map. That matches the design decision "Unknown fields are
// tolerated" — we do not fail startup on unknown keys.
//
// Schema v1.1 (V123-2.1) added the optional `signature` block — a per-entry
// cosign-keyless attestation pointer. Older catalogs without it deserialize
// cleanly (the pointer stays nil); older Sharko binaries that don't yet know
// the field tolerate it as unknown per design §4.2.
//
// V123-2.2 introduced two computed-at-load fields — `Verified` and
// `SignatureIdentity` — that surface the cosign verification outcome on
// the API. Both are tagged `yaml:"-"` for the same forgery-resistance
// reason as `Source` (V123-1.4): without the dash, a hostile third-party
// YAML could set `verified: true` and masquerade as cosign-attested
// without ever producing a valid signature.
type CatalogEntry struct {
	Name                 string        `yaml:"name" json:"name"`
	Description          string        `yaml:"description" json:"description"`
	Chart                string        `yaml:"chart" json:"chart"`
	Repo                 string        `yaml:"repo" json:"repo"`
	DefaultNamespace     string        `yaml:"default_namespace" json:"default_namespace"`
	DefaultSyncWave      int           `yaml:"default_sync_wave" json:"default_sync_wave"`
	DocsURL              string        `yaml:"docs_url,omitempty" json:"docs_url,omitempty"`
	Homepage             string        `yaml:"homepage,omitempty" json:"homepage,omitempty"`
	SourceURL            string        `yaml:"source_url,omitempty" json:"source_url,omitempty"`
	Maintainers          []string      `yaml:"maintainers" json:"maintainers"`
	License              string        `yaml:"license" json:"license"`
	Category             string        `yaml:"category" json:"category"`
	CuratedBy            []string      `yaml:"curated_by" json:"curated_by"`
	SecurityScore        ScoreValue    `yaml:"security_score,omitempty" json:"security_score,omitempty"`
	SecurityScoreUpdated string        `yaml:"security_score_updated,omitempty" json:"security_score_updated,omitempty"`
	GitHubStars          int           `yaml:"github_stars,omitempty" json:"github_stars,omitempty"`
	MinKubernetesVersion string        `yaml:"min_kubernetes_version,omitempty" json:"min_kubernetes_version,omitempty"`
	Deprecated           bool          `yaml:"deprecated,omitempty" json:"deprecated,omitempty"`
	SupersededBy         string        `yaml:"superseded_by,omitempty" json:"superseded_by,omitempty"`

	// Signature is the optional cosign-keyless attestation pointer
	// (schema v1.1+; V123-2.1). nil when the entry is unsigned.
	Signature *Signature `yaml:"signature,omitempty" json:"signature,omitempty"`

	// SecurityTier is derived from SecurityScore by the API layer (Strong /
	// Moderate / Weak / unknown) and is never read from YAML.
	SecurityTier string `yaml:"-" json:"security_tier,omitempty"`

	// Source is the origin of the entry — "embedded" for the binary-shipped
	// catalog, or the full third-party catalog URL (from SHARKO_CATALOG_URLS).
	// Computed at load/merge time — NOT persisted in YAML. The `yaml:"-"` tag
	// is mandatory: without it, a malicious third-party YAML could set
	// `source: embedded` and masquerade as curated. Stateless per NFR §2.7 —
	// never written to disk.
	Source string `yaml:"-" json:"source,omitempty"`

	// Verified is the post-load cosign-verification outcome (V123-2.2).
	// True only when the entry had a valid `signature.bundle` URL whose
	// fetched Sigstore bundle verified against the configured trust
	// policy AND whose OIDC subject matched a TrustPolicy.Identities
	// regex. False for unsigned entries, fail-closed defaults, sig
	// mismatches, untrusted identities, and infrastructure failures
	// fetching the bundle. Computed at load via LoadBytesWithVerifier;
	// never persisted to YAML (`yaml:"-"` matches the Source pattern).
	Verified bool `yaml:"-" json:"verified"`

	// SignatureIdentity is the OIDC subject (cert SAN) of the verified
	// signer when Verified is true. Empty otherwise. Powers the UI's
	// "Verified by <issuer>" pill (V123-2.4). Same forgery-resistant
	// `yaml:"-"` posture as Source/Verified.
	SignatureIdentity string `yaml:"-" json:"signature_identity,omitempty"`
}

// ScoreValue is a small wrapper around "either a 0-10 float or the literal
// string 'unknown'". It round-trips through YAML + JSON keeping the raw text
// when the score is unknown, and a numeric value otherwise.
type ScoreValue struct {
	Known bool
	Value float64 // 0 when Known is false
}

// UnmarshalYAML accepts either a number or the string "unknown".
func (s *ScoreValue) UnmarshalYAML(n *yaml.Node) error {
	if n == nil {
		return nil
	}
	raw := strings.TrimSpace(n.Value)
	if raw == "" || strings.EqualFold(raw, "unknown") {
		s.Known = false
		s.Value = 0
		return nil
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return fmt.Errorf("security_score must be a number 0-10 or \"unknown\" (got %q)", raw)
	}
	if v < 0 || v > 10 {
		return fmt.Errorf("security_score must be in [0,10] (got %v)", v)
	}
	s.Known = true
	s.Value = v
	return nil
}

// MarshalYAML emits either the numeric value or the literal "unknown".
func (s ScoreValue) MarshalYAML() (interface{}, error) {
	if !s.Known {
		return "unknown", nil
	}
	return s.Value, nil
}

// MarshalJSON emits either the numeric value or the literal "unknown" so the
// REST API keeps the same affordance the YAML uses.
func (s ScoreValue) MarshalJSON() ([]byte, error) {
	if !s.Known {
		return []byte(`"unknown"`), nil
	}
	// json-friendly number without trailing zeros when possible
	return []byte(strconv.FormatFloat(s.Value, 'f', -1, 64)), nil
}

// UnmarshalJSON accepts either a number or the literal string "unknown" so
// API consumers (and tests that decode our own responses) can round-trip.
func (s *ScoreValue) UnmarshalJSON(data []byte) error {
	raw := strings.TrimSpace(string(data))
	if raw == "" || raw == "null" {
		s.Known = false
		s.Value = 0
		return nil
	}
	if strings.HasPrefix(raw, `"`) {
		// Quoted string — only "unknown" is meaningful here.
		unquoted := strings.Trim(raw, `"`)
		if strings.EqualFold(unquoted, "unknown") {
			s.Known = false
			s.Value = 0
			return nil
		}
		// Try parsing the unquoted value as a number to be tolerant.
		v, err := strconv.ParseFloat(unquoted, 64)
		if err != nil {
			return fmt.Errorf("security_score must be a number 0-10 or \"unknown\" (got %q)", unquoted)
		}
		if v < 0 || v > 10 {
			return fmt.Errorf("security_score must be in [0,10] (got %v)", v)
		}
		s.Known = true
		s.Value = v
		return nil
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return fmt.Errorf("security_score: %w", err)
	}
	if v < 0 || v > 10 {
		return fmt.Errorf("security_score must be in [0,10] (got %v)", v)
	}
	s.Known = true
	s.Value = v
	return nil
}

// Tier returns the coarse tier label the UI displays next to the numeric
// score. Empty when the score is unknown — callers render a grey pill.
func (s ScoreValue) Tier() string {
	if !s.Known {
		return ""
	}
	switch {
	case s.Value >= 8.0:
		return "Strong"
	case s.Value >= 5.0:
		return "Moderate"
	default:
		return "Weak"
	}
}

// yamlRoot is the top-level shape of catalog/addons.yaml.
type yamlRoot struct {
	Addons []CatalogEntry `yaml:"addons"`
}

// Catalog is the parsed, validated, in-memory catalog. It is immutable after
// construction except for per-entry score refreshes done by the Scorecard job.
type Catalog struct {
	entries []CatalogEntry
	byName  map[string]int // name -> index in entries
}

// Load parses + validates + indexes the embedded catalog. Returns a descriptive
// error (naming the offending entry) on any structural problem so boot-time
// logs are actionable.
func Load() (*Catalog, error) {
	return LoadBytes(catalogembed.AddonsYAML())
}

// VerifyEntryFunc is the per-entry verification callback shape the
// loader invokes when an entry has a non-nil Signature.Bundle URL and a
// caller has opted into the verification path via LoadBytesWithVerifier.
//
// The function MUST honor the SidecarVerifier return contract verbatim
// (see internal/catalog/sources/verifier.go):
//
//   - (true, "<subject>", nil) — signature verifies, identity trusted.
//   - (false, "", nil)         — sig mismatch, untrusted identity, or
//     fail-closed empty trust policy. NOT an error.
//   - (false, "", err)         — infrastructure failure (network fetch,
//     malformed bundle bytes, bad cert chain).
//
// canonicalEntryBytes is the deterministic YAML serialization of the
// entry MINUS its Signature + computed-only fields (the message a
// per-entry signature attests to). The loader computes the canonical
// bytes inline via CatalogEntry.canonicalBytes — keeping that knowledge
// in this package preserves the import-direction invariant
// (signing → catalog → sources, never reversed; signing → catalog
// stays clean by exposing a public CanonicalBytes accessor).
//
// Note: the trust policy is NOT a parameter on this callback. The
// loader has no business knowing the trust policy structure — it lives
// in the sources package, which the loader cannot import (would create
// a cycle). Callers in cmd/sharko/serve.go close over the trust policy
// when they construct the VerifyEntryFunc, baking it into the verifier.
// signing.Verifier.VerifyEntryWithPolicy returns a closure of this
// shape.
type VerifyEntryFunc func(
	ctx context.Context,
	canonicalEntryBytes []byte,
	bundleURL string,
) (verified bool, issuer string, err error)

// LoadBytes parses the given YAML payload. Exposed for tests; production code
// uses Load() which reads the embedded bytes.
func LoadBytes(data []byte) (*Catalog, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("catalog: empty payload")
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var root yamlRoot
	if err := dec.Decode(&root); err != nil && err != io.EOF {
		return nil, fmt.Errorf("catalog: parse yaml: %w", err)
	}
	if len(root.Addons) == 0 {
		return nil, fmt.Errorf("catalog: no entries found under 'addons:'")
	}
	cat := &Catalog{
		entries: make([]CatalogEntry, 0, len(root.Addons)),
		byName:  make(map[string]int, len(root.Addons)),
	}
	for i, e := range root.Addons {
		if err := validateEntry(&e); err != nil {
			return nil, fmt.Errorf("catalog: entry #%d (name=%q): %w", i+1, e.Name, err)
		}
		if existing, dup := cat.byName[e.Name]; dup {
			return nil, fmt.Errorf("catalog: duplicate entry name %q (entries #%d and #%d)",
				e.Name, existing+1, i+1)
		}
		e.SecurityTier = e.SecurityScore.Tier()
		// Every entry produced by the loader originates from the embedded
		// catalog YAML (LoadBytes is also used by the third-party fetcher,
		// but the fetcher overrides Source downstream — see V123-1.4 §4).
		// The `yaml:"-"` tag on Source prevents a hostile third-party feed
		// from pre-seeding this field; we always set it ourselves here.
		e.Source = SourceEmbedded
		cat.entries = append(cat.entries, e)
		cat.byName[e.Name] = len(cat.entries) - 1
	}
	// Deterministic order — list endpoints return entries sorted by name by
	// default so the UI shows a stable layout; filters can re-sort downstream.
	sort.SliceStable(cat.entries, func(i, j int) bool { return cat.entries[i].Name < cat.entries[j].Name })
	// Rebuild byName because we reordered.
	cat.byName = make(map[string]int, len(cat.entries))
	for i, e := range cat.entries {
		cat.byName[e.Name] = i
	}
	return cat, nil
}

// LoadBytesWithVerifier is the verification-aware variant of LoadBytes.
// When verifyFn is non-nil AND an entry has a non-nil Signature.Bundle,
// the entry's Verified + SignatureIdentity fields are populated from
// the verifier's outcome. Failures (sig mismatch, untrusted identity,
// missing trust policy) land as Verified=false — they are NOT load
// errors, because the design accepts unsigned-but-valid entries
// alongside signed-and-verified ones.
//
// Infrastructure errors from the verifier (network fetch, malformed
// bundle bytes) are also tolerated: the entry is loaded with
// Verified=false and a WARN log line is emitted with the URL
// fingerprint. The reasoning: a transient bundle-host outage should
// not blackhole the catalog. Operators can tighten this stance later
// via a "strict mode" knob, but the v1.23 ship is best-effort.
//
// Existing Load() / LoadBytes() callers see no change — they remain
// the no-verification fast path used by tests and the embedded-catalog
// boot path that hasn't been wired through serve.go yet.
//
// The trust policy is closed over by verifyFn at construction time
// (see signing.Verifier.VerifyEntryFunc). An empty trust policy
// triggers the verifier's fail-closed branch — every signed entry
// surfaces Verified=false. This is the canonical default for a v1.23
// install with no SHARKO_CATALOG_TRUSTED_IDENTITIES set yet (V123-2.3
// lands the env var parser).
func LoadBytesWithVerifier(
	ctx context.Context,
	data []byte,
	verifyFn VerifyEntryFunc,
) (*Catalog, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("catalog: empty payload")
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var root yamlRoot
	if err := dec.Decode(&root); err != nil && err != io.EOF {
		return nil, fmt.Errorf("catalog: parse yaml: %w", err)
	}
	if len(root.Addons) == 0 {
		return nil, fmt.Errorf("catalog: no entries found under 'addons:'")
	}
	cat := &Catalog{
		entries: make([]CatalogEntry, 0, len(root.Addons)),
		byName:  make(map[string]int, len(root.Addons)),
	}
	log := slog.Default().With("component", "catalog-loader")
	for i, e := range root.Addons {
		if err := validateEntry(&e); err != nil {
			return nil, fmt.Errorf("catalog: entry #%d (name=%q): %w", i+1, e.Name, err)
		}
		if existing, dup := cat.byName[e.Name]; dup {
			return nil, fmt.Errorf("catalog: duplicate entry name %q (entries #%d and #%d)",
				e.Name, existing+1, i+1)
		}
		e.SecurityTier = e.SecurityScore.Tier()
		e.Source = SourceEmbedded

		// Per-entry verification — only when caller wired a verifier
		// AND the entry actually carries a Signature.Bundle URL.
		// Unsigned entries silently surface Verified=false (the zero
		// value); they are accepted as legitimate "operator hasn't
		// signed this yet" entries.
		if verifyFn != nil && e.Signature != nil && e.Signature.Bundle != "" {
			payload, cerr := e.canonicalBytes()
			if cerr != nil {
				// Defensive — yaml.Marshal of a struct copy shouldn't
				// fail in practice. Treat as infra error: log + leave
				// Verified=false; don't fail the load.
				log.Warn("catalog entry canonical serialization failed",
					"entry", e.Name, "err", cerr.Error())
			} else {
				ok, issuer, verr := verifyFn(ctx, payload, e.Signature.Bundle)
				if verr != nil {
					// Infra error — log with URL fingerprint (not the
					// raw URL — bundle URLs may encode auth tokens).
					log.Warn("catalog entry signature verification errored",
						"entry", e.Name,
						"err", verr.Error())
				}
				e.Verified = ok
				e.SignatureIdentity = issuer
			}
		}

		cat.entries = append(cat.entries, e)
		cat.byName[e.Name] = len(cat.entries) - 1
	}
	// Deterministic order — same as LoadBytes; the merger downstream
	// relies on byte-stable iteration.
	sort.SliceStable(cat.entries, func(i, j int) bool { return cat.entries[i].Name < cat.entries[j].Name })
	cat.byName = make(map[string]int, len(cat.entries))
	for i, e := range cat.entries {
		cat.byName[e.Name] = i
	}
	return cat, nil
}

// canonicalBytes returns the deterministic YAML serialization of this
// entry minus its Signature + computed-only fields. This is the message
// a per-entry cosign signature attests to, and it must be byte-identical
// to whatever the signing tool produced when it signed the entry.
//
// yaml.v3 marshals struct fields in declaration order, so the output is
// deterministic across Sharko binaries as long as CatalogEntry's field
// order is stable. The signing package's CanonicalEntryBytes delegates
// to this method to ensure both paths (loader-side verification + any
// future signer tooling) produce the same bytes from the same struct.
func (e CatalogEntry) canonicalBytes() ([]byte, error) {
	e.Signature = nil
	e.Verified = false
	e.SignatureIdentity = ""
	e.Source = ""
	e.SecurityTier = ""
	return yaml.Marshal(&e)
}

// CanonicalBytes is the public accessor for canonicalBytes — exported
// so the signing package (which can't call the unexported method
// directly across packages) can produce the same canonical form. Same
// semantics: copy + zero out runtime fields + Marshal.
func (e CatalogEntry) CanonicalBytes() ([]byte, error) {
	return e.canonicalBytes()
}

// validateEntry enforces the schema constraints the loader is responsible for
// — presence of required fields, enum membership of category + curated_by,
// numeric bounds on security score (handled by ScoreValue). License allow-list
// enforcement is deliberately not done here — per design §4.9 that is a CI-PR
// concern that can flag for human review without failing startup.
func validateEntry(e *CatalogEntry) error {
	if e == nil {
		return fmt.Errorf("nil entry")
	}
	if strings.TrimSpace(e.Name) == "" {
		return fmt.Errorf("missing required field: name")
	}
	if strings.TrimSpace(e.Description) == "" {
		return fmt.Errorf("missing required field: description")
	}
	if strings.TrimSpace(e.Chart) == "" {
		return fmt.Errorf("missing required field: chart")
	}
	if strings.TrimSpace(e.Repo) == "" {
		return fmt.Errorf("missing required field: repo")
	}
	if !strings.HasPrefix(e.Repo, "http://") &&
		!strings.HasPrefix(e.Repo, "https://") &&
		!strings.HasPrefix(e.Repo, "oci://") {
		// http/https for classic Helm repos; oci:// for Helm 3.8+ OCI
		// registries (e.g. Karpenter ships oci://public.ecr.aws/karpenter).
		return fmt.Errorf("repo must be http(s) or oci URL: %q", e.Repo)
	}
	if strings.TrimSpace(e.DefaultNamespace) == "" {
		return fmt.Errorf("missing required field: default_namespace")
	}
	if strings.TrimSpace(e.License) == "" {
		return fmt.Errorf("missing required field: license")
	}
	if len(e.Maintainers) == 0 {
		return fmt.Errorf("missing required field: maintainers (must be non-empty)")
	}
	if _, ok := allowedCategories[e.Category]; !ok {
		return fmt.Errorf("category %q is not in the allowed set (see catalog/schema.json)", e.Category)
	}
	if len(e.CuratedBy) == 0 {
		return fmt.Errorf("missing required field: curated_by (must be non-empty)")
	}
	seen := make(map[string]struct{}, len(e.CuratedBy))
	for _, c := range e.CuratedBy {
		if _, ok := allowedCuratedBy[c]; !ok {
			return fmt.Errorf("curated_by tag %q is not in the allowed set (see catalog/schema.json)", c)
		}
		if _, dup := seen[c]; dup {
			return fmt.Errorf("curated_by has duplicate tag %q", c)
		}
		seen[c] = struct{}{}
	}
	if e.Signature != nil {
		if strings.TrimSpace(e.Signature.Bundle) == "" {
			return fmt.Errorf("signature.bundle is required when signature is present")
		}
		if !strings.HasPrefix(e.Signature.Bundle, "https://") {
			return fmt.Errorf("signature.bundle must be an https:// URL: %q", e.Signature.Bundle)
		}
		if _, err := url.Parse(e.Signature.Bundle); err != nil {
			return fmt.Errorf("signature.bundle is not a valid URL: %w", err)
		}
	}
	return nil
}

// Get returns the entry matching name (exact, case-sensitive) and a bool
// indicating whether it was found. The returned value is a copy — mutating it
// does not affect the catalog.
func (c *Catalog) Get(name string) (CatalogEntry, bool) {
	if c == nil {
		return CatalogEntry{}, false
	}
	i, ok := c.byName[name]
	if !ok {
		return CatalogEntry{}, false
	}
	return c.entries[i], true
}

// Entries returns a snapshot slice of every catalog entry, sorted by name.
// The returned slice is a copy; callers may modify it freely.
func (c *Catalog) Entries() []CatalogEntry {
	if c == nil {
		return nil
	}
	out := make([]CatalogEntry, len(c.entries))
	copy(out, c.entries)
	return out
}

// Len returns the number of entries. Cheap; used for metrics + tests.
func (c *Catalog) Len() int {
	if c == nil {
		return 0
	}
	return len(c.entries)
}

// UpdateScore lets the Scorecard refresh job (internal/catalog/scorecard.go,
// Story V121-1.5) update the in-memory score for a given entry. No-op and
// returns false when the name is unknown.
func (c *Catalog) UpdateScore(name string, score float64, refreshed string) bool {
	if c == nil {
		return false
	}
	i, ok := c.byName[name]
	if !ok {
		return false
	}
	c.entries[i].SecurityScore = ScoreValue{Known: true, Value: score}
	c.entries[i].SecurityScoreUpdated = refreshed
	c.entries[i].SecurityTier = c.entries[i].SecurityScore.Tier()
	return true
}
