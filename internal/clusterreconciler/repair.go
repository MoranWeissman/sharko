package clusterreconciler

// repair.go — the reconciler's half of the connection repair (R3-1).
//
// The handler owns the request: it decides whether a repair may be offered at
// all (the step-2 policy), reads the stored facts from the secrets backend, and
// builds the expected Secret with the canonical builder. What it does NOT own is
// anything the reconciler is already the single source of: the configured
// branch, the commit a pass compared at, the provenance annotations a write
// stamps, the per-cluster reconcile record, and the applied-revision fact.
//
// So the write goes through here. That keeps two properties that matter:
//
//   - There is still exactly one place that stamps connection provenance and
//     moves the applied revision, so a repaired cluster's row reads the same way
//     a reconciled cluster's row does.
//   - The repair's own outcome lands in the same per-cluster record the tick
//     writes, so the page cannot show a stale "succeeded" from a pass that ran
//     before a failed repair.

import (
	"context"
	"errors"
	"fmt"

	corev1 "k8s.io/api/core/v1"

	"github.com/MoranWeissman/sharko/internal/argosecrets"
	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/credsafe"
	"github.com/MoranWeissman/sharko/internal/events"
	"github.com/MoranWeissman/sharko/internal/logging"
)

// ErrRepairNoClient means this server has no in-cluster Kubernetes client, so
// there is nothing to write a connection Secret with.
var ErrRepairNoClient = errors.New("no Kubernetes client is configured on this reconciler, so Sharko cannot write a cluster connection")

// RepairResult is what a full-connection repair did.
type RepairResult struct {
	// Changed is true when a real write happened.
	Changed bool

	// FieldsWritten names the owned field paths that changed, sorted. Field
	// paths only — never a value.
	FieldsWritten []string

	// PreservedForeignLabels and PreservedForeignDataKeys count what was
	// deliberately left alone.
	PreservedForeignLabels   int
	PreservedForeignDataKeys int

	// AppliedRevision is the commit this repair was built from, or empty when
	// the git provider could not report one. A repair is only ever allowed to
	// run with a known revision (see the endpoint's revision guard), so in
	// practice this is set; it is reported rather than assumed.
	AppliedRevision string
}

// RepairOwnedConnectionSecret writes the already-built desired connection Secret
// onto the live one, in place, and records the outcome the same way a reconcile
// pass would.
//
// desired MUST have come from argosecrets.BuildClusterSecret and MUST have been
// built from the secrets backend plus git — never from the live Secret. This
// function does not build it, on purpose: building it here would put a
// credential fetch between the ownership recheck and the write, which is exactly
// the window rule 3 is about.
//
// comparedRevision is the commit the caller resolved and verified immediately
// before calling. It is used for the provenance annotations and the applied
// revision, so a repaired Secret says which commit it was made to match.
//
// The ownership recheck and the write both live in
// argosecrets.Manager.RepairOwnedConnection — see that function for why they are
// adjacent. Every refusal it can return is passed through unchanged so the
// handler can map each one to its own answer.
func (r *Reconciler) RepairOwnedConnectionSecret(ctx context.Context, desired *corev1.Secret, comparedRevision string) (RepairResult, error) {
	if r.deps.ArgoClient == nil {
		return RepairResult{}, ErrRepairNoClient
	}
	if desired == nil {
		return RepairResult{}, fmt.Errorf("no desired connection Secret was built")
	}
	name := desired.Name
	log := logging.LoggerFromContext(ctx)

	// Provenance for this write: where the desired state came from and when.
	// The path is this reconciler's configured managed-clusters path as the
	// caller read it; the revision is the one the caller verified. Never a
	// value, never a hash, never a store path.
	_, passPath := r.currentPassCompared()
	if passPath == "" {
		passPath = r.managedClustersPath
	}
	provenance := connectionProvenanceAnnotations(passPath, comparedRevision, r.now())

	mgr := argosecrets.NewManager(r.deps.ArgoClient, r.namespace)
	outcome, err := mgr.RepairOwnedConnection(ctx, desired, provenance)
	if err != nil {
		// The error's own text is safe to record here: it is a Kubernetes error
		// or one of the primitive's fixed refusals, never a credentials-backend
		// error — the credential read happened in the caller, before this was
		// called, and a failure there never reaches this function. The
		// CredentialFailure flag is still set from the live error rather than
		// assumed false, so a future caller that does pass one through is
		// covered by the audit sanitizer instead of leaking.
		log.Error("[clusterreconciler] connection repair failed — nothing was written",
			"cluster", name, "namespace", r.namespace, "error", err)
		r.audit(audit.Entry{
			Level:             "error",
			Event:             "cluster_connection_repair",
			User:              "sharko",
			Action:            "repair_connection",
			Resource:          fmt.Sprintf("cluster:%s", name),
			Source:            "reconciler",
			Result:            "failure",
			Error:             err.Error(),
			CredentialFailure: credsafe.Is(err),
			RequestID:         logging.RequestID(ctx),
		})
		r.recordReconcile(name, OutcomeFailed,
			"Sharko couldn't repair this cluster's ArgoCD connection: "+err.Error(), nil)
		return RepairResult{}, err
	}

	result := RepairResult{
		Changed:                  outcome.Changed,
		FieldsWritten:            outcome.FieldsWritten,
		PreservedForeignLabels:   outcome.PreservedForeignLabels,
		PreservedForeignDataKeys: outcome.PreservedForeignDataKeys,
	}

	if !outcome.Changed {
		// Already what Sharko intends. No write happened, so the applied
		// revision is untouched — the previous one is still the honest answer
		// for "what commit was the last write built from".
		log.Info("[clusterreconciler] connection repair found nothing to change",
			"cluster", name, "namespace", r.namespace)
		r.audit(audit.Entry{
			Level:     "info",
			Event:     "cluster_connection_repair",
			User:      "sharko",
			Action:    "repair_connection",
			Resource:  fmt.Sprintf("cluster:%s", name),
			Source:    "reconciler",
			Result:    "success",
			Detail:    "the connection already matched what Sharko intends; nothing was written",
			RequestID: logging.RequestID(ctx),
		})
		result.AppliedRevision = r.appliedRevisionFor(name)
		return result, nil
	}

	recordWrite("updated")
	log.Info("[clusterreconciler] cluster connection repaired to match git and the secrets backend",
		"cluster", name, "namespace", r.namespace,
		"fields_written", len(outcome.FieldsWritten),
		"revision", comparedRevision)
	r.audit(audit.Entry{
		Level:     "info",
		Event:     "cluster_connection_repair",
		User:      "sharko",
		Action:    "repair_connection",
		Resource:  fmt.Sprintf("cluster:%s", name),
		Source:    "reconciler",
		Result:    "success",
		Detail:    fmt.Sprintf("rewrote %d owned field(s) to match commit %s", len(outcome.FieldsWritten), comparedRevision),
		RequestID: logging.RequestID(ctx),
	})

	// A real write landed. Stamp the applied revision from the commit this
	// repair was actually built against, not from whatever the last periodic
	// pass happened to see.
	r.stampAppliedRevisionTo(name, comparedRevision)
	result.AppliedRevision = comparedRevision

	// The drift this repair just corrected is no longer drift. Clear the
	// notice state so a NEW drift episode later gets its own event instead of
	// being silenced by this one (see connection_drift_notice.go).
	r.clearConnectionDriftNotice(name)

	r.recordReconcile(name, OutcomeSucceeded, "connection repaired — rewritten to match git and the stored sign-in details", nil)
	return result, nil
}

