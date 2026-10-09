package connectioncompare

import (
	"encoding/json"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/MoranWeissman/sharko/internal/argosecrets"
	"github.com/MoranWeissman/sharko/internal/models"
)

func TestOwnedLabelKey(t *testing.T) {
	tests := []struct {
		key   string
		scope Scope
		want  bool
	}{
		// Addon-enablement keys — Sharko's at every scope where labels are
		// checked at all.
		{"datadog", ScopeFull, true},
		{"datadog", ScopeAddonLabelsOnly, true},
		{models.V4AddonLabelKey("datadog"), ScopeFull, true},
		{models.V4AddonLabelKey("datadog"), ScopeAddonLabelsOnly, true},

		// System labels — Sharko's only on a connection Sharko owns.
		{argosecrets.LabelManagedBy, ScopeFull, true},
		{argosecrets.LabelSecretType, ScopeFull, true},
		{argosecrets.LabelManagedBy, ScopeLimited, true},
		{argosecrets.LabelManagedBy, ScopeAddonLabelsOnly, false},
		{argosecrets.LabelSecretType, ScopeAddonLabelsOnly, false},

		// Connectivity check — same rule, both key spellings.
		{models.LabelConnectivityCheck, ScopeFull, true},
		{models.LabelConnectivityCheckLegacy, ScopeFull, true},
		{models.LabelConnectivityCheck, ScopeAddonLabelsOnly, false},
		{models.LabelConnectivityCheckLegacy, ScopeAddonLabelsOnly, false},

		// Foreign qualified labels — never Sharko's.
		{"app.kubernetes.io/instance", ScopeFull, false},
		{"example.com/team", ScopeFull, false},
		{"argocd.argoproj.io/something-else", ScopeFull, false},

		// Empty key is never a label.
		{"", ScopeFull, false},
	}
	for _, tt := range tests {
		t.Run(tt.key+"/"+string(tt.scope), func(t *testing.T) {
			if got := ownedLabelKey(tt.key, tt.scope); got != tt.want {
				t.Errorf("ownedLabelKey(%q, %q) = %v, want %v", tt.key, tt.scope, got, tt.want)
			}
		})
	}
}

// TestComparedAnnotationsIsDeliberatelyEmpty pins the CC2-2 finding: not one
// annotation Sharko writes onto a connection Secret has a stable expected
// value, so the compared-annotation set is empty on purpose. Adding one must
// be a deliberate act with its own reasoning, not a drift.
func TestComparedAnnotationsIsDeliberatelyEmpty(t *testing.T) {
	if len(comparedAnnotationKeys) != 0 {
		t.Fatalf("comparedAnnotationKeys = %v, want empty. Every annotation Sharko writes changes on "+
			"every write (written-at, revision, source-file) or records a one-off event (adopted, "+
			"taken-over-at, takeover-preserved-labels). If you are adding one, say in the doc comment "+
			"what its stable expected value is and where it comes from.", comparedAnnotationKeys)
	}
}

// TestIgnoredAnnotationsCoverEveryProvenanceKey makes sure the per-write
// provenance keys are all accounted for as ignored, so none of them can turn
// into reported drift. A timestamp is not drift.
func TestIgnoredAnnotationsCoverEveryProvenanceKey(t *testing.T) {
	ignored := map[string]bool{}
	for _, k := range ignoredAnnotationKeys {
		ignored[k] = true
	}
	for _, k := range []string{
		"sharko.dev/written-at",
		"sharko.dev/revision",
		"sharko.dev/source-file",
		"sharko.dev/adopted",
		"sharko.dev/taken-over-at",
		"sharko.dev/takeover-preserved-labels",
	} {
		if !ignored[k] {
			t.Errorf("annotation %q is not in ignoredAnnotationKeys", k)
		}
	}
}

func TestVolatileMetadataFieldsListed(t *testing.T) {
	joined := strings.Join(volatileMetadataFields, ",")
	for _, want := range []string{"resourceVersion", "uid", "generation", "creationTimestamp", "managedFields"} {
		if !strings.Contains(joined, want) {
			t.Errorf("volatileMetadataFields is missing %q: %v", want, volatileMetadataFields)
		}
	}
}

