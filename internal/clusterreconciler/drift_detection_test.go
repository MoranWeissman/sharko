package clusterreconciler

import (
	"testing"

	"github.com/MoranWeissman/sharko/internal/models"
)

// TestComputeLabelDrift_NoD rift verifies computeLabelDrift returns nil when
// git-desired and live labels match perfectly (V3 G1 — drift detection).
func TestComputeLabelDrift_NoDrift(t *testing.T) {
	desired := map[string]string{
		"addon-foo": "enabled",
		"addon-bar": "disabled",
	}
	// Live Secret has same addon labels plus ownership labels
	have := map[string]string{
		"addon-foo":                        "enabled",
		"addon-bar":                        "disabled",
		LabelManagedBy:                     LabelValueSharko,
		models.LabelConnectivityCheck:      "enabled",
	}

	drift := computeLabelDrift(desired, have)

	if drift != nil {
		t.Errorf("expected nil drift when labels match, got %+v", drift)
	}
}

// TestComputeLabelDrift_Added verifies keys present in git but missing from
// live Secret are reported as Added.
func TestComputeLabelDrift_Added(t *testing.T) {
	desired := map[string]string{
		"addon-foo": "enabled",
		"addon-bar": "enabled",
		"addon-baz": "disabled",
	}
	// Live Secret is missing addon-baz
	have := map[string]string{
		"addon-foo":                   "enabled",
		"addon-bar":                   "enabled",
		LabelManagedBy:                LabelValueSharko,
		models.LabelConnectivityCheck: "enabled",
	}

	drift := computeLabelDrift(desired, have)

	if drift == nil {
		t.Fatal("expected non-nil drift when labels differ")
	}
	if len(drift.Added) != 1 || drift.Added[0] != "addon-baz" {
		t.Errorf("expected Added=['addon-baz'], got %v", drift.Added)
	}
	if len(drift.Removed) != 0 {
		t.Errorf("expected no Removed, got %v", drift.Removed)
	}
	if len(drift.Changed) != 0 {
		t.Errorf("expected no Changed, got %v", drift.Changed)
	}
}

// TestComputeLabelDrift_Removed verifies keys present on live Secret but
// missing from git are reported as Removed.
func TestComputeLabelDrift_Removed(t *testing.T) {
	desired := map[string]string{
		"addon-foo": "enabled",
	}
	// Live Secret has extra addon keys
	have := map[string]string{
		"addon-foo":                   "enabled",
		"addon-bar":                   "disabled",
		"addon-baz":                   "enabled",
		LabelManagedBy:                LabelValueSharko,
		models.LabelConnectivityCheck: "enabled",
	}

	drift := computeLabelDrift(desired, have)

	if drift == nil {
		t.Fatal("expected non-nil drift when labels differ")
	}
	if len(drift.Added) != 0 {
		t.Errorf("expected no Added, got %v", drift.Added)
	}
	if len(drift.Removed) != 2 {
		t.Errorf("expected 2 Removed, got %d: %v", len(drift.Removed), drift.Removed)
	}
	// Check both keys are present (order doesn't matter)
	removed := make(map[string]bool)
	for _, k := range drift.Removed {
		removed[k] = true
	}
	if !removed["addon-bar"] || !removed["addon-baz"] {
		t.Errorf("expected Removed to contain addon-bar and addon-baz, got %v", drift.Removed)
	}
	if len(drift.Changed) != 0 {
		t.Errorf("expected no Changed, got %v", drift.Changed)
	}
}

// TestComputeLabelDrift_Changed verifies keys present in both but with
// different values are reported as Changed.
func TestComputeLabelDrift_Changed(t *testing.T) {
	desired := map[string]string{
		"addon-foo": "enabled",
		"addon-bar": "disabled",
	}
	// Live Secret has different values
	have := map[string]string{
		"addon-foo":                   "disabled", // changed
		"addon-bar":                   "enabled",  // changed
		LabelManagedBy:                LabelValueSharko,
		models.LabelConnectivityCheck: "enabled",
	}

	drift := computeLabelDrift(desired, have)

	if drift == nil {
		t.Fatal("expected non-nil drift when labels differ")
	}
	if len(drift.Added) != 0 {
		t.Errorf("expected no Added, got %v", drift.Added)
	}
	if len(drift.Removed) != 0 {
		t.Errorf("expected no Removed, got %v", drift.Removed)
	}
	if len(drift.Changed) != 2 {
		t.Errorf("expected 2 Changed, got %d: %v", len(drift.Changed), drift.Changed)
	}
	// Check both keys are present
	changed := make(map[string]bool)
	for _, k := range drift.Changed {
		changed[k] = true
	}
	if !changed["addon-foo"] || !changed["addon-bar"] {
		t.Errorf("expected Changed to contain addon-foo and addon-bar, got %v", drift.Changed)
	}
}

