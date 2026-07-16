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
const LabelConnectivityCheck = "sharko.dev/connectivity-check"

// LabelConnectivityCheckLegacy is the pre-V2-cleanup-59 key
// (sharko.io — a domain the project never owned). Sharko WRITES only
// LabelConnectivityCheck; the legacy key is recognised on READ so live
// cluster Secrets stamped before the rename are handled correctly:
//
//   - The adopted / self-managed guest gates strip BOTH keys, so a lingering
//     legacy label can never keep selecting a guest cluster into the check
//     ApplicationSet.
//   - Sharko-created Secrets migrate automatically: Manager.Ensure replaces
//     the full label set on its first pass after upgrade (write-new +
//     remove-old), so the legacy key disappears from Sharko-owned Secrets
//     without any dedicated migration code.
//
// Transition note for EXISTING repos: the connectivity-check ApplicationSet
// rendered into a pre-rename repo selects on the legacy key. Once the
// reconciler migrates Secret labels to the new key, that old-selector
// ApplicationSet stops matching and ArgoCD prunes the check Applications —
// cluster status falls back to ArgoCD's own connection status until the
// repo's bootstrap templates are refreshed. Bounded to zero-addon clusters;
// no data or connectivity is lost.
const LabelConnectivityCheckLegacy = "sharko.io/connectivity-check"

// HasConnectivityCheckLabel reports whether labels carry the connectivity-
// check key under EITHER the canonical or the legacy name. nil-safe.
// Readers must use this predicate rather than a direct map lookup so
// pre-rename Secrets keep being recognised (V2-cleanup-59).
func HasConnectivityCheckLabel(labels map[string]string) bool {
	if labels == nil {
		return false
	}
	if _, ok := labels[LabelConnectivityCheck]; ok {
		return true
	}
	_, ok := labels[LabelConnectivityCheckLegacy]
	return ok
}

// RemoveConnectivityCheckLabels deletes BOTH the canonical and the legacy
// connectivity-check keys from labels. Mutates in place. nil-safe.
// Used by every strip site (adopted gate, self-managed guest sync) so a
// legacy key can never survive a strip.
func RemoveConnectivityCheckLabels(labels map[string]string) {
	if labels == nil {
		return
	}
	delete(labels, LabelConnectivityCheck)
	delete(labels, LabelConnectivityCheckLegacy)
}

// ApplyConnectivityCheckLabel sets or removes the connectivity-check label
// on labels (the working copy of a cluster Secret's labels before it is
// written to the API server). Mutates in place. nil-safe.
//
// Logic:
//   - If featureOn AND the map has ZERO entries whose value equals
//     LabelEnabled (the canonical "addon is on" value), EXCLUDING the
//     connectivity-check keys themselves (they are not addon labels),
//     → set labels[LabelConnectivityCheck] = LabelEnabled.
//   - Otherwise → remove the connectivity-check keys.
//
// Only the canonical LabelConnectivityCheck is ever WRITTEN; the legacy key
// is removed on every call so a Secret that carried the pre-rename key
// converges to exactly one (new) key on the next write (V2-cleanup-59).
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
		RemoveConnectivityCheckLabels(labels)
		return
	}
	// Count addon labels: entries whose value equals LabelEnabled, excluding
	// the connectivity-check keys themselves (which would create a
	// self-referential loop) and the standard system labels that are never
	// addon-enablement keys.
	enabledCount := 0
	for k, v := range labels {
		if k == LabelConnectivityCheck || k == LabelConnectivityCheckLegacy {
			continue // exclude self (both spellings)
		}
		if AddonLabelEnabled(v) {
			enabledCount++
		}
	}
	if enabledCount == 0 {
		// W4b (V3 RW1.8): Stamp BOTH the canonical AND the legacy key
		// transitionally so ANY ApplicationSet selector (old or new) matches.
		// This makes the sharko.io → sharko.dev label rename non-stranding:
		// existing appsets with the old selector keep working, new appsets with
		// the new selector work immediately, and once all appsets are upgraded
		// this transitional double-stamp can be removed in a future cleanup.
		labels[LabelConnectivityCheck] = LabelEnabled
		labels[LabelConnectivityCheckLegacy] = LabelEnabled
	} else {
		RemoveConnectivityCheckLabels(labels)
	}
}
