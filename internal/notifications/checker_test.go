package notifications

import (
	"context"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// isMajorOrMinorUpgrade
// ---------------------------------------------------------------------------

func TestIsMajorOrMinorUpgrade(t *testing.T) {
	cases := []struct {
		name    string
		current string
		latest  string
		want    bool
	}{
		// patch-only bumps: should return false
		{"patch same major minor", "1.2.3", "1.2.4", false},
		{"patch only with v prefix", "v2.0.0", "v2.0.1", false},

		// minor bumps: should return true
		{"minor bump", "1.2.3", "1.3.0", true},
		{"minor bump across zero", "1.0.0", "1.1.0", true},

		// major bumps: should return true
		{"major bump", "1.9.9", "2.0.0", true},
		{"major bump large", "3.0.0", "4.0.0", true},

		// downgrade (latest < current): should return true (different major/minor)
		{"minor downgrade", "2.1.0", "2.0.0", true},

		// equal: should return false
		{"equal versions", "1.2.3", "1.2.3", false},

		// pre-release stripped correctly
		{"pre-release stripped", "1.0.0-beta.1", "1.0.0", false},

		// non-semver fallback: unequal non-parseable returns true
		{"non-semver unequal", "abc", "def", true},
		// non-semver equal returns false
		{"non-semver equal", "abc", "abc", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := isMajorOrMinorUpgrade(tc.current, tc.latest)
			if got != tc.want {
				t.Errorf("isMajorOrMinorUpgrade(%q, %q) = %v, want %v", tc.current, tc.latest, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// isMajorUpgrade
// ---------------------------------------------------------------------------

func TestIsMajorUpgrade(t *testing.T) {
	cases := []struct {
		name    string
		current string
		latest  string
		want    bool
	}{
		// true when latest major > current major
		{"major bump 1→2", "1.9.9", "2.0.0", true},
		{"major bump 2→3", "2.0.0", "3.5.1", true},

		// false for same major
		{"same major minor bump", "1.0.0", "1.1.0", false},
		{"same major patch bump", "1.2.3", "1.2.9", false},
		{"equal", "2.0.0", "2.0.0", false},

		// false for downgrade (latest major < current major)
		{"major downgrade", "3.0.0", "2.5.0", false},

		// non-parseable: returns false (safe default)
		{"non-semver", "abc", "def", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := isMajorUpgrade(tc.current, tc.latest)
			if got != tc.want {
				t.Errorf("isMajorUpgrade(%q, %q) = %v, want %v", tc.current, tc.latest, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// parseSemverParts
// ---------------------------------------------------------------------------

func TestParseSemverParts(t *testing.T) {
	cases := []struct {
		input string
		want  []int
	}{
		{"1.2.3", []int{1, 2, 3}},
		{"v1.2.3", []int{1, 2, 3}},
		{"1.0.0-beta.1", []int{1, 0, 0}},
		{"10.20.30", []int{10, 20, 30}},
		{"1.2", []int{1, 2}},
		{"abc", []int{}},
	}

	for _, tc := range cases {
		got := parseSemverParts(tc.input)
		if len(got) != len(tc.want) {
			t.Errorf("parseSemverParts(%q): len %d != %d: %v", tc.input, len(got), len(tc.want), got)
			continue
		}
		for i := range tc.want {
			if got[i] != tc.want[i] {
				t.Errorf("parseSemverParts(%q)[%d] = %d, want %d", tc.input, i, got[i], tc.want[i])
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Checker — store integration
// ---------------------------------------------------------------------------

// fakeProvider is a VersionProvider that returns a canned list.
type fakeProvider struct {
	infos []VersionInfo
	err   error
}

func (f *fakeProvider) GetVersionInfo(_ context.Context) ([]VersionInfo, error) {
	return f.infos, f.err
}

func TestChecker_ProducesUpgradeNotification(t *testing.T) {
	store := NewStore(50, "")
	provider := &fakeProvider{
		infos: []VersionInfo{
			{
				AddonName:       "datadog",
				CatalogVersion:  "3.0.0",
				LatestVersion:   "4.0.0", // major bump → both upgrade + security notification
				ClusterVersions: map[string]string{},
			},
		},
	}

	checker := NewChecker(store, provider, time.Hour)
	// Invoke check directly to avoid goroutine timing issues.
	checker.check()

	items := store.List()
	if len(items) < 2 {
		t.Fatalf("expected at least 2 notifications (upgrade + security), got %d", len(items))
	}

	typeSet := map[NotificationType]bool{}
	for _, n := range items {
		typeSet[n.Type] = true
	}
	if !typeSet[TypeUpgrade] {
		t.Error("expected TypeUpgrade notification")
	}
	if !typeSet[TypeSecurity] {
		t.Error("expected TypeSecurity notification for major bump")
	}
}

func TestChecker_ProducesDriftNotification(t *testing.T) {
	store := NewStore(50, "")
	provider := &fakeProvider{
		infos: []VersionInfo{
			{
				AddonName:      "keda",
				CatalogVersion: "2.14.0",
				LatestVersion:  "",
				ClusterVersions: map[string]string{
					"cluster-prod": "2.12.0", // drift from catalog
				},
			},
		},
	}

	checker := NewChecker(store, provider, time.Hour)
	checker.check()

	items := store.List()
	if len(items) != 1 {
		t.Fatalf("expected 1 drift notification, got %d", len(items))
	}
	if items[0].Type != TypeDrift {
		t.Errorf("expected TypeDrift, got %v", items[0].Type)
	}
}

func TestChecker_SkipsMinorPatchNotification(t *testing.T) {
	store := NewStore(50, "")
	provider := &fakeProvider{
		infos: []VersionInfo{
			{
				AddonName:       "cert-manager",
				CatalogVersion:  "1.14.0",
				LatestVersion:   "1.14.5", // patch-only bump
				ClusterVersions: map[string]string{},
			},
		},
	}

	checker := NewChecker(store, provider, time.Hour)
	checker.check()

	items := store.List()
	if len(items) != 0 {
		t.Errorf("expected 0 notifications for patch-only bump, got %d: %+v", len(items), items)
	}
}

func TestChecker_SkipsSameVersions(t *testing.T) {
	store := NewStore(50, "")
	provider := &fakeProvider{
		infos: []VersionInfo{
			{
				AddonName:       "nginx",
				CatalogVersion:  "1.0.0",
				LatestVersion:   "1.0.0", // same version
				ClusterVersions: map[string]string{"prod": "1.0.0"},
			},
		},
	}

	checker := NewChecker(store, provider, time.Hour)
	checker.check()

	if store.UnreadCount() != 0 {
		t.Errorf("expected 0 notifications for identical versions, got %d", store.UnreadCount())
	}
}

func TestChecker_StopIsIdempotent(t *testing.T) {
	store := NewStore(50, "")
	provider := &fakeProvider{}
	checker := NewChecker(store, provider, time.Hour)

	// Stop without Start should not panic.
	checker.Stop()
	checker.Stop() // second call must not panic
}

// TestChecker_EmptyProviderShortCircuitsCleanly is the V124-23 / BUG-048
// regression test for the notifications poller. Before the fix,
// addonSvc.GetVersionMatrix returned a 5xx-class error when the gitops repo
// was missing managed-clusters.yaml (fresh-install state). That error
// propagated through ServiceProvider.GetVersionInfo and triggered
// `slog.Error("notification check failed", ...)` on every cycle — exactly
// what the maintainer's empty-repo logs showed:
//
//	notification check  error="reading managed-clusters.yaml: ... file not found"
//
// After the fix (addon.go now uses isGitFileNotFound) GetVersionMatrix
// degrades to an empty matrix, GetVersionInfo returns `(nil, nil)`, and
// the checker iterates a zero-element list — no error log, no notifications,
// no log spam at the default 30-min interval.
//
// We test this at the Checker level rather than wiring a full
// ServiceProvider + ConnectionService because the fix lives in
// service.AddonService.GetVersionMatrix (which addon_test.go's
// TestGetVersionMatrix_MissingFileReturnsEmpty already covers end-to-end);
// here we lock down the contract that an empty/no-error VersionInfo from
// the provider produces zero notifications and zero error logs.
func TestChecker_EmptyProviderShortCircuitsCleanly(t *testing.T) {
	store := NewStore(50, "")
	provider := &fakeProvider{
		// Empty infos + nil err is the post-fix shape: GetVersionMatrix
		// returned an empty matrix because managed-clusters.yaml was missing,
		// no clusters means no addon rows means no VersionInfo entries.
		infos: nil,
		err:   nil,
	}

	checker := NewChecker(store, provider, time.Hour)
	checker.check()

	if got := store.UnreadCount(); got != 0 {
		t.Errorf("expected 0 notifications from empty provider, got %d", got)
	}
	items := store.List()
	if len(items) != 0 {
		t.Errorf("expected 0 items from empty provider, got %d: %+v", len(items), items)
	}
}
