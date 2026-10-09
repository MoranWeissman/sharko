package api

// user_sentences_git_exact_test.go — exact pins on the three sentences in this
// package that name Git and had no pin of their own.
//
// B18 capitalised them. Before that, nothing in the tree compared any of them
// against a literal: the managed-secrets ones were only asserted non-empty,
// and the cluster-secrets refusal was only checked for "the raw error did not
// leak". Both of those stay green however the sentence is worded — the same
// shape as the repair refusals in connection_sentence_guards_test.go, and the
// same shape as the project lesson about a wrong sentence surviving four
// review rounds because its only test asserted != "".
//
// Every want is TYPED OUT HERE. Comparing against the production literal would
// compare a string with itself and prove nothing.

import (
	"net/http"
	"testing"
)

func TestGitUnreadableSentencesExact(t *testing.T) {
	// The three sentences are near-twins that differ only in the last clause,
	// and that difference is load-bearing: two of them sit on a page with a
	// Refresh button and one does not, so only the right one may say "click
	// Refresh". Pinning them together is what keeps a copy edit from folding
	// them into one.
	const rawCatalogUnreadable = "could not read the addon catalog or managed clusters list: dial tcp 10.0.0.1:443: connection refused"

	status, refusal := clusterSecretsRefreshRefusal("prod-eu", "", rawCatalogUnreadable)
	if status != http.StatusBadGateway {
		t.Errorf("the catalog-unreadable refusal returned status %d, want %d", status, http.StatusBadGateway)
	}
	exact := []struct{ name, got, want string }{
		{"clusterSecretsRefreshRefusal", refusal,
			"Sharko couldn't read the addon catalog or managed-clusters file in Git. Check that Sharko can reach your Git host, then try again."},
		{"addonValuesSecretCheckFailureSentence", addonValuesSecretCheckFailureSentence(rawCatalogUnreadable),
			"Sharko couldn't read the addon catalog or managed-clusters file in Git. Check that Sharko can reach your Git host, then try again."},
		{"addonValuesSecretSyncFailureSentence", addonValuesSecretSyncFailureSentence(rawCatalogUnreadable),
			"Sharko couldn't read the addon catalog or managed-clusters file in Git. Check that Sharko can reach your Git host, then try again."},
	}
	for _, tc := range exact {
		if tc.got != tc.want {
			t.Errorf("%s drifted:\n got %q\nwant %q", tc.name, tc.got, tc.want)
		}
	}
}
