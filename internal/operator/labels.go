package operator

import (
	v1alpha1 "github.com/MoranWeissman/sharko/api/v1alpha1"
	"github.com/MoranWeissman/sharko/internal/models"
)

// AddonAssignmentsToLabels converts a list of AddonAssignment (from a
// ClusterAddons CR spec) into the addon-label map that should be written to
// the ArgoCD cluster Secret. This is the INVERSE of the generator's
// mapLabelsToAddonAssignments (generator.go:300) — they must round-trip
// exactly so Git↔CR↔Secret stays consistent.
//
// Rules (mirroring the reconciler's desiredAddonLabels at reconciler.go:822):
//   - For each assignment with Enabled == nil OR Enabled == true: emit an
//     addon label with key = Name, value = models.LabelEnabled ("enabled").
//   - For assignments with Enabled == false: SKIP (no label emitted) — the
//     "enabled"/"disabled" vocabulary is the canonical on/off, and ArgoCD's
//     ApplicationSet selector treats ONLY "enabled" as on (any other value or
//     absence reads as off). Emitting "disabled" would be redundant; absence
//     is clearer.
//   - Version handling: OMIT version from the label. Per-cluster version
//     overrides live in the values file (not in the CR spec and not in the
//     cluster Secret labels — the reconciler/generator never write version to
//     labels).
//   - Normalization: run every value through models.NormalizeAddonLabelValue
//     (the SAME helper the reconciler uses) so legacy "true"/"false" self-heals.
//
// Exported so Story 2.4's round-trip test can verify this and the generator's
// inverse compose back to the same label set.
func AddonAssignmentsToLabels(addons []v1alpha1.AddonAssignment) map[string]string {
	if len(addons) == 0 {
		return nil
	}

	labels := make(map[string]string, len(addons))
	for _, addon := range addons {
		// SKIP explicitly disabled addons (Enabled == false). Default (nil) is on.
		if addon.Enabled != nil && !*addon.Enabled {
			continue
		}

		// Emit the addon label: key = addon name, value = "enabled".
		value := models.LabelEnabled

		// Normalize the value (self-heal legacy "true"/"false" — though in
		// practice the generator never emits those, this is defensive +
		// matches reconciler.go:826).
		if normalized, changed := models.NormalizeAddonLabelValue(value); changed {
			value = normalized
		}

		labels[addon.Name] = value
	}

	return labels
}
