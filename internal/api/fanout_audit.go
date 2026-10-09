package api

// fanout_audit.go — the ONE place that turns "what a fan-out operation
// actually did" into the audit trail's outcome and change answer.
//
// # Why this file exists
//
// A fan-out endpoint (register a batch of clusters, adopt several clusters)
// gets one result per item, and each item can come back three ways, not two:
//
//	success  the whole thing finished
//	partial  it stopped halfway — and NOTHING was rolled back. The pull
//	         request merged, the cluster was registered, its Secrets landed.
//	         Real things changed.
//	failed   nothing landed for that item at all
//
// Every handler that folded those three into a two-answer audit line got it
// wrong in one of two directions, and both were shipped:
//
//   - R2-7 (batch register): every partial was counted as a failure, so a
//     batch where EVERY cluster came back partial was written into the audit
//     trail as "it all failed, nothing changed" — while pull requests had in
//     fact merged.
//   - R2-8 (adopt): the mirror image, and worse, because it claims success.
//     Every partial was counted as a success, so an adoption where EVERY
//     cluster stopped halfway was written into the audit trail as "Cluster
//     adopted · success · changes applied". It claimed full completion for
//     work that did not complete.
//
// The two handlers had the same rule to apply and each wrote its own version
// of it, which is how they ended up wrong in opposite directions. There is now
// one rule, in one function, and both call it.
//
// # What this file deliberately does NOT decide
//
// The HTTP status code. That an all-failed adoption answers 200 while an
// all-failed registration answers 207 is a real inconsistency, but both
// endpoints are stable and by Sharko's own rule any status change is a MAJOR
// version bump. It belongs to the product owner. Each handler keeps its own
// status function, unchanged, and a test pins it.

import (
	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/fanout"
)

// fanoutCounts is what a fan-out operation actually did, counted by item.
//
// Succeeded + Partial + HardFailed + Other must equal Total. Other exists so
// an item status nobody has seen before cannot silently be read as a clean
// success: it is counted, and it pulls the answer down to "partial" — the
// honest "something happened here that we cannot call finished".
type fanoutCounts struct {
	Total      int
	Succeeded  int
	Partial    int
	HardFailed int
	Other      int
}

// fanoutCountsFrom is the ONE conversion from the counts that go out on the
// wire and drive the CLI's exit code into the shape the audit rule reads.
//
// It exists so the audit trail, the response body, the printed summary and
// the exit code cannot disagree: there is one count, made once by
// fanout.Count from the per-item statuses, and every surface is a view of it.
func fanoutCountsFrom(o fanout.Outcome) fanoutCounts {
	return fanoutCounts{
		Total:      o.Total,
		Succeeded:  o.Completed,
		Partial:    o.PartlyCompleted,
		HardFailed: o.Failed,
		Other:      o.Unrecognized,
	}
}

// fanoutAuditOutcome is the rule. Both fan-out handlers call it.
//
//	every item succeeded                  → success, changes applied
//	nothing landed anywhere               → failure, no changes
//	anything got part-way (or came back
//	  with a status we do not know)       → partial, changes MAY be applied
//	a mix of finished and failed, no
//	  part-way item                       → partial, changes applied
//
// The four cases come straight from the product owner's ruling on B2, and the
// two middle ones are the reason the change answer has four values rather
// than three.
//
// Why "may be applied" rather than "applied" for a part-way item: an item
// that stopped halfway usually DID change something real — a pull request
// merged, an ArgoCD connection was swapped, Secrets were written. But not
// always. A registration whose Git commit fails before any pull request
// exists is also recorded as partial (internal/orchestrator/cluster.go, the
// git_commit branch), and on a server with no ArgoCD Secret manager and no
// addon secrets that one genuinely left nothing behind. So neither certainty
// is honest, and the rule uses the answer that says what is actually known.
//
// A mix of finished and failed items with nothing part-way is different: at
// least one item completed every step, so we KNOW changes were applied.
//
// "Nothing changed" stays reserved for the one case where it is true: no item
// succeeded, none got part-way, and none came back unreadable. A partial is
// never a rollback — whatever it did do is still there.
func fanoutAuditOutcome(c fanoutCounts) (string, audit.ChangeResult) {
	switch {
	case c.Total > 0 && c.Succeeded == c.Total:
		return "success", audit.ChangesApplied
	case c.Succeeded == 0 && c.Partial == 0 && c.Other == 0:
		return "failure", audit.ChangesNone
	case c.Partial > 0 || c.Other > 0:
		return "partial", audit.ChangesMayBeApplied
	default:
		return "partial", audit.ChangesApplied
	}
}
