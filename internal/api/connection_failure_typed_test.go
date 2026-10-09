package api

// connection_failure_typed_test.go — the proofs for the typed-fact refactor.
//
// # What was wrong
//
// connectioncompare.Result carried the whole SENTENCE a person reads as its
// only record of why a check could not finish, and the connection page routed
// on it — a switch whose case values were full paragraphs. So a copy edit
// decided which branch ran and which failed step the page named. Change a
// comma, and the page silently falls into its default branch: no compiler
// error, no failing test, nothing to notice.
//
// The product owner's ruling: "presentation structure must follow typed facts,
// never equality between human sentences."
//
// # What this file proves, and what it deliberately does not
//
//  1. Every declared connectioncompare.CheckFailure has a sentence, and every
//     sentence in the map still has a reason — BY NAME in both directions,
//     never a count.
//  2. The words each reason renders to, pinned as LITERALS. Nine of the ten
//     are byte-identical to what shipped before the refactor; the tenth
//     changed on purpose and says so below.
//  3. An undeclared reason still renders a TRUE sentence rather than a blank.
//
// The guard that stops a NEW routing site keying on a sentence again lives in
// connection_sentence_routing_test.go — a different failure, so a different
// file.

import (
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/connectioncompare"
)

// setCheckFailure puts a view into the check_failed state the way the SERVER
// does: the typed fact, and the sentence derived from it by the one production
// mapper.
//
// Test fixtures used to set FailureReason by hand and nothing else. That was
// survivable while the sentence WAS the fact; now it builds a view the server
// could never produce — a check_failed answer with words but no fact — and
// every routing assertion made on it would be meaningless. Setting both from
// one call is what stops a fixture drifting away from a real response.
func setCheckFailure(v *connectionComparisonView, f connectioncompare.CheckFailure) {
	v.Status = string(connectioncompare.StatusCheckFailed)
	v.Scope = string(connectioncompare.Scope(""))
	v.failure = f
	v.FailureReason = connectionFailureSentence(f)
}

// TestConnectionFailure_EveryTypedReasonHasASentence walks the closed set in
// BOTH directions and names what is missing.
//
// NOT A COUNT. A count passes while a stale entry rots, and gets HAPPIER as
// the bug appears — add a reason and delete a sentence in the same change and
// a count-based guard sails through. Both halves below list identifiers.
func TestConnectionFailure_EveryTypedReasonHasASentence(t *testing.T) {
	declared := connectioncompare.CheckFailures()
	if len(declared) == 0 {
		t.Fatal("connectioncompare.CheckFailures() is empty — this guard would pass vacuously")
	}

	// Direction 1: a reason with no words. It would reach a person's screen as
	// the last-resort sentence, which is true but says nothing useful.
	inSet := map[connectioncompare.CheckFailure]bool{}
	var noSentence []string
	for _, f := range declared {
		inSet[f] = true
		if _, ok := connectionFailureSentences[f]; !ok {
			noSentence = append(noSentence, "  "+string(f))
		}
	}
	if len(noSentence) > 0 {
		t.Errorf("check failure reason(s) with no sentence in connectionFailureSentences.\n"+
			"A check_failed answer for one of these would fall back to the last-resort wording and\n"+
			"tell the reader nothing about what actually failed:\n%s", strings.Join(noSentence, "\n"))
	}

	// Direction 2: words for a reason that no longer exists. Dead weight that
	// reads as coverage, and the catalogue ships it to the browser.
	var noReason []string
	for f := range connectionFailureSentences {
		if !inSet[f] {
			noReason = append(noReason, "  "+string(f))
		}
	}
	if len(noReason) > 0 {
		t.Errorf("connectionFailureSentences has words for reason(s) connectioncompare no longer declares.\n"+
			"Nothing can produce them, so they are shipped to the browser as sentences the server\n"+
			"can never send:\n%s", strings.Join(noReason, "\n"))
	}

	// And the empty reason is never given words. "Nothing failed" is not a
	// failure, and a sentence for it is a sentence nobody can be shown.
	if _, ok := connectionFailureSentences[connectioncompare.CheckFailureNone]; ok {
		t.Error("connectionFailureSentences has an entry for the empty reason — that is the ABSENCE of a failure, not one")
	}
	if got := connectionFailureSentence(connectioncompare.CheckFailureNone); got != "" {
		t.Errorf("the empty reason rendered %q, want no words at all", got)
	}
}