// RepairAddonLabelsOnly re-applies just Sharko's addon labels for one cluster,
// which is the whole of what a repair may do on a guest connection.
//
// IMPORTANT: This is the OLD UNSAFE PATH. It delegates to ResyncClusterLabels,
// which resolves git at the CURRENT BRANCH HEAD and re-reads the cluster
// definition to build addon labels. This is correct for the periodic reconciler
// and for `POST /clusters/{name}/resync` (where re-reading the latest git state
// is the whole point), but it is NOT correct for the repair endpoint.
//
// The repair endpoint must use the NEW SAFE PRIMITIVE:
//
//	argosecrets.Manager.RepairAddonLabelsWithOwnershipCheck(ctx, name, pinnedAddonLabels, expectedOwned)
//
// That primitive accepts PRE-RESOLVED pinned labels built from a VERIFIED commit,
// and performs the ownership recheck immediately before writing. The pinned
// labels and the verified commit are both supplied by the endpoint (PR B), which
// is why the wiring to the new primitive belongs in PR B, not here.
//
// This function remains as-is in PR A (core) so that:
// 1. PR A builds and passes with no endpoint wired.
// 2. POST /clusters/{name}/resync keeps its current behaviour (re-read git).
// 3. Nobody wires the repair endpoint to this function by accident.
func (r *Reconciler) RepairAddonLabelsOnly(ctx context.Context, name string) (ResyncResult, error) {
	return r.ResyncClusterLabels(ctx, name)
}

// stampAppliedRevisionTo records an explicit commit as the one this cluster's
// last successful write was built from.
//
// stampAppliedRevision (revision.go) takes the CURRENT PASS's compared
// revision, which is right for a tick: the pass read git, then wrote. A repair
// is different — it resolves and verifies its own revision as part of the
// request, and that is the commit the write was actually built against, so it
// must be the one recorded. A no-op on an empty revision, same rule as the
// pass-based version: an unknown fact never overwrites a known one with a blank.
func (r *Reconciler) stampAppliedRevisionTo(name, revision string) {
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

// EmitConnectionRepairEvent records a Kubernetes event for a repair a person
// asked for. Nil-safe through the recorder itself.
//
// It exists so an operator watching `kubectl get events` sees the same story the
// audit log tells. The message names the cluster and how many owned fields
// changed — never a field's value.
func (r *Reconciler) EmitConnectionRepairEvent(cluster string, fieldsWritten int) {
	if r == nil || r.eventRecorder == nil {
		return
	}
	r.eventRecorder.Eventf(
		events.ReasonConnectionRepaired,
		"Cluster %s: Sharko repaired its ArgoCD connection to match git and the stored sign-in details (%d owned field(s) rewritten).",
		events.EventTypeNormal,
		cluster, fieldsWritten,
	)
}
