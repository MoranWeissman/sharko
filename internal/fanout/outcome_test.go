package fanout_test

import (
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/fanout"
	"github.com/MoranWeissman/sharko/internal/fanout/fanouttest"
)

func TestCount_IsAccurateForEveryShape(t *testing.T) {
	for _, s := range fanouttest.EverySmall() {
		o := fanout.Count(s.Statuses())
		if o.Completed != s.Completed || o.PartlyCompleted != s.PartlyCompleted ||
			o.Failed != s.Failed || o.Unrecognized != s.Unrecognized {
			t.Errorf("%+v counted as %+v", s, o)
		}
		if got := o.Completed + o.PartlyCompleted + o.Failed + o.Unrecognized; got != o.Total {
			t.Errorf("%+v: the buckets add up to %d but total says %d — a count that does not "+
				"add up cannot be shown to anybody", s, got, o.Total)
		}
	}
}

// TestCount_NeverReadsAnUnknownStatusAsCompleted is the specific thing the
// separate Unrecognized bucket buys. A status nobody has seen before must not
// be counted as a clean completion, because that is what makes an exit code
// say "all good" about something nobody understands.
func TestCount_NeverReadsAnUnknownStatusAsCompleted(t *testing.T) {
	o := fanout.Count([]string{fanout.StatusCompleted, "something-new", ""})
	if o.Completed != 1 {
		t.Errorf("completed = %d, want 1 — only the one real success counts", o.Completed)
	}
	if o.Unrecognized != 2 {
		t.Errorf("unrecognized = %d, want 2", o.Unrecognized)
	}
	if o.EverythingCompleted() {
		t.Error("an unknown status was read as a clean completion")
	}
	if err := o.ExitError("Test"); err == nil {
		t.Error("an unknown status exited 0 — a script would be told everything worked")
	}
}

// TestExitError_ZeroOnlyWhenEverythingCompleted is the CLI exit rule, driven
// through the real production function.
func TestExitError_ZeroOnlyWhenEverythingCompleted(t *testing.T) {
	for _, s := range fanouttest.EverySmall() {
		o := fanout.Count(s.Statuses())
		err := o.ExitError("Test")
		wantZero := o.Total > 0 && o.Completed == o.Total
		if wantZero && err != nil {
			t.Errorf("%+v: every item completed, yet the command would exit non-zero: %v", s, err)
		}
		if !wantZero && err == nil {
			t.Errorf("%+v: %d partly completed and %d failed, yet the command would exit 0. "+
				"A script checking the exit code would be told everything worked.",
				s, o.PartlyCompleted, o.Failed)
		}
	}
}

// TestExitError_PartlyCompletedAloneIsNonZero is the case that used to exit 0
// most quietly: nothing FAILED, so a two-way reading called it a success,
// while every item had in fact stopped halfway.
func TestExitError_PartlyCompletedAloneIsNonZero(t *testing.T) {
	o := fanout.Count([]string{fanout.StatusPartlyCompleted, fanout.StatusPartlyCompleted})
	if err := o.ExitError("Cluster registration"); err == nil {
		t.Fatal("every cluster stopped halfway and the command would still exit 0")
	}
}

// TestExitError_EmptyResultIsNotASuccess — no per-item answers came back at
// all. That is not "everything completed"; there is nothing to confirm.
func TestExitError_EmptyResultIsNotASuccess(t *testing.T) {
	o := fanout.Count(nil)
	if o.EverythingCompleted() {
		t.Error("an empty result reported as everything completed")
	}
	if o.ExitError("Test") == nil {
		t.Error("an empty result exited 0")
	}
}

// completionWords are the words a summary may not use about a run that did
// not complete. The old CLI printed "done" and then "Batch complete:" above
// per-cluster lines that each said the opposite.
var completionWords = []string{"done", "complete", "success", "succeeded", "all good", "finished for every"}

func TestSummaryLine_SaysAllThreeCountsAndNeverClaimsCompletion(t *testing.T) {
	for _, s := range fanouttest.EverySmall() {
		o := fanout.Count(s.Statuses())
		line := o.SummaryLine("Cluster registration")

		// All three counts, every time.
		for _, want := range []string{
			itoa(o.Completed) + " fully completed",
			itoa(o.PartlyCompleted) + " partly completed",
			itoa(o.Failed) + " failed",
		} {
			if !strings.Contains(line, want) {
				t.Errorf("%+v: summary %q does not carry %q", s, line, want)
			}
		}

		lower := strings.ToLower(line)
		if o.EverythingCompleted() {
			if !strings.Contains(lower, "finished for every cluster") {
				t.Errorf("%+v: everything completed, yet the summary does not say so: %q", s, line)
			}
			continue
		}
		// Nothing that reads as "it all worked" may appear.
		if !strings.Contains(lower, "did not finish for every cluster") {
			t.Errorf("%+v: not everything completed, yet the summary does not say so: %q", s, line)
		}
		for _, w := range completionWords {
			// "fully completed"/"partly completed" are counts, not verdicts —
			// the verdict clause is what is checked, and it is the one above.
			if w == "complete" {
				continue
			}
			if strings.Contains(lower, w) {
				t.Errorf("%+v: summary of a run that did NOT complete uses the word %q: %q", s, w, line)
			}
		}
	}
}

// TestReviewWarning_OnlyAndAlwaysWhenSomethingStoppedHalfway. The ruling's
// wording: partly completed work may have left real changes behind and needs
// looking at.
func TestReviewWarning_OnlyAndAlwaysWhenSomethingStoppedHalfway(t *testing.T) {
	for _, s := range fanouttest.EverySmall() {
		o := fanout.Count(s.Statuses())
		w := o.ReviewWarning()
		if o.PartlyCompleted > 0 {
			if w == "" {
				t.Errorf("%+v: %d item(s) stopped halfway and nothing warns that real changes "+
					"may be left behind", s, o.PartlyCompleted)
				continue
			}
			for _, want := range []string{"Nothing was undone", "may already be"} {
				if !strings.Contains(w, want) {
					t.Errorf("%+v: the warning %q does not get across %q", s, w, want)
				}
			}
		} else if w != "" {
			t.Errorf("%+v: nothing stopped halfway, yet a review warning was printed: %q", s, w)
		}
	}
}

// TestNoBackendErrorTextCanReachTheSummary — everything this package prints is
// built from counts and fixed sentences. There is no seam for a Git,
// Kubernetes or credentials-backend message to arrive through, and this test
// is what says so out loud: the only inputs are integers.
func TestNoBackendErrorTextCanReachTheSummary(t *testing.T) {
	o := fanout.Count([]string{fanout.StatusPartlyCompleted, fanout.StatusFailed})
	for _, text := range []string{
		o.SummaryLine("Cluster registration"),
		o.ReviewWarning(),
		o.ExitError("Cluster registration").Error(),
	} {
		for _, leak := range []string{"401", "403", "x509", "dial tcp", "AccessDenied", "token"} {
			if strings.Contains(text, leak) {
				t.Errorf("%q carries %q — this package must only ever print counts and fixed wording", text, leak)
			}
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		return "-" + string(b)
	}
	return string(b)
}