// TestConnectionFailure_RenderedSentencesAreExact is the byte-for-byte pin.
//
// The expected text is a LITERAL on every line. Reading it from the production
// constant would compare a thing with itself: both sides move together and the
// test passes no matter what the sentence says. That exact shape has already
// been found twice in this release round.
func TestConnectionFailure_RenderedSentencesAreExact(t *testing.T) {
	// A SLICE OF NAMED CASES, not a map keyed by the sentence — two reasons
	// holding the same words would collapse into one entry and the test would
	// pass having silently checked one fewer.
	cases := []struct {
		reason connectioncompare.CheckFailure
		want   string
	}{
		{connectioncompare.CheckFailureNoReconciler,
			"The part of Sharko that manages cluster connections is not running on this server, so it cannot check this connection."},
		{connectioncompare.CheckFailureNoGitConnection,
			"Sharko is not connected to a Git repository right now, so it cannot see what this connection should look like."},
		{connectioncompare.CheckFailureNoHubClient,
			"Sharko is not connected to its own cluster on this server, so it cannot read this connection."},
		{connectioncompare.CheckFailureGitRead,
			"Sharko could not read this cluster's record from Git, so it cannot tell what the connection should look like. Check the Git connection and try again."},
		{connectioncompare.CheckFailureLiveRead,
			"Sharko could not read this cluster's connection from its own cluster, so the check did not finish. Try again in a moment."},
		{connectioncompare.CheckFailureBackendRead,
			"Sharko could not read this cluster's configured credentials source from the secrets backend, so it could not work out what the connection should look like. Check the secrets backend connection and try again."},
		{connectioncompare.CheckFailureNotManaged,
			"This cluster has no entry in the Git-managed cluster list, so Sharko has nothing to compare its connection against."},
		{connectioncompare.CheckFailureCredentialsUnavailable,
			"Sharko could not read this cluster's configured credentials source, so the check did not finish."},

		// Byte-identical to what internal/connectioncompare/compare.go used to
		// type inline. Only its address changed in R2-4, not one character of
		// its wording.
		{connectioncompare.CheckFailureAddonLabelsUnknown,
			"Sharko could not read which addons should be on for this cluster, so it cannot tell whether this connection's labels are right. Check that the cluster's addon file is readable in Git, then check again."},

		// THE ONE SENTENCE THAT CHANGED. It used to end "Check again in a
		// moment." This failure is a marshalling failure inside
		// argosecrets.BuildClusterSecret over Sharko's own struct: the same
		// inputs fail the same way every time, so "check again in a moment"
		// told the reader to do a thing that cannot help and sent them looking
		// at their cluster and their Git repository for a fault in neither.
		{connectioncompare.CheckFailureExpectedBuild,
			"Sharko could not work out what this cluster's connection should look like, so there is nothing to compare against. This is a fault in Sharko itself — nothing on the cluster or in Git needs changing."},
	}

	// Every declared reason must appear here. Without this the table could
	// silently cover nine of ten and still pass.
	covered := map[connectioncompare.CheckFailure]bool{}
	for _, tc := range cases {
		covered[tc.reason] = true
		if got := connectionFailureSentence(tc.reason); got != tc.want {
			t.Errorf("the sentence for %q drifted:\n got %q\nwant %q", tc.reason, got, tc.want)
		}
	}
	for _, f := range connectioncompare.CheckFailures() {
		if !covered[f] {
			t.Errorf("check failure %q is declared but has no pinned sentence in this table — its wording could change unnoticed", f)
		}
	}

	// The old promise must not come back on the build failure. It was removed
	// on purpose, so ban the words rather than only pinning the new ones — a
	// pin alone is satisfied by any rewrite that happens to be deliberate.
	if strings.Contains(connectionFailureSentence(connectioncompare.CheckFailureExpectedBuild), "in a moment") {
		t.Error("the expected-build failure promises a retry that cannot help — the same inputs fail the same way every time")
	}
}

