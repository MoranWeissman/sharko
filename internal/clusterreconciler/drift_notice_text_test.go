package clusterreconciler

import (
	"strings"
	"testing"
)

// TestDriftNoticeText pins the exact text of the drift notice constant and
// bans phrases from earlier iterations that told a story the code no longer
// matches.
//
// Product owner signed off on this wording on 2026-08-13.
func TestDriftNoticeText(t *testing.T) {
	// Pin the exact full text. The whole sentence must be present — a break
	// that drops the first half must fail, not just one that changes the
	// second clause.
	const want = "Sharko's background pass checks that this connection is structurally intact and still Sharko's. It does not compare the configured credentials source on a timer — open the cluster's connection check for that."
	if driftNoticeUncheckedNote != want {
		t.Errorf("driftNoticeUncheckedNote text changed.\n\nGot:\n%s\n\nWant:\n%s\n\nThis wording was signed off by the product owner on 2026-08-13. Change it on purpose, not by paraphrasing.", driftNoticeUncheckedNote, want)
	}

	// Assert the removed phrase is absent.
	if strings.Contains(driftNoticeUncheckedNote, "stored sign-in details") {
		t.Errorf("driftNoticeUncheckedNote = %q\n\nMust not say 'stored sign-in details' — for an EKS cluster the backend stores metadata to create a credential, not a reusable sign-in credential. Say 'configured credentials source', which is true for both.", driftNoticeUncheckedNote)
	}

	// Assert other dead phrasings are absent too.
	banned := []string{
		"independently stored copy",
		"cannot mint",
		"mints a fresh token on every fetch",
		"every fetch",
		"no query parameter is read",
	}
	for _, phrase := range banned {
		if strings.Contains(driftNoticeUncheckedNote, phrase) {
			t.Errorf("driftNoticeUncheckedNote = %q\n\nMust not contain the phrase %q — it comes from an earlier iteration that told a story the code no longer matches.", driftNoticeUncheckedNote, phrase)
		}
	}
}
