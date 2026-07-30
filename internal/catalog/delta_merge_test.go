package catalog

import (
	"errors"
	"testing"

	"github.com/MoranWeissman/sharko/internal/config"
)

// curatedFixture returns a small, hand-built curated catalog (NOT the
// embedded one) so these tests don't drift when catalog/addons.yaml
// content changes. cert-manager mirrors the design doc's own worked
// example (§2.3, §6) closely enough to double as documentation.
func curatedFixture(t *testing.T) *Catalog {
	t.Helper()
	y := `
addons:
  - name: cert-manager
    description: Automated TLS certificate lifecycle management.
    chart: cert-manager
    repo: https://charts.jetstack.io
    default_namespace: cert-manager
    docs_url: https://cert-manager.io/docs
    maintainers: [jetstack]
    license: Apache-2.0
    category: security
    curated_by: [cncf-graduated]
    required_values:
      - key: installCRDs
        description: Install cert-manager's CRDs.
    quirks:
      - "Webhook caBundle rewrites on every reconcile."
  - name: metrics-server
    description: Resource metrics for kubectl top and HPA.
    chart: metrics-server
    repo: https://kubernetes-sigs.github.io/metrics-server/
    default_namespace: kube-system
    maintainers: [kubernetes-sigs]
    license: Apache-2.0
    category: observability
    curated_by: [cncf-graduated]
`
	cat, err := LoadBytes([]byte(y))
	if err != nil {
		t.Fatalf("LoadBytes fixture: %v", err)
	}
	return cat
}

// TestMergeDelta_PureCuratedEntryUnmodified is the "no delta touches it"
// baseline: an addon absent from delta.Addons still appears in the merged
// result, Origin=curated, Customized=false, and every knowledge field
// (description, docs link, required values, quirks) intact.
func TestMergeDelta_PureCuratedEntryUnmodified(t *testing.T) {
	cat := curatedFixture(t)
	merged, err := MergeDelta(cat, config.AddonCatalogDeltaSpec{})
	if err != nil {
		t.Fatalf("MergeDelta: %v", err)
	}
	m, ok := merged["metrics-server"]
	if !ok {
		t.Fatalf("expected metrics-server in merged result")
	}
	if m.Origin != OriginCurated {
		t.Errorf("Origin = %q, want %q", m.Origin, OriginCurated)
	}
	if m.Customized {
		t.Errorf("Customized = true, want false (delta never mentioned it)")
	}
	if m.Chart != "metrics-server" || m.RepoURL != "https://kubernetes-sigs.github.io/metrics-server/" {
		t.Errorf("chart location not carried through: chart=%q repo=%q", m.Chart, m.RepoURL)
	}
	if m.Version != "" || m.VersionSource != "" {
		t.Errorf("expected no version (curated ships none, delta didn't set one): version=%q source=%q", m.Version, m.VersionSource)
	}
}

// TestMergeDelta_CuratedPlusUserVersionOverride is the Story 3.2 AC
// verbatim: "given a curated entry for cert-manager and a user override of
// its default version, when Sharko loads the catalog, the merged view
// shows the curated set plus the user's entries with the user's values
// winning on conflict."
func TestMergeDelta_CuratedPlusUserVersionOverride(t *testing.T) {
	cat := curatedFixture(t)
	delta := config.AddonCatalogDeltaSpec{
		Addons: map[string]config.AddonCatalogDeltaEntry{
			"cert-manager": {Version: "1.14.5"},
		},
	}
	merged, err := MergeDelta(cat, delta)
	if err != nil {
		t.Fatalf("MergeDelta: %v", err)
	}
	m, ok := merged["cert-manager"]
	if !ok {
		t.Fatalf("expected cert-manager in merged result")
	}
	if m.Origin != OriginCurated {
		t.Errorf("Origin = %q, want %q (still shipped, just overridden)", m.Origin, OriginCurated)
	}
	if !m.Customized {
		t.Errorf("Customized = false, want true (delta set a version)")
	}
	if m.Version != "1.14.5" {
		t.Errorf("Version = %q, want the delta's 1.14.5", m.Version)
	}
	if m.VersionSource != "delta" {
		t.Errorf("VersionSource = %q, want %q", m.VersionSource, "delta")
	}
	// The delta only touched Version — every knowledge field must still
	// come through from curated untouched (the whole point of a delta:
	// "only your changes", never a copy of the curated set on the user's
	// side, but the MERGED view still carries the full curated knowledge).
	if m.Description == "" || m.DocsURL == "" {
		t.Errorf("curated knowledge fields lost after a narrow delta override: description=%q docs_url=%q", m.Description, m.DocsURL)
	}
	if len(m.RequiredValues) != 1 || len(m.Quirks) != 1 {
		t.Errorf("curated required_values/quirks lost after a narrow delta override: %+v / %+v", m.RequiredValues, m.Quirks)
	}
	// Chart location is untouched — delta never set RepoURL/Chart.
	if m.Chart != "cert-manager" || m.RepoURL != "https://charts.jetstack.io" {
		t.Errorf("chart location changed unexpectedly: chart=%q repo=%q", m.Chart, m.RepoURL)
	}
}

