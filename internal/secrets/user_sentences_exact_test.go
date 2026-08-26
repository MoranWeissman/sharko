package secrets

// user_sentences_exact_test.go — the exact pin on the one sentence in this
// package that names Git.
//
// B18 capitalised it, and it had no exact pin: failure_sentence_test.go
// asserts only that the mapped sentence is non-empty, is not the raw error
// text, and carries no forbidden substrings. All of that stays green however
// the sentence is worded. The want below is TYPED OUT HERE, not read from the
// production code, so the two cannot drift together.

import "testing"

func TestFailureSentence_CatalogUnreadableExact(t *testing.T) {
	const want = "Sharko couldn't read the addon catalog or the managed-clusters file in Git. Check that Sharko can reach your Git host, then click Refresh."

	// Both raw shapes the plan-level failure arrives in, driven through the
	// REAL mapping function.
	for _, raw := range []string{
		"could not read the addon catalog or managed clusters list: dial tcp 10.0.0.1:443: connection refused",
		"no addon catalog could be read at any known path",
	} {
		if got := FailureSentence(raw); got != want {
			t.Errorf("FailureSentence(%q):\n got %q\nwant %q", raw, got, want)
		}
	}
}
