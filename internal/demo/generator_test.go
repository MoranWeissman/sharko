package demo

import (
	"testing"
)

// TestGenerateEstate_Sizing verifies the generator honors the requested
// cluster/addon counts and stays within the documented per-cluster addon
// range (S1).
func TestGenerateEstate_Sizing(t *testing.T) {
	cfg := ScaleConfig{Clusters: 20, Addons: 15, Seed: 7}
	estate, err := GenerateEstate(cfg)
	if err != nil {
		t.Fatalf("GenerateEstate: %v", err)
	}
	if len(estate.Clusters) != cfg.Clusters {
		t.Errorf("clusters = %d, want %d", len(estate.Clusters), cfg.Clusters)
	}
	if len(estate.Addons) != cfg.Addons {
		t.Errorf("addons = %d, want %d", len(estate.Addons), cfg.Addons)
	}
	if len(estate.UnregisteredClusters) < minUnregisteredCandidates || len(estate.UnregisteredClusters) > maxUnregisteredCandidates {
		t.Errorf("unregistered clusters = %d, want [%d,%d]", len(estate.UnregisteredClusters), minUnregisteredCandidates, maxUnregisteredCandidates)
	}
	for _, c := range estate.Clusters {
		n := len(c.Addons)
		// 0 is valid too: applyStateCoverage deliberately zeroes one
		// cluster's addons for the "connectivity-check-only" exemplar.
		if n > maxAddonsPerCluster {
			t.Errorf("cluster %s has %d addons, want at most %d",
				c.Name, n, maxAddonsPerCluster)
		}
	}
}

// TestGenerateEstate_Deterministic verifies the same ScaleConfig always
// produces the same estate — the generator's only source of randomness is
// math/rand seeded from cfg.Seed (S1).
func TestGenerateEstate_Deterministic(t *testing.T) {
	cfg := ScaleConfig{Clusters: 12, Addons: 10, Seed: 99}
	a, err := GenerateEstate(cfg)
	if err != nil {
		t.Fatalf("GenerateEstate (1st): %v", err)
	}
	b, err := GenerateEstate(cfg)
	if err != nil {
		t.Fatalf("GenerateEstate (2nd): %v", err)
	}

	if len(a.Clusters) != len(b.Clusters) {
		t.Fatalf("cluster count differs: %d vs %d", len(a.Clusters), len(b.Clusters))
	}
	for i := range a.Clusters {
		if a.Clusters[i].Name != b.Clusters[i].Name {
			t.Fatalf("cluster[%d] name differs: %q vs %q", i, a.Clusters[i].Name, b.Clusters[i].Name)
		}
		if a.Clusters[i].ConnStatus != b.Clusters[i].ConnStatus {
			t.Errorf("cluster %s ConnStatus differs: %q vs %q", a.Clusters[i].Name, a.Clusters[i].ConnStatus, b.Clusters[i].ConnStatus)
		}
		for addonName, version := range a.Clusters[i].Addons {
			if b.Clusters[i].Addons[addonName] != version {
				t.Errorf("cluster %s addon %s version differs: %q vs %q", a.Clusters[i].Name, addonName, version, b.Clusters[i].Addons[addonName])
			}
		}
	}
	if len(a.TrackedPRs) != len(b.TrackedPRs) {
		t.Fatalf("tracked PR count differs: %d vs %d", len(a.TrackedPRs), len(b.TrackedPRs))
	}
	for i := range a.TrackedPRs {
		if a.TrackedPRs[i].PRTitle != b.TrackedPRs[i].PRTitle {
			t.Errorf("tracked PR[%d] title differs: %q vs %q", i, a.TrackedPRs[i].PRTitle, b.TrackedPRs[i].PRTitle)
		}
	}
}

// TestNewMockGitProviderWithConfig_DefaultUnchanged verifies the default
// ScaleConfig bypasses the generator entirely — byte-identical to
// NewMockGitProvider()'s hand-written fixture (S1 "defaults unchanged").
func TestNewMockGitProviderWithConfig_DefaultUnchanged(t *testing.T) {
	want := NewMockGitProvider()
	got, err := NewMockGitProviderWithConfig(DefaultScaleConfig)
	if err != nil {
		t.Fatalf("NewMockGitProviderWithConfig: %v", err)
	}
	for _, path := range []string{
		"configuration/managed-clusters.yaml",
		"configuration/addons-catalog.yaml",
	} {
		wantBody, _ := want.files[path]
		gotBody, _ := got.files[path]
		if string(wantBody) != string(gotBody) {
			t.Errorf("%s differs between NewMockGitProvider and NewMockGitProviderWithConfig(default)", path)
		}
	}
	if want.nextPRID != got.nextPRID {
		t.Errorf("nextPRID = %d, want %d (default path must not touch PR numbering)", got.nextPRID, want.nextPRID)
	}
}

// TestNewMockArgocdServerWithConfig_DefaultUnchanged verifies the default
// ScaleConfig produces the same cluster/app counts as NewMockArgocdServer()
// (S1 "defaults unchanged").
func TestNewMockArgocdServerWithConfig_DefaultUnchanged(t *testing.T) {
	want, err := NewMockArgocdServer()
	if err != nil {
		t.Fatalf("NewMockArgocdServer: %v", err)
	}
	defer want.Close()
	got, err := NewMockArgocdServerWithConfig(DefaultScaleConfig)
	if err != nil {
		t.Fatalf("NewMockArgocdServerWithConfig(default): %v", err)
	}
	defer got.Close()

	if len(want.clusters) != len(got.clusters) {
		t.Errorf("cluster count = %d, want %d", len(got.clusters), len(want.clusters))
	}
	if len(want.apps) != len(got.apps) {
		t.Errorf("app count = %d, want %d", len(got.apps), len(want.apps))
	}
}

// TestGenerateEstate_BigPreset_Sizing pins BigScaleConfig's documented shape
// (S1/S2's --demo-scale=big preset).
func TestGenerateEstate_BigPreset_Sizing(t *testing.T) {
	estate, err := GenerateEstate(BigScaleConfig)
	if err != nil {
		t.Fatalf("GenerateEstate(BigScaleConfig): %v", err)
	}
	if len(estate.Clusters) != BigClusterCount {
		t.Errorf("clusters = %d, want %d", len(estate.Clusters), BigClusterCount)
	}
	if len(estate.Addons) != BigAddonCount {
		t.Errorf("addons = %d, want %d", len(estate.Addons), BigAddonCount)
	}
	if len(estate.TrackedPRs) != targetOpenPRs+targetMergedPRs {
		t.Errorf("tracked PRs = %d, want %d", len(estate.TrackedPRs), targetOpenPRs+targetMergedPRs)
	}
}
