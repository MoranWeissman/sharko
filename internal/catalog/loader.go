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
	"fmt"
	"io"
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

// CatalogEntry is the Sharko-native curated catalog shape. Fields match the
// YAML keys in catalog/addons.yaml and the JSON Schema in catalog/schema.json.
//
// Unknown YAML fields are tolerated by the loader (forward-compatibility); they
// are silently dropped during unmarshal because this struct does not have a
// catch-all map. That matches the design decision "Unknown fields are
// tolerated" — we do not fail startup on unknown keys.
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

	// SecurityTier is derived from SecurityScore by the API layer (Strong /
	// Moderate / Weak / unknown) and is never read from YAML.
	SecurityTier string `yaml:"-" json:"security_tier,omitempty"`
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
