package secrets

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/MoranWeissman/sharko/internal/credsafe"
	"github.com/MoranWeissman/sharko/internal/logging"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/remoteclient"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// orphans.go — leftover-secrets Story 1.1. Sharko once wrote a values
// secret for an addon; somebody hand-deleted that addon's push definition
// from the catalog in Git without ever asking Sharko to clean the secret
// up. The secret keeps sitting on the cluster, still carrying Sharko's own
// labels, invisible to every list this engine already builds — because
// every one of them is built FROM the current plan, and the plan no longer
// mentions it at all.
//
// This file is the other direction: instead of "here is what the plan
// wants, go make it true", it asks "here is what is actually out there,
// does the plan still want it?" — and reports what's left over. It NEVER
// deletes anything itself; DeleteOrphanedSecret below is a separate,
// explicitly human-triggered path with its own re-verification, called
// only from the operator-gated DELETE endpoint (internal/api/orphaned_secret.go).
//
// THE SAFETY DECISION this whole file rests on: a live secret is a
// candidate ONLY if it carries BOTH app.kubernetes.io/managed-by=sharko
// AND the sharko.dev/addon provenance annotation. The label alone is not
// enough — cluster-registration kubeconfig secrets (providers.
// KubernetesSecretProvider) and ArgoCD connection secrets
// (internal/clusterreconciler) also carry that label, and offering to
// delete one of THOSE would take down a cluster connection, not clean up a
// leftover. Honest trade-off, accepted: a values secret written before the
// provenance annotation existed is invisible to this scan — it looks
// exactly like a non-values managed secret from the outside, and there is
// no way to tell them apart without the annotation.

// ErrOrphanUnknown means the (cluster, namespace, name) a delete request
// named is not a currently-known orphan record — either it was never one,
// or it already stopped being one (deleted already, or reclaimed and since
// removed from the record by a later scan). The caller gets a 404: there is
// nothing here to delete.
var ErrOrphanUnknown = errors.New("no leftover secret is on record for that cluster, namespace, and name")

// ErrOrphanReclaimed means a FRESH read of the addon catalog and managed
// clusters list — done at delete time, not from the stale scan record —
// shows some addon's push definition claims this (namespace, name) on this
// cluster again. Between the last scan and this click, the addon was
// re-added (or never really removed) — this is no longer a leftover, and
// deleting it would delete something the values engine is about to write
// right back.
var ErrOrphanReclaimed = errors.New("this secret is claimed by the current catalog again — it is no longer a leftover, so Sharko will not delete it")

// ErrOrphanRefused means the live secret's ownership evidence changed
// between the scan that found it and this delete attempt: either its
// sharko.dev/addon provenance annotation is gone, or DeleteSecretIfManaged's
// own label gate refused it (remoteclient.ErrForeignSecret). Either way,
// what Sharko is looking at right now no longer looks like the leftover it
// recorded, so it refuses rather than guess.
var ErrOrphanRefused = errors.New("this secret's ownership marker has changed since it was found — Sharko will not delete it")

