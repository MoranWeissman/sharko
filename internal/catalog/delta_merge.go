// Merge logic for the v4 catalog delta model (design doc
// docs/design/2026-07-30-v4-data-file-format.md §2.3 and §4.7, v4 wave 1
// Story 3.2 "delta model" / Story 3.3 "internal addons").
//
// The shipped (curated) catalog is this package's Catalog — the same
// entries the v3 Marketplace browse screen already reads, now carrying the
// extended knowledge fields from Story 3.1. A user's fleet-wide overrides
// live in their own git repo at catalog/addons.yaml (kind AddonCatalogDelta,
// internal/config.AddonCatalogDeltaSpec) and hold ONLY their changes — never
// a copy of the curated set. MergeDelta produces the merged, per-addon view
// both the engine chart (Helm's own mergeOverwrite, design doc §4.7) and the
// Sharko server (this Go implementation) need, so the two never drift on
// what "merged" means.
package catalog

import (
	"fmt"
	"sort"

	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/models"
)

// Origin marks where a merged catalog entry's addon definition comes from —
// the "internal origin marker" the v4 wave 1 Story 3.3 UI surfaces next to
// each addon.
type Origin string

const (
	// OriginCurated means the addon is in the shipped catalog. It may or
	// may not carry fleet-wide overrides from the user's delta — Origin
	// alone doesn't say; see MergedAddon.Customized.
	OriginCurated Origin = "curated"
	// OriginInternal means the addon has NO shipped-catalog entry — it
	// exists solely because the user's own catalog/addons.yaml defines it
	// (design doc §2.3's "in-house service", Story 3.3's "private OCI
	// chart reference"). Every knowledge field (Description, DocsURL,
	// RequiredValues, ...) is empty for an internal addon: nothing but the
	// user's own delta entry has ever described it.
	OriginInternal Origin = "internal"
)

// MissingRequiredFieldError is returned by MergeDelta when an addon with no
// shipped-catalog entry (Origin == OriginInternal) is missing one of the
// three fields nothing else can supply — RepoURL, Chart, or Version (design
// doc §2.3, "The note on required"). This is the merge-time enforcement the
// AddonCatalogDeltaEntry doc comment defers to whichever story has the
// shipped catalog in hand — that story is this one.
type MissingRequiredFieldError struct {
	Addon string
	Field string
}

func (e *MissingRequiredFieldError) Error() string {
	return fmt.Sprintf(
		"addon %q is missing required field %q: it has no shipped catalog entry, so repoURL, chart, and version must all be set in your catalog/addons.yaml to be deployable",
		e.Addon, e.Field,
	)
}