// TestConnectionFailure_BackendReadBansDeadPhrasings moves a check that used to
// sit on a TEST-LOCAL constant in internal/connectioncompare.
//
// That constant was a fixture. It was never the sentence the server sends, so
// pinning its wording and banning phrases in it proved nothing about anything a
// person could ever read. The bans belong on the shipped sentence, and this is
// it.
func TestConnectionFailure_BackendReadBansDeadPhrasings(t *testing.T) {
	got := connectionFailureSentence(connectioncompare.CheckFailureBackendRead)

	// For an EKS cluster the backend stores metadata, not a reusable
	// credential, so "stored sign-in details" tells a story the code does not
	// match.
	banned := []string{
		"stored sign-in details",
		"independently stored copy",
		"cannot mint",
		"mints a fresh token on every fetch",
		"every fetch",
		"no query parameter is read",
	}
	for _, phrase := range banned {
		if strings.Contains(got, phrase) {
			t.Errorf("the backend-read failure sentence contains %q, which comes from an earlier iteration that told a story the code no longer matches:\n  %q", phrase, got)
		}
	}
}

// TestConnectionFailure_UndeclaredReasonStillSaysSomethingTrue pins the
// fallback.
//
// It is unreachable while the exhaustiveness guard above passes. It exists
// because the alternative — a check_failed answer with an EMPTY explanation —
// is the worst outcome available: a page that says something went wrong and
// then refuses to say what.
func TestConnectionFailure_UndeclaredReasonStillSaysSomethingTrue(t *testing.T) {
	invented := connectioncompare.CheckFailure("a_reason_nobody_declared")
	if connectioncompare.IsDeclared(invented) {
		t.Fatal("the invented reason is actually declared — pick another and the fallback is untested")
	}
	got := connectionFailureSentence(invented)
	if got == "" {
		t.Fatal("an undeclared reason rendered no words at all — the reader would be told something failed and not what")
	}
	if got != "Sharko could not finish checking this connection." {
		t.Errorf("the last-resort sentence drifted:\n got %q\nwant %q", got, "Sharko could not finish checking this connection.")
	}
}

// TestConnectionCanonical_EKSHeadlineFactMatchesTheWords proves the fact and
// the words cannot drift apart.
//
// connectionSyncQualifier used to ask "is this the clean-EKS row?" by comparing
// the HEADLINE'S WORDS against headlineConfigurationMatchesEKS. Reword that
// headline and the de-duplication silently stops working — the page says the
// same thing twice, with nothing to notice. headlineIsConfigurationMatchesEKS
// answers the same question from the FACTS instead, which is only safe if the
// two agree everywhere. So drive every combination of every field either
// function reads and fail on any disagreement.
func TestConnectionCanonical_EKSHeadlineFactMatchesTheWords(t *testing.T) {
	syncStates := []string{syncStateSynced, syncStateOutOfSync, syncStateBlocked, syncStateUnknown, "some_state_nobody_declared"}
	scopes := []string{verificationScopeFull, verificationScopePartial, verificationScopeNone, ""}
	modes := []string{managementModeSharkoManaged, managementModeSelfManaged, managementModeLegacyInline, managementModeForeignOwned, ""}
	managedScopes := []string{managedScopeFullConnection, managedScopeAddonLabels, managedScopeNone, ""}
	checkedAts := []string{"", "2026-08-20T00:00:00Z"}

	combinations := 0
	for _, syncState := range syncStates {
		for _, scope := range scopes {
			for _, mode := range modes {
				for _, managed := range managedScopes {
					for _, checkedAt := range checkedAts {
						for _, missing := range []bool{false, true} {
							for _, approval := range []bool{false, true} {
								st := connectionCanonicalState{
									SyncState:         syncState,
									VerificationScope: scope,
									ManagementMode:    mode,
									ManagedScope:      managed,
									CheckedAt:         checkedAt,
									LiveSecretMissing: missing,
									ApprovalRequired:  approval,
								}
								combinations++
								fact := headlineIsConfigurationMatchesEKS(st)
								words := connectionSyncHeadline(st) == headlineConfigurationMatchesEKS
								if fact != words {
									t.Fatalf("the fact and the words disagree for %+v:\n"+
										"  headlineIsConfigurationMatchesEKS = %v\n"+
										"  headline == headlineConfigurationMatchesEKS = %v\n"+
										"The qualifier routes on the fact, so this disagreement is a page that either\n"+
										"repeats itself or drops a qualifier it owes the reader.",
										st, fact, words)
								}
							}
						}
					}
				}
			}
		}
	}
	// A drive that covered nothing would pass silently.
	if combinations != len(syncStates)*len(scopes)*len(modes)*len(managedScopes)*len(checkedAts)*2*2 {
		t.Fatalf("the drive covered %d combinations, which is not the full product — it has lost its reach", combinations)
	}
	t.Logf("fact and words agreed across all %d combinations", combinations)
}
