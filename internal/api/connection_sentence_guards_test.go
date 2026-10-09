package api

// connection_sentence_guards_test.go — the pin on the connection repair
// refusals.
//
// # The failure it exists to stop
//
// Every other sentence block on this surface is pinned character for character
// somewhere. The repairFail* block was not: nothing in the tree referenced a
// single one of those constants, so any of them could be reworded, or quietly
// broken, and the whole Go suite stayed green. One of them was wrong — it
// promised a timeframe ("check again in a moment") for an action whose
// interval the operator sets, so the sentence was false on any install that
// sets it longer. It was corrected on 2026-08-20 and pinned here.
//
// # Where the Git-capitalisation rule now lives
//
// This file used to hold two more guards, each enforcing the product owner's
// ruling that Git is capitalised in text a person reads, and each reading a
// HAND-WRITTEN LIST of files. Both were green on 2026-08-21 while twelve
// shipped sentences and eleven published swagger descriptions wrote the word
// in lowercase — every one of them in a file nobody had listed, including
// connection_credential_check.go, which the sentence CATALOG already treats as
// a file that authors sentences.
//
// The lists were the defect, so both guards were replaced by a single sweep
// with no file list at all: internal/api/git_capitalisation_test.go, which
// walks every non-test .go file under internal/ and cmd/ and covers swagger
// annotations as well. See that file's header.

import (
	"strings"
	"testing"
)

// TestConnectionRepair_RefusalSentencesExact pins the repair refusals as
// LITERALS here — not as references to the production constants, which would
// compare a constant with itself and pass no matter what the sentence said.
func TestConnectionRepair_RefusalSentencesExact(t *testing.T) {
	// A SLICE OF NAMED CASES, NOT A MAP KEYED BY THE VALUE — the same fix as
	// TestConnectionReconciliation_NewSentencesExact. Keyed by the sentence,
	// two constants holding the same string collapse into one entry and the
	// test passes having checked one fewer, saying so nowhere.
	exact := []struct{ name, got, want string }{
		{"repairFailSecretGoneSharkoCreates", repairFailSecretGoneSharkoCreates,
			"There is no ArgoCD connection for this cluster to repair. Sharko automatically creates it from Git and the configured credentials source."},
		{"repairFailSecretGoneLabelsOnly", repairFailSecretGoneLabelsOnly,
			"There is no ArgoCD connection for this cluster, so there are no addon labels for Sharko to re-apply. Sharko changed nothing. Run the connection check on this cluster to see why the connection is missing and what to do about it."},
		{"repairFailNoGit", repairFailNoGit,
			"Sharko is not connected to a Git repository right now, so it cannot see what this connection should look like."},
		{"repairFailGitRead", repairFailGitRead,
			"Sharko could not read this cluster's record from Git, so it does not know what the connection should be. Check the Git connection and try again."},
		{"repairFailNotManaged", repairFailNotManaged,
			"This cluster has no entry in the Git-managed cluster list, so Sharko has nothing to repair its connection against."},
		{"repairFailRevisionUnknown", repairFailRevisionUnknown,
			"Sharko cannot tell which commit your Git branch is on right now, so it will not rewrite this connection. Sharko only makes this change when it can name the exact commit it is matching. Re-run the connection check and try again."},
		{"repairFailRevisionMoved", repairFailRevisionMoved,
			"Your Git branch moved while you were looking at this connection, so what you reviewed is not what Sharko would write now. Sharko changed nothing. Run the connection check again and repair from the fresh result."},
		{"repairFailWrite", repairFailWrite,
			"Sharko could not write this cluster's connection. Nothing was changed. Try again in a moment."},
	}
	for _, tc := range exact {
		if tc.got != tc.want {
			t.Errorf("the repair refusal %s drifted:\n got %q\nwant %q", tc.name, tc.got, tc.want)
		}
	}

	// The two missing-connection sentences describe what Sharko does by
	// itself. Neither may promise a timeframe: the interval is
	// operator-settable, so any "in a moment" is a lie on an installation that
	// sets it longer.
	for _, sentence := range []struct{ name, text string }{
		{"repairFailSecretGoneSharkoCreates", repairFailSecretGoneSharkoCreates},
		{"repairFailSecretGoneLabelsOnly", repairFailSecretGoneLabelsOnly},
	} {
		for _, clock := range []string{"in a moment", "shortly", "within", "in a few"} {
			if strings.Contains(strings.ToLower(sentence.text), clock) {
				t.Errorf("%s promises a timeframe (%q) for an automatic action — banned:\n  %q",
					sentence.name, clock, sentence.text)
			}
		}
	}

	// B3: ONLY the full-connection sentence may say Sharko creates the
	// connection by itself. The labels-only path is reached by connections
	// Sharko will not, or cannot, build — a self-managed one, a legacy pasted
	// kubeconfig, a source Sharko does not understand, a backend it cannot
	// read — so a creation promise there is false whichever way it is worded.
	// This is the pin that stops the two sentences being folded back together.
	for _, promise := range []string{"automatically creates", "sharko creates", "will create"} {
		if strings.Contains(strings.ToLower(repairFailSecretGoneLabelsOnly), promise) {
			t.Errorf("the labels-only missing-connection sentence promises creation (%q), which is false for every connection that reaches it:\n  %q",
				promise, repairFailSecretGoneLabelsOnly)
		}
	}
	if !strings.Contains(repairFailSecretGoneSharkoCreates, "Sharko automatically creates it from Git and the configured credentials source.") {
		t.Error("the full-connection missing-connection sentence lost the promise it is allowed to make — it is only reached for a cluster Sharko really does build the connection for")
	}
	// repairFailWrite's "Try again in a moment." is deliberately NOT covered
	// by that rule and must stay: it tells a PERSON what to do after a
	// failure, which is not a promise about what Sharko does by itself.
	if !strings.Contains(repairFailWrite, "Try again in a moment.") {
		t.Error("repairFailWrite lost its retry instruction — that sentence is a deliberate exception, not an oversight")
	}
}

// Both Git-capitalisation guards that used to live here have been REPLACED by
// internal/api/git_capitalisation_test.go, which walks every non-test .go file
// under internal/ and cmd/ instead of two hand-written file lists.
//
// The lists were the whole defect. Both guards were green on 2026-08-21 while
// twelve shipped sentences and eleven published swagger descriptions wrote the
// word in lowercase, every one of them in a file nobody had listed — including
// connection_credential_check.go, which the sentence CATALOG already treats as
// a file that authors sentences. See that file's header for the replacement.