// TestMergeDelta_DeltaOverridesChartLocationToo confirms EVERY deployment
// field on AddonCatalogDeltaEntry — not just Version — wins on conflict,
// field by field, per design doc §4.7.
func TestMergeDelta_DeltaOverridesChartLocationToo(t *testing.T) {
	cat := curatedFixture(t)
	delta := config.AddonCatalogDeltaSpec{
		Addons: map[string]config.AddonCatalogDeltaEntry{
			"cert-manager": {
				RepoURL:   "oci://my-registry.example.com/mirror",
				Namespace: "custom-cert-manager",
				Settings: &config.AddonSettings{
					SyncOptions: []string{"ServerSideApply=true"},
				},
			},
		},
	}
	merged, err := MergeDelta(cat, delta)
	if err != nil {
		t.Fatalf("MergeDelta: %v", err)
	}
	m := merged["cert-manager"]
	if m.RepoURL != "oci://my-registry.example.com/mirror" {
		t.Errorf("RepoURL not overridden: %q", m.RepoURL)
	}
	if m.Namespace != "custom-cert-manager" {
		t.Errorf("Namespace not overridden: %q", m.Namespace)
	}
	if m.Chart != "cert-manager" {
		t.Errorf("Chart should be untouched (delta didn't set it): %q", m.Chart)
	}
	if m.Settings == nil || len(m.Settings.SyncOptions) != 1 || m.Settings.SyncOptions[0] != "ServerSideApply=true" {
		t.Errorf("Settings.SyncOptions not carried through as a whole-list replace: %+v", m.Settings)
	}
}

// TestMergeDelta_InternalAddonFullyDefined is the Story 3.3 AC's "private
// OCI chart reference" case: an addon with no curated entry, fully defined
// by the user's own delta. It must merge as first-class, Origin=internal,
// with everything the engine needs to deploy it.
func TestMergeDelta_InternalAddonFullyDefined(t *testing.T) {
	cat := curatedFixture(t)
	delta := config.AddonCatalogDeltaSpec{
		Addons: map[string]config.AddonCatalogDeltaEntry{
			"billing-api": {
				RepoURL:   "oci://registry.example.com/charts",
				Chart:     "billing-api",
				Version:   "2.4.0",
				Namespace: "billing",
			},
		},
	}
	merged, err := MergeDelta(cat, delta)
	if err != nil {
		t.Fatalf("MergeDelta: %v", err)
	}
	m, ok := merged["billing-api"]
	if !ok {
		t.Fatalf("expected billing-api in merged result")
	}
	if m.Origin != OriginInternal {
		t.Errorf("Origin = %q, want %q", m.Origin, OriginInternal)
	}
	if !m.Customized {
		t.Errorf("Customized = false, want true")
	}
	if m.RepoURL != "oci://registry.example.com/charts" || m.Chart != "billing-api" || m.Version != "2.4.0" {
		t.Errorf("internal addon deployment fields not carried through: %+v", m)
	}
	if m.VersionSource != "delta" {
		t.Errorf("VersionSource = %q, want %q", m.VersionSource, "delta")
	}
	// Knowledge fields must be empty — nothing curated ever described this
	// addon, and the delta has no knowledge fields to supply them either.
	if m.Description != "" || m.DocsURL != "" || len(m.RequiredValues) != 0 {
		t.Errorf("internal addon should have zero-value knowledge fields, got %+v", m)
	}
}

