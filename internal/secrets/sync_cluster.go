// sync_cluster.go — the git-backed engine behind "refresh this cluster's
// secrets now" (task #152, story 152.A).
//
// POST /api/v1/clusters/{name}/secrets/refresh used to deliver whatever an
// in-memory map said — a map an API caller could fill with any backend
// path and any destination, with no Git, no preview, no pull request. That
// was the exfiltration hole the Secret Sync security closure exists to
// shut. The refresh path now runs THIS code instead: the same
// planPushes → reconcileSecret pipeline the periodic timer uses, so both
// triggers read the same git-approved definitions, hit the same provider
// boundary, and go through the same destination ownership gate. There is
// deliberately NO second implementation of "work out what this cluster
// should get" — SyncCluster filters the one existing plan down to one
// cluster.
package secrets

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/MoranWeissman/sharko/internal/credsafe"
)

// ErrClusterNotInGit is what SyncCluster returns when the named cluster is
// not in the managed-clusters list in the connected Git repo. Like
// ErrForeignSecret, this text is Sharko's own fixed sentence — safe to
// show a caller verbatim, and internal/api matches on its wording to pick
// the HTTP status (the same substring convention
// addonValuesSecretSyncFailureSentence already uses).
var ErrClusterNotInGit = errors.New("this cluster is not in the managed clusters list in Git — nothing to refresh")

// ErrAddonNotInGit is what SyncCluster returns when a caller names an
// addon that Git does not define an addon-values secret for on this
// cluster. Same fixed-sentence contract as ErrClusterNotInGit above.
var ErrAddonNotInGit = errors.New("Git does not define an addon-values secret for this addon on this cluster — nothing to refresh")

// syncWork pushes one plan item right now — reconcileSecret plus the same
// per-item record bookkeeping and per-item audit callback the periodic
// pass keeps, factored out so SyncOne (one cluster+addon pair) and
// SyncCluster (every pair on one cluster) cannot drift apart on what "push
// this secret" means. Uses a throwaway *ReconcileStats scoped to this one
// call — never merged into r.lastStats, which reports the periodic pass's
// own counters.
//
// The returned outcome/err pair is reconcileSecret's own: a foreign secret
// comes back (ItemOutcomeForeign, nil) — a boundary, not a failure — and
// each caller decides what that means for its caller (SyncOne turns it
// into ErrForeignSecret; SyncCluster leaves the row alone and moves on).
func (r *Reconciler) syncWork(ctx context.Context, w secretWork) (ItemOutcome, error) {
	stats := &ReconcileStats{}
	outcome, syncErr := r.reconcileSecret(ctx, stats, w.clusterName, w.credLookup, w.addon, w.push)

	// credsafe.Sentence, not syncErr.Error(). This is the third writer of
	// ItemRecord.Error and it was the only one that skipped it — the periodic
	// pass (reconcile, "safeText") and the check path (checkWork's credentials
	// branch) both went through credsafe already. An inconsistency between
	// three writers of one field in one package is an oversight, not a
	// decision, so this one now matches. A credentials or secrets-backend
	// error becomes the fixed sentence; every other error keeps its full text,
	// which is what the API layer's stage-matching still reads.
	errMsg := ""
	if syncErr != nil {
		errMsg = credsafe.Sentence(syncErr)
	}

	key := ItemKey{Cluster: w.clusterName, Addon: w.addon}
	r.mu.Lock()
	if r.itemRecords == nil {
		r.itemRecords = make(map[ItemKey]ItemRecord)
	}
	prev := r.itemRecords[key]
	rec := ItemRecord{LastChecked: time.Now(), Outcome: outcome, Error: errMsg, ChangedAt: prev.ChangedAt}
	if outcome == ItemOutcomeCreated || outcome == ItemOutcomeUpdated {
		rec.ChangedAt = rec.LastChecked
	}
	// L10 (code review): same carry-forward rule recordItemCheck applies to
	// CheckOne/CheckAll — a real sync failure (syncErr != nil) extends the
	// streak the periodic pass would also be building on this pair; any
	// real outcome (created/updated/unchanged/foreign) resets it, including
	// the refused-foreign case, which is a boundary, not a failure.
	if syncErr != nil {
		rec.ConsecutiveFailures = prev.ConsecutiveFailures + 1
	}
	r.itemRecords[key] = rec
	r.mu.Unlock()

	if r.itemAuditFn != nil && syncErr == nil &&
		(outcome == ItemOutcomeCreated || outcome == ItemOutcomeUpdated) {
		r.itemAuditFn(w.clusterName, w.addon, outcome)
	}

	return outcome, syncErr
}

// SyncCluster delivers every addon-values secret Git defines for ONE
// cluster, right now — the engine behind POST
// /clusters/{name}/secrets/refresh. addonName narrows the push to a single
// addon when non-empty; that addon must already be defined in Git for this
// cluster, or the whole call is refused with ErrAddonNotInGit.
//
// What to deliver comes EXCLUSIVELY from the connected Git repo, through
// the exact planPushes plan the periodic pass computes — never from a
// request body, never from any in-memory definition. A cluster the
// managed-clusters file does not list is refused with ErrClusterNotInGit.
//
// Like SyncOne (and unlike the periodic pass and CheckAll), this is
// deliberately NOT gated on the engine's off switch: it only runs when a
// person explicitly asks for this one cluster, which is the same locked
// decision that keeps the single-row Sync action ungated.
//
// Returns the secret names it delivered or verified current (created,
// updated, or already matching), and the secret names whose push attempt
// failed. A foreign secret — one Sharko did not create — is left alone and
// appears in NEITHER list: the ownership gate refused the write, the
// per-item record says so, and that is a boundary, not a failure. The
// returned error text follows the same safety split the rest of this
// package uses: the two sentinel refusals above and ErrNoGitConnection are
// fixed sentences safe to show verbatim; anything else is wrapped
// lower-level text a caller must map to a canned sentence first.
func (r *Reconciler) SyncCluster(ctx context.Context, clusterName, addonName string) (refreshed []string, failed []string, err error) {
	gr := r.gitReader()
	if gr == nil {
		return nil, nil, ErrNoGitConnection
	}
	plan, planErr := r.planPushes(ctx, gr)
	if planErr != nil {
		return nil, nil, fmt.Errorf("could not read the addon catalog or managed clusters list: %w", planErr)
	}

	known := false
	for _, c := range plan.clusters {
		if c.name == clusterName {
			known = true
			break
		}
	}
	if !known {
		return nil, nil, ErrClusterNotInGit
	}

	var work []secretWork
	for _, w := range plan.work {
		if w.clusterName != clusterName {
			continue
		}
		if addonName != "" && w.addon != addonName {
			continue
		}
		work = append(work, w)
	}
	if addonName != "" && len(work) == 0 {
		return nil, nil, ErrAddonNotInGit
	}

	for _, w := range work {
		outcome, syncErr := r.syncWork(ctx, w)
		switch {
		case syncErr != nil:
			failed = append(failed, w.push.SecretName)
		case outcome == ItemOutcomeForeign:
			// Left exactly where it is — Sharko did not create it.
		default:
			refreshed = append(refreshed, w.push.SecretName)
		}
	}
	return refreshed, failed, nil
}