// scanOrphans finds every leftover values secret across every cluster the
// just-computed plan knows about, and replaces this engine's per-cluster
// orphan records with what it found — one cluster at a time, so a single
// unreachable cluster never touches any other cluster's records (locked
// decision #10).
//
// Called at the end of reconcile()'s per-item loop and at the end of
// CheckAll() — never inline with a single item's check or push, and never
// from a list read: GET /system/managed-secrets (OrphanedSecrets below)
// only ever reads the LAST scan's results, in memory, no K8s call of its
// own (locked decision #2).
func (r *Reconciler) scanOrphans(ctx context.Context, plan pushPlan) {
	log := logging.LoggerFromContext(ctx)

	// claimed[cluster][namespace/name] is what THIS pass's plan still
	// wants pushed there — subtracted from what LIST finds so a secret a
	// live push definition still claims is never reported as an orphan
	// (locked decision #4).
	claimed := make(map[string]map[string]bool, len(plan.clusters))
	for _, w := range plan.work {
		if claimed[w.clusterName] == nil {
			claimed[w.clusterName] = make(map[string]bool)
		}
		claimed[w.clusterName][w.push.Namespace+"/"+w.push.SecretName] = true
	}

	for _, c := range plan.clusters {
		creds, err := r.credProvider.GetCredentials(c.credLookup)
		if err != nil {
			// A credentials-backend error's own text never reaches this log
			// line; credsafe.Sentence swaps in the fixed sentence for exactly
			// that case and leaves every other error alone.
			log.Warn("[secrets] orphan scan: could not get credentials — keeping this cluster's previous leftover-secret records",
				"cluster", c.name, "error", credsafe.Sentence(err))
			continue
		}
		client, err := r.remoteClientFn(creds.Raw)
		if err != nil {
			log.Warn("[secrets] orphan scan: could not connect — keeping this cluster's previous leftover-secret records",
				"cluster", c.name, "error", err)
			continue
		}

		infos, err := remoteclient.ListManagedSecrets(ctx, client, "")
		if err != nil {
			log.Warn("[secrets] orphan scan: could not list secrets — keeping this cluster's previous leftover-secret records",
				"cluster", c.name, "error", err)
			continue
		}

		now := time.Now()
		found := make([]models.OrphanedSecret, 0)
		for _, info := range infos {
			if info.Addon == "" {
				// The kubeconfig-secret / ArgoCD-connection-secret safety
				// pin — see this file's header comment. The managed-by
				// label alone proves nothing about what kind of secret
				// this is.
				continue
			}
			if claimed[c.name][info.Namespace+"/"+info.Name] {
				continue
			}
			found = append(found, models.OrphanedSecret{
				Cluster:     c.name,
				Namespace:   info.Namespace,
				Name:        info.Name,
				Addon:       info.Addon,
				LastChecked: now,
			})
		}

		r.orphanMu.Lock()
		if r.orphanRecords == nil {
			r.orphanRecords = make(map[string][]models.OrphanedSecret)
		}
		r.orphanRecords[c.name] = found
		r.orphanMu.Unlock()

		if len(found) > 0 {
			log.Info("[secrets] orphan scan found leftover secrets", "cluster", c.name, "count", len(found))
		}
	}
}

