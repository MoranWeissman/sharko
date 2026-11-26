package notifications

import (
	"strings"
	"testing"
	"time"

	"github.com/MoranWeissman/sharko/internal/helm"
)

// Pre-release walk 1 L2 and walk 4 finding 4: right after somebody added the
// newest stable reloader, the bell said "Major update: reloader 3.0.0-beta.6
// ... review for security patches". A beta is not the latest version.
func TestLatestStableVersion_NeverPicksAPreRelease(t *testing.T) {
	versions := []helm.ChartVersion{
		{Version: "3.0.0-beta.6"}, {Version: "3.0.0-rc.1"}, {Version: "2.2.21"}, {Version: "2.2.20"}, {Version: ""},
	}
	if got := latestStableVersion(versions); got != "2.2.21" {
		t.Errorf("latest = %q, want 2.2.21 (the newest released version)", got)
	}
	if got := latestStableVersion([]helm.ChartVersion{{Version: "1.0.0-alpha"}}); got != "" {
		t.Errorf("only pre-releases published: latest = %q, want nothing to announce", got)
	}
	if isPreRelease("v1.2.3+build-5") {
		t.Error("build metadata after a plus sign is not a pre-release")
	}
	for _, pre := range []string{"3.0.0-beta.6", "v2.0.0-rc.1", "1.0.0-alpha"} {
		if !isPreRelease(pre) {
			t.Errorf("%q is a pre-release", pre)
		}
	}
}

// With the newest RELEASED version handed to the checker, a beta upstream
// raises nothing at all.
func TestChecker_BetaUpstream_RaisesNoMajorUpdate(t *testing.T) {
	store := NewStore(50, nil)
	latest := latestStableVersion([]helm.ChartVersion{{Version: "3.0.0-beta.6"}, {Version: "2.2.21"}})
	checker := NewChecker(store, &fakeProvider{infos: []VersionInfo{{
		AddonName: "reloader", CatalogVersion: "2.2.21", LatestVersion: latest, ClusterVersions: map[string]string{"spoke-1": "2.2.21"},
	}}}, time.Hour)
	checker.check()
	for _, n := range store.List() {
		t.Errorf("nothing should be on the bell, found %q / %q", n.Title, n.Description)
	}
}

// The drift sentence says what Sharko knows: the version Git asks for on
// that cluster. It used to say "Running 99.0.0" for a version that never ran.
func TestDriftSentence_SaysSetTo_NotRunning(t *testing.T) {
	n := New(CodeAddonVersionDrift, "", Params{Addon: "podinfo", Cluster: "spoke-1", Version: "99.0.0", CatalogVersion: "6.9.0"}, time.Now())
	if want := "Set to 99.0.0, catalog has 6.9.0"; n.Description != want {
		t.Errorf("description = %q, want %q", n.Description, want)
	}
	if strings.Contains(n.Description, "Running") {
		t.Errorf("the old wording is back: %q", n.Description)
	}
}

// Walk 4 finding 3: notes stayed on the bell after they stopped being true.
func TestChecker_ClearsNotesThatAreNoLongerTrue(t *testing.T) {
	store := NewStore(50, nil)
	provider := &fakeProvider{infos: []VersionInfo{
		{AddonName: "podinfo", CatalogVersion: "6.7.1", LatestVersion: "6.17.0", ClusterVersions: map[string]string{"spoke-1": "6.9.0", "spoke-2": "6.7.1"}},
		{AddonName: "reloader", CatalogVersion: "1.0.0", LatestVersion: "2.2.21", ClusterVersions: map[string]string{"spoke-1": "1.0.0"}},
	}}
	checker := NewChecker(store, provider, time.Hour)
	checker.check()

	ids := func() map[string]bool {
		out := map[string]bool{}
		for _, n := range store.List() {
			out[n.ID] = true
		}
		return out
	}
	before := ids()
	for _, want := range []string{"upgrade-podinfo-6.17.0", "drift-podinfo-spoke-1", "upgrade-reloader-2.2.21", "security-reloader-2.2.21"} {
		if !before[want] {
			t.Fatalf("setup: expected note %s, have %v", want, before)
		}
	}

	// spoke-1 is rolled back to the catalog version, podinfo is upgraded to
	// the newest version, and reloader is removed from the catalog.
	provider.infos = []VersionInfo{
		{AddonName: "podinfo", CatalogVersion: "6.17.0", LatestVersion: "6.17.0", ClusterVersions: map[string]string{"spoke-1": "6.17.0", "spoke-2": "6.17.0"}},
	}
	checker.check()
	if after := ids(); len(after) != 0 {
		t.Errorf("every note is about something that is no longer so, but these stayed: %v", after)
	}
}

// "Could not check" is not "no longer true": a round where the newest
// version could not be read keeps the upgrade note, and a round with no
// connection at all clears nothing.
func TestChecker_KeepsNotesItCouldNotRecheck(t *testing.T) {
	store := NewStore(50, nil)
	provider := &fakeProvider{infos: []VersionInfo{
		{AddonName: "podinfo", CatalogVersion: "6.7.1", LatestVersion: "6.17.0", ClusterVersions: map[string]string{"spoke-1": "6.9.0"}},
	}}
	checker := NewChecker(store, provider, time.Hour)
	checker.check()
	if len(store.List()) != 2 {
		t.Fatalf("setup: expected an upgrade note and a drift note, got %d", len(store.List()))
	}

	provider.infos = nil // no Git or ArgoCD connection this round
	checker.check()
	if len(store.List()) != 2 {
		t.Errorf("a round with no answer cleared notes: %d left", len(store.List()))
	}

	provider.infos = []VersionInfo{
		{AddonName: "podinfo", CatalogVersion: "6.7.1", LatestVersion: "", ClusterVersions: map[string]string{"spoke-1": "6.9.0"}},
	}
	checker.check()
	found := false
	for _, n := range store.List() {
		if n.ID == "upgrade-podinfo-6.17.0" {
			found = true
		}
	}
	if !found {
		t.Error("the upgrade note was cleared in a round where the chart repository could not be read")
	}
}