// TestMergeDelta_InternalAddonMissingRequiredField_RepoURL,
// TestMergeDelta_InternalAddonMissingRequiredField_Chart, and
// TestMergeDelta_InternalAddonMissingRequiredField_Version are the deferred
// check named in AddonCatalogDeltaEntry's doc comment: an addon with no
// curated backing MUST carry repoURL, chart, and version, enforced here
// (merge time) with an error naming the addon and the specific missing
// field.
func TestMergeDelta_InternalAddonMissingRequiredField(t *testing.T) {
	cases := []struct {
		name      string
		entry     config.AddonCatalogDeltaEntry
		wantField string
	}{
		{
			name:      "missing repoURL",
			entry:     config.AddonCatalogDeltaEntry{Chart: "billing-api", Version: "2.4.0"},
			wantField: "repoURL",
		},
		{
			name:      "missing chart",
			entry:     config.AddonCatalogDeltaEntry{RepoURL: "oci://registry.example.com/charts", Version: "2.4.0"},
			wantField: "chart",
		},
		{
			name:      "missing version",
			entry:     config.AddonCatalogDeltaEntry{RepoURL: "oci://registry.example.com/charts", Chart: "billing-api"},
			wantField: "version",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cat := curatedFixture(t)
			delta := config.AddonCatalogDeltaSpec{
				Addons: map[string]config.AddonCatalogDeltaEntry{"billing-api": tc.entry},
			}
			_, err := MergeDelta(cat, delta)
			if err == nil {
				t.Fatalf("expected an error, got nil")
			}
			var mrf *MissingRequiredFieldError
			if !errors.As(err, &mrf) {
				t.Fatalf("expected *MissingRequiredFieldError, got %T: %v", err, err)
			}
			if mrf.Addon != "billing-api" {
				t.Errorf("Addon = %q, want billing-api", mrf.Addon)
			}
			if mrf.Field != tc.wantField {
				t.Errorf("Field = %q, want %q", mrf.Field, tc.wantField)
			}
			if err.Error() == "" {
				t.Errorf("expected a non-empty error message")
			}
		})
	}
}

// TestMergeDelta_CuratedAddonNeverTriggersRequirednessCheck confirms a
// curated addon overridden by the delta is EXEMPT from the
// repoURL/chart/version requiredness rule even when the delta touches only
// one field (e.g. just Version, as in the AC 3.2 scenario) — the rule is
// specifically about addons with NO shipped backing.
func TestMergeDelta_CuratedAddonNeverTriggersRequirednessCheck(t *testing.T) {
	cat := curatedFixture(t)
	delta := config.AddonCatalogDeltaSpec{
		Addons: map[string]config.AddonCatalogDeltaEntry{
			"cert-manager": {Version: "1.14.5"},
		},
	}
	if _, err := MergeDelta(cat, delta); err != nil {
		t.Fatalf("MergeDelta on a curated addon with a version-only override should not error: %v", err)
	}
}

// TestMergeDelta_NilCuratedEveryAddonIsInternal exercises the nil-catalog
// path (e.g. embedded catalog failed to load) — every delta addon becomes
// OriginInternal and is subject to the full requiredness check.
func TestMergeDelta_NilCuratedEveryAddonIsInternal(t *testing.T) {
	delta := config.AddonCatalogDeltaSpec{
		Addons: map[string]config.AddonCatalogDeltaEntry{
			"billing-api": {RepoURL: "oci://registry.example.com/charts", Chart: "billing-api", Version: "2.4.0"},
		},
	}
	merged, err := MergeDelta(nil, delta)
	if err != nil {
		t.Fatalf("MergeDelta: %v", err)
	}
	if merged["billing-api"].Origin != OriginInternal {
		t.Errorf("expected OriginInternal with a nil curated catalog")
	}
}

// TestMergeDelta_EmptyDeltaReturnsUntouchedCurated is design doc D16
// ("missing means empty") applied to the merge: a zero-value
// AddonCatalogDeltaSpec must not error and must return every curated entry
// unmodified.
func TestMergeDelta_EmptyDeltaReturnsUntouchedCurated(t *testing.T) {
	cat := curatedFixture(t)
	merged, err := MergeDelta(cat, config.AddonCatalogDeltaSpec{})
	if err != nil {
		t.Fatalf("MergeDelta: %v", err)
	}
	if len(merged) != cat.Len() {
		t.Errorf("merged result has %d entries, want %d (curated set unchanged)", len(merged), cat.Len())
	}
	for _, m := range merged {
		if m.Customized {
			t.Errorf("%q: Customized = true with an empty delta", m.Name)
		}
	}
}

// TestMergeDelta_MutatingResultDoesNotAffectCatalog guards the deepCopy
// half of design doc §4.7's mergeOverwrite(deepCopy(curated), delta):
// mutating a MergedAddon's slice fields must never reach back into the
// Catalog's own backing arrays.
func TestMergeDelta_MutatingResultDoesNotAffectCatalog(t *testing.T) {
	cat := curatedFixture(t)
	merged, err := MergeDelta(cat, config.AddonCatalogDeltaSpec{})
	if err != nil {
		t.Fatalf("MergeDelta: %v", err)
	}
	m := merged["cert-manager"]
	if len(m.Quirks) == 0 {
		t.Fatalf("fixture expected at least one quirk")
	}
	m.Quirks[0] = "MUTATED"

	e, ok := cat.Get("cert-manager")
	if !ok {
		t.Fatalf("expected cert-manager in catalog")
	}
	if len(e.Quirks) == 0 || e.Quirks[0] == "MUTATED" {
		t.Errorf("mutating the merged result's Quirks slice leaked back into the Catalog: %+v", e.Quirks)
	}
}
