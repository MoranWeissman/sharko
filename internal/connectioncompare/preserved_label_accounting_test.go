package connectioncompare

// preserved_label_accounting_test.go — B15.
//
// The promise: "synced" means Sharko compared everything it owns on this
// connection and it all matched. A label that is deliberately left out of the
// comparison is, by definition, not compared — so if Sharko claims that label,
// it has to say so out loud, and the answer cannot be synced.
//
// Before B15 it did not. A label key recorded as the previous owner's was
// dropped from the comparison, not counted as a difference, and not added to
// NotChecked — so the clause that gates synced on an empty NotChecked list saw
// nothing and let "Connection synced" through. Git could say an addon was on,
// the cluster could say it was off, nothing would converge it, and the page
// said everything was fine.

import (
	"testing"

	"github.com/MoranWeissman/sharko/internal/argosecrets"
	"github.com/MoranWeissman/sharko/internal/models"
)

// notCheckedFor returns the not-checked entry for one path, or fails.
func notCheckedFor(t *testing.T, res Result, path string) NotCheckedField {
	t.Helper()
	for _, nc := range res.NotChecked {
		if nc.Path == path {
			return nc
		}
	}
	t.Fatalf("no not-checked entry for %q. NotChecked = %+v", path, res.NotChecked)
	return NotCheckedField{}
}

// THIS IS THE ONE. Git declares the addon label, and the connection also
// carries a record saying that same key belongs to whoever managed the cluster
// before Sharko. Sharko leaves recorded keys alone — it does not compare this
// one and it does not converge it — so the connection is NOT fully checked and
// must not be reported as synced.
//
// Deleting `&& len(res.NotChecked) == 0` from Compare's synced case makes this
// test fail. That clause had no test at all before B15: the other half of the
// same line (`Scope == ScopeFull &&`) was covered by two tests, this half by
// none.
func TestCompare_PreservedLabelGitAlsoDeclaresIsNotChecked(t *testing.T) {
	spec := argosecrets.ClusterSecretSpec{Server: testServer, Token: "made-up-token-for-tests-only"}
	req, _ := ownedRequest(t, spec, map[string]string{"datadog": models.LabelEnabled})

	// The cluster says the addon is OFF while Git says it is ON — and the
	// preserved-label record hides exactly that key from the comparison.
	req.Live.Labels["datadog"] = models.LabelDisabled
	req.Live.Annotations = map[string]string{
		argosecrets.AnnotationTakeoverPreservedLabels: "datadog",
	}

	res := Compare(req)

	if res.Scope != ScopeFull {
		t.Fatalf("fixture drifted: scope = %q, want %q", res.Scope, ScopeFull)
	}
	if len(res.Differences) != 0 {
		t.Fatalf("the key is skipped, so there is no difference to find — this test would then prove "+
			"nothing about the synced clause. Differences = %+v", res.Differences)
	}
	if res.Status == StatusSynced {
		t.Fatalf("Sharko never compared %q and still called the connection synced. "+
			"NotChecked = %+v", "datadog", res.NotChecked)
	}
	if res.Status != StatusLimited {
		t.Fatalf("status = %q, want %q", res.Status, StatusLimited)
	}

	nc := notCheckedFor(t, res, labelFieldPath("datadog"))
	if nc.Reason != ReasonLabelPreservedForPreviousOwner {
		t.Errorf("reason = %q, want the preserved-label reason", nc.Reason)
	}
}

// The other half, and the reason the fix is not "record every skip". A label
// the previous owner left behind that Git says nothing about is not a gap in
// Sharko's check — it is outside Sharko's scope. Recording it would put every
// taken-over connection permanently in the "not fully checked" state for
// something Sharko never intended to check, which trains people to ignore the
// state.
func TestCompare_ForeignPreservedLabelIsNotAGapInTheCheck(t *testing.T) {
	spec := argosecrets.ClusterSecretSpec{Server: testServer, Token: "made-up-token-for-tests-only"}
	req, _ := ownedRequest(t, spec, map[string]string{"datadog": models.LabelEnabled})
	req.Live.Labels["env"] = "prod"
	req.Live.Annotations = map[string]string{
		argosecrets.AnnotationTakeoverPreservedLabels: "env",
	}

	res := Compare(req)

	if res.Status != StatusSynced {
		t.Fatalf("status = %q, want %q — a label Git never declares is not something Sharko failed to "+
			"check. Differences = %+v, NotChecked = %+v", res.Status, StatusSynced, res.Differences, res.NotChecked)
	}
	if len(res.NotChecked) != 0 {
		t.Errorf("NotChecked = %+v, want empty", res.NotChecked)
	}
}