// MergedAddon is the per-addon result of overlaying a user's catalog delta
// onto the shipped curated catalog: design doc §4.7's
// `mergeOverwrite(deepCopy(curated), delta)`, as Go. Field by field, the
// delta's value wins when set; list fields (AdditionalSources,
// Settings.SyncOptions, Settings.IgnoreDifferences) replace the curated
// value whole rather than merging element-by-element — same rule as design
// doc §3.3 decision D12, for the same reason: there is no honest way to
// know whether a shorter list was meant to add to or replace a longer one.
type MergedAddon struct {
	Name   string `json:"name"`
	Origin Origin `json:"origin"`
	// Customized is true when the user's delta carries an entry for this
	// addon at all — i.e. it touched at least one field, even if Origin is
	// OriginCurated. False means "shown exactly as the shipped catalog
	// ships it, untouched by your git".
	Customized bool `json:"customized"`

	// Deployment fields — what the engine actually needs to render an
	// ApplicationSet for this addon.
	RepoURL   string `json:"repo_url,omitempty"`
	Chart     string `json:"chart,omitempty"`
	Version   string `json:"version,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	// VersionSource names where Version came from: "delta" when the user's
	// catalog/addons.yaml set it, "" when nothing has (per design doc D7,
	// the shipped catalog itself never carries a version — a shipped
	// version would go stale inside a signed artefact). A per-cluster
	// clusters/<name>.yaml pin (design doc §2.1) is a later precedence
	// step this merge does not see; VersionSource only speaks to the
	// curated/delta layer.
	VersionSource string `json:"version_source,omitempty"`

	Settings          *config.AddonSettings `json:"settings,omitempty"`
	AdditionalSources []models.AddonSource  `json:"additional_sources,omitempty"`
	ExtraHelmValues   map[string]string     `json:"extra_helm_values,omitempty"`

	// Knowledge fields — Story 3.1's extended entry, carried straight
	// through from the curated CatalogEntry. Always zero-value for
	// OriginInternal addons: nothing but the user's own delta (which has
	// no knowledge fields) has ever described them.
	Description          string          `json:"description,omitempty"`
	DocsURL              string          `json:"docs_url,omitempty"`
	Homepage             string          `json:"homepage,omitempty"`
	SourceURL            string          `json:"source_url,omitempty"`
	Maintainers          []string        `json:"maintainers,omitempty"`
	License              string          `json:"license,omitempty"`
	Category             string          `json:"category,omitempty"`
	CuratedBy            []string        `json:"curated_by,omitempty"`
	SecurityScore        ScoreValue      `json:"security_score,omitempty"`
	SecurityTier         string          `json:"security_tier,omitempty"`
	GitHubStars          int             `json:"github_stars,omitempty"`
	MinKubernetesVersion string          `json:"min_kubernetes_version,omitempty"`
	Deprecated           bool            `json:"deprecated,omitempty"`
	SupersededBy         string          `json:"superseded_by,omitempty"`
	RequiredValues       []RequiredValue `json:"required_values,omitempty"`
	Secrets              []SecretRequirement `json:"secrets,omitempty"`
	Quirks               []string        `json:"quirks,omitempty"`
	Verified             bool            `json:"verified,omitempty"`
	SignatureIdentity    string          `json:"signature_identity,omitempty"`
}

// mergedFromCurated seeds a MergedAddon from a shipped catalog entry — the
// `deepCopy(curated)` half of design doc §4.7's mergeOverwrite. Every field
// copies by value (strings, ints, bools) or via a fresh slice/map (never
// aliasing the Catalog's own backing arrays), so later delta overlays can
// never mutate the source Catalog.
func mergedFromCurated(e CatalogEntry) MergedAddon {
	return MergedAddon{
		Name:                 e.Name,
		Origin:               OriginCurated,
		RepoURL:              e.Repo,
		Chart:                e.Chart,
		Namespace:            e.DefaultNamespace,
		Description:          e.Description,
		DocsURL:              e.DocsURL,
		Homepage:             e.Homepage,
		SourceURL:            e.SourceURL,
		Maintainers:          append([]string(nil), e.Maintainers...),
		License:              e.License,
		Category:             e.Category,
		CuratedBy:            append([]string(nil), e.CuratedBy...),
		SecurityScore:        e.SecurityScore,
		SecurityTier:         e.SecurityTier,
		GitHubStars:          e.GitHubStars,
		MinKubernetesVersion: e.MinKubernetesVersion,
		Deprecated:           e.Deprecated,
		SupersededBy:         e.SupersededBy,
		RequiredValues:       append([]RequiredValue(nil), e.RequiredValues...),
		Secrets:              append([]SecretRequirement(nil), e.Secrets...),
		Quirks:               append([]string(nil), e.Quirks...),
		Verified:             e.Verified,
		SignatureIdentity:    e.SignatureIdentity,
	}
}

// applyDeltaOverlay applies one AddonCatalogDeltaEntry onto m, field by
// field, the delta winning whenever it sets a field — the `delta` half of
// mergeOverwrite. Only fields AddonCatalogDeltaEntry actually carries
// (deployment fields) can be overridden; knowledge fields (Description,
// DocsURL, ...) have no delta-side equivalent and are left exactly as
// mergedFromCurated set them (or zero-value, for an internal addon).
func applyDeltaOverlay(m *MergedAddon, d config.AddonCatalogDeltaEntry) {
	m.Customized = true
	if d.RepoURL != "" {
		m.RepoURL = d.RepoURL
	}
	if d.Chart != "" {
		m.Chart = d.Chart
	}
	if d.Version != "" {
		m.Version = d.Version
		m.VersionSource = "delta"
	}
	if d.Namespace != "" {
		m.Namespace = d.Namespace
	}
	if d.Settings != nil {
		// List fields replace whole (design doc §3.3 D12) — trivially true
		// here because the curated side never sets Settings at all today,
		// but the assignment is unconditional-replace by construction so
		// the rule holds the moment a curated quirk-default settings block
		// exists (engine chart wiring, Story 2.4).
		m.Settings = d.Settings
	}
	if len(d.AdditionalSources) > 0 {
		m.AdditionalSources = d.AdditionalSources
	}
	if len(d.ExtraHelmValues) > 0 {
		m.ExtraHelmValues = d.ExtraHelmValues
	}
}

// MergeDelta overlays a fleet-wide catalog delta (a user's
// catalog/addons.yaml, kind AddonCatalogDelta) onto the shipped curated
// catalog and returns the merged, per-addon view keyed by addon name.
//
// curated may be nil (no embedded catalog loaded) — every addon in delta
// then merges as OriginInternal. delta.Addons may be nil/empty (design doc
// D16, "missing means empty") — the result is then every curated entry,
// Customized=false, unchanged.
//
// Returns *MissingRequiredFieldError, naming the addon and the missing
// field, for any addon that ends up with Origin == OriginInternal but is
// missing RepoURL, Chart, or Version — the requiredness rule from design
// doc §2.3 ("The note on required"), enforced at merge time per the
// AddonCatalogDeltaEntry doc comment. Addons are checked in sorted-name
// order so the reported failure is deterministic across runs.
func MergeDelta(curated *Catalog, delta config.AddonCatalogDeltaSpec) (map[string]MergedAddon, error) {
	out := make(map[string]MergedAddon)

	if curated != nil {
		for _, e := range curated.Entries() {
			out[e.Name] = mergedFromCurated(e)
		}
	}

	for name, d := range delta.Addons {
		m, existed := out[name]
		if !existed {
			m = MergedAddon{Name: name, Origin: OriginInternal}
		}
		applyDeltaOverlay(&m, d)
		out[name] = m
	}

	names := make([]string, 0, len(out))
	for name := range out {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		m := out[name]
		if m.Origin != OriginInternal {
			continue
		}
		switch {
		case m.RepoURL == "":
			return nil, &MissingRequiredFieldError{Addon: name, Field: "repoURL"}
		case m.Chart == "":
			return nil, &MissingRequiredFieldError{Addon: name, Field: "chart"}
		case m.Version == "":
			return nil, &MissingRequiredFieldError{Addon: name, Field: "version"}
		}
	}

	return out, nil
}
