package clusterreconciler

// desired_view.go — the read-only window onto "what does git say this one
// cluster's connection should look like", for the connection comparison
// (P2 step 2).
//
// Nothing here writes. There is no Kubernetes write and no git write on any
// path in this file, and no function here calls one. It exists so the
// comparison reads the desired state through the SAME code the reconciler
// converges from — readDesiredState plus desiredAddonLabels — rather than a
// second git-reading path that could quietly answer a different question.

import (
	"context"
	"fmt"

	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/models"
)

// DesiredConnectionState is what git says about one cluster's connection at
// one commit.
type DesiredConnectionState struct {
	// Found is false when the cluster has no entry in the managed-clusters
	// file — it is not Sharko-managed, so there is nothing to compare
	// against.
	Found bool

	// Entry is the cluster's managed-clusters record.
	Entry models.ManagedClusterEntry

	// AddonLabels is the addon-enablement label set git wants on this
	// cluster's connection Secret, computed by the same desiredAddonLabels
	// the reconciler uses (record labels, legacy values upgraded, v4
	// assignment file merged on top).
	AddonLabels map[string]string

	// AddonLabelsKnown is false on a v4 repo whose cluster-addons file for
	// this cluster could not be read or parsed. The reconciler refuses to
	// touch a cluster's labels in that state; the comparison must likewise
	// refuse to call the labels wrong, because it does not know what right
	// is. This is a "could not finish", not a difference.
	AddonLabelsKnown bool

	// ComparedPath is the managed-clusters path actually read, reported even
	// when the read failed so a caller can say where Sharko looked.
	ComparedPath string
}

// DesiredConnectionStateAt reads one cluster's desired connection state from
// git at the given ref. Pass a resolved commit SHA to pin every file read to
// one commit; pass a branch name to read the branch head.
//
// A cluster with no entry returns Found=false and a nil error — "this cluster
// is not in the list" is an ordinary answer, not a failure. A git read or
// schema failure returns an error, which the comparison turns into "Sharko
// could not finish the check" and never into a difference.
func (r *Reconciler) DesiredConnectionStateAt(ctx context.Context, name, ref string) (DesiredConnectionState, error) {
	if r.deps.GitProvider == nil {
		return DesiredConnectionState{}, fmt.Errorf("no git provider configured on this reconciler")
	}
	gp := r.deps.GitProvider()
	if gp == nil {
		return DesiredConnectionState{}, fmt.Errorf("no active git provider configured")
	}

	spec, v4, _, readPath, err := r.readDesiredStateAtRef(ctx, gp, ref)
	out := DesiredConnectionState{ComparedPath: readPath}
	if err != nil {
		return out, err
	}
	if spec == nil {
		return out, nil
	}
	for _, c := range spec.Clusters {
		if c.Name != name {
			continue
		}
		out.Found = true
		out.Entry = c
		// v4 == nil means a v3 repo, where the desired addon set comes
		// entirely from the record's own labels block and is always known.
		out.AddonLabelsKnown = v4 == nil || v4.desiredKnown(name)
		if out.AddonLabelsKnown {
			out.AddonLabels = desiredAddonLabels(c, v4.labelsFor(name))
		}
		return out, nil
	}
	return out, nil
}

// ResolveComparedRevision returns the commit the comparison should read
// everything at, or "" when the active git provider cannot say.
//
// Best-effort by the same rule the reconcile passes follow (see
// fetchComparedRevision): a provider that does not implement the optional
// gitprovider.BranchRevisioner capability, or a call that fails, gives an
// empty string. Never a guessed, cached or stale commit. A caller that gets ""
// reads at the configured branch instead and reports no compared commit —
// which is honest about what it knows, unlike naming a commit it did not
// verify.
//
// It does NOT discover a default branch. The branch is the configured one,
// full stop.
func (r *Reconciler) ResolveComparedRevision(ctx context.Context) string {
	if r.deps.GitProvider == nil {
		return ""
	}
	gp := r.deps.GitProvider()
	if gp == nil {
		return ""
	}
	return r.fetchComparedRevision(ctx, gp)
}

// Branch returns the git ref this reconciler reads desired state from — the
// configured branch, never discovered.
func (r *Reconciler) Branch() string { return r.branch }

// ConnectivityCheckEnabled reports whether the connectivity-check label is
// switched on right now, combining the static env escape hatch with the live
// probe_mode server setting exactly as the write path does.
//
// The comparison needs this because the label is DERIVED at write time from
// this same answer: models.ApplyConnectivityCheckLabel stamps it on a
// zero-addon cluster when the feature is on and removes it when it is off. A
// comparison that assumed one answer would report false drift on every
// zero-addon cluster the moment somebody flipped probe_mode.
func (r *Reconciler) ConnectivityCheckEnabled(ctx context.Context) bool {
	return !r.effectiveDisableConnectivityCheck(ctx)
}

// GitProviderForRead returns the active git provider, or nil when none is
// configured. Named for what callers may do with it: the comparison only ever
// reads.
func (r *Reconciler) GitProviderForRead() gitprovider.GitProvider {
	if r.deps.GitProvider == nil {
		return nil
	}
	return r.deps.GitProvider()
}