func TestLabelSkipSet_TakeoverPreservedLabels(t *testing.T) {
	live := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				argosecrets.AnnotationTakeoverPreservedLabels: "old-tool-label,another.example.com/thing",
			},
		},
	}
	skip := labelSkipSet(live)
	if !skip["old-tool-label"] {
		t.Error("an unqualified preserved label must be skipped — IsAddonLabelKey would otherwise call it Sharko's own")
	}
	if !skip["another.example.com/thing"] {
		t.Error("a qualified preserved label must be skipped")
	}

	if got := labelSkipSet(nil); got != nil {
		t.Errorf("labelSkipSet(nil) = %v, want nil", got)
	}
	if got := labelSkipSet(&corev1.Secret{}); got != nil {
		t.Errorf("labelSkipSet with no annotations = %v, want nil", got)
	}
}

func TestSensitiveDataKeys(t *testing.T) {
	if !sensitiveDataKeys[dataKeyConfig] {
		t.Error("data.config holds the credential blob and must be sensitive")
	}
	if sensitiveDataKeys[dataKeyName] || sensitiveDataKeys[dataKeyServer] {
		t.Error("data.name and data.server are already returned by other endpoints; marking them sensitive would hide facts for no gain")
	}
}

// TestSensitiveDifference_HasNoSides is the structural half of the hard rule:
// a sensitive field's JSON must carry NO expected property and NO live
// property. Not empty strings — the keys must be absent.
func TestSensitiveDifference_HasNoSides(t *testing.T) {
	for _, status := range []FieldStatus{FieldSame, FieldDifferent, FieldMissing, FieldUnexpected} {
		d := sensitiveDifference(FieldPathDataConfig, status)
		raw, err := json.Marshal(d)
		if err != nil {
			t.Fatalf("marshalling: %v", err)
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("unmarshalling: %v", err)
		}
		if _, ok := m["expected"]; ok {
			t.Errorf("status %q: sensitive difference carries an \"expected\" key: %s", status, raw)
		}
		if _, ok := m["live"]; ok {
			t.Errorf("status %q: sensitive difference carries a \"live\" key: %s", status, raw)
		}
		if _, ok := m["sensitive"]; !ok {
			t.Errorf("status %q: sensitive difference does not say it is sensitive: %s", status, raw)
		}
	}
}

// TestSensitiveDifference_DropsSidesEvenIfSomebodyFillsThemIn covers the case
// the constructors cannot: a struct literal built by hand later, with values
// in it. MarshalJSON must still drop both sides.
func TestSensitiveDifference_DropsSidesEvenIfSomebodyFillsThemIn(t *testing.T) {
	d := Difference{
		Path:      FieldPathDataConfig,
		Status:    FieldDifferent,
		Sensitive: true,
		Expected:  strPtr("a-value-that-must-never-ship"),
		Live:      strPtr("another-value-that-must-never-ship"),
	}
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}
	if strings.Contains(string(raw), "must-never-ship") {
		t.Fatalf("a hand-built sensitive Difference leaked its values: %s", raw)
	}
}

func TestSafeDifference_KeepsBothSides(t *testing.T) {
	d := safeDifference(FieldPathDataServer, FieldDifferent, strPtr("https://expected.example"), strPtr("https://live.example"))
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}
	if !strings.Contains(string(raw), "https://expected.example") || !strings.Contains(string(raw), "https://live.example") {
		t.Fatalf("a safe difference must show both sides: %s", raw)
	}
}

func TestSortDifferences_StableOrder(t *testing.T) {
	build := func() []Difference {
		return []Difference{
			safeDifference(FieldPathDataServer, FieldDifferent, strPtr("a"), strPtr("b")),
			safeDifference(labelFieldPath("zebra"), FieldMissing, strPtr("enabled"), nil),
			sensitiveDifference(FieldPathDataConfig, FieldDifferent),
			safeDifference(labelFieldPath("alpha"), FieldUnexpected, nil, strPtr("enabled")),
			safeDifference(FieldPathSecretType, FieldDifferent, strPtr("Opaque"), strPtr("kubernetes.io/tls")),
		}
	}
	first := build()
	sortDifferences(first)
	for i := 0; i < 20; i++ {
		again := build()
		// Shuffle deterministically by reversing, so the input order differs.
		for l, r := 0, len(again)-1; l < r; l, r = l+1, r-1 {
			again[l], again[r] = again[r], again[l]
		}
		sortDifferences(again)
		for j := range first {
			if first[j].Path != again[j].Path {
				t.Fatalf("ordering is not stable: run %d position %d has %q, first run had %q", i, j, again[j].Path, first[j].Path)
			}
		}
	}
}
