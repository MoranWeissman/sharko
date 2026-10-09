package credsafe_test

// repourl_corpus_test.go — the address rule, checked against the list that was
// written down separately from the code.
//
// # Why this test is in an external test package
//
// It imports internal/addresscorpus, which reads testdata/address-rule-corpus.yaml
// at the repository root. Sitting in credsafe_test rather than credsafe keeps
// the reader out of the package under test, so nothing here can reach a
// package-level helper and quietly agree with it.
//
// # Why the expectations are not computed
//
// The test this file replaces worked out what it expected by calling the very
// function it was checking:
//
//	wantOpen := ClassifyAddress(raw) == AddressCredentialFree
//
// That passes for any classifier at all. Loosen the classifier and the
// expectation loosens with it, and the guard reports green while the thing it
// guards has stopped working. Every expectation below comes from the list on
// disk, which was written from the rule before this code was touched, so the
// code cannot shape its own test data.
//
// # How a row maps onto the three verdicts
//
// The list says "accepted" or "refused" and has no third word, because the
// rule has no third answer. The classifier has three states, and two of them
// — "carries a credential" and "could not read it" — are both refusals. So:
//
//	accepted  MUST be exactly AddressCredentialFree
//	refused   MUST be anything except AddressCredentialFree
//
// Which of the two refusals a row lands on is deliberately not pinned here.
// The rule does not distinguish them, and a test that pinned it would be
// asserting an implementation detail the list never claimed.

import (
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/addresscorpus"
	"github.com/MoranWeissman/sharko/internal/credsafe"
)

