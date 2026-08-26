// Package fanout holds the ONE accurate answer to "what did a fan-out
// operation actually do", and the ONE place that turns that answer into what
// a person reads and what a script's exit code says.
//
// A fan-out operation — registering a batch of clusters, adopting several
// clusters — gets one result per cluster, and each cluster can come back
// three ways, not two:
//
//	completed        the whole thing finished
//	partly completed it stopped halfway, and NOTHING was rolled back. The
//	                 pull request may have merged, the ArgoCD connection may
//	                 have been swapped, Secrets may have landed. Real changes
//	                 may already be out there.
//	failed           nothing landed for that cluster at all
//
// Everything that reads a fan-out result — the HTTP response body, the audit
// trail, the summary the CLI prints, and the CLI's exit code — has to agree
// about those counts, or one of them is lying to somebody. They agree because
// they all come from this one type, counted by this one function.
//
// This package holds no error text from Git, Kubernetes, a credentials
// backend or any other provider, and it never will: everything below is
// built from counts and fixed sentences.
package fanout

import "fmt"

// The per-item status strings a fan-out result carries on the wire. They are
// the orchestrator's own values and are matched here rather than re-declared
// somewhere else, because the whole point is that one counting rule reads
// exactly what the producers write.
const (
	StatusCompleted       = "success"
	StatusPartlyCompleted = "partial"
	StatusFailed          = "failed"
)

// Outcome is the accurate aggregate count of a fan-out operation.
//
// Completed + PartlyCompleted + Failed + Unrecognized always equals Total.
//
// Unrecognized exists so a per-item answer nobody has seen before cannot be
// read as a clean completion. It is counted on its own, it makes
// EverythingCompleted false, and it makes the CLI exit non-zero — the honest
// "something happened here that we cannot call finished".
type Outcome struct {
	Total int `json:"total"`
	// Completed is how many items finished every step.
	Completed int `json:"completed"`
	// PartlyCompleted is how many stopped halfway with nothing rolled back.
	PartlyCompleted int `json:"partly_completed"`
	// Failed is how many failed outright — nothing landed for them at all.
	// This is NOT the same number as the older top-level `failed` field on
	// the batch response, which counts every item that did not fully
	// succeed, partials included.
	Failed int `json:"failed"`
	// Unrecognized is how many came back with a status this build does not
	// know. Normally zero.
	Unrecognized int `json:"unrecognized"`
}

// Count is the one counting rule. Give it the per-item status strings in the
// order they came back and it returns the accurate trio.
func Count(statuses []string) Outcome {
	o := Outcome{Total: len(statuses)}
	for _, s := range statuses {
		switch s {
		case StatusCompleted:
			o.Completed++
		case StatusPartlyCompleted:
			o.PartlyCompleted++
		case StatusFailed:
			o.Failed++
		default:
			o.Unrecognized++
		}
	}
	return o
}

// EverythingCompleted is true only when every item finished every step. An
// empty result is not "everything completed" — nothing came back, which is
// not something to report as a clean run.
func (o Outcome) EverythingCompleted() bool {
	return o.Total > 0 && o.Completed == o.Total
}

// AnythingLanded reports whether real changes are known or suspected to exist
// anywhere. A partly completed item counts, because it is not a rollback.
func (o Outcome) AnythingLanded() bool {
	return o.Completed > 0 || o.PartlyCompleted > 0 || o.Unrecognized > 0
}

// NeedsReview reports whether at least one item stopped halfway, which is the
// case where somebody has to go and look at what was left behind.
func (o Outcome) NeedsReview() bool { return o.PartlyCompleted > 0 }

// SummaryLine is the single line a person reads above the per-item lines.
//
// It always carries all three counts, and it says plainly whether the
// operation finished for every item. It must never carry completion wording
// for a run that did not complete: an operation where every item stopped
// halfway used to print a cheerful "done" and then "Batch complete", above
// per-item lines that each said the opposite.
//
// operation is a short noun phrase naming what was attempted, e.g.
// "Cluster registration".
func (o Outcome) SummaryLine(operation string) string {
	verdict := "did NOT finish for every cluster"
	if o.EverythingCompleted() {
		verdict = "finished for every cluster"
	}
	line := fmt.Sprintf("%s %s: %d fully completed, %d partly completed, %d failed (of %d).",
		operation, verdict, o.Completed, o.PartlyCompleted, o.Failed, o.Total)
	if o.Unrecognized > 0 {
		line += fmt.Sprintf(" %d came back with an answer this version of the CLI does not know.", o.Unrecognized)
	}
	return line + "\n"
}

// ReviewWarning is the extra sentence printed when something stopped halfway.
// Empty when nothing did.
//
// The point it has to get across is that "partly completed" is not "did not
// happen": nothing was undone, so real changes may already be in Git and on
// the cluster, and somebody has to look before running the command again.
func (o Outcome) ReviewWarning() string {
	if !o.NeedsReview() {
		return ""
	}
	noun := "cluster"
	verb := "it"
	if o.PartlyCompleted > 1 {
		noun, verb = "clusters", "them"
	}
	return fmt.Sprintf(
		"%d %s only got part of the way. Nothing was undone, so real changes may already be "+
			"in Git and on the cluster — check %s below before running this again.\n",
		o.PartlyCompleted, noun, verb)
}

// ExitError is the CLI's exit decision, and it is the ONLY one. It returns
// nil — meaning exit 0 — only when every item completed fully. Anything else
// gets an error, so a script that checks the exit code is told the truth
// whether an item failed outright or merely stopped halfway.
//
// The command exited 0 for a batch in which every single cluster failed,
// because the code returned nil as long as the HTTP call itself came back.
// A script had no way to notice.
//
// The returned text carries counts and fixed wording only — never a message
// from Git, Kubernetes or a credentials backend.
func (o Outcome) ExitError(operation string) error {
	if o.EverythingCompleted() {
		return nil
	}
	if o.Total == 0 {
		return fmt.Errorf("%s: no per-cluster results came back, so there is nothing to confirm", operation)
	}
	return fmt.Errorf("%s did not finish for every cluster: %d fully completed, %d partly completed, %d failed (of %d) — see the summary above",
		operation, o.Completed, o.PartlyCompleted, o.Failed, o.Total)
}
