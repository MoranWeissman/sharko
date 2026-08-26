package fanout

import "fmt"

// Single is the outcome of a command that acted on exactly ONE cluster —
// `sharko add-cluster`, `remove-cluster`, `update-cluster`, `unadopt` and
// `takeover`.
//
// Those five printed something like "Cluster registered with warnings
// (partial success)." and then returned nil, so they exited 0 for a run that
// had stopped halfway with real changes left behind. A script wrapping any of
// them was told the operation completed. `remove-cluster` was worse still:
// the server answers 200 with a body saying "failed", and the command printed
// "Cluster prod-eu removed." over it and exited 0.
//
// A single-cluster command is a fan-out of one, so Single is an Outcome with
// Total 1 and it takes the exit decision from Outcome.ExitError — literally
// the same function the fan-out commands call. Only the WORDING is its own,
// because "did NOT finish for every cluster" is nonsense about one cluster.
// Copying the rule instead of calling it is exactly how the batch and adopt
// handlers ended up wrong in opposite directions, so the rule is not copied
// here either.
type Single struct {
	Outcome
}

// SingleStatus reads the one per-cluster answer a single-cluster command got
// back.
//
// multiStatus says whether the server answered 207 Multi-Status, which is the
// server saying plainly that not all of it went through. When it did, a body
// that still claims a clean completion is not believed — the smaller claim
// wins. This mirrors what the five commands already computed inline
// (`result.Status == "partial" || status == 207`), with the difference that
// the answer now reaches the exit code instead of only the printed line.
//
// A status this build does not know lands in Outcome.Unrecognized, so it can
// never be read as a clean completion.
func SingleStatus(status string, multiStatus bool) Single {
	if multiStatus && status == StatusCompleted {
		status = StatusPartlyCompleted
	}
	return Single{Count([]string{status})}
}

// Completed is true only when the one cluster finished every step.
func (s Single) Completed() bool { return s.EverythingCompleted() }

// ProgressWord finishes the "Registering cluster prod-eu... " progress line.
//
// Every one of the five commands used to print "done" here the moment the
// HTTP call came back, before anything had even looked at the body — the same
// defect the fan-out commands had.
func (s Single) ProgressWord() string {
	if s.Completed() {
		return "done"
	}
	return "not finished"
}

// TroubleHeadline is the line printed in place of a command's own completion
// line when the one cluster did not fully complete. It is empty when it did,
// because each command has its own wording for the good case ("Cluster
// registered:", "Sharko now owns prod-eu.") and those stay where they are.
//
// No completion word appears in any of these: the old "partial success" said
// the opposite of what had happened.
//
// operation is a short noun phrase naming what was attempted, e.g. "Cluster
// registration". cluster is the cluster's name.
func (s Single) TroubleHeadline(operation, cluster string) string {
	switch {
	case s.Completed():
		return ""
	case s.PartlyCompleted == 1:
		// Deliberately short: the sentence after it, ReviewWarning, is the
		// one that says what stopping half-way means. Saying it twice
		// wastes the reader's attention on the line that matters most.
		return fmt.Sprintf("%s did not finish on %s.\n", operation, cluster)
	case s.Failed == 1:
		return fmt.Sprintf("%s did not go through on %s.\n", operation, cluster)
	default:
		return fmt.Sprintf("%s on %s came back with an answer this version of the CLI does not "+
			"know, so it cannot be reported as finished.\n", operation, cluster)
	}
}

// ExitError is the exit decision for a single-cluster command, and the
// decision itself is not made here: Outcome.ExitError makes it, and this
// function only rewords the result for one cluster. Change the rule there and
// every command follows, fan-out and single-cluster alike.
//
// The returned text carries fixed wording and the cluster's name only — never
// a message from Git, Kubernetes or a credentials backend.
func (s Single) ExitError(operation, cluster string) error {
	// The ONE decision. nil here means exit 0, for the same reason it means
	// exit 0 for `sharko add-clusters`.
	if s.Outcome.ExitError(operation) == nil {
		return nil
	}
	switch {
	case s.PartlyCompleted == 1:
		return fmt.Errorf("%s stopped part-way on %s — nothing was undone, so check what is "+
			"already in Git and on the cluster before running this again", operation, cluster)
	case s.Failed == 1:
		return fmt.Errorf("%s did not go through on %s", operation, cluster)
	default:
		return fmt.Errorf("%s on %s came back with an answer this version of the CLI does not "+
			"know, so it cannot be reported as finished", operation, cluster)
	}
}
