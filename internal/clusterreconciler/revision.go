package clusterreconciler

import (
	"context"
	"time"

	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/logging"
)

// revision.go — P2-C1 (two revisions + the file path on connection rows)
// and P2-C5's connection-secret half (provenance annotations). Two pieces
// of state, deliberately kept separate because they answer different
// questions and change on different rhythms:
//
//   - passCompared* — "what did THIS PASS read from git?" A pass-level
//     fact (the branch head SHA is fetched ONCE per pass, and the file
//     path is the same for every cluster a pass touches), set at the start
//     of pollOnce/checkOnce and read by every recordReconcile call made
//     during that pass.
//   - appliedRevision — "what commit was the last SUCCESSFUL WRITE to THIS
//     CLUSTER's secret built from?" A per-cluster fact that survives
//     across passes untouched until the next real write, which is the
//     whole point of the generation/observedGeneration pair: a check pass
//     must never move it, only a write does.

// currentPassRevisionState holds the pass-level "what was compared"
// facts. A tiny struct (rather than two loose fields) so passMu guards
// both together — a reader must never see one updated and the other still
// stale mid-write.
type currentPassRevisionState struct {
	revision string
	path     string
}

// setPassCompared records the branch head SHA and file path THIS pass read
// from git — called once, near the top of pollOnce/checkOnce, before any
// per-cluster work begins (or from stampAbortedTick with two empty strings
// when the pass could not complete a real comparison).
func (r *Reconciler) setPassCompared(revision, path string) {
	r.passMu.Lock()
	r.passCompared = currentPassRevisionState{revision: revision, path: path}
	r.passMu.Unlock()
}

// currentPassCompared returns the pass-level compared revision + path set
// by the most recent setPassCompared call. Safe to call concurrently with
// setPassCompared — recordReconcile reads this on every call.
func (r *Reconciler) currentPassCompared() (revision, path string) {
	r.passMu.RLock()
	defer r.passMu.RUnlock()
	return r.passCompared.revision, r.passCompared.path
}

// fetchComparedRevision asks the active git provider for the branch head
// SHA via the OPTIONAL gitprovider.BranchRevisioner capability — once per
// pass. Best effort, on purpose: a provider that does not implement the
// capability, or a call that fails, leaves the returned revision empty.
// Never a guessed, cached, or stale value — see BranchRevisioner's own doc
// comment for why this is a type-asserted optional interface rather than a
// required GitProvider method.
func (r *Reconciler) fetchComparedRevision(ctx context.Context, gp gitprovider.GitProvider) string {
	brp, ok := gp.(gitprovider.BranchRevisioner)
	if !ok {
		return ""
	}
	revision, err := brp.GetBranchHeadSHA(ctx, r.branch)
	if err != nil {
		logging.LoggerFromContext(ctx).Debug(
			"[clusterreconciler] couldn't get the branch head commit for this pass — compared revision will show empty",
			"branch", r.branch, "error", err,
		)
		return ""
	}
	return revision
}

// stampAppliedRevision records the CURRENT pass's compared revision as the
// commit the cluster's last successful write was built from. Call this
// ONLY at the moment a real Kubernetes write for this cluster's secret
// succeeds (createOne's Create call, a changed selfHealManagedCluster
// write, a changed syncSelfManaged write) — never from a check pass, and
// never when the write failed. A no-op when this pass's revision is
// unknown: an unknown fact is never allowed to overwrite a previously
// known one with a blank.
func (r *Reconciler) stampAppliedRevision(name string) {
	revision, _ := r.currentPassCompared()
	if revision == "" {
		return
	}
	r.appliedRevMu.Lock()
	if r.appliedRevision == nil {
		r.appliedRevision = make(map[string]string)
	}
	r.appliedRevision[name] = revision
	r.appliedRevMu.Unlock()
}

// appliedRevisionFor returns the persisted "last successful write was
// built from commit X" fact for one cluster, or "" when this server
// instance has never successfully written that cluster's secret.
func (r *Reconciler) appliedRevisionFor(name string) string {
	r.appliedRevMu.RLock()
	defer r.appliedRevMu.RUnlock()
	return r.appliedRevision[name]
}

// Provenance annotation keys stamped on connection secrets Sharko OWNS
// (P2-C5) — never on a self-managed/user-owned Secret, where Sharko only
// ever converges labels (syncSelfManaged / argosecrets.SyncLabelsOnly stay
// annotation-free by design; see that function's doc comment).
const (
	// AnnotationSourceFile names the git path the write was built from —
	// "where," not "what." Set only when the pass that produced the write
	// actually knows the path (always true for a completed pass).
	AnnotationSourceFile = "sharko.dev/source-file"
	// AnnotationRevision names the commit the write was built from. Set
	// only when the pass's compared revision is known (BranchRevisioner
	// implemented and the call succeeded) — never a guessed value.
	AnnotationRevision = "sharko.dev/revision"
	// AnnotationWrittenAt is the RFC3339 timestamp of the write itself —
	// always known, since it is measured at the moment of the write, not
	// derived from anything that could fail to answer.
	AnnotationWrittenAt = "sharko.dev/written-at"
)

// connectionProvenanceAnnotations builds the sharko.dev/* provenance
// annotations for a connection-secret write (P2-C5): where the desired
// state came from and when Sharko wrote it. HARD RULE, enforced by
// construction: this function only ever takes a file path, a commit SHA,
// and a timestamp — there is no parameter it could receive a secret value,
// a hash of one, or a store path through, so there is no code path here
// that could leak one. sourceFile/revision are omitted (not written as
// empty strings) when unknown, matching every other "never a guessed
// value" rule in this lane.
func connectionProvenanceAnnotations(sourceFile, revision string, at time.Time) map[string]string {
	ann := map[string]string{
		AnnotationWrittenAt: at.UTC().Format(time.RFC3339),
	}
	if sourceFile != "" {
		ann[AnnotationSourceFile] = sourceFile
	}
	if revision != "" {
		ann[AnnotationRevision] = revision
	}
	return ann
}