// loadCorpus reads the list, or stops the test. A list that fails to load must
// never read as "nothing to check".
func loadCorpus(t *testing.T) []addresscorpus.Row {
	t.Helper()
	rows, err := addresscorpus.Load()
	if err != nil {
		t.Fatalf("the address rule list did not load, so nothing below checked anything: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("the address rule list holds no rows, so every check below would pass without looking at an address")
	}
	refused, accepted := addresscorpus.Counts(rows)
	if refused == 0 || accepted == 0 {
		t.Fatalf("the list has %d refused and %d accepted rows — it needs both sides to prove anything", refused, accepted)
	}
	return rows
}

// TestClassifyAddress_AgreesWithTheWrittenDownRule is the test of record for
// the classifier. Every row in the list is put through ClassifyAddress and the
// answer is compared with what the rule says, not with what the code says.
func TestClassifyAddress_AgreesWithTheWrittenDownRule(t *testing.T) {
	rows := loadCorpus(t)

	checked := 0
	for _, row := range rows {
		got := credsafe.ClassifyAddress(row.Address)
		checked++
		switch row.Verdict {
		case addresscorpus.Accepted:
			if got != credsafe.AddressCredentialFree {
				t.Errorf("ClassifyAddress(%q) = %v, but the rule accepts it.\n  the rule says: %s",
					row.Address, got, strings.TrimSpace(row.Reason))
			}
		case addresscorpus.Refused:
			if got == credsafe.AddressCredentialFree {
				t.Errorf("ClassifyAddress(%q) = credential-free, but the rule refuses it.\n  the rule says: %s",
					row.Address, strings.TrimSpace(row.Reason))
			}
		}
	}
	if checked != len(rows) {
		t.Fatalf("looked at %d addresses out of %d rows", checked, len(rows))
	}
	t.Logf("checked %d addresses against the written-down rule", checked)
}

// TestSafeRepoURL_NeverHandsBackAnAddressTheRuleRefuses is the second half of
// the same promise, and the half that actually leaked.
//
// A classifier that says "refused" is worth nothing if the display helper next
// to it hands the operator's string straight back. Every refused row must come
// back changed — either emptied, or with the credential-carrying parts taken
// off — and never byte-for-byte as it was written.
func TestSafeRepoURL_NeverHandsBackAnAddressTheRuleRefuses(t *testing.T) {
	rows := loadCorpus(t)

	// Positive control: the check below can only mean something if an
	// address the rule ACCEPTS does come back unchanged. If nothing comes
	// back unchanged, "changed" proves nothing.
	sawAnUntouchedAcceptedAddress := false

	checkedRefused := 0
	for _, row := range rows {
		got := credsafe.SafeRepoURL(row.Address)
		switch row.Verdict {
		case addresscorpus.Refused:
			checkedRefused++
			if row.Address == "" {
				// There is nothing to hand back, so "unchanged" and
				// "emptied" are the same string. Nothing to prove.
				continue
			}
			if got == row.Address {
				t.Errorf("SafeRepoURL(%q) handed the address back exactly as written, but the rule refuses it.\n  the rule says: %s",
					row.Address, strings.TrimSpace(row.Reason))
			}
		case addresscorpus.Accepted:
			if got == "" {
				t.Errorf("SafeRepoURL(%q) came back empty, but the rule accepts the address, so an operator sees a blank where their own address should be.\n  the rule says: %s",
					row.Address, strings.TrimSpace(row.Reason))
			}
			if got == row.Address {
				sawAnUntouchedAcceptedAddress = true
			}
		}
	}
	if checkedRefused == 0 {
		t.Fatal("no refused row was examined, so this test proved nothing")
	}
	if !sawAnUntouchedAcceptedAddress {
		t.Fatal("not one accepted address came back exactly as written, so 'came back changed' below is not evidence of anything")
	}
}

// TestSafeRepoURL_NeverLeaksTheSyntheticPassword is the blunt version of the
// same question, aimed at the one thing that must never travel.
//
// Every row in the list that carries the obviously-fake password carries it in
// a different position — plainly in the authority, behind a mistyped scheme,
// after a raw control character, inside a percent escape. Whatever position it
// is written in, what SafeRepoURL returns must not contain it, unless the rule
// itself accepts the address (which it does for the one row where the password
// text is provably ordinary path data).
func TestSafeRepoURL_NeverLeaksTheSyntheticPassword(t *testing.T) {
	rows := loadCorpus(t)

	const password = "synthetic-pw-not-real"

	carriers := 0
	for _, row := range rows {
		if !strings.Contains(row.Address, password) {
			continue
		}
		carriers++
		if row.Verdict == addresscorpus.Accepted {
			// The rule accepts it, so the text is not in a credential
			// position at all and there is nothing to strip.
			continue
		}
		if got := credsafe.SafeRepoURL(row.Address); strings.Contains(got, password) {
			t.Errorf("SafeRepoURL returned the password for a refused address.\n  address: %q\n  returned: %q\n  the rule says: %s",
				row.Address, got, strings.TrimSpace(row.Reason))
		}
	}
	if carriers == 0 {
		t.Fatal("no row in the list carries the password, so this test looked at nothing")
	}
	t.Logf("checked %d addresses that carry the password", carriers)
}

// TestTheDoorsAgreeWithTheWrittenDownRule checks the two callers that decide
// whether an operator-supplied address may be SAVED, rather than merely shown.
//
// # The one deliberate difference from the list
//
// Both doors return nil for an empty address on purpose: "nothing configured
// yet" is a different condition from "this address is unusable", and the
// caller says so in its own words. The list has no way to express that, so the
// empty row is skipped here and only here.
func TestTheDoorsAgreeWithTheWrittenDownRule(t *testing.T) {
	rows := loadCorpus(t)

	doors := map[string]func(string) error{
		"the catalog repository address rule": credsafe.ValidateSupportedRepoURL,
		"the Sharko server address rule":      credsafe.ValidateServerAddress,
	}

	opened, shut := 0, 0
	for _, row := range rows {
		if row.Address == "" {
			continue
		}
		for name, door := range doors {
			err := door(row.Address)
			switch row.Verdict {
			case addresscorpus.Accepted:
				opened++
				if err != nil {
					t.Errorf("%s refused %q, but the rule accepts it.\n  the rule says: %s",
						name, row.Address, strings.TrimSpace(row.Reason))
				}
			case addresscorpus.Refused:
				shut++
				if err == nil {
					t.Errorf("%s accepted %q, but the rule refuses it.\n  the rule says: %s",
						name, row.Address, strings.TrimSpace(row.Reason))
				}
			}
		}
	}
	if opened == 0 || shut == 0 {
		t.Fatalf("the sweep proved nothing: %d addresses let through, %d turned away — it needs both", opened, shut)
	}
}
