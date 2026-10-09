package addresscorpus

// corpus_test.go — checks on the list itself, and nothing else.
//
// These tests do not classify a single address. They are here so that a
// mistake in the file is a loud failure right where the mistake is, instead of
// showing up later as a classifier test that quietly checks nothing. Every
// check below has been proved by breaking the file on purpose and watching the
// named test fail.

import (
	"strings"
	"testing"
)

// TestTheCorpusFileIsIntact is the one that has to fail when the file is
// missing, empty, or has a broken row in it. Load does the checking; this test
// exists so the check runs on every `go test ./...`.
func TestTheCorpusFileIsIntact(t *testing.T) {
	rows, err := Load()
	if err != nil {
		t.Fatalf("the address corpus did not load: %v", err)
	}
	if len(rows) == 0 {
		// Load already refuses an empty list. This second look is here
		// because "no rows" is the failure that looks identical to success
		// from anywhere downstream, and it should be impossible to reach
		// this line with an empty slice.
		t.Fatal("Load returned no rows and no error, so every test built on this list would pass without checking anything")
	}
	refused, accepted := Counts(rows)
	if refused+accepted != len(rows) {
		t.Fatalf("counted %d refused plus %d accepted, which does not add up to the %d rows in the list",
			refused, accepted, len(rows))
	}
	t.Logf("the address corpus holds %d rows: %d refused, %d accepted", len(rows), refused, accepted)
}

// TestTheCorpusHoldsBothVerdicts guards the shape of the list. A list that is
// all refusals proves a classifier that refuses everything, and a list that is
// all accepts proves one that accepts everything; neither is worth running.
func TestTheCorpusHoldsBothVerdicts(t *testing.T) {
	rows, err := Load()
	if err != nil {
		t.Fatalf("the address corpus did not load: %v", err)
	}
	refused, accepted := Counts(rows)
	if refused == 0 {
		t.Fatal("no row in the list says refused, so the list cannot show that anything is turned away")
	}
	if accepted == 0 {
		t.Fatal("no row in the list says accepted, so the list cannot show that anything ordinary still works")
	}
}

// codeWords are words that only appear in a reason written by reading the
// code. The whole point of the reason column is that it argues from the rule,
// so a reason that names a Go function, a Go type, a package or the chart is
// wrong however true it happens to be.
var codeWords = []string{
	"classifyaddress",
	"addressverdict",
	"saferepourl",
	"credsafe",
	"url.parse",
	"net/url",
	"helm",
	"template",
	"func ",
	"returns ",
}

// TestEveryReasonArguesFromTheRuleAndNotFromTheCode keeps the reason column
// honest. It cannot tell good reasoning from bad, but it can tell that a
// reason is talking about the rule rather than about an implementation.
func TestEveryReasonArguesFromTheRuleAndNotFromTheCode(t *testing.T) {
	rows, err := Load()
	if err != nil {
		t.Fatalf("the address corpus did not load: %v", err)
	}

	// Positive control first: a matcher that finds nothing reports every
	// reason clean whether it is or not.
	planted := "refused because ClassifyAddress returns AddressUnclassifiable"
	if !mentionsCode(planted) {
		t.Fatal("the matcher does not find a planted code-shaped reason, so the sweep below would report every row clean whether it was or not")
	}

	for i, row := range rows {
		if mentionsCode(row.Reason) {
			t.Errorf("row %d (%q): the reason argues from the code rather than from the rule: %q",
				i+1, row.Address, row.Reason)
		}
	}
}

func mentionsCode(reason string) bool {
	lower := strings.ToLower(reason)
	for _, w := range codeWords {
		if strings.Contains(lower, w) {
			return true
		}
	}
	return false
}
