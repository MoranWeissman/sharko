package models

import "strings"

// Addon-enablement label vocabulary — ONE source of truth.
//
// "Is this addon on for this cluster?" is recorded as a string label in two
// places that must agree:
//
//   - the per-cluster `labels:` block in configuration/managed-clusters.yaml
//   - the ArgoCD cluster Secret's metadata.labels (which the reconciler
//     copies verbatim from the managed-clusters entry)
//
// The live ArgoCD ApplicationSet selector and config.GetEnabledAddons both
// treat ONLY the literal word "enabled" as on (see the chart's
// templates/bootstrap/templates/_helpers.tpl `eq (index .metadata.labels
// "<name>") "enabled"` and internal/config/parser.go's
// `EqualFold(labelValue, "enabled")`). Any other value — including the
// legacy "true" — reads as off, so the addon never deploys.
//
// Therefore the canonical on/off vocabulary at BOTH layers is
// "enabled"/"disabled". `bool` is the API/request type only; convert at the
// boundary with AddonLabelValue. Read a stored label back with
// AddonLabelEnabled. Do NOT hand-write "true"/"false"/"enabled"/"disabled"
// for addon labels anywhere else — route every read and write through these
// helpers so the representation can never drift again.
const (
	// LabelEnabled is the canonical "addon is on" label value.
	LabelEnabled = "enabled"
	// LabelDisabled is the canonical "addon is off" label value.
	LabelDisabled = "disabled"
)

// AddonLabelValue maps the API/request bool to the canonical label string
// written to managed-clusters.yaml and the ArgoCD cluster Secret.
func AddonLabelValue(enabled bool) string {
	if enabled {
		return LabelEnabled
	}
	return LabelDisabled
}

// AddonLabelEnabled reports whether a stored addon-label value means "on".
// Only the canonical "enabled" (case-insensitive) counts as on — this is
// the exact predicate the ArgoCD selector and GetEnabledAddons apply, so
// callers that gate behaviour on it stay in lockstep with what actually
// deploys. The legacy "true" is intentionally NOT treated as on here; it is
// normalised to "enabled" on the next write (see NormalizeAddonLabelValue).
func AddonLabelEnabled(value string) bool {
	return strings.EqualFold(value, LabelEnabled)
}

// NormalizeAddonLabelValue upgrades a legacy addon-label value to the
// canonical vocabulary so already-registered clusters self-heal on the next
// write. The legacy "true"/"false" booleans (case-insensitive) become
// "enabled"/"disabled"; values that are already canonical (or any other
// unrecognised value) are returned unchanged. ok reports whether a
// normalisation actually happened, so callers can decide whether a rewrite
// is worth it.
func NormalizeAddonLabelValue(value string) (normalized string, ok bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true":
		return LabelEnabled, true
	case "false":
		return LabelDisabled, true
	default:
		return value, false
	}
}
