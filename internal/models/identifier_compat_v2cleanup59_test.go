package models

// V2-cleanup-59 regression tests: the identifier rename from sharko.io (a
// domain the project never owned) to the maintainer-owned sharko.dev is
// READ-BOTH / EMIT-NEW. These tests pin the models-level equivalence
// helpers plus the envelope reader's acceptance of both API groups.

import (
	"strings"
	"testing"
	"time"
)

// --- envelope: LoadManagedClusters accepts old group, new group; writer emits new ---

const legacyGroupManagedClusters = `apiVersion: sharko.io/v1
kind: ManagedClusters
metadata:
  name: managed-clusters
spec:
  clusters:
    - name: prod-eu
      labels:
        addon-datadog: enabled
`

const newGroupManagedClusters = `apiVersion: sharko.dev/v1
kind: ManagedClusters
metadata:
  name: managed-clusters
spec:
  clusters:
    - name: prod-eu
      labels:
        addon-datadog: enabled
`

func TestLoadManagedClusters_LegacyGroupParsesAndValidatesClean(t *testing.T) {
	t.Parallel()
	spec, err := LoadManagedClusters([]byte(legacyGroupManagedClusters))
	if err != nil {
		t.Fatalf("old-group (sharko.io/v1) file must keep parsing + validating clean: %v", err)
	}
	if len(spec.Clusters) != 1 || spec.Clusters[0].Name != "prod-eu" {
		t.Fatalf("old-group parse produced wrong spec: %+v", spec)
	}
	if spec.Clusters[0].Labels["addon-datadog"] != "enabled" {
		t.Errorf("old-group labels lost: %+v", spec.Clusters[0].Labels)
	}
}

func TestLoadManagedClusters_NewGroupParsesAndValidatesClean(t *testing.T) {
	t.Parallel()
	spec, err := LoadManagedClusters([]byte(newGroupManagedClusters))
	if err != nil {
		t.Fatalf("new-group (sharko.dev/v1) file must parse + validate clean: %v", err)
	}
	if len(spec.Clusters) != 1 || spec.Clusters[0].Name != "prod-eu" {
		t.Fatalf("new-group parse produced wrong spec: %+v", spec)
	}
}

func TestSaveManagedClusters_EmitsOnlyNewGroup(t *testing.T) {
	t.Parallel()
	body, err := SaveManagedClusters(ManagedClustersSpec{
		Clusters: []ManagedClusterEntry{{Name: "prod-eu"}},
	})
	if err != nil {
		t.Fatalf("SaveManagedClusters: %v", err)
	}
	out := string(body)
	if !strings.Contains(out, "apiVersion: sharko.dev/v1") {
		t.Errorf("writer must emit the new group; got:\n%s", out)
	}
	if strings.Contains(out, "sharko.io/v1") {
		t.Errorf("writer must NEVER emit the legacy group; got:\n%s", out)
	}
}

// --- registration-pending: old key keeps its grace window ---

func TestIsRegistrationPending_LegacyKeyEquivalence(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	fresh := RegistrationPendingTimestamp(now.Add(-1 * time.Minute))
	expired := RegistrationPendingTimestamp(now.Add(-RegistrationPendingGraceWindow - time.Minute))

	cases := []struct {
		name        string
		annotations map[string]string
		wantPending bool
	}{
		{"new key fresh", map[string]string{AnnotationRegistrationPending: fresh}, true},
		{"legacy key fresh", map[string]string{AnnotationRegistrationPendingLegacy: fresh}, true},
		{"legacy key expired", map[string]string{AnnotationRegistrationPendingLegacy: expired}, false},
		{"neither key", map[string]string{}, false},
		{"new key wins over legacy", map[string]string{
			AnnotationRegistrationPending:       fresh,
			AnnotationRegistrationPendingLegacy: expired,
		}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			pending, malformed := IsRegistrationPending(tc.annotations, now)
			if malformed {
				t.Fatalf("unexpected malformed=true for %v", tc.annotations)
			}
			if pending != tc.wantPending {
				t.Errorf("IsRegistrationPending(%v) = %v, want %v", tc.annotations, pending, tc.wantPending)
			}
		})
	}
}