// TestComputeLabelDrift_Combined verifies all three diff types can coexist.
func TestComputeLabelDrift_Combined(t *testing.T) {
	desired := map[string]string{
		"addon-foo": "enabled",  // changed (live has disabled)
		"addon-bar": "disabled", // unchanged
		"addon-new": "enabled",  // added (not on live)
	}
	have := map[string]string{
		"addon-foo":                   "disabled", // changed
		"addon-bar":                   "disabled", // unchanged
		"addon-old":                   "enabled",  // removed (not in desired)
		LabelManagedBy:                LabelValueSharko,
		models.LabelConnectivityCheck: "enabled",
	}

	drift := computeLabelDrift(desired, have)

	if drift == nil {
		t.Fatal("expected non-nil drift")
	}
	if len(drift.Added) != 1 || drift.Added[0] != "addon-new" {
		t.Errorf("expected Added=['addon-new'], got %v", drift.Added)
	}
	if len(drift.Removed) != 1 || drift.Removed[0] != "addon-old" {
		t.Errorf("expected Removed=['addon-old'], got %v", drift.Removed)
	}
	if len(drift.Changed) != 1 || drift.Changed[0] != "addon-foo" {
		t.Errorf("expected Changed=['addon-foo'], got %v", drift.Changed)
	}
}

// TestComputeLabelDrift_IgnoresOwnershipLabels verifies that ownership
// bookkeeping labels (managed-by, connectivity-check) are excluded from the
// comparison — only addon labels matter.
func TestComputeLabelDrift_IgnoresOwnershipLabels(t *testing.T) {
	desired := map[string]string{
		"addon-foo": "enabled",
	}
	// Live Secret has ownership labels + addon labels
	have := map[string]string{
		"addon-foo":                   "enabled",
		LabelManagedBy:                LabelValueSharko,
		models.LabelConnectivityCheck: "enabled",
	}

	drift := computeLabelDrift(desired, have)

	// Ownership labels should NOT appear in Removed even though they're not
	// in desired — they're excluded from the comparison by design.
	if drift != nil {
		t.Errorf("expected nil drift (ownership labels ignored), got %+v", drift)
	}
}

// TestComputeLabelDrift_EmptyLive verifies the edge case where the live
// Secret has no labels at all.
func TestComputeLabelDrift_EmptyLive(t *testing.T) {
	desired := map[string]string{
		"addon-foo": "enabled",
		"addon-bar": "disabled",
	}
	have := map[string]string{} // empty live labels

	drift := computeLabelDrift(desired, have)

	if drift == nil {
		t.Fatal("expected non-nil drift")
	}
	if len(drift.Added) != 2 {
		t.Errorf("expected 2 Added (all desired keys), got %d: %v", len(drift.Added), drift.Added)
	}
	if len(drift.Removed) != 0 {
		t.Errorf("expected no Removed, got %v", drift.Removed)
	}
	if len(drift.Changed) != 0 {
		t.Errorf("expected no Changed, got %v", drift.Changed)
	}
}

// TestComputeLabelDrift_EmptyDesired verifies the edge case where git wants
// no addon labels (empty desired map).
func TestComputeLabelDrift_EmptyDesired(t *testing.T) {
	desired := map[string]string{} // no addon labels wanted
	have := map[string]string{
		"addon-foo":                   "enabled",
		"addon-bar":                   "disabled",
		LabelManagedBy:                LabelValueSharko,
		models.LabelConnectivityCheck: "enabled",
	}

	drift := computeLabelDrift(desired, have)

	if drift == nil {
		t.Fatal("expected non-nil drift")
	}
	if len(drift.Added) != 0 {
		t.Errorf("expected no Added, got %v", drift.Added)
	}
	if len(drift.Removed) != 2 {
		t.Errorf("expected 2 Removed (all live addon keys), got %d: %v", len(drift.Removed), drift.Removed)
	}
	if len(drift.Changed) != 0 {
		t.Errorf("expected no Changed, got %v", drift.Changed)
	}
}
