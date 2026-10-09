package fanout_test

import (
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/fanout"
)

// The status strings are written out as literals here, not read from the
// package under test, so nothing below compares a constant with itself.
var singleCases = []struct {
	what string
	// status is the `status` field the server sent.
	status string
	// multiStatus is whether the server answered 207.
	multiStatus bool
	// completed is what this answer means, decided here rather than by the
	// code being tested.
	completed bool
	// partWay is whether real changes may have been left behind.
	partWay bool
}{
	{what: "finished", status: "success", completed: true},
	{what: "stopped half-way", status: "partial", partWay: true},
	{what: "failed outright", status: "failed"},
	{what: "a status nobody has seen", status: "half-done-ish"},
	{what: "no status at all", status: ""},
	{what: "207 over a body claiming success", status: "success", multiStatus: true, partWay: true},
	{what: "207 over a partial", status: "partial", multiStatus: true, partWay: true},
	{what: "207 over a failure", status: "failed", multiStatus: true},
}

// TestSingle_ExitsZeroOnlyOnAFullCompletion is the B2 rule applied to one
// cluster: exit 0 only when it completed fully.
func TestSingle_ExitsZeroOnlyOnAFullCompletion(t *testing.T) {
	for _, c := range singleCases {
		s := fanout.SingleStatus(c.status, c.multiStatus)
		err := s.ExitError("Cluster registration", "prod-eu")
		if c.completed && err != nil {
			t.Errorf("%s: the cluster completed, yet the command would exit non-zero: %v", c.what, err)
		}
		if !c.completed && err == nil {
			t.Errorf("%s: the cluster did not complete, yet the command would exit 0. A script "+
				"checking the exit code would be told everything worked.", c.what)
		}
		if got := s.Completed(); got != c.completed {
			t.Errorf("%s: Completed() = %v, want %v", c.what, got, c.completed)
		}
	}
}

// TestSingle_NeverPrintsACompletionWordAboutAnUnfinishedRun. "partial
// success" was the old headline, and it contains the word that says the
// opposite of what had happened.
func TestSingle_NeverPrintsACompletionWordAboutAnUnfinishedRun(t *testing.T) {
	for _, c := range singleCases {
		s := fanout.SingleStatus(c.status, c.multiStatus)
		text := s.ProgressWord() + "\n" + s.TroubleHeadline("Cluster registration", "prod-eu") +
			s.ReviewWarning()
		if c.completed {
			if s.ProgressWord() != "done" {
				t.Errorf("%s: the progress line ends with %q, not \"done\"", c.what, s.ProgressWord())
			}
			if s.TroubleHeadline("Cluster registration", "prod-eu") != "" {
				t.Errorf("%s: the cluster completed, yet a trouble headline was produced: %q",
					c.what, text)
			}
			continue
		}
		if s.ProgressWord() == "done" {
			t.Errorf("%s: the progress line still ends with \"done\"", c.what)
		}
		lower := strings.ToLower(text)
		for _, w := range []string{"success", "succeeded", "with warnings", "all good"} {
			if strings.Contains(lower, w) {
				t.Errorf("%s: a run that did not complete uses the word %q: %q", c.what, w, text)
			}
		}
		if !strings.Contains(text, "prod-eu") {
			t.Errorf("%s: nothing printed names the cluster it is about: %q", c.what, text)
		}
	}
}

// TestSingle_WarnsAboutLeftoverChangesExactlyWhenSomethingStoppedHalfWay.
func TestSingle_WarnsAboutLeftoverChangesExactlyWhenSomethingStoppedHalfWay(t *testing.T) {
	for _, c := range singleCases {
		s := fanout.SingleStatus(c.status, c.multiStatus)
		w := s.ReviewWarning()
		if c.partWay {
			if w == "" {
				t.Errorf("%s: the cluster stopped half-way and nothing warns that real changes "+
					"may be left behind", c.what)
				continue
			}
			for _, want := range []string{"Nothing was undone", "may already be"} {
				if !strings.Contains(w, want) {
					t.Errorf("%s: the warning %q does not get across %q", c.what, w, want)
				}
			}
		} else if w != "" {
			t.Errorf("%s: nothing stopped half-way, yet a review warning was printed: %q", c.what, w)
		}
	}
}

// TestSingle_TakesItsExitDecisionFromTheSharedOne. Single must not hold a
// second copy of the rule. Whatever Outcome.ExitError decides for the same
// one answer, Single agrees with — otherwise the two can drift apart, which
// is how the fan-out and single-cluster commands came to disagree in the
// first place.
func TestSingle_TakesItsExitDecisionFromTheSharedOne(t *testing.T) {
	for _, c := range singleCases {
		// The 207 rule, written out here rather than borrowed from the code
		// under test: a server that answered Multi-Status has said not all
		// of it went through, so a body still claiming a clean completion
		// does not get believed.
		effective := c.status
		if c.multiStatus && effective == "success" {
			effective = "partial"
		}
		// What `sharko add-clusters` would decide about a batch of exactly
		// this one cluster, through the real fan-out path.
		shared := fanout.Count([]string{effective}).ExitError("Cluster registration")

		s := fanout.SingleStatus(c.status, c.multiStatus)
		mine := s.ExitError("Cluster registration", "prod-eu")
		if (shared == nil) != (mine == nil) {
			t.Errorf("%s: the shared decision says exit-zero=%v and the single-cluster one says "+
				"exit-zero=%v — two rules where there must be one",
				c.what, shared == nil, mine == nil)
		}
	}
}

// TestSingle_CarriesNoBackendErrorText. The only inputs are a status string
// the CLI matched against its own known values, an operation name and a
// cluster name — there is no seam for a Git, Kubernetes or credentials
// message to arrive through.
func TestSingle_CarriesNoBackendErrorText(t *testing.T) {
	s := fanout.SingleStatus("partial", false)
	for _, text := range []string{
		s.ProgressWord(),
		s.TroubleHeadline("Cluster registration", "prod-eu"),
		s.ReviewWarning(),
		s.ExitError("Cluster registration", "prod-eu").Error(),
	} {
		for _, leak := range []string{"401", "403", "x509", "dial tcp", "AccessDenied", "token"} {
			if strings.Contains(text, leak) {
				t.Errorf("%q carries %q — this package must only ever print counts, fixed "+
					"wording and the cluster's name", text, leak)
			}
		}
	}
}