func TestRegistrationPendingValue_ReadsBothKeys(t *testing.T) {
	t.Parallel()
	if _, ok := RegistrationPendingValue(nil); ok {
		t.Error("nil annotations must report not-present")
	}
	if v, ok := RegistrationPendingValue(map[string]string{AnnotationRegistrationPendingLegacy: "x"}); !ok || v != "x" {
		t.Errorf("legacy key not read: (%q, %v)", v, ok)
	}
	if v, ok := RegistrationPendingValue(map[string]string{
		AnnotationRegistrationPending:       "new",
		AnnotationRegistrationPendingLegacy: "old",
	}); !ok || v != "new" {
		t.Errorf("canonical key must win: (%q, %v)", v, ok)
	}
}

// --- connectivity-check: legacy key recognised on read, never written ---

func TestHasConnectivityCheckLabel_BothKeys(t *testing.T) {
	t.Parallel()
	if HasConnectivityCheckLabel(nil) {
		t.Error("nil labels must report false")
	}
	if !HasConnectivityCheckLabel(map[string]string{LabelConnectivityCheck: LabelEnabled}) {
		t.Error("new key not recognised")
	}
	if !HasConnectivityCheckLabel(map[string]string{LabelConnectivityCheckLegacy: LabelEnabled}) {
		t.Error("legacy key not recognised")
	}
}

func TestApplyConnectivityCheckLabel_MigratesLegacyKey(t *testing.T) {
	t.Parallel()

	// Zero-addon cluster whose working labels still carry the legacy key:
	// the writer must stamp ONLY the new key and drop the old one.
	labels := map[string]string{LabelConnectivityCheckLegacy: LabelEnabled}
	ApplyConnectivityCheckLabel(labels, true)
	if labels[LabelConnectivityCheck] != LabelEnabled {
		t.Errorf("new key not stamped: %v", labels)
	}
	if _, still := labels[LabelConnectivityCheckLegacy]; still {
		t.Errorf("legacy key must be removed by the writer: %v", labels)
	}

	// The legacy key must not count as an "enabled addon" (its value is
	// LabelEnabled) — that would wrongly suppress the check label.
	labels = map[string]string{LabelConnectivityCheckLegacy: LabelEnabled}
	ApplyConnectivityCheckLabel(labels, true)
	if _, has := labels[LabelConnectivityCheck]; !has {
		t.Errorf("legacy check key was counted as an addon label: %v", labels)
	}

	// Feature off removes both spellings.
	labels = map[string]string{
		LabelConnectivityCheck:       LabelEnabled,
		LabelConnectivityCheckLegacy: LabelEnabled,
	}
	ApplyConnectivityCheckLabel(labels, false)
	if HasConnectivityCheckLabel(labels) {
		t.Errorf("feature-off must remove both check keys: %v", labels)
	}

	// First addon enabled removes both spellings too.
	labels = map[string]string{
		"addon-datadog":              LabelEnabled,
		LabelConnectivityCheckLegacy: LabelEnabled,
	}
	ApplyConnectivityCheckLabel(labels, true)
	if HasConnectivityCheckLabel(labels) {
		t.Errorf("addon-enabled cluster must carry no check key under either spelling: %v", labels)
	}
}

func TestRemoveConnectivityCheckLabels_RemovesBoth(t *testing.T) {
	t.Parallel()
	RemoveConnectivityCheckLabels(nil) // nil-safe

	labels := map[string]string{
		LabelConnectivityCheck:       LabelEnabled,
		LabelConnectivityCheckLegacy: LabelEnabled,
		"addon-x":                    LabelEnabled,
	}
	RemoveConnectivityCheckLabels(labels)
	if HasConnectivityCheckLabel(labels) {
		t.Errorf("both check keys must be removed: %v", labels)
	}
	if labels["addon-x"] != LabelEnabled {
		t.Errorf("unrelated labels must be untouched: %v", labels)
	}
}
