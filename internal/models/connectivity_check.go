package models

// LabelConnectivityCheck is the cluster-Secret label key Sharko sets on
// newly-registered clusters with zero enabled addons when the connectivity-
// check feature is on. The label selects those clusters into the
// connectivity-check ApplicationSet (templates/bootstrap/templates/
// connectivity-check-appset.yaml), which deploys a harmless ConfigMap
// through ArgoCD. Synced+Healthy = end-to-end proof; failed deploy =
// diagnosable reason.
//
// Rules:
//   - Only Sharko-REGISTERED clusters with zero enabled addons carry this
//     label. ADOPTED clusters never carry it — the adopted gate in
//     internal/argosecrets/manager.go strips it before any write.
//   - The label is DERIVED at secret-build time and NEVER written to
//     managed-clusters.yaml (no Git churn, no schema regen).
//   - First addon enabled → label removed → ApplicationSet no longer
//     selects the cluster → ArgoCD cascade-deletes the Application.
//   - All addons later disabled → count returns to 0 → label returns
//     (deterministic, self-healing).
const LabelConnectivityCheck = "sharko.io/connectivity-check"

// ApplyConnectivityCheckLabel sets or removes the connectivity-check label
// on labels (the working copy of a cluster Secret's labels before it is
// written to the API server). Mutates in place. nil-safe.
//
// Logic:
//   - If featureOn AND the map has ZERO entries whose value equals
//     LabelEnabled (the canonical "addon is on" value), EXCLUDING the
//     LabelConnectivityCheck key itself (it is not an addon label),
//     → set labels[LabelConnectivityCheck] = LabelEnabled.
//   - Otherwise → delete(labels, LabelConnectivityCheck).
//
// This means:
//   - Feature disabled → label always absent.
//   - Feature enabled + cluster has at least one addon enabled → label
//     absent (check auto-removes itself on first addon).
//   - Feature enabled + zero addons enabled → label present (check
//     active).
//
// Non-addon labels (e.g. region) carry arbitrary values, not LabelEnabled,
// so they do not count toward the "enabled addons" tally.
func ApplyConnectivityCheckLabel(labels map[string]string, featureOn bool) {
	if labels == nil {
		return
	}
	if !featureOn {
		delete(labels, LabelConnectivityCheck)
		return
	}
	// Count addon labels: entries whose value equals LabelEnabled, excluding
	// the connectivity-check key itself (which would create a self-referential
	// loop) and the standard system labels that are never addon-enablement keys.
	enabledCount := 0
	for k, v := range labels {
		if k == LabelConnectivityCheck {
			continue // exclude self
		}
		if AddonLabelEnabled(v) {
			enabledCount++
		}
	}
	if enabledCount == 0 {
		labels[LabelConnectivityCheck] = LabelEnabled
	} else {
		delete(labels, LabelConnectivityCheck)
	}
}