// OrphanedSecrets returns a deterministic, sorted snapshot of every orphan
// candidate this engine currently knows about — a pure in-memory read, the
// same shape GET /system/managed-secrets needs (locked decision #2: never a
// K8s call of its own). Sorted by cluster, then namespace, then name, so
// paging through unchanged data never reshuffles a row.
func (r *Reconciler) OrphanedSecrets() []models.OrphanedSecret {
	r.orphanMu.RLock()
	defer r.orphanMu.RUnlock()
	out := make([]models.OrphanedSecret, 0)
	for _, list := range r.orphanRecords {
		out = append(out, list...)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Cluster != out[j].Cluster {
			return out[i].Cluster < out[j].Cluster
		}
		if out[i].Namespace != out[j].Namespace {
			return out[i].Namespace < out[j].Namespace
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// OrphanedSecretCount is the total number of orphan candidates this engine
// currently holds across every cluster — used for the
// sharko_managed_secrets_state "orphaned" gauge without flattening and
// sorting the full snapshot just to count it.
func (r *Reconciler) OrphanedSecretCount() int {
	r.orphanMu.RLock()
	defer r.orphanMu.RUnlock()
	n := 0
	for _, list := range r.orphanRecords {
		n += len(list)
	}
	return n
}

// isKnownOrphan reports whether (cluster, namespace, name) is currently on
// record as an orphan, and the addon name recorded for it (for logging
// only — never trusted for anything security-relevant, which is exactly
// why DeleteOrphanedSecret re-verifies everything below instead of trusting
// this record).
func (r *Reconciler) isKnownOrphan(cluster, namespace, name string) (addon string, ok bool) {
	r.orphanMu.RLock()
	defer r.orphanMu.RUnlock()
	for _, rec := range r.orphanRecords[cluster] {
		if rec.Namespace == namespace && rec.Name == name {
			return rec.Addon, true
		}
	}
	return "", false
}

// forgetOrphan removes one record from the in-memory map — called only
// after a real, confirmed delete, so the row disappears from the next list
// read immediately (locked decision #8) rather than waiting for the next
// scan pass.
func (r *Reconciler) forgetOrphan(cluster, namespace, name string) {
	r.orphanMu.Lock()
	defer r.orphanMu.Unlock()
	list := r.orphanRecords[cluster]
	for i, rec := range list {
		if rec.Namespace == namespace && rec.Name == name {
			r.orphanRecords[cluster] = append(list[:i:i], list[i+1:]...)
			return
		}
	}
}

// DeleteOrphanedSecret deletes one leftover secret — the operator-gated,
// human-confirmed action behind DELETE
// /clusters/{name}/orphaned-secrets/{namespace}/{secret}
// (internal/api/orphaned_secret.go). The scan above only ever reports; this
// is the ONLY path in this engine that deletes anything, and it never
// trusts the scan's own record as proof — it re-verifies everything against
// the live world before touching Kubernetes (locked decision #7):
//
//  1. The record must currently be on file as an orphan — ErrOrphanUnknown
//     otherwise.
//  2. A FRESH planPushes read must still NOT claim this (namespace, name)
//     on this cluster — ErrOrphanReclaimed otherwise. The scan that found
//     this could be minutes old; the catalog may have changed since.
//  3. The live secret must still carry the sharko.dev/addon provenance
//     annotation (a Get, checked BEFORE the delete call) and still pass
//     remoteclient.DeleteSecretIfManaged's own managed-by label gate —
//     ErrOrphanRefused otherwise. Something could have re-labeled or
//     re-annotated it between the scan and this click.
//
// A secret that is simply already gone (NotFound on the pre-delete Get, or
// DeleteSecretIfManaged reporting deleted=false with no error) is treated
// as accomplished, not an error: the record is forgotten and this returns
// nil, the same as a delete that actually removed something.
func (r *Reconciler) DeleteOrphanedSecret(ctx context.Context, clusterName, namespace, name string) error {
	log := logging.LoggerFromContext(ctx)

	addon, known := r.isKnownOrphan(clusterName, namespace, name)
	if !known {
		return ErrOrphanUnknown
	}

	gr := r.gitReader()
	if gr == nil {
		return ErrNoGitConnection
	}
	plan, err := r.planPushes(ctx, gr)
	if err != nil {
		return fmt.Errorf("could not re-check the addon catalog and managed clusters list before deleting: %w", err)
	}
	for _, w := range plan.work {
		if w.clusterName == clusterName && w.push.Namespace == namespace && w.push.SecretName == name {
			return ErrOrphanReclaimed
		}
	}

	// The cluster's credential lookup key comes from this FRESH plan, never
	// from the stale orphan record (which carries no credential info of its
	// own) — falls back to the cluster's own name when a fresh read no
	// longer lists it (e.g. deregistered since the scan), matching the
	// no-SecretPath-override default everywhere else in this file.
	credLookup := clusterName
	for _, c := range plan.clusters {
		if c.name == clusterName {
			credLookup = c.credLookup
			break
		}
	}

	creds, err := r.credProvider.GetCredentials(credLookup)
	if err != nil {
		return fmt.Errorf("getting credentials for cluster %q: %w", clusterName, err)
	}
	client, err := r.remoteClientFn(creds.Raw)
	if err != nil {
		return fmt.Errorf("connecting to cluster %q: %w", clusterName, err)
	}

	existing, err := client.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			// Already gone — nothing left to delete. Forget the stale
			// record so the row disappears and report success.
			r.forgetOrphan(clusterName, namespace, name)
			return nil
		}
		return fmt.Errorf("checking secret %s/%s before deleting it: %w", namespace, name, err)
	}
	if existing.Annotations[remoteclient.AnnotationAddon] == "" {
		return ErrOrphanRefused
	}

	deleted, err := remoteclient.DeleteSecretIfManaged(ctx, client, namespace, name)
	if err != nil {
		if errors.Is(err, remoteclient.ErrForeignSecret) {
			return ErrOrphanRefused
		}
		return fmt.Errorf("deleting secret %s/%s: %w", namespace, name, err)
	}

	r.forgetOrphan(clusterName, namespace, name)
	log.Info("[secrets] deleted leftover secret",
		"cluster", clusterName, "namespace", namespace, "name", name, "addon", addon, "deleted", deleted)
	return nil
}