// The same connection twice in a row lists its unchecked labels in the same
// order. The list is built by walking a map, so without the sort this is a
// coin flip and the page reorders itself between refreshes.
func TestCompare_NotCheckedLabelOrderIsStable(t *testing.T) {
	spec := argosecrets.ClusterSecretSpec{Server: testServer, Token: "made-up-token-for-tests-only"}
	var first []string
	for run := 0; run < 25; run++ {
		req, _ := ownedRequest(t, spec, map[string]string{
			"zebra": models.LabelEnabled, "alpha": models.LabelEnabled, "middle": models.LabelEnabled,
		})
		req.Live.Annotations = map[string]string{
			argosecrets.AnnotationTakeoverPreservedLabels: "zebra,alpha,middle",
		}
		res := Compare(req)
		paths := make([]string, 0, len(res.NotChecked))
		for _, nc := range res.NotChecked {
			paths = append(paths, nc.Path)
		}
		if len(paths) != 3 {
			t.Fatalf("run %d: NotChecked = %+v, want three entries", run, res.NotChecked)
		}
		if first == nil {
			first = paths
			continue
		}
		for i := range paths {
			if paths[i] != first[i] {
				t.Fatalf("run %d ordered the unchecked list differently: %v vs %v", run, paths, first)
			}
		}
	}
}

// The backstop. unaccountedOwnedLabels is what stands between a skip somebody
// adds tomorrow and a false "synced" — it re-derives the set of labels Sharko
// declares and owns, and names anything the comparison did not account for.
// Nothing in today's code reaches it, which is exactly why it needs a test of
// its own: an unreached safety net that has quietly stopped working looks
// identical to one that is doing its job.
func TestCompare_UnaccountedOwnedLabelIsNotChecked(t *testing.T) {
	expected := map[string]string{
		"datadog":                   models.LabelEnabled,
		"cert-manager":              models.LabelEnabled,
		"example.com/somebody-else": "value",
	}

	// Nothing accounted for at all — the shape a future skip would produce.
	got := unaccountedOwnedLabels(expected, ScopeFull, map[string]bool{})
	if len(got) != 2 {
		t.Fatalf("unaccountedOwnedLabels returned %+v, want the two labels Sharko owns and no more", got)
	}
	if got[0].Path != labelFieldPath("cert-manager") || got[1].Path != labelFieldPath("datadog") {
		t.Errorf("paths = %q, %q — want them sorted", got[0].Path, got[1].Path)
	}
	for _, nc := range got {
		if nc.Reason != ReasonLabelNotCompared {
			t.Errorf("%s: reason = %q, want the not-compared reason", nc.Path, nc.Reason)
		}
	}

	// A key that WAS accounted for is not reported twice.
	got = unaccountedOwnedLabels(expected, ScopeFull, map[string]bool{"datadog": true, "cert-manager": true})
	if len(got) != 0 {
		t.Errorf("unaccountedOwnedLabels = %+v, want nothing once every owned key is accounted for", got)
	}
}

// The exact words, typed out here rather than read back off the constant. A
// test that compares a constant with itself passes no matter what the sentence
// says, which is how a wrong explanation once survived four review rounds.
func TestNotCheckedLabelReasonsAreExact(t *testing.T) {
	const wantPreserved = "Git declares this label, and a takeover also recorded it as belonging to " +
		"whoever managed this cluster before Sharko. Sharko leaves recorded labels alone, so it " +
		"neither compared this one nor changes it on the cluster."
	if ReasonLabelPreservedForPreviousOwner != wantPreserved {
		t.Errorf("ReasonLabelPreservedForPreviousOwner changed.\n got: %q\nwant: %q",
			ReasonLabelPreservedForPreviousOwner, wantPreserved)
	}

	const wantNotCompared = "Sharko owns this label and Git declares a value for it, but this check " +
		"did not compare it. Sharko will not call the connection synced while that is true."
	if ReasonLabelNotCompared != wantNotCompared {
		t.Errorf("ReasonLabelNotCompared changed.\n got: %q\nwant: %q",
			ReasonLabelNotCompared, wantNotCompared)
	}
}
