package models

import "time"

// OrphanedSecret is one leftover values secret the addon-values engine
// found on a cluster: it carries Sharko's ownership label AND the addon
// provenance annotation (see remoteclient.AnnotationAddon), but nothing in
// the current desired-state plan claims it any more — usually because the
// catalog entry (or its push block) that used to ask for it was removed or
// hand-deleted from Git, while the Secret itself was left behind on the
// cluster.
//
// This type is the shared shape internal/secrets (which finds these) and
// internal/api (which lists and deletes them) both use, so that api can
// report on them without importing secrets and secrets can be tested
// without importing api — the same import-free-boundary pattern every
// other cross-package status type in this codebase follows (see
// internal/secrets.ItemRecord's sibling accessors on the Reconciler).
type OrphanedSecret struct {
	// Cluster is the display name of the cluster the secret lives on.
	Cluster string `json:"cluster"`
	// Namespace is the K8s namespace the secret lives in.
	Namespace string `json:"namespace"`
	// Name is the K8s Secret's own name.
	Name string `json:"name"`
	// Addon is the addon name read off the secret's sharko.dev/addon
	// provenance annotation at scan time — never guessed, and never
	// present on a row unless the annotation itself was present (the
	// safety pin that keeps cluster-registration kubeconfig secrets and
	// ArgoCD connection secrets, which carry only the managed-by label,
	// out of this list entirely).
	Addon string `json:"addon"`
	// LastChecked is when the scan that most recently confirmed this
	// secret is still an orphan ran. A cluster whose scan failed carries
	// its previous records — and their LastChecked timestamps — forward
	// unchanged, so this is never fabricated or advanced without a real
	// look at the cluster.
	LastChecked time.Time `json:"last_checked"`
}
